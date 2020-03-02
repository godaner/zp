package client

import (
	"encoding/binary"
	"encoding/json"
	"github.com/godaner/zp/zpp"
	"github.com/godaner/zp/zpp/zppnew"
	zpnet "github.com/godaner/zp/net"
	"io"
	"log"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	restart_interval           = 5
	wait_server_hello_time_sec = 5
	hb_interval_sec            = 10
)

type Client struct {
	ProxyAddr            string
	IPPVersion           int
	LocalProxyPort    string
	TempCliID            uint16
	V2Secret             string
	proxyConn            *zpnet.IPConn
	forwardConnRID       sync.Map // map[uint16]net.Conn
	seq                  int32
	cliID                uint16
	proxyHelloSignal     chan bool
	destroySignal        chan bool
	stopSignal           chan bool
	startSignal          chan bool
	isStart              bool
	sync.Once
}

func (p *Client) Destroy() error {
	close(p.destroySignal)
	return nil
}

func (p *Client) GetID() (id uint16) {
	p.init()
	return p.cliID
}

func (p *Client) IsStart() bool {
	p.init()
	return p.isStart
}

func (p *Client) Restart() error {
	p.init()
	if p.IsStart() {
		err := p.Stop()
		if err != nil {
			return err
		}
	}
	err := p.Start()
	if err != nil {
		return err
	}
	return nil
}

func (p *Client) Start() (err error) {
	p.init()
	if p.IsStart() {
		return
	}
	if p.startSignal == nil {
		return nil
	}
	startSignal := p.startSignal
	select {
	case <-startSignal:
		// already start , never happen
	default:
		// omit start signal
		p.isStart = true
		p.startSignal = make(chan bool)
		close(startSignal)
	}
	return nil
}
func (p *Client) Stop() (err error) {
	p.init()
	if !p.IsStart() {
		return
	}
	if p.stopSignal == nil {
		return nil
	}
	stopSignal := p.stopSignal
	select {
	case <-stopSignal:
		// already stop , never happen
	default:
		// omit close signal
		p.isStart = false
		p.stopSignal = make(chan bool)
		close(stopSignal)

	}
	return nil
}

// init
func (p *Client) init() {
	p.Do(func() {
		//// init var ////
		p.destroySignal = make(chan bool)
		p.stopSignal = make(chan bool)
		p.startSignal = make(chan bool)
		// temp client id
		p.cliID = p.TempCliID
		// handler

		go func() {
			for {
				select {
				case <-p.destroySignal:
					log.Printf("Client#init : get destroy the client signal , we will destroy the client , cliID is : %v !", p.cliID)
					return
				case <-p.stopSignal: // wanna stop
					log.Printf("Client#init : get stop the client signal , we will stop the client , cliID is : %v !", p.cliID)
					continue
				}
			}
		}()
		go func() {
			for {
				select {
				case <-p.destroySignal:
					log.Printf("Client#init : get destroy the client signal , we will destroy the client , cliID is : %v !", p.cliID)
					return
				case <-p.startSignal: // wanna start
					log.Printf("Client#init : get start the client signal , we will start the client in %vs, cliID is : %v !", restart_interval, p.cliID)
					ticker := time.NewTimer(restart_interval * time.Second)
					select {
					case <-p.stopSignal:
						log.Printf("Client#init : when we wanna start client , but get stop signal , so stop the client , cliID is : %v !", p.cliID)
						continue
					case <-ticker.C:
						go p.listenProxy()
						continue
					}
				}
			}
		}()
		// wait the select
		time.Sleep(500 * time.Millisecond)
	})

}

// closeAndNew
//  close old , new signal
func (p *Client) closeAndNew(signal chan bool) (newSignal chan bool) {
	if signal != nil {
		select {
		case <-signal:
		default:
			close(signal)
		}
	}
	return make(chan bool)
}
func (p *Client) listenProxy() {
	//// print info ////
	i, _ := json.Marshal(p)
	log.Printf("Client#listenProxy : start listen proxy , print client info , cliID is : %v , info is : %v !", p.cliID, string(i))

	//// init var ////
	// reset restart signal
	p.forwardConnRID = sync.Map{}

	//// dial proxy conn ////
	addr := p.ProxyAddr
	c, err := net.Dial("tcp", addr)
	if err != nil {
		log.Printf("Client#listenProxy : dial proxy addr err , cliID is : %v , err is : %v !", p.cliID, err)
		p.Restart()
		return
	}
	p.proxyConn = zpnet.NewIPConn(c)
	p.proxyConn.AddCloseTrigger(func(conn net.Conn) {
		log.Printf("Client#listenProxy : proxy conn is close by self , cliID is : %v  !", p.cliID)
		p.Restart()
	}, &zpnet.ConnCloseTrigger{
		Signal: p.stopSignal,
		Handler: func(conn net.Conn) {
			log.Printf("Client#listenProxy : proxy conn is close by stopSignal , cliID is : %v  !", p.cliID)
			p.proxyConn.Close()
		},
	}, &zpnet.ConnCloseTrigger{
		Signal: p.destroySignal,
		Handler: func(conn net.Conn) {
			log.Printf("Client#listenProxy : proxy conn is close by destroySignal , cliID is : %v  !", p.cliID)
			p.proxyConn.Close()
		},
	})

	log.Printf("Client#listenProxy : dial proxy success , cliID is : %v , proxy addr is : %v !", p.cliID, addr)

	//// receive proxy msg ////
	go func() {
		p.receiveProxyMsg()
	}()

	//// say hello to proxy ////
	// wait some time , then check the proxy hello response
	go func() {
		p.proxyHelloSignal = p.closeAndNew(p.proxyHelloSignal)
		// check
		select {
		case <-time.After(wait_server_hello_time_sec * time.Second):
			log.Printf("Client#listenProxy : can't receive proxy hello in %vs , some reasons as follow : 1. maybe client's zpp version is diff from proxy , 2. maybe client's zppv2 secret is diff from proxy , 3. maybe the data sent to proxy is not right , cliID is : %v !", wait_server_hello_time_sec, p.cliID)
			p.Restart()
			return
		case <-p.stopSignal:
			return
		case <-p.proxyConn.CloseSignal():
			return
		case <-p.proxyHelloSignal:
			return
		case <-p.destroySignal:
			return
		}
	}()
	// send hello zpp
	cID := uint16(0)
	sID := p.newSerialNo()
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	clientWannaProxyPorts := p.ClientWannaProxyPort
	m.ForClientHelloReq([]byte(clientWannaProxyPorts), sID)
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err = p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#listenProxy : say hello to proxy err , cliID is : %v , cID is : %v , sID is : %v , err : %v !", p.cliID, cID, sID, err)
		return
	}
	log.Printf("Client#listenProxy : say hello to proxy success , cliID is : %v , cID is : %v , sID is : %v , proxy addr is : %v !", p.cliID, cID, sID, addr)

}

// receiveProxyMsg
//  监听proxy返回的消息
func (p *Client) receiveProxyMsg() {
	for {
		select {
		case <-p.proxyConn.CloseSignal():
			log.Printf("Client#receiveProxyMsg : get proxy conn close signal , will stop read proxy conn , cliID is : %v !", p.cliID)
			return
		default:
			// parse protocol
			sID := p.newSerialNo()
			length := make([]byte, 4, 4)
			_, err := p.proxyConn.Read(length)
			if err != nil {
				log.Printf("Client#receiveProxyMsg : receive proxy zpp len err , cliID is : %v , err is : %v !", p.cliID, err)
				continue
			}
			zppLength := binary.BigEndian.Uint32(length)
			bs := make([]byte, zppLength, zppLength)
			_, err = io.ReadFull(p.proxyConn, bs)
			if err != nil {
				log.Printf("Client#receiveProxyMsg : receive proxy err , cliID is : %v , err is : %v !", p.cliID, err)
				continue
			}
			m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
			err = m.UnMarshall(bs)
			if err != nil {
				log.Printf("Client#receiveProxyMsg : UnMarshall proxy err , maybe version not right or data is not right , cliID is : %v , err is : %v !", p.cliID, err)
				continue
			}
			cID := m.CID()
			// choose handler
			switch m.Type() {
			case zpp.MSG_TYPE_PROXY_HELLO:
				log.Printf("Client#receiveProxyMsg : receive proxy hello , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyHelloHandler(m, cID, sID)
			case zpp.MSG_TYPE_CONN_CREATE:
				log.Printf("Client#receiveProxyMsg : receive browser conn create , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyCreateBrowserConnHandler(m, cID, sID)
			case zpp.MSG_TYPE_CONN_CLOSE:
				log.Printf("Client#receiveProxyMsg : receive browser conn close , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyCloseBrowserConnHandler(cID, sID)
			case zpp.MSG_TYPE_REQ:
				log.Printf("Client#receiveProxyMsg : receive proxy req , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				// receive proxy req info , we should dispatch the info
				p.proxyReqHandler(m)
			default:
				log.Printf("Client#receiveProxyMsg : receive proxy msg , but can't find type , cliID is : %v !", p.cliID)
			}
		}
	}

}

// proxyReqHandler
func (p *Client) proxyReqHandler(m zpp.Message) {
	cID := m.CID()
	sID := m.SerialId()
	b := m.AttributeByType(zpp.ATTR_TYPE_BODY)
	log.Printf("Client#proxyReqHandler : receive proxy req , cliID is : %v , cID is : %v , sID is : %v , len is : %v !", p.cliID, cID, sID, len(b))
	v, ok := p.forwardConnRID.Load(cID)
	if !ok {
		log.Printf("Client#proxyReqHandler : receive proxy req but no forward conn find , not ok , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	forwardConn, _ := v.(net.Conn)
	if forwardConn == nil {
		log.Printf("Client#proxyReqHandler : receive proxy req but no forward conn find , forwardConnis nil , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	n, err := forwardConn.Write(b)
	if err != nil {
		log.Printf("Client#proxyReqHandler : receive proxy req , cliID is : %v , cID is : %v , sID is : %v , forward err , err : %v !", p.cliID, cID, sID, err)
	}
	log.Printf("Client#proxyReqHandler : from proxy to forward , cliID is : %v , cID is : %v , sID is : %v , len is : %v !", p.cliID, cID, sID, n)
}

// proxyCreateBrowserConnHandler
//  处理proxy回复的conn_create信息
func (p *Client) proxyCreateBrowserConnHandler(m zpp.Message, cID, sID uint16) {
	//// proxy return browser conn create , we should dial forward addr ////
	port := string(m.AttributeByType(zpp.ATTR_TYPE_PORT))
	log.Printf("Client#proxyCreateBrowserConnHandler : accept proxy create browser conn , cliID is : %v , cID is : %v , sID is : %v , port is : %v !", p.cliID, cID, sID, port)
	forwardAddr := p.ClientForwardAddr
	c, err := net.Dial("tcp", forwardAddr)
	if err != nil {
		// if dial fail , tell proxy to close browser net
		log.Printf("Client#proxyCreateBrowserConnHandler : after get proxy browser conn create , dial forward err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err)
		p.sendForwardConnCloseEvent(cID, sID)
		return
	}
	forwardConn := zpnet.NewIPConn(c)
	p.forwardConnRID.Store(cID, forwardConn)

	forwardConn.AddCloseTrigger(func(conn net.Conn) {
		log.Printf("Client#proxyCreateBrowserConnHandler : forward conn is close by self , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		_, ok := p.forwardConnRID.Load(cID)
		if !ok {
			return
		}
		p.forwardConnRID.Delete(cID)
		p.sendForwardConnCloseEvent(cID, sID)
	}, &zpnet.ConnCloseTrigger{
		Signal: p.stopSignal,
		Handler: func(conn net.Conn) {
			log.Printf("Client#proxyCreateBrowserConnHandler : forward conn is close by stopSignal , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
			_, ok := p.forwardConnRID.Load(cID)
			if !ok {
				return
			}
			p.forwardConnRID.Delete(cID)
			p.sendForwardConnCloseEvent(cID, sID)
			forwardConn.Close()
		},
	}, &zpnet.ConnCloseTrigger{
		Signal: p.destroySignal,
		Handler: func(conn net.Conn) {
			log.Printf("Client#proxyCreateBrowserConnHandler : forward conn is close by destroySignal , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
			_, ok := p.forwardConnRID.Load(cID)
			if !ok {
				return
			}
			p.forwardConnRID.Delete(cID)
			p.sendForwardConnCloseEvent(cID, sID)
			forwardConn.Close()
		},
	}, &zpnet.ConnCloseTrigger{
		Signal: p.proxyConn.CloseSignal(),
		Handler: func(conn net.Conn) {
			log.Printf("Client#proxyCreateBrowserConnHandler : forward conn is close by proxyConn.CloseSignal , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
			_, ok := p.forwardConnRID.Load(cID)
			if !ok {
				return
			}
			p.forwardConnRID.Delete(cID)
			p.sendForwardConnCloseEvent(cID, sID)
			forwardConn.Close()
		},
	})

	log.Printf("Client#proxyCreateBrowserConnHandler : dial forward addr success , cliID is : %v , cID is : %v , sID is : %v , forward local address is : %v , forward remote address is : %v !", p.cliID, cID, sID, forwardConn.LocalAddr(), forwardConn.RemoteAddr())

	//// read forward data ////
	bs := make([]byte, 4096, 4096)
	go func() {
		for {
			select {
			case <-forwardConn.CloseSignal():
				log.Printf("Client#proxyCreateBrowserConnHandler : get forward conn close signal , will stop read forward conn , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				return
			default:
				sID = p.newSerialNo()
				log.Printf("Client#proxyCreateBrowserConnHandler : wait receive forward msg , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				n, err := forwardConn.Read(bs)
				if err != nil {
					log.Printf("Client#proxyCreateBrowserConnHandler : read forward data err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err)
					continue
				}
				log.Printf("Client#proxyCreateBrowserConnHandler : receive forward msg , cliID is : %v , cID is : %v , sID is : %v , len is : %v !", p.cliID, cID, sID, n)
				//if n <= 0 {
				//	return
				//}

				m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
				m.ForReq(bs[0:n], p.cliID, cID, sID)
				//marshal
				b := m.Marshall()
				zppLen := make([]byte, 4, 4)
				binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
				b = append(zppLen, b...)
				n, err = p.proxyConn.Write(b)
				if err != nil {
					log.Printf("Client#proxyCreateBrowserConnHandler : write forward's data to proxy err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
					continue
				}
				log.Printf("Client#proxyCreateBrowserConnHandler : from client to proxy msg , cliID is : %v , cID is : %v , sID is : %v , len is : %v !", p.cliID, cID, sID, n)

			}

		}
	}()

	//// notify proxy ////
	p.sendCreateConnDoneEvent(cID, sID)
}
func (p *Client) sendCreateConnDoneEvent(cID, sID uint16) {

	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnCreateDone([]byte{}, p.cliID, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#sendForwardConnCloseEvent : notify proxy conn close err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
		return
	}
	return
}
func (p *Client) sendForwardConnCloseEvent(cID, sID uint16) {

	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnClose([]byte{}, p.cliID, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#sendForwardConnCloseEvent : notify proxy conn close err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
		return
	}
	return
}

// proxyCloseBrowserConnHandler
func (p *Client) proxyCloseBrowserConnHandler(cID, sID uint16) {
	v, ok := p.forwardConnRID.Load(cID)
	if !ok {
		return
	}
	c, _ := v.(net.Conn)
	if c == nil {
		return
	}
	p.forwardConnRID.Delete(cID)
	err := c.Close()
	if err != nil {
		log.Printf("Client#proxyCloseBrowserConnHandler : close forward conn err , cliID is : %v , cID is : %v , sID is : %v , err : %v !", p.cliID, cID, sID, err.Error())
	}
}

// proxyHelloHandler
//  处理proxy返回的hello信息
func (p *Client) proxyHelloHandler(m zpp.Message, cID uint16, sID uint16) {
	close(p.proxyHelloSignal)
	// check err code
	if m.ErrorCode() == zpp.ERROR_CODE_BROWSER_PORT_OCUP {
		log.Printf("Client#proxyHelloHandler : receive browser port be occupied err code , we will restart the client , cliID is : %v , cID is : %v , sID is : %v , errCode is : %v !", p.cliID, cID, sID, m.ErrorCode())
		p.Restart()
		return
	}
	// check err code
	if m.ErrorCode() == zpp.ERROR_CODE_VERSION_NOT_MATCH {
		log.Printf("Client#proxyHelloHandler : receive version not match err code , cliID is : %v , cID is : %v , sID is : %v , errCode is : %v !", p.cliID, cID, sID, m.ErrorCode())
		p.Restart()
		return
	}
	// get client id from proxy response
	cliID, err := strconv.ParseInt(string(m.AttributeByType(zpp.ATTR_TYPE_CLI_ID)), 10, 32)
	if err != nil {
		log.Printf("Client#proxyHelloHandler : accept proxy hello , parse cliID err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
		p.Restart()
		return
	}
	p.cliID = uint16(cliID)

	// check version
	if m.Version() != byte(p.IPPVersion) {
		log.Printf("Client#proxyHelloHandler : accept proxy hello , but zpp version is not right , proxy version is : %v , client version is :%v , cliID is : %v , cID is : %v , sID is : %v !", m.Version(), p.IPPVersion, p.cliID, cID, sID)
		p.Restart()
		return
	}

	// check port
	port := string(m.AttributeByType(zpp.ATTR_TYPE_PORT))
	if port != p.ClientWannaProxyPort {
		log.Printf("Client#proxyHelloHandler : maybe listen port in porxy side be occupied , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		p.Restart()
		return
	}
	log.Printf("Client#proxyHelloHandler : accept proxy hello , and listen port success in porxy side , cliID is : %v , port is : %v , cID is : %v , sID is : %v !", p.cliID, port, cID, sID)

	//// client heart beat msg ////
	go p.hb()
	return
}

//产生随机序列号
func (p *Client) newSerialNo() uint16 {
	atomic.CompareAndSwapInt32(&p.seq, math.MaxUint16, 0)
	atomic.AddInt32(&p.seq, 1)
	return uint16(p.seq)
}

// hb heart beat
func (p *Client) hb() {
	p.sendHB()
	ticker := time.NewTicker(time.Duration(hb_interval_sec) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.proxyConn.CloseSignal():
			log.Printf("Client#hb : will stop send heart beat to proxy , cliID is : %v !", p.cliID)
			return
		case <-ticker.C:
			p.sendHB()
		}
	}
}

// sendHB
func (p *Client) sendHB() {
	sID := p.newSerialNo()
	log.Printf("Client#sendHB : will send heart beat to proxy , cliID is : %v , sID is : %v !", p.cliID, sID)
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnHB(p.cliID, 0, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#sendHB : send heart beat to proxy , cliID is : %v , sID is : %v , err is : %v !", p.cliID, sID, err.Error())
		return
	}
}