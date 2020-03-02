package v1

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/godaner/zp/zpp"
	"github.com/godaner/zp/zpp/v2/encrypt"
	"log"
)

// Header
type Header struct {
	HVersion   byte
	HType      byte
	HSerialNo  uint16
	HCID       uint16
	HCliID     uint16
	HErrorCode byte
	HAttrNum   byte
}

func (h *Header) ErrorCode() byte {
	return h.HErrorCode
}

func (h *Header) SerialNo() uint16 {
	return h.HSerialNo
}

func (h *Header) CliID() uint16 {
	return h.HCliID
}

func (h *Header) Version() byte {
	return h.HVersion
}

func (h *Header) Type() byte {
	return h.HType
}

func (h *Header) CID() uint16 {
	return h.HCID
}

func (h *Header) AttrNum() byte {
	return h.HAttrNum
}

// Attr
type Attr struct {
	AT byte
	AL uint16
	AV []byte
}

func (a *Attr) T() byte {
	return a.AT
}

func (a *Attr) L() uint16 {
	return a.AL
}

func (a *Attr) V() []byte {
	return a.AV
}

// Message
type Message struct {
	Header   Header
	Attr     []Attr
	AttrMaps map[byte][]byte
	Secret   string
}

func (m *Message) ErrorCode() byte {
	return m.Header.ErrorCode()
}

func (m *Message) Version() byte {
	return m.Header.Version()
}

func (m *Message) CliID() uint16 {
	return m.Header.CliID()
}

func (m *Message) SerialId() uint16 {
	return m.Header.SerialNo()
}

func (m *Message) AttributeByType(t byte) []byte {
	return m.AttrMaps[t]
}

func (m *Message) Type() byte {
	return m.Header.Type()
}

func (m *Message) CID() uint16 {
	return m.Header.CID()
}
func (m *Message) Attribute(index int) zpp.Attr {
	return &m.Attr[index]
}
func (m *Message) Marshall() []byte {
	defer func() {
		if info := recover(); info != nil {
			log.Printf("Message#Marshall : panic err , err is : %v !", info)
		}
	}()
	buf := new(bytes.Buffer)
	var err error
	err = binary.Write(buf, binary.BigEndian, m.Header.HVersion)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.Version err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HType)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.Type err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HSerialNo)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.SerialNo err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HCID)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.HCID err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HCliID)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.HCliID err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HErrorCode)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.HErrorCode err , err is : %v !", err.Error())
	}
	err = binary.Write(buf, binary.BigEndian, m.Header.HAttrNum)
	if err != nil {
		log.Printf("Message#Marshall : binary.Write m.Header.AttrNum err , err is : %v !", err.Error())
	}
	for _, v := range m.Attr {
		err = binary.Write(buf, binary.BigEndian, v.AT)
		if err != nil {
			log.Printf("Message#Marshall : binary.Write m.Header.AttrType err , err is : %v !", err.Error())
		}
		//be careful
		err = binary.Write(buf, binary.BigEndian, v.AL+3)
		if err != nil {
			log.Printf("Message#Marshall : binary.Write m.Header.AttrLen err , err is : %v !", err.Error())
		}
		err = binary.Write(buf, binary.BigEndian, v.AV)
		if err != nil {
			log.Printf("Message#Marshall : binary.Write m.Header.AttrStr err , err is : %v !", err.Error())
		}
	}
	b, err := encrypt.AesCBCEncrypt(buf.Bytes(), []byte(m.Secret))
	if err != nil {
		log.Printf("Message#Marshall : encrypt err , err is : %v !", err.Error())
	}
	return b
}

func (m *Message) UnMarshall(message []byte) (err error) {
	defer func() {
		if info := recover(); info != nil {
			log.Printf("Message#UnMarshall : panic err , err is : %v !", info)
			err = errors.New(fmt.Sprint(info))
		}
	}()
	b, err := encrypt.AesCBCDncrypt(message, []byte(m.Secret))
	if err != nil {
		log.Printf("Message#UnMarshall : dncrypt err , err is : %v !", err.Error())
		return err
	}
	buf := bytes.NewBuffer(b)
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HVersion); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HVersion err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HType); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HType err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HSerialNo); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HSerialNo err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HCID); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HCID err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HCliID); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HCliID err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HErrorCode); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HErrorCode err , err is : %v !", err.Error())
		return err
	}
	if err := binary.Read(buf, binary.BigEndian, &m.Header.HAttrNum); err != nil {
		log.Printf("Message#UnMarshall : binary.Readm.Header.HAttrNum err , err is : %v !", err.Error())
		return err
	}

	m.Attr = make([]Attr, m.Header.AttrNum())
	m.AttrMaps = make(map[byte][]byte)
	for i := byte(0); i < m.Header.AttrNum(); i++ {
		attr := &m.Attr[i]
		err := binary.Read(buf, binary.BigEndian, &attr.AT)
		if err != nil {
			log.Printf("Message#UnMarshall : binary.Read 0 err , err is : %v !", err.Error())
			return err
		}
		err = binary.Read(buf, binary.BigEndian, &attr.AL)
		if err != nil {
			log.Printf("Message#UnMarshall : binary.Read 1 err , err is : %v !", err.Error())
			return err
		}
		attr.AL -= 3 //be careful
		attr.AV = make([]byte, attr.AL)
		if err := binary.Read(buf, binary.BigEndian, &attr.AV); err != nil {
			log.Printf("Message#UnMarshall : binary.Read 2 err , err is : %v !", err.Error())
			return err
		}
		m.AttrMaps[attr.AT] = attr.AV

	}
	return nil
}
func (m *Message) ForConnHB(cliID, cID, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_CONN_HB, cliID, cID, sID, 0)
}
func (m *Message) ForConnCreateDone(body []byte, cliID, cID, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_CONN_CREATE_DONE, cliID, cID, sID, 0)
}
func (m *Message) ForConnCreate(body []byte, cliID, cID, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_CONN_CREATE, cliID, cID, sID, 0)
	m.Attr = []Attr{
		{
			AT: zpp.ATTR_TYPE_PORT, AL: uint16(len(body)), AV: body,
		},
	}
	m.Header.HAttrNum = byte(len(m.Attr))
}

func (m *Message) ForConnClose(body []byte, cliID, cID, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_CONN_CLOSE, cliID, cID, sID, 0)
}

func (m *Message) ForClientHelloReq(port []byte, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_CLIENT_HELLO, 0, 0, sID, 0)
	m.Attr = []Attr{
		{
			AT: zpp.ATTR_TYPE_PORT, AL: uint16(len(port)), AV: port,
		},
	}
	m.Header.HAttrNum = byte(len(m.Attr))
}

func (m *Message) ForServerHelloReq(cliID []byte, port []byte, sID uint16, errCode byte) {
	m.newMessage(zpp.MSG_TYPE_PROXY_HELLO, 0, 0, sID, errCode)
	m.Attr = []Attr{
		{
			AT: zpp.ATTR_TYPE_PORT, AL: uint16(len(port)), AV: port,
		},
		{
			AT: zpp.ATTR_TYPE_CLI_ID, AL: uint16(len(cliID)), AV: cliID,
		},
	}
	m.Header.HAttrNum = byte(len(m.Attr))
}
func (m *Message) ForReq(body []byte, cliID, cID, sID uint16) {
	m.newMessage(zpp.MSG_TYPE_REQ, cliID, cID, sID, 0)
	m.Attr = []Attr{
		{
			AT: zpp.ATTR_TYPE_BODY, AL: uint16(len(body)), AV: body,
		},
	}
	m.Header.HAttrNum = byte(len(m.Attr))
}

func (m *Message) newMessage(typ byte, cliID, cID, sID uint16, errCode byte) {
	header := Header{}
	header.HVersion = zpp.VERSION_V2
	header.HType = typ
	header.HSerialNo = sID
	header.HCID = cID
	header.HCliID = cliID
	header.HErrorCode = errCode
	m.Header = header
}
