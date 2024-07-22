package main

import (
	"flag"
	"fmt"
	"webSocks5/client"
	"webSocks5/server"
)

var mode string
var wsServer string

func main() {
	flag.StringVar(&mode, "mode", "server", "mode server or client")
	flag.StringVar(&wsServer, "wsServer", "ws://localhost:1323/ws", "websocket Server")
	flag.Parse()

	if mode == "server" {
		server.Listen()
	} else if mode == "client" {
		client.Listen(client.Config{WsServerAddr: wsServer})
	} else {
		fmt.Println("不支持该运行模式")
	}
}
