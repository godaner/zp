package client

import (
	"github.com/godaner/zp/endpoint"
	"github.com/godaner/zp/endpoint/client"
	"log"
)

// Progress
type Progress struct {
}

func (p *Progress) Launch() (err error) {
	// log
	log.SetFlags(log.Lmicroseconds)

	c := new(Config)
	var cli endpoint.Endpoint
	cliID := uint16(0)
	c.SetUpdateEventHandler(func(c *Config) {
		// stop first
		if cli != nil {
			cli.Destroy()
			cli = nil
		}
		cli := &client.Client{
			ProxyAddr:      c.ProxyAddr,
			IPPVersion:     c.IPPVersion,
			LocalProxyPort: c.LocalProxyPort,
			TempCliID:      cliID,
			V2Secret:       c.V2Secret,
		}
		go func() {
			err := cli.Start()
			if err != nil {
				log.Printf("Progress#Launch : start client err , err is : %v !", err.Error())
			}
		}()
	})
	return nil
}
