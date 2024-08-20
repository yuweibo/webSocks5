package client

import (
	"bufio"
	"context"
	"github.com/dgrijalva/jwt-go"
	"github.com/labstack/gommon/log"
	"github.com/patrickmn/go-cache"
	"golang.org/x/net/websocket"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
	"webSocks5/server"
)

type WsRequest1 struct {
	SocksId     string `json:"socksId"`
	WsType      int    `json:"wsType"`
	DstAddrType int    `json:"dstAddrType"`
	DstAddr     string `json:"dstAddr"`
	DstPort     int    `json:"dstPort"`
	Data        []byte `json:"data"`
}

type WsResponse1 struct {
	SocksId       string `json:"socksId"`
	WsType        int    `json:"wsType"`
	CommandStatus int    `json:"commandStatus"`
	Data          []byte `json:"data"`
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

	//初始化webSocket客户端连接池
	wsCount := 10
	err = initWsClientCache(wsCount, config)
	if err != nil {
		log.Error(err)
		return
	}
	//打印连接数
	go func() {
		for {
			log.Info("socksServer连接数:" + strconv.Itoa(connections))
			log.Info("socksServer缓存数:" + strconv.Itoa(socksConnCache.ItemCount()))
			log.Info("webSocket缓存数:" + strconv.Itoa(wsClientCache.ItemCount()))
			time.Sleep(10 * time.Second)
		}
	}()
	defer listener.Close()
	socksIdSeq := 1
	socksIdPrefix := rand.Intn(10000)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error(err)
			continue
		}
		socksIdSeq += 1
		ws, found := wsClientCache.Get(strconv.Itoa(rand.Intn(wsCount)))
		if !found {
			log.Error("wsClientCache.Get not found")
			conn.Close()
			continue
		}
		wsCon := ws.(*websocket.Conn)
		go rwConn(wsCon, conn, strconv.Itoa(socksIdPrefix)+":"+strconv.Itoa(socksIdSeq))
	}
}

var wsClientCache = cache.New(cache.NoExpiration, cache.NoExpiration)
var socksConnCache = cache.New(cache.NoExpiration, cache.NoExpiration)

func initWsClientCache(wsCount int, config Config) error {
	if config.JwtPrivateKeyFilePath != "" {
		token, err := genJwtToken(config.JwtPrivateKeyFilePath)
		if err != nil {
			log.Error(err)
			return err
		}
		config.WsServerAddr += ("?token=" + token)
	}
	for i := 0; i < wsCount; i++ {
		wsConn, err := websocket.Dial(config.WsServerAddr, "", "http://localhost:1323")
		if err != nil {
			log.Error(err)
			return err
		}
		key := strconv.Itoa(i)
		wsClientCache.SetDefault(key, wsConn)

		go func(key string, wsConn *websocket.Conn) {
			defer func() {
				log.Warn("webSocket closed")
				//remove from cache
				wsClientCache.Delete(key)
				//ws conn close
				wsConn.Close()
			}()
			for {
				var wsResponse1 WsResponse1
				err = websocket.JSON.Receive(wsConn, &wsResponse1)
				if err != nil {
					log.Error(err)
					//todo 关闭所有关联的socks客户端
					return
				}
				socksConnValue, found := socksConnCache.Get(wsResponse1.SocksId)
				if !found {
					log.Error("SocksId:" + wsResponse1.SocksId + " not found")
					continue
				}
				socksConn := socksConnValue.(net.Conn)
				if server.OPEN == wsResponse1.WsType {
					if wsResponse1.CommandStatus != server.SUCCESS {
						log.Error("wsResponse1.CommandStatus != 0")
						socksConn.Close()
						socksConnCache.Delete(wsResponse1.SocksId)
						continue
					}
					wl, err := socksConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					if err != nil {
						log.Error(err)
					}
					log.Debug(wl)
				} else if server.DATA == wsResponse1.WsType {
					socksConn.Write(wsResponse1.Data)
				} else if server.CLOSE == wsResponse1.WsType {
					socksConn.Close()
					socksConnCache.Delete(wsResponse1.SocksId)
				} else {
					log.Error("WsType not define:" + strconv.Itoa(wsResponse1.WsType))
				}
			}
		}(key, wsConn)
	}
	return nil
}

// WsWriteWrapper 自定义 Write
type WsWriteWrapper struct {
	conn    *websocket.Conn
	socksId string
}

func (writer WsWriteWrapper) Write(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}
	e := websocket.JSON.Send(writer.conn, WsRequest1{writer.socksId, server.DATA, 0, "", 0, p})
	if e != nil {
		return 0, e
	}
	return len(p), nil
}

func rwConn(wsCon *websocket.Conn, conn net.Conn, socksId string) {
	socksConnCache.SetDefault(socksId, conn)
	connOp(1)
	defer func() {
		//remove from cache
		websocket.JSON.Send(wsCon, WsRequest1{socksId, server.CLOSE, 0, "", 0, nil})
		socksConnCache.Delete(socksId)
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

	err = websocket.JSON.Send(wsCon, WsRequest1{socksId, server.OPEN, int(atyp), string(dstAddr), dstPort, nil})
	if err != nil {
		log.Error(err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		io.Copy(WsWriteWrapper{wsCon, socksId}, reader)
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
