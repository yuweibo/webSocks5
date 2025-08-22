package main

import (
	"flag"
	"fmt"
	"webSocks5/pkg/client"
	"webSocks5/pkg/server"
)

var mode string
var wsServer string
var clientSocks5Port int
var clientJwtPrivateKeyFilePath string
var serverPort string
var serverWsAutoClose bool
var serverWsInitSize int
var serverWsMaxSize int

func main() {
	flag.StringVar(&mode, "m", "s", "mode s(server) or c(client)")
	flag.StringVar(&wsServer, "ws", "ws://localhost:1323/ws", "websocket Server")
	flag.IntVar(&clientSocks5Port, "csp", 1080, "client Socks5 Port")
	flag.StringVar(&clientJwtPrivateKeyFilePath, "cjp", "", "client Jwt PrivateKey File Path")
	flag.StringVar(&serverPort, "sp", "1323", "server Socks5 Port,default 1323")
	flag.BoolVar(&serverWsAutoClose, "swa", true, "server webSocket Connection Auto Close Config,default true")
	flag.IntVar(&serverWsInitSize, "swi", 10, "server webSocket Connection Init Size,default 10")
	flag.IntVar(&serverWsMaxSize, "swm", 50, "server webSocket Connection Max Size,default 50")
	flag.Parse()

	if mode == "s" {
		server.Listen(serverPort)
	} else if mode == "c" {
		client.Listen(client.Config{WsServerAddr: wsServer, Socks5Port: clientSocks5Port,
			JwtPrivateKeyFilePath: clientJwtPrivateKeyFilePath, WsAutoClose: serverWsAutoClose,
			WsConnInitSize: serverWsInitSize, WsConnMaxSize: serverWsMaxSize})
	} else {
		fmt.Println("不支持该运行模式")
	}
}
