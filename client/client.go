package client

import (
	"bufio"
	"context"
	"github.com/dgrijalva/jwt-go"
	"github.com/labstack/gommon/log"
	"github.com/patrickmn/go-cache"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
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
	WsServerAddr          string
	Socks5Port            int
	JwtPrivateKeyFilePath string
}

/*
*

	连接数
*/
var connections = 0
var connMu sync.Mutex

func Listen(config Config) {
	log.SetLevel(log.INFO)
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(config.Socks5Port))
	if err != nil {
		log.Error(err)
		return
	}
	//打印连接数
	go func() {
		for {
			log.Info("connections:" + strconv.Itoa(connections))
			time.Sleep(10 * time.Second)
		}
	}()
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error(err)
			continue
		}
		go rwConn(conn, config)
	}
}

func rwConn(conn net.Conn, config Config) {
	connOp(1)
	defer func() {
		conn.Close()
		connOp(-1)
	}()
	reader := bufio.NewReader(conn)
	version, err := reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	//只支持socks5

	if version != 5 {
		log.Warn("not support socks5 version")
		return
	}
	numberMethod, err := reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	if numberMethod < 1 {
		log.Warn("numberMethod <1")
		return
	}
	for i := 0; i < int(numberMethod); i++ {
		_, _ = reader.ReadByte()
	}
	noAuth := []byte{version, 0}
	conn.Write(noAuth)

	version, err = reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	if version != 5 {
		log.Warn("not support socks5 version")
		return
	}

	cmd, err := reader.ReadByte()
	if cmd != 1 {
		log.Warn("cmd != 1")
		return
	}
	//rsv
	_, _ = reader.ReadByte()
	atyp, err := reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	//ipv4
	var dstAddr string
	if atyp == 1 {
		ipv4 := make([]byte, net.IPv4len)
		reader.Read(ipv4)
		dstAddr = net.IP(ipv4).String()
	} else if atyp == 4 {
		ipv6 := make([]byte, net.IPv6len)
		reader.Read(ipv6)
		dstAddr = net.IP(ipv6).String()
	} else if atyp == 3 {
		hostLength, err := reader.ReadByte()
		if err != nil {
			log.Error(err)
			return
		}
		hostName := make([]byte, hostLength)
		reader.Read(hostName)
		dstAddr = string(hostName)
	}
	p1, err := reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	p2, err := reader.ReadByte()
	if err != nil {
		log.Error(err)
		return
	}
	dstPort := int(p1)<<8 + int(p2)
	log.Debug("dstAddr:", dstAddr, "dstPort:", dstPort)

	if config.JwtPrivateKeyFilePath != "" {
		token, err := genJwtToken(config.JwtPrivateKeyFilePath)
		if err != nil {
			log.Error(err)
			return
		}
		config.WsServerAddr += ("?token=" + token)
	}

	wsConn, err := websocket.Dial(config.WsServerAddr, "", "http://localhost:1323")
	if err != nil {
		log.Error(err)
		return
	}

	defer func() {
		wsConn.Close()
		log.Debug("wsConn closed")
	}()

	err = websocket.JSON.Send(wsConn, WsRequest1{int(atyp), string(dstAddr), dstPort})
	if err != nil {
		log.Error(err)
		return
	}

	var wsResponse1 WsResponse1
	err = websocket.JSON.Receive(wsConn, &wsResponse1)
	if err != nil {
		log.Error(err)
		return
	}
	if wsResponse1.CommandStatus != 0 {
		log.Error("wsResponse1.CommandStatus != 0")
		return
	}
	wl, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	if err != nil {
		log.Error(err)
		return
	}
	log.Debug(wl)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		io.Copy(conn, wsConn)
		cancel()
	}()

	go func() {
		wsWriter, err := wsConn.NewFrameWriter(websocket.BinaryFrame)
		if err != nil {
			log.Error(err)
			cancel()
			return
		}
		io.Copy(wsWriter, reader)
		cancel()
	}()

	<-ctx.Done()

	log.Debug("socks connection closed")

}

// 操作连接数
func connOp(con int) {
	connMu.Lock()
	connections += con
	connMu.Unlock()
}

var jwtCache = cache.New(10*time.Hour, 10*time.Minute)

func genJwtToken(privateKeyFilePath string) (string, error) {

	ctoken, found := jwtCache.Get(privateKeyFilePath)
	if found {
		return ctoken.(string), nil
	}

	// 私钥
	privateKey, err := os.ReadFile(privateKeyFilePath)
	if err != nil {
		return "", err
	}
	// 设置JWT的claims
	claims := jwt.MapClaims{}
	claims["name"] = "clientGo"
	claims["exp"] = time.Now().Add(time.Hour * 24).Unix() // token过期时间

	// 使用RS256算法生成token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	privateKeyData, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", err
	}
	// 签名token
	signedToken, err := token.SignedString(privateKeyData)
	if err != nil {
		return "", err
	}
	jwtCache.SetDefault(privateKeyFilePath, signedToken)
	return signedToken, nil
}
