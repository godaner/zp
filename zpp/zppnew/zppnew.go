package zppnew

import (
	"github.com/godaner/zp/zpp"
	v1 "github.com/godaner/zp/zpp/v1"
	v2 "github.com/godaner/zp/zpp/v2"
	"strings"
)

func NewMessage(v int, opts ...Option) (m zpp.Message) {
	options := Options{}
	for _, o := range opts {
		o(&options)
	}
	if v == zpp.VERSION_V1 {
		return new(v1.Message)
	}
	if v == zpp.VERSION_V2 {
		m := new(v2.Message)
		if strings.Trim(options.V2Secret," ")==""{
			panic("NewMessage : zpp v2 can't set the secret to empty !")
		}
		m.Secret = options.V2Secret
		return m
	}
	return nil
}
