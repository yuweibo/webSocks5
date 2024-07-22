package main

import (
	"flag"
	"fmt"
	"webSocks5/client"
	"webSocks5/server"
)

var mode string

func main() {
	flag.StringVar(&mode, "mode", "server", "mode server or client")
	flag.Parse()

	if mode == "server" {
		server.Listen()
	} else if mode == "client" {
		client.Listen()
	} else {
		fmt.Println("不支持该运行模式")
	}
}
