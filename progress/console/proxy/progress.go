package proxy

import (
	"github.com/godaner/zp/endpoint"
	"github.com/godaner/zp/endpoint/proxy"
	"log"
)

type Progress struct {
}

func (p *Progress) Launch() (err error) {
	// log
	log.SetFlags(log.Lmicroseconds)
	// config
	c := new(Config)
	var pry endpoint.Endpoint
	c.SetUpdateEventHandler(func(c *Config) {
		if pry != nil {
			err := pry.Destroy()
			if err != nil {
				log.Printf("Progress#Launch : destroy proxy err , err is : %v !", err.Error())
			}
		}
		pry = &proxy.Proxy{
			LocalPort:  c.LocalPort,
			IPPVersion: c.IPPVersion,
			V2Secret:   c.V2Secret,
		}
		go func() {
			err := pry.Start()
			if err != nil {
				log.Printf("Progress#Launch : start proxy err , err is : %v !", err.Error())
			}
		}()
	})

	return nil
}
