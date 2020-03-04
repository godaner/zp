package proxy

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
	"math/rand"
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
	hb_interval_sec            = 15
)

type Proxy struct {
	LocalPort      string
	IPPVersion     int
	V2Secret       string
	browserConnRID sync.Map // map[uint16]net.Conn
	seq            int32
	destroySignal  chan bool
	stopSignal     chan bool
	stopSignal1     chan bool
	startSignal1    chan bool
	isStart        bool
	sync.Once
}

func (p *Proxy) IsStart() bool {
	p.init()
	return p.isStart
}

func (p *Proxy) Destroy() error {
	close(p.destroySignal)
	return nil
}

func (p *Proxy) GetID() uint16 {
	return 0
}

func (p *Proxy) Restart() error {
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

func (p *Proxy) Start() (err error) {
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
func (p *Proxy) Stop() (err error) {
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
func (p *Proxy) init() {
	p.Do(func() {
		//// init var ////
		p.destroySignal = make(chan bool)
		p.stopSignal1 = make(chan bool)
		p.stopSignal = make(chan bool)
		p.startSignal1 = make(chan bool)

		//// log ////
		p.browserConnRID = sync.Map{} // map[uint16]net.Conn{}
		go func() {
			for {
				select {
				case <-p.destroySignal:
					log.Printf("Proxy#init : get destroy the proxy signal , we will destroy the proxy !")
					return
				case <-p.stopSignal1:
					log.Printf("Proxy#init : get stop the proxy signal , we will stop the proxy !")
					continue
				}
			}
		}()
		go func() {
			for {
				select {
				case <-p.destroySignal:
					log.Printf("Proxy#init : get destroy the proxy signal , we will destroy the proxy !")
					return
				case <-p.startSignal1: // wanna start
					log.Printf("Proxy#init : get start the proxy signal , we will start the proxy in %vs !", restart_interval)
					ticker := time.NewTimer(restart_interval * time.Second)
					select {
					case <-p.stopSignal1:
						log.Printf("Proxy#init : when we wanna start proxy , but get stop signal , so stop the proxy !")
						continue
					case <-ticker.C:
						go p.startListen()
						continue
					}
				}
			}
		}()

		// wait the select
		time.Sleep(500 * time.Millisecond)
	})

}
func (p *Proxy) startListen() {
	//// print info ////
	i, _ := json.Marshal(p)
	log.Printf("Proxy#startListen : print proxy info , info is : %v !", string(i))
	//// listen client conn ////
	go func() {
		// lis
		addr := ":" + p.LocalPort
		lis, err := net.Listen("tcp", addr)
		if err != nil {
			p.Restart()
			return
		}
		cl := zpnet.NewIPListener(lis)
		cl.AddCloseTrigger(func(listener net.Listener) {
			log.Printf("Proxy#startListen : client listener close by self !")
			p.Restart()
		}, &zpnet.ListenerCloseTrigger{
			Signal: p.stopSignal,
			Handler: func(listener net.Listener) {
				log.Printf("Proxy#startListen : client listener close by stopSignal !")
				listener.Close()
			},
		}, &zpnet.ListenerCloseTrigger{
			Signal: p.destroySignal,
			Handler: func(listener net.Listener) {
				log.Printf("Proxy#startListen : client listener close by destroySignal !")
				listener.Close()
			},
		})
		log.Printf("Proxy#startListen : local addr is : %v !", addr)

		// accept conn
		p.acceptClientConn(cl)

		log.Println("Progress#startListen : stop the client success !")
	}()
}

// acceptClientConn
func (p *Proxy) acceptClientConn(cl *zpnet.IPListener) {
	for {
		select {
		case <-cl.CloseSignal():
			log.Println("Proxy#acceptClientConn : stop client accept !")
			return
		default:
			c, err := cl.Accept()
			if err != nil {
				log.Printf("Proxy#acceptClientConn : accept client conn err , err is : %v !", err.Error())
				continue
			}

			// client conn
			cc := c.(*zpnet.IPConn)
			// set hb interval and check client heart beat
			cc.SetHeartBeatInterval(time.Duration(hb_interval_sec) * time.Second)
			go p.checkClientHB(cc)
			// add trigger
			cc.AddCloseTrigger(func(conn net.Conn) {
				log.Printf("Proxy#acceptClientConn : client conn close by self !")
			}, &zpnet.ConnCloseTrigger{
				Signal: cl.CloseSignal(),
				Handler: func(conn net.Conn) {
					log.Printf("Proxy#acceptClientConn : client conn close by client listener closeSignal !")
					conn.Close()
				},
			}, &zpnet.ConnCloseTrigger{
				Signal: p.stopSignal,
				Handler: func(conn net.Conn) {
					log.Printf("Proxy#acceptClientConn : client conn close by client stopSignal !")
					conn.Close()
				},
			}, &zpnet.ConnCloseTrigger{
				Signal: p.destroySignal,
				Handler: func(conn net.Conn) {
					log.Printf("Proxy#acceptClientConn : client conn close by client destroySignal !")
					conn.Close()
				},
			})
			go p.receiveClientMsg(cc)
		}
	}
}

// receiveClientMsg
//  接受client消息
func (p *Proxy) receiveClientMsg(clientConn *zpnet.IPConn) {
	for {
		select {
		case <-clientConn.CloseSignal():
			log.Println("Proxy#receiveClientMsg : get client conn close signal , we will stop read client conn !")
			return
		default:
			// parse protocol
			length := make([]byte, 4, 4)
			_, err := clientConn.Read(length)
			if err != nil {
				log.Printf("Proxy#receiveClientMsg : read zpp len info from client err , err is : %v !", err.Error())
				continue
			}
			zppLength := binary.BigEndian.Uint32(length)
			bs := make([]byte, zppLength, zppLength)
			_, err = io.ReadFull(clientConn, bs)
			if err != nil {
				log.Printf("Proxy#receiveClientMsg : read info from client err , err is : %v !", err.Error())
				continue
			}
			m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
			err = m.UnMarshall(bs)
			if err != nil {
				log.Printf("Proxy#receiveClientMsg : UnMarshall proxy err , some reasons as follow : 1. maybe client's zpp version is diff from proxy , 2. maybe client's zppv2 secret is diff from proxy , 3. maybe the data sent to proxy is not right , err is : %v !", err)
				continue
			}
			cID := m.CID()
			sID := m.SerialId()
			cliID := m.CliID()
			// choose handler
			switch m.Type() {
			case zpp.MSG_TYPE_CLIENT_HELLO:
				log.Printf("Proxy#receiveClientMsg : receive client hello , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
				// receive client hello , we should listen the client_wanna_proxy_port , and dispatch browser data to this client.
				p.clientHelloHandler(clientConn, cliID, sID)
			case zpp.MSG_TYPE_REQ:
				log.Printf("Proxy#receiveClientMsg : receive client req , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
				// receive client req , we should judge the client port , and dispatch the data to all browser who connect to this port.
				p.clientReqHandler(clientConn, m)
			case zpp.MSG_TYPE_CONN_CLOSE:
				log.Printf("Proxy#receiveClientMsg : receive client conn close , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
				p.clientConnCloseHandler(clientConn, m)
			case zpp.MSG_TYPE_CONN_CREATE:
				log.Printf("Proxy#receiveClientMsg : receive client conn create , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
				p.clientConnCreateHandler(clientConn, m)
			case zpp.MSG_TYPE_CONN_HB:
				log.Printf("Proxy#receiveClientMsg : receive client heart beat , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
				p.clientHBHandler(clientConn, m)
			}
		}
	}

}

// clientHelloHandler
//  处理client发送过来的hello
func (p *Proxy) clientHelloHandler(clientConn *zpnet.IPConn, cliID, sID uint16) {
	// say hello to client
	p.sayHello(clientConn, sID)
}

// sayHello
func (p *Proxy) sayHello(clientConn net.Conn, sID uint16) {
	// return client hello
	cliID := p.newCID()
	errCode := byte(0)
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForServerHelloReq([]byte(strconv.FormatInt(int64(cliID), 10)), sID, errCode)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := clientConn.Write(b)
	if err != nil {
		log.Printf("Proxy#sayHello : return client hello err , cliID is : %v , err is : %v !", cliID, err.Error())
		return
	}
	log.Printf("Proxy#sayHello : say hello to client success , cliID is : %v , sID is : %v !", cliID, sID)
}

//产生随机序列号
func (p *Proxy) newSerialNo() uint16 {
	atomic.CompareAndSwapInt32(&p.seq, math.MaxUint16, 0)
	atomic.AddInt32(&p.seq, 1)
	return uint16(p.seq)
}

//产生随机序列号
func (p *Proxy) newCID() uint16 {
	rand.Seed(time.Now().UnixNano())
	r := rand.Intn(math.MaxUint16)
	return uint16(r)
}

// clientHBHandler
func (p *Proxy) clientHBHandler(conn *zpnet.IPConn, message zpp.Message) {
	conn.ResetHeartBeatTimer()
}

// checkClientHB
func (p *Proxy) checkClientHB(clientConn *zpnet.IPConn) {
	<-clientConn.GetHeartBeatTimer().C
	log.Printf("Proxy#checkClientHB : not receive the client heart beat , client info is : %v->%v !", clientConn.LocalAddr(), clientConn.RemoteAddr())
	clientConn.Close()
}

func (p *Proxy) getTargetInfo(b []byte) (method, host, address string) {
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

func (p *Proxy) clientConnCloseHandler(clientConn *zpnet.IPConn, message zpp.Message) {
	cID := message.CID()
	cliID := message.CliID()
	sID := message.SerialId()
	c, ok := p.browserConnRID.Load(cID)
	if !ok {
		log.Printf("Proxy#clientConnCloseHandler : can't find browser conn , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	if c == nil {
		log.Printf("Proxy#clientConnCloseHandler : get nil browser conn , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	conn, _ := c.(*zpnet.IPConn)
	if conn == nil {
		log.Printf("Proxy#clientConnCloseHandler : parse browser conn to zpnet.IPConn fail , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	conn.Close()
}

// clientReqHandler
func (p *Proxy) clientReqHandler(clientConn net.Conn, m zpp.Message) {
	cID := m.CID()
	cliID := m.CliID()
	sID := m.SerialId()
	data := m.AttributeByType(zpp.ATTR_TYPE_BODY)
	c, ok := p.browserConnRID.Load(cID)
	if !ok {
		log.Printf("Proxy#clientReqHandler : can't find browser conn , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	if c == nil {
		log.Printf("Proxy#clientReqHandler : get nil browser conn , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	conn, _ := c.(*zpnet.IPConn)
	if conn == nil {
		log.Printf("Proxy#clientReqHandler : parse browser conn to zpnet.IPConn fail , cliID is : %v , sID is : %v !", cliID, sID)
		return
	}
	conn.Write(data)
}

func (p *Proxy) clientConnCreateHandler(clientConn *zpnet.IPConn, message zpp.Message) {
	go func() {
		cID := message.CID()
		cliID := message.CliID()
		sID := message.SerialId()
		body := message.AttributeByType(zpp.ATTR_TYPE_BODY)
		method, host, address := p.getTargetInfo(body)
		log.Printf("Proxy#clientConnCreateHandler : receive info , cliID is : %v , sID is : %v , method is : %v , host is : %v , address is : %v , msg is :%v !", cliID, sID, method, host, address, string(body))
		if method == "CONNECT" {
			//获得了请求的host和port，就开始拨号吧
			log.Printf("Proxy#clientConnCreateHandler : dial web start , cliID is : %v , sID is : %v , method is : %v , host is : %v , address is : %v !", cliID, sID, method, host, address)
			c, err := net.Dial("tcp", address)
			if err != nil {
				log.Printf("Proxy#clientConnCreateHandler : dial web err , cliID is : %v , sID is : %v , method is : %v , host is : %v , address is : %v , err is : %v !", cliID, sID, method, host, address, err.Error())
				return
			}
			log.Printf("Proxy#clientConnCreateHandler : dial web success , cliID is : %v , sID is : %v , method is : %v , host is : %v , address is : %v !", cliID, sID, method, host, address)
			p.sendConnCreateDoneEvent(clientConn, cliID, cID, sID)
			browserConn := zpnet.NewIPConn(c)
			browserConn.AddCloseTrigger(func(conn net.Conn) {
				log.Printf("Proxy#clientConnCreateHandler : browser conn close by self !")
				sID := p.newSerialNo()
				p.sendConnCloseEvent(clientConn, cliID, cID, sID)
				conn.Close()
			}, &zpnet.ConnCloseTrigger{
				Signal: p.stopSignal,
				Handler: func(conn net.Conn) {
					log.Printf("Proxy#clientConnCreateHandler : browser conn close by client stopSignal !")
					conn.Close()
				},
			}, &zpnet.ConnCloseTrigger{
				Signal: p.destroySignal,
				Handler: func(conn net.Conn) {
					log.Printf("Proxy#clientConnCreateHandler : browser conn close by client destroySignal !")
					conn.Close()
				},
			})
			p.browserConnRID.Store(cID, browserConn)
			go func() {
				bs := make([]byte, 4096, 4096)
				for {
					select {
					case <-browserConn.CloseSignal():
						log.Printf("Proxy#clientConnCreateHandler : browser conn close signal !")
						return
					default:
						n, err := browserConn.Read(bs)
						if err != nil {
							log.Println(err)
							return
						}
						sID := p.newSerialNo()
						log.Printf("Proxy#clientConnCreateHandler : get web info , cliID is : %v , sID is : %v , data len is : %v !", cliID, sID, len(bs[0:n]))
						m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
						m.ForReq(bs[0:n], cliID, cID, sID)
						//marshal
						b := m.Marshall()
						zppLen := make([]byte, 4, 4)
						binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
						b = append(zppLen, b...)
						_, err = clientConn.Write(b)
						if err != nil {
							log.Printf("Proxy#clientConnCreateHandler : send web info to client app , cliID is : %v , sID is : %v , err is : %v !", cliID, sID, err.Error())
							return
						}

					}
				}
			}()

		}
	}()

}

func (p *Proxy) sendConnCreateDoneEvent(clientConn *zpnet.IPConn, cliID, cID, sID uint16) {
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnCreateDone([]byte{}, 0, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := clientConn.Write(b)
	if err != nil {
		log.Printf("Proxy#sendConnCreateDoneEvent : write data to proxy conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", cliID, cID, sID, err.Error())
	}
	log.Printf("Proxy#sendConnCreateDoneEvent : notify conn create done , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)

}

// sendConnCloseEvent
func (p *Proxy) sendConnCloseEvent(clientConn *zpnet.IPConn, cliID, cID uint16, sID uint16) {
	m := zppnew.NewMessage(p.IPPVersion, zppnew.SetV2Secret(p.V2Secret))
	m.ForConnClose([]byte{}, 0, cID, sID)
	//marshal
	b := m.Marshall()
	zppLen := make([]byte, 4, 4)
	binary.BigEndian.PutUint32(zppLen, uint32(len(b)))
	b = append(zppLen, b...)
	_, err := clientConn.Write(b)
	if err != nil {
		log.Printf("Proxy#sendConnCloseEvent : write data to proxy conn err , cliID is : %v , cID is : %v , sID is : %v , err is : %v !", cliID, cID, sID, err.Error())
	}
	log.Printf("Proxy#sendConnCloseEvent : notify conn close , cliID is : %v , cID is : %v , sID is : %v !", cliID, cID, sID)
}
