package client

import (
	"bufio"
	"context"
	"fmt"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"strconv"
)

type WsRequest1 struct {
	DstAddrType int    `json:"dstAddrType"`
	DstAddr     string `json:"dstAddr"`
	DstPort     int    `json:"dstPort"`
}

type WsResponse1 struct {
	CommandStatus int    `json:"commandStatus"`
	DstAddrType   int    `json:"dstAddrType"`
	DstAddr       string `json:"dstAddr"`
	DstPort       int    `json:"dstPort"`
}

type Config struct {
	WsServerAddr string `json:"wsServerAddr"`
}

func Listen(config Config) {
	listener, err := net.Listen("tcp", ":1080")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}
		go rwConn(conn, config)
	}
}

func rwConn(conn net.Conn, config Config) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	version, err := reader.ReadByte()
	if err != nil {
		fmt.Println(err)
		return
	}
	//只支持socks5
	if version != 5 {
		fmt.Println("not support socks5 version")
		return
	}
	numberMethod, err := reader.ReadByte()
	if err != nil {
		fmt.Println(err)
		return
	}
	if numberMethod < 1 {
		fmt.Println("numberMethod <1")
		return
	}
	for i := 0; i < int(numberMethod); i++ {
		_, _ = reader.ReadByte()
	}
	noAuth := []byte{version, 0}
	conn.Write(noAuth)

	version, err = reader.ReadByte()
	if err != nil {
		fmt.Println(err)
		return
	}
	if version != 5 {
		fmt.Println("not support socks5 version")
		return
	}

	cmd, err := reader.ReadByte()
	if cmd != 1 {
		fmt.Println("cmd != 1")
		return
	}
	//rsv
	rsv, _ := reader.ReadByte()
	fmt.Println(rsv)
	atyp, err := reader.ReadByte()
	if err != nil {
		fmt.Println(err)
		return
	}
	//ipv4
	var dstAddr string
	var hostLength byte
	if atyp == 1 {
		ip := make([]byte, net.IPv4len)
		reader.Read(ip)
		dstAddr = strconv.Itoa(int(ip[0])) + "." + strconv.Itoa(int(ip[1])) + "." + strconv.Itoa(int(ip[2])) + "." + strconv.Itoa(int(ip[3]))
	} else if atyp == 4 {
		dstAddr := make([]byte, net.IPv6len)
		reader.Read(dstAddr)
	} else if atyp == 3 {
		hostLength, err = reader.ReadByte()
		if err != nil {
			fmt.Println(err)
			return
		}
		hostName := make([]byte, hostLength)
		reader.Read(hostName)
		dstAddr = string(hostName)
	}
	fmt.Println(string(dstAddr))
	p1, err := reader.ReadByte()
	p2, err := reader.ReadByte()
	dstPort := int(p1)<<8 + int(p2)
	fmt.Println(dstPort)

	wsConn, err := websocket.Dial(config.WsServerAddr, "", "http://localhost:1323")
	if err != nil {
		fmt.Println(err)
		return
	}

	defer wsConn.Close()

	err = websocket.JSON.Send(wsConn, WsRequest1{int(atyp), string(dstAddr), dstPort})
	if err != nil {
		fmt.Println(err)
		return
	}

	var wsResponse1 WsResponse1
	err = websocket.JSON.Receive(wsConn, &wsResponse1)
	if err != nil {
		fmt.Println(err)
		return
	}
	if wsResponse1.CommandStatus != 0 {
		fmt.Println("wsResponse1.CommandStatus != 0")
		return
	}
	wl, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(wl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, err = io.Copy(conn, wsConn)
		if err != nil {
			fmt.Println("write to conn:" + err.Error())
		}
		cancel()
	}()

	go func() {
		wsWriter, err := wsConn.NewFrameWriter(websocket.BinaryFrame)
		if err != nil {
			fmt.Println(err)
			cancel()
			return
		}
		_, err = io.Copy(wsWriter, reader)
		if err != nil {
			fmt.Println("write to wsWriter:" + err.Error())
		}
		cancel()
	}()

	<-ctx.Done()

	fmt.Println("socks connection closed")

}
