package main

import (
	"github.com/godaner/zp/progress/console/client"
	"log"
)

func main() {
	p := new(client.Progress)
	err := p.Launch()
	if err != nil {
		log.Printf("mian : progress Start err , err is : %v !", err.Error())
		return
	}
	f := make(chan int, 1)
	<-f
}
