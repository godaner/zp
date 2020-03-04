package client

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	zpnet "github.com/godaner/zp/net"
	"github.com/godaner/zp/zpp"
	"github.com/godaner/zp/zpp/zppnew"
	"io"
	"log"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
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
	ProxyAddr         string
	IPPVersion        int
	LocalProxyPort    string
	TempCliID         uint16
	V2Secret          string
	appConnMap        *sync.Map
	proxyConn         *zpnet.IPConn
	seq               int32
	cliID             uint16
	proxyHelloSignal  chan bool
	destroySignal     chan bool
	stopSignal        chan bool // notify child
	stopSignal1        chan bool
	startSignal1       chan bool
	isStart           bool
	connCreateDoneBus map[uint16]chan bool
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
	if p.startSignal1 == nil {
		return nil
	}
	select {
	case <-p.startSignal1:
		// already start , never happen
	default:
		// omit start signal
		p.isStart = true
		p.startSignal1<-true
		p.stopSignal=make(chan bool)
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
	select {
	case <-p.stopSignal1:
		// already stop , never happen
	default:
		// omit close signal
		p.isStart = false
		p.stopSignal1<-true
		close(p.stopSignal)
	}
	return nil
}

// init
func (p *Client) init() {
	p.Do(func() {
		//// init var ////
		p.destroySignal = make(chan bool)
		p.stopSignal = make(chan bool)
		p.stopSignal1 = make(chan bool)
		p.startSignal1 = make(chan bool)
		p.appConnMap = &sync.Map{}
		p.connCreateDoneBus = map[uint16]chan bool{}
		// temp client id
		p.cliID = p.TempCliID
		// handler

		go func() {
			for {
				select {
				case <-p.destroySignal:
					log.Printf("Client#init : get destroy the client signal , we will destroy the client , cliID is : %v !", p.cliID)
					return
				case <-p.stopSignal1: // wanna stop
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
				case <-p.startSignal1: // wanna start
					log.Printf("Client#init : get start the client signal , we will start the client in %vs, cliID is : %v !", restart_interval, p.cliID)
					ticker := time.NewTimer(restart_interval * time.Second)
					select {
					case <-p.stopSignal1:
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
	p.appConnMap = &sync.Map{}
	p.connCreateDoneBus = map[uint16]chan bool{}

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
	m.ForClientHelloReq(sID)
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
			case zpp.MSG_TYPE_REQ:
				log.Printf("Client#receiveProxyMsg : receive proxy req , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyReqHandler(m)
			case zpp.MSG_TYPE_CONN_CLOSE:
				log.Printf("Client#receiveProxyMsg : receive proxy conn close , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyConnCloseHandler(m)
			case zpp.MSG_TYPE_CONN_CREATE_DONE:
				log.Printf("Client#receiveProxyMsg : receive proxy conn create done , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
				p.proxyConnCreateDoneHandler(m)
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
	v, ok := p.appConnMap.Load(cID)
	if !ok {
		log.Printf("Client#proxyReqHandler : receive proxy req but no forward conn find , not ok , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	appConn, _ := v.(net.Conn)
	if appConn == nil {
		log.Printf("Client#proxyReqHandler : receive proxy req but no forward conn find , appConnis nil , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	n, err := appConn.Write(b)
	if err != nil {
		log.Printf("Client#proxyReqHandler : receive proxy req , cliID is : %v , cID is : %v , sID is : %v , forward err , err : %v !", p.cliID, cID, sID, err)
	}
	log.Printf("Client#proxyReqHandler : from proxy to forward , cliID is : %v , cID is : %v , sID is : %v , len is : %v !", p.cliID, cID, sID, n)
}

// proxyHelloHandler
//  处理proxy返回的hello信息
func (p *Client) proxyHelloHandler(m zpp.Message, cID uint16, sID uint16) {
	close(p.proxyHelloSignal)

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
	log.Printf("Client#proxyHelloHandler : accept proxy hello , and listen port success in porxy side , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)

	// client listen app
	go p.listenApp()
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

//产生随机序列号
func (p *Client) newClientConnID() uint16 {
	atomic.CompareAndSwapInt32(&p.seq, math.MaxUint16, 0)
	atomic.AddInt32(&p.seq, 1)
	return uint16(p.seq + int32(p.cliID))
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

// listenApp
func (p *Client) listenApp() {
	l, err := net.Listen("tcp", ":"+fmt.Sprint(p.LocalProxyPort))
	if err != nil {
		log.Printf("Client#listenApp : local proxy port listener  err , cliID is : %v !", p.cliID)
		p.Restart()
		return
	}
	appLis := zpnet.NewIPListener(l)
	appLis.AddCloseTrigger(func(listener net.Listener) {
		log.Printf("Client#listenApp : app listener is close by self , cliID is : %v  !", p.cliID)
		p.Restart()
	}, &zpnet.ListenerCloseTrigger{
		Signal: p.stopSignal,
		Handler: func(l net.Listener) {
			log.Printf("Client#listenApp : app listener is close by stopSignal , cliID is : %v !", p.cliID)
			p.Restart()
		},
	}, &zpnet.ListenerCloseTrigger{
		Signal: p.destroySignal,
		Handler: func(l net.Listener) {
			log.Printf("Client#listenApp : app listener is close by destroySignal , cliID is : %v !", p.cliID)
			p.Restart()
		},
	})
	for {
		select {
		case <-appLis.CloseSignal():
			log.Printf("Client#handleAppRequest : get app listener close signal , will stop accput listener conn , cliID is : %v !", p.cliID)
			return
		default:
			c, err := appLis.Accept()
			if err != nil {
				log.Printf("Client#listenApp : app listener accept conn err , cliID is : %v ,err is : %v !", p.cliID, err.Error())
				return
			}

			// save app conn
			appConn := zpnet.NewIPConn(c)
			cID := p.newClientConnID()
			p.appConnMap.Store(cID, appConn)

			// add trigger
			appConn.AddCloseTrigger(func(conn net.Conn) {
				log.Printf("Client#listenApp : app conn is close by self , cliID is : %v  !", p.cliID)
				sID:=p.newSerialNo()
				p.sendConnCloseEvent(cID,sID)
				appConn.Close()
			}, &zpnet.ConnCloseTrigger{
				Signal: appLis.CloseSignal(),
				Handler: func(conn net.Conn) {
					log.Printf("Client#listenApp : app conn is close by app listener closeSignal , cliID is : %v  !", p.cliID)
					appConn.Close()
				},
			}, &zpnet.ConnCloseTrigger{
				Signal: p.stopSignal,
				Handler: func(conn net.Conn) {
					log.Printf("Client#listenApp : app conn is close by stopSignal , cliID is : %v  !", p.cliID)
					appConn.Close()
				},
			}, &zpnet.ConnCloseTrigger{
				Signal: p.destroySignal,
				Handler: func(conn net.Conn) {
					log.Printf("Client#listenApp : app conn is close by destroySignal , cliID is : %v  !", p.cliID)
					appConn.Close()
				},
			})
			go p.handleAppConnRequest(appConn, cID)
		}

	}
}

// handleAppConnRequest
func (p *Client) handleAppConnRequest(appConn *zpnet.IPConn, cID uint16) {
	log.Printf("Client#handleAppConnRequest : handle client req , cliID is : %v , cID is : %v !", p.cliID, cID)
	connected := false
	bs := make([]byte, 10240, 10240)
	for {
		select {
		case <-appConn.CloseSignal():
			log.Printf("Client#handleAppConnRequest : get app conn close signal , will stop read app conn , cliID is : %v , cID is : %v !", p.cliID, cID)
			return
		default:
			n, err := appConn.Read(bs)
			sID := p.newSerialNo()
			if err != nil {
				log.Printf("Client#handleAppConnRequest : read app conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
				return
			}
			if !connected {
				method, _, address := p.getTargetInfo(bs[0:n])
				if method == "CONNECT" && address != "" {
					log.Printf("Client#handleAppConnRequest : will create conn , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
					p.sendConnCreateEvent(bs[0:n], cID, sID)
					connected = true
					bus := make(chan bool)
					p.connCreateDoneBus[cID] = bus
					<-bus
					appConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
					log.Printf("Client#handleAppConnRequest : create conn success , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
					continue
				}
			}
			m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
			m.ForReq(bs[0:n], p.cliID, cID, sID)
			//marshal
			b := m.Marshall()
			zppLen := make([]byte, 4, 4)
			binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
			b = append(zppLen, b...)
			_, err = p.proxyConn.Write(b)
			if err != nil {
				log.Printf("Client#handleAppConnRequest : write data to proxy conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
				return
			}
			log.Printf("Client#handleAppConnRequest : send data to proxy , cliID is : %v , cID is : %v , sID is : %v , data len is : %v !", p.cliID, cID, sID, len(bs[0:n]))
		}
	}

}

func (p *Client) getTargetInfo(b []byte) (method, host, address string) {
	defer func() {
		if err := recover(); err != nil {
			log.Printf("Proxy#getTargetInfo : panic is : %v !", err)
		}
	}()
	log.Printf("Proxy#getTargetInfo : info is : %v !", string(b))
	fmt.Sscanf(string(b[:bytes.IndexByte(b[:], '\n')]), "%s%s", &method, &host)
	hostPortURL, err := url.Parse(host)
	if err != nil {
		log.Println(err)
		return
	}

	if hostPortURL.Opaque == "443" { //https访问
		address = hostPortURL.Scheme + ":443"
	} else { //http访问
		if strings.Index(hostPortURL.Host, ":") == -1 { //host不带端口， 默认80
			address = hostPortURL.Host + ":80"
		} else {
			address = hostPortURL.Host
		}
	}
	return method, host, address
}

// sendConnCreateEvent
func (p *Client) sendConnCreateEvent(bs []byte, cID uint16, sID uint16) {
	log.Printf("Client#sendConnCreateEvent : send conn create event to proxy , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnCreate(bs, p.cliID, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#sendConnCreateEvent : write data to proxy conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
	}
}

// sendConnCloseEvent
func (p *Client) sendConnCloseEvent(cID uint16, sID uint16) {
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnClose([]byte{}, p.cliID, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := p.proxyConn.Write(b)
	if err != nil {
		log.Printf("Client#sendConnCloseEvent : write data to proxy conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", p.cliID, cID, sID, err.Error())
	}
	log.Printf("Client#sendConnCloseEvent : notify conn close , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)

}

func (p *Client) proxyConnCloseHandler(message zpp.Message) {
	cID, sID := message.CID(), message.SerialId()
	v, ok := p.appConnMap.Load(cID)
	if !ok {
		log.Printf("Client#proxyConnCloseHandler : receive proxy conn close but no forward conn find , not ok , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	appConn, _ := v.(net.Conn)
	if appConn == nil {
		log.Printf("Client#proxyConnCloseHandler : receive proxy conn close but no forward conn find , appConnis nil , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	appConn.Close()
}

// proxyConnCreateDoneHandler
func (p *Client) proxyConnCreateDoneHandler(m zpp.Message) {
	cID := m.CID()
	sID := m.SerialId()
	bus := p.connCreateDoneBus[cID]
	if bus == nil {
		log.Printf("Client#proxyConnCreateDoneHandler : not bus to find , appConnis nil , cliID is : %v , cID is : %v , sID is : %v !", p.cliID, cID, sID)
		return
	}
	bus <- true
}
