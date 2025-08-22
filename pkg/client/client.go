package client

import (
	"bufio"
	"context"
	"github.com/dgrijalva/jwt-go"
	"github.com/google/uuid"
	"github.com/labstack/gommon/log"
	"github.com/patrickmn/go-cache"
	"golang.org/x/net/websocket"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"time"
	"webSocks5/pkg/protocol"
)

type Config struct {
	WsServerAddr          string
	Socks5Port            int
	JwtPrivateKeyFilePath string
	WsAutoClose           bool
	//webSocket初始化连接数
	WsConnInitSize int
	//webSocket最大连接数
	WsConnMaxSize int
}

type WsConnectionType struct {
	Connection *websocket.Conn
	SocksConn  net.Conn
	OpenChan   *chan bool
	count      int64
	closed     bool
}

var socksIdPrefix = func() string {
	rand.Seed(time.Now().UnixNano())
	return strconv.Itoa(rand.Intn(10000))
}()

func Listen(config Config) {
	log.SetLevel(log.INFO)
	wsAddr, err := wsAddr(config)
	if err != nil {
		log.Error(err)
		return
	}

	listener, err := net.Listen("tcp", ":"+strconv.Itoa(config.Socks5Port))
	if err != nil {
		log.Error(err)
		return
	}
	defer listener.Close()
	//打开清理ws连接定时器
	if config.WsAutoClose {
		go cleanWsConn()
	}
	//打开运行状态打印
	go info()
	//预创建ws连接
	go prepareWsClient(wsAddr, config)
	//监听连接
	socksIdSeq := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error(err)
			continue
		}
		socksIdSeq += 1
		//解析socks协议传输数据
		go rwSocksConn(wsAddr, conn, socksIdSeq, config)
	}
}

// 打印连接数
func info() {
	for {
		log.Info("socksServer连接数:" + strconv.Itoa(socksConnCache.ItemCount()))
		log.Info("webSocket连接数:" + strconv.Itoa(wsClientCache.ItemCount()))
		log.Info("wsTypeChan:" + strconv.Itoa(len(wsTypeChan)))
		var totalCount int64 = 0
		for _, item := range wsClientCache.Items() {
			wsConnType := item.Object.(*WsConnectionType)
			totalCount += wsConnType.count
		}
		log.Info("wsReuseCount:" + strconv.FormatInt(totalCount, 10))
		time.Sleep(10 * time.Second)
	}
}

// 定时器每60s检查有没有socks连接，没有连接就关闭ws连接
func cleanWsConn() {
	for {
		time.Sleep(10 * time.Second)
		if socksConnCache.ItemCount() > 0 {
			continue
		}
		for wsKey, wsConnItem := range wsClientCache.Items() {
			wsConnType := wsConnItem.Object.(*WsConnectionType)
			closeWsConn(wsKey, wsConnType)
		}
		//清空wsTypeChan
		close(wsTypeChan)
		wsTypeChan = make(chan *WsConnectionType, 200)
	}
}

func wsAddr(config Config) (string, error) {
	wsAddr := config.WsServerAddr
	if config.JwtPrivateKeyFilePath != "" {
		token, err := genJwtToken(config.JwtPrivateKeyFilePath)
		if err != nil {
			return "", err
		}
		wsAddr = config.WsServerAddr + ("?token=" + token)
	}
	return wsAddr, nil
}

func selectWsTypeConn(wsAddr string, conn net.Conn, ch *chan bool, config Config) *WsConnectionType {
	tryCount := 0
	for {
		if tryCount > 10 {
			return nil
		}
		select {
		case wsType := <-wsTypeChan:
			socksConn := wsType.SocksConn
			if wsType.closed {
				log.Warn("wsType closed")
			}
			if socksConn == nil && !wsType.closed {
				wsType.SocksConn = conn
				wsType.OpenChan = ch
				wsType.count += 1
				return wsType
			}
		case <-time.After(time.Second):
			newWsConn(uuid.NewString(), wsAddr, config)
		}
		tryCount++
	}
}

func prepareWsClient(wsAddr string, config Config) {
	for {
		if socksConnCache.ItemCount() > 0 {
			if wsClientCache.ItemCount() >= config.WsConnInitSize {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			id := uuid.NewString()
			newWsConn(id, wsAddr, config)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var wsClientCache = cache.New(cache.NoExpiration, cache.NoExpiration)
var socksConnCache = cache.New(cache.NoExpiration, cache.NoExpiration)
var wsTypeChan = make(chan *WsConnectionType, 200)

func newWsConn(key string, wsAddr string, config Config) {
	if wsClientCache.ItemCount() >= config.WsConnMaxSize {
		//log.Warn("wsClientCache too many")
		return
	}
	_, found := wsClientCache.Get(key)
	if found {
		return
	}
	wsConn, err := websocket.Dial(wsAddr, "", "http://localhost:1323")
	if err != nil {
		log.Error(err)
		return
	}
	wsConnType := &WsConnectionType{
		Connection: wsConn,
		SocksConn:  nil,
		OpenChan:   nil,
	}
	defer func() {
		if r := recover(); r != nil {
			// 捕获并忽略 panic
			log.Error("Suppressing panic2:", r)
		}
	}()
	wsClientCache.SetDefault(key, wsConnType)
	wsTypeChan <- wsConnType
	go func(key string, wsConn *websocket.Conn, wsConnType *WsConnectionType) {
		defer closeWsConn(key, wsConnType)
		for {
			var wsResponse protocol.WsProtocol
			err = websocket.JSON.Receive(wsConn, &wsResponse)
			if err != nil {
				log.Error(err)
				return
			}
			socksConnValue, found := socksConnCache.Get(wsResponse.SocksId)
			if !found {
				log.Debug("SocksId:" + wsResponse.SocksId + " not found")
				continue
			}
			socksConn := socksConnValue.(net.Conn)
			if protocol.OPEN == wsResponse.Op {
				go func() {
					ch := wsConnType.OpenChan
					if ch == nil {
						return
					}
					defer func() {
						wsConnType.OpenChan = nil
					}()
					if wsResponse.OpStatus != protocol.SUCCESS {
						log.Debug("wsResponse.OpStatus failed")
						if ch != nil {
							select {
							case *ch <- false:
								log.Error("error open SocksId ", wsResponse.SocksId)
							default:
								// 通道满或已关闭，避免 panic
								log.Error("error open2 SocksId ", wsResponse.SocksId)
							}
						}
						closeSocksConn(nil, wsResponse.SocksId, socksConn)
						return
					}
					wl, err := socksConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					if err != nil {
						log.Error(err)
					}
					log.Debug(wl)
					if ch != nil {
						select {
						case *ch <- err == nil:
						default:
							// 通道满或已关闭，避免 panic
							log.Error("Failed to send signal to channel for SocksId ", wsResponse.SocksId)
						}
					}
				}()
			} else if protocol.DATA == wsResponse.Op {
				socksConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				socksConn.Write(wsResponse.Data)
			} else if protocol.CLOSE == wsResponse.Op {
				closeSocksConn(nil, wsResponse.SocksId, socksConn)
			} else {
				log.Error("Op not define:" + strconv.Itoa(wsResponse.Op))
			}
		}
	}(key, wsConn, wsConnType)

}

func closeWsConn(key string, wsType *WsConnectionType) {
	log.Info("webSocket closed key:" + key)
	//remove from cache
	wsClientCache.Delete(key)
	wsType.closed = true
	//ws conn close
	wsConn := wsType.Connection
	wsConn.Close()
	//关闭关联的socks连接
	socksConn := wsType.SocksConn
	if socksConn != nil {
		socksConn.Close()
	}
	//关闭关联的通道
	ch := wsType.OpenChan
	if ch != nil {
		close(*ch)
	}
}

func rwSocksConn(wsAddr string, conn net.Conn, socksIdSeq int, config Config) {
	defer func() {
		if r := recover(); r != nil {
			// 捕获并忽略 panic
			log.Error("Suppressing rwSocksConn panic:", r)
		}
	}()
	socksId := socksIdPrefix + ":" + strconv.Itoa(socksIdSeq)
	socksConnCache.SetDefault(socksId, conn)
	defer closeSocksConn(nil, socksId, conn)
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
	ch := make(chan bool)
	defer close(ch)

	wsConType := selectWsTypeConn(wsAddr, conn, &ch, config)
	if wsConType == nil {
		log.Warn("wsCon is nil")
		return
	}
	defer closeSocksConn(wsConType, socksId, conn)
	err = websocket.JSON.Send(wsConType.Connection, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, DstAddrType: int(atyp), TargetAddr: dstAddr, TargetPort: dstPort})
	if err != nil {
		log.Error(err)
		return
	}
	select {
	case success := <-ch:
		log.Info("chan Received data:", success)
		if success {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				io.Copy(protocol.WsWriteWrapper{WsConn: wsConType.Connection, SocksId: socksId}, reader)
				cancel()
			}()
			<-ctx.Done()
		}
	case <-time.After(50 * time.Second):
		log.Warn("chan Request timed out dstAddr:" + dstAddr)
	}
	log.Debug("socks connection closed")
}

func closeSocksConn(wsConnType *WsConnectionType, socksId string, conn net.Conn) {
	if wsConnType != nil {
		defer func() {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						// 捕获并忽略 panic
						log.Error("Suppressing panic:", r)
					}
				}()
				// 将wsConnType重新放入缓存
				now := time.Now()
				wsTypeChan <- wsConnType
				log.Info("return to wsTypeChan cost:", time.Since(now))
			}()
		}()
		wsConnType.SocksConn = nil
		wsConnType.OpenChan = nil
		wsCon := wsConnType.Connection
		//remove from cache
		if wsCon != nil {
			websocket.JSON.Send(wsCon, protocol.WsProtocol{SocksId: socksId, Op: protocol.CLOSE})
		}
	}
	socksConnCache.Delete(socksId)
	conn.Close()
}

var jwtCache = cache.New(cache.NoExpiration, cache.NoExpiration)

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
	claims["exp"] = time.Now().Add(time.Hour * 24 * 365).Unix() // token过期时间

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
