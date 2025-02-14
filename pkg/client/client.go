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

var wsClientInitMu sync.Mutex
var chooseRWMu sync.RWMutex

// webSocket客户端连接数
var initWsCount = 10
var maxWsCount = 200
var thresholdWsCount = 3

var socksIdPrefix = strconv.Itoa(rand.Intn(10000))

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
	go cleanWsConn()
	//打开运行状态打印
	go info()
	//监听连接
	socksIdSeq := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Error(err)
			continue
		}
		socksIdSeq += 1
		//异步动态扩容ws连接
		go prepareWsClient(wsAddr)
		//解析socks协议传输数据
		go rwSocksConn(conn, wsAddr, socksIdSeq)
	}
}

// 打印连接数
func info() {
	for {
		log.Info("socksServer连接数:" + strconv.Itoa(socksConnCache.ItemCount()))
		wsKeyConnMap := getWsKeySocksConn()
		wsKeyConn := ""
		for wsKey := range wsKeyConnMap {
			wsKeyConn += wsKey + ":" + strconv.Itoa(wsKeyConnMap[wsKey]) + ","
		}
		log.Info("webSocket连接数:" + strconv.Itoa(wsClientCache.ItemCount()) + ",分布情况:" + wsKeyConn)
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
		chooseRWMu.Lock()
		wsClientInitMu.Lock()
		wsKeyConnMap := getWsKeySocksConn()
		for wsKey, wsConnItem := range wsClientCache.Items() {
			socksConn := wsKeyConnMap[wsKey]
			if socksConn <= 0 {
				wsConn := wsConnItem.Object.(*websocket.Conn)
				closeWsConn(false, wsKey, wsConn)
			}
		}
		wsClientInitMu.Unlock()
		chooseRWMu.Unlock()
	}
}

func getWsKeySocksConn() map[string]int {
	wsKeyConnMap := make(map[string]int)
	for _, wsKeyItem := range socksWsKeyCache.Items() {
		wsKeyValue := wsKeyItem.Object.(string)
		wsKeyConnMap[wsKeyValue] = wsKeyConnMap[wsKeyValue] + 1
	}
	return wsKeyConnMap
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

func chooseWsConn(tryCount int, wsAddr string, socksIdSeq int) (*websocket.Conn, string) {
	chooseRWMu.RLock()
	defer chooseRWMu.RUnlock()
	return findWsConn(tryCount, wsAddr, socksIdSeq)
}

func findWsConn(tryCount int, wsAddr string, socksIdSeq int) (*websocket.Conn, string) {
	if tryCount >= 10 {
		return nil, ""
	}
	wsClientSize := wsClientCache.ItemCount()
	if wsClientSize == 0 {
		//同步初始化ws连接
		initWsClientCache(initWsCount, wsAddr)
	}
	//异步动态扩容ws连接
	go prepareWsClient(wsAddr)
	keyIndex := 0
	//最少连接数查找
	sockSConn := 1000000
	for wsKey := range wsClientCache.Items() {
		cacheCount := getWsKeySocksConn()[wsKey]
		if cacheCount < sockSConn {
			keyIndex, _ = strconv.Atoi(wsKey)
			sockSConn = cacheCount
		}
	}
	if sockSConn >= thresholdWsCount {
		keyIndex = -1
		prepareWsClient(wsAddr)
	}
	key := strconv.Itoa(keyIndex)
	ws, found := wsClientCache.Get(key)
	if !found {
		return findWsConn(tryCount+1, wsAddr, socksIdSeq)
	}
	return ws.(*websocket.Conn), key
}

func prepareWsClient(wsAddr string) {
	wsClientSize := wsClientCache.ItemCount()
	if wsClientSize != 0 {
		wsClientAvg := socksConnCache.ItemCount() / wsClientSize
		if wsClientAvg >= thresholdWsCount {
			prepareWsSize := (socksConnCache.ItemCount() / thresholdWsCount) + 1
			if prepareWsSize > maxWsCount {
				prepareWsSize = maxWsCount
			}
			initWsClientCache(prepareWsSize, wsAddr)
		}
	}
}

var wsClientCache = cache.New(cache.NoExpiration, cache.NoExpiration)
var socksConnCache = cache.New(cache.NoExpiration, cache.NoExpiration)
var socksWsKeyCache = cache.New(cache.NoExpiration, cache.NoExpiration)

func initWsClientCache(wsCount int, wsAddr string) {
	wsClientInitMu.Lock()
	defer wsClientInitMu.Unlock()
	wg := sync.WaitGroup{}
	for i := 0; i < wsCount; i++ {
		wg.Add(1)
		go newWsConn(strconv.Itoa(i), wsAddr, &wg)
	}
	wg.Wait()
}

func newWsConn(key string, wsAddr string, wg *sync.WaitGroup) {
	defer wg.Done()
	_, found := wsClientCache.Get(key)
	if found {
		return
	}
	wsConn, err := websocket.Dial(wsAddr, "", "http://localhost:1323")
	if err != nil {
		log.Error(err)
		return
	}
	wsClientCache.SetDefault(key, wsConn)

	go func(key string, wsConn *websocket.Conn) {
		defer closeWsConn(true, key, wsConn)
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
					if wsResponse.OpStatus != protocol.SUCCESS {
						log.Debug("wsResponse.OpStatus failed")
						closeSocksConn(wsConn, wsResponse.SocksId, socksConn)
						return
					}
					wl, err := socksConn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
					if err != nil {
						log.Error(err)
					}
					log.Debug(wl)
				}()
			} else if protocol.DATA == wsResponse.Op {
				socksConn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				socksConn.Write(wsResponse.Data)
			} else if protocol.CLOSE == wsResponse.Op {
				closeSocksConn(wsConn, wsResponse.SocksId, socksConn)
			} else {
				log.Error("Op not define:" + strconv.Itoa(wsResponse.Op))
			}
		}
	}(key, wsConn)
}

func closeWsConn(lock bool, key string, wsConn *websocket.Conn) {
	log.Info("webSocket closed key:" + key)
	if lock {
		chooseRWMu.Lock()
		defer chooseRWMu.Unlock()
	}
	//remove from cache
	wsClientCache.Delete(key)
	//ws conn close
	wsConn.Close()
	//关闭关联的socks连接
	for socksId := range socksWsKeyCache.Items() {
		wsKey, found := socksWsKeyCache.Get(socksId)
		if !found {
			continue
		}
		if wsKey == key {
			con, f := socksConnCache.Get(socksId)
			if f {
				socksConn := con.(net.Conn)
				closeSocksConn(wsConn, socksId, socksConn)
			}
		}
	}
}

func rwSocksConn(conn net.Conn, wsAddr string, socksIdSeq int) {
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
	wsCon, wsKey := chooseWsConn(0, wsAddr, socksIdSeq)
	if wsCon == nil {
		log.Warn("wsCon is nil")
		return
	}
	socksWsKeyCache.SetDefault(socksId, wsKey)
	defer closeSocksConn(wsCon, socksId, conn)
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

func closeSocksConn(wsCon *websocket.Conn, socksId string, conn net.Conn) {
	//remove from cache
	if wsCon != nil {
		websocket.JSON.Send(wsCon, protocol.WsProtocol{SocksId: socksId, Op: protocol.CLOSE})
	}
	socksConnCache.Delete(socksId)
	socksWsKeyCache.Delete(socksId)
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
