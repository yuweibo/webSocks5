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
var clientProxyDomainFilePath string
var forceWsProxy bool
var serverPort string

func main() {
	flag.StringVar(&mode, "m", "s", "mode s(server) or c(client)")
	flag.StringVar(&wsServer, "ws", "ws://localhost:1323/ws", "websocket Server")
	flag.IntVar(&clientSocks5Port, "csp", 1080, "client Socks5 Port")
	flag.StringVar(&clientJwtPrivateKeyFilePath, "cjp", "", "client Jwt PrivateKey File Path")
	flag.StringVar(&clientProxyDomainFilePath, "dp", "", "client Proxy Domain File Path")
	flag.BoolVar(&forceWsProxy, "fwp", true, "流量是否全部走webSocket")
	flag.StringVar(&serverPort, "sp", "1323", "server Socks5 Port,default 1323")
	flag.Parse()

	if mode == "s" {
		server.Listen(serverPort)
	} else if mode == "c" {
		client.Listen(client.Config{WsServerAddr: wsServer, Socks5Port: clientSocks5Port, JwtPrivateKeyFilePath: clientJwtPrivateKeyFilePath, ClientProxyDomainFilePath: clientProxyDomainFilePath, ForceWsProxy: forceWsProxy})
	} else {
		fmt.Println("不支持该运行模式")
	}
}
