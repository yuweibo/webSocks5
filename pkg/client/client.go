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
	"webSocks5/pkg/protocol"
)

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
		wsCon := randWsConn(0)
		if wsCon == nil {
			log.Error("wsCon null")
			conn.Close()
			continue
		}
		go rwConn(wsCon, conn, strconv.Itoa(socksIdPrefix)+":"+strconv.Itoa(socksIdSeq))
	}
}

func randWsConn(tryCount int) *websocket.Conn {
	if tryCount >= 10 {
		return nil
	}
	key := strconv.Itoa(rand.Intn(wsClientCache.ItemCount()))
	ws, found := wsClientCache.Get(key)
	if !found {
		log.Warn("wsClientCache.Get not found,key:" + key)
		return randWsConn(tryCount + 1)
	}
	return ws.(*websocket.Conn)
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
		err := newWsConn(strconv.Itoa(i), config.WsServerAddr)
		if err != nil {
			return err
		}
	}
	return nil
}

func newWsConn(key string, wsAddr string) error {
	wsConn, err := websocket.Dial(wsAddr, "", "http://localhost:1323")
	if err != nil {
		log.Error(err)
		return err
	}
	wsClientCache.SetDefault(key, wsConn)

	go func(key string, wsConn *websocket.Conn) {
		defer func() {
			log.Warn("webSocket closed")
			//remove from cache
			wsClientCache.Delete(key)
			//ws conn close
			wsConn.Close()
			//open new ws Conn
			newWsConn(key, wsAddr)
		}()
		for {
			var wsResponse protocol.WsProtocol
			err = websocket.JSON.Receive(wsConn, &wsResponse)
			if err != nil {
				log.Error(err)
				//todo 关闭所有关联的socks客户端
				return
			}
			socksConnValue, found := socksConnCache.Get(wsResponse.SocksId)
			if !found {
				log.Debug("SocksId:" + wsResponse.SocksId + " not found")
				continue
			}
			socksConn := socksConnValue.(net.Conn)
			if protocol.OPEN == wsResponse.Op {
				if wsResponse.OpStatus != protocol.SUCCESS {
					log.Debug("wsResponse.OpStatus failed")
					socksConn.Close()
					socksConnCache.Delete(wsResponse.SocksId)
					continue
				}
				wl, err := socksConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
				if err != nil {
					log.Error(err)
				}
				log.Debug(wl)
			} else if protocol.DATA == wsResponse.Op {
				socksConn.Write(wsResponse.Data)
			} else if protocol.CLOSE == wsResponse.Op {
				socksConn.Close()
				socksConnCache.Delete(wsResponse.SocksId)
			} else {
				log.Error("Op not define:" + strconv.Itoa(wsResponse.Op))
			}
		}
	}(key, wsConn)
	return nil
}

func rwConn(wsCon *websocket.Conn, conn net.Conn, socksId string) {
	socksConnCache.SetDefault(socksId, conn)
	connOp(1)
	defer func() {
		//remove from cache
		websocket.JSON.Send(wsCon, protocol.WsProtocol{SocksId: socksId, Op: protocol.CLOSE})
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

	err = websocket.JSON.Send(wsCon, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, DstAddrType: int(atyp), TargetAddr: dstAddr, TargetPort: dstPort})
	if err != nil {
		log.Error(err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		io.Copy(protocol.WsWriteWrapper{WsConn: wsCon, SocksId: socksId}, reader)
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