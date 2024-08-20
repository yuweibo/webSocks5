package main

import (
	"flag"
	"fmt"
	"webSocks5/client"
	"webSocks5/server"
)

var mode string
var wsServer string
var clientSocks5Port int
var clientJwtPrivateKeyFilePath string

func main() {
	flag.StringVar(&mode, "m", "s", "mode s(server) or c(client)")
	flag.StringVar(&wsServer, "ws", "ws://localhost:8080/ws", "websocket Server")
	flag.IntVar(&clientSocks5Port, "csp", 1080, "client Socks5 Port")
	flag.StringVar(&clientJwtPrivateKeyFilePath, "cjp", "", "client Jwt PrivateKey File Path")
	flag.Parse()

	if mode == "s" {
		server.Listen()
	} else if mode == "c" {
		client.Listen(client.Config{WsServerAddr: wsServer, Socks5Port: clientSocks5Port, JwtPrivateKeyFilePath: clientJwtPrivateKeyFilePath})
	} else {
		fmt.Println("不支持该运行模式")
	}
}
