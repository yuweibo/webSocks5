package server

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"github.com/patrickmn/go-cache"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"strconv"
	"time"
)

// WsType
// 1 connection OPEN,传输ip 端口信息
// 2 DATA 传输data
const (
	SUCCESS = 0
	FAILURE = 1
	OPEN    = 1
	DATA    = 2
	CLOSE   = 3
)

type WsRequest struct {
	SocksId     string `json:"socksId"`
	WsType      int    `json:"wsType"`
	DstAddrType int    `json:"dstAddrType"`
	DstAddr     string `json:"dstAddr"`
	DstPort     int    `json:"dstPort"`
	Data        []byte `json:"data"`
}

func (ws *WsRequest) Address() string {
	return ws.DstAddr + ":" + strconv.Itoa(ws.DstPort)
}

type WsResponse struct {
	SocksId       string `json:"socksId"`
	WsType        int    `json:"wsType"`
	CommandStatus int    `json:"commandStatus"`
	Data          []byte `json:"data"`
}

var socksConnCache = cache.New(cache.NoExpiration, cache.NoExpiration)

// WsWriteWrapper 自定义 Write
type WsWriteWrapper struct {
	conn    *websocket.Conn
	socksId string
}

func (writer WsWriteWrapper) Write(p []byte) (n int, err error) {
	e := websocket.JSON.Send(writer.conn, WsResponse{writer.socksId, DATA, SUCCESS, p})
	if e != nil {
		return 0, e
	}
	return len(p), nil
}

func hello(c echo.Context) error {
	websocket.Handler(func(ws *websocket.Conn) {
		defer func() {
			c.Logger().Info("wsClosed")
			ws.Close()
		}()
		for {
			// Read
			var wsRequest WsRequest
			err := websocket.JSON.Receive(ws, &wsRequest)
			if err != nil {
				c.Logger().Error(err)
				return
			}
			c.Logger().Info(wsRequest)
			socksId := wsRequest.SocksId
			if OPEN == wsRequest.WsType {
				go func() {
					clientConn, err := net.DialTimeout("tcp", wsRequest.Address(), 5*time.Second)
					if err != nil {
						c.Logger().Error(err)
						websocket.JSON.Send(ws, WsResponse{socksId, OPEN, FAILURE, nil})
						return
					}
					err = websocket.JSON.Send(ws, WsResponse{socksId, OPEN, SUCCESS, nil})
					if err != nil {
						log.Error(err)
						clientConn.Close()
					}
					socksConnCache.SetDefault(socksId, clientConn)
					defer func() {
						socksConnCache.Delete(socksId)
						clientConn.Close()
						websocket.JSON.Send(ws, WsResponse{socksId, CLOSE, SUCCESS, nil})
					}()
					_, err = io.Copy(WsWriteWrapper{ws, socksId}, clientConn)
					if err != nil {
						log.Error(err)
						//connection close
					}
				}()
			} else if DATA == wsRequest.WsType {
				conn, found := socksConnCache.Get(socksId)
				if !found {
					log.Error("SocksId not found in cache")
					continue
				}
				c := conn.(net.Conn)
				c.Write(wsRequest.Data)
			} else if CLOSE == wsRequest.WsType {
				conn, found := socksConnCache.Get(socksId)
				if !found {
					log.Warn("socksId not found in cache,maybe closed")
					continue
				}
				socksConnCache.Delete(socksId)
				c := conn.(net.Conn)
				c.Close()
			} else {
				log.Error("WsType not defined" + strconv.Itoa(wsRequest.WsType))
				continue
			}
		}
		return
	}).ServeHTTP(c.Response(), c.Request())
	return nil
}

func Listen() {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.GET("/ws", hello)
	e.Logger.Fatal(e.Start(":8080"))
}
