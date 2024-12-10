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
	"webSocks5/pkg/protocol"
)

var socksConnCache = cache.New(cache.NoExpiration, cache.NoExpiration)

func hello(c echo.Context) error {
	websocket.Handler(func(ws *websocket.Conn) {
		defer func() {
			c.Logger().Info("wsClosed")
			ws.Close()
		}()
		for {
			// Read
			var wsRequest protocol.WsProtocol
			err := websocket.JSON.Receive(ws, &wsRequest)
			if err != nil {
				c.Logger().Error(err)
				return
			}
			c.Logger().Info(wsRequest)
			socksId := wsRequest.SocksId
			if protocol.OPEN == wsRequest.Op {
				go func() {
					clientConn, err := net.DialTimeout("tcp", wsRequest.Address(), 5*time.Second)
					if err != nil {
						c.Logger().Debug(err)
						websocket.JSON.Send(ws, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, OpStatus: protocol.FAILURE})
						return
					}
					err = websocket.JSON.Send(ws, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, OpStatus: protocol.SUCCESS})
					if err != nil {
						log.Error(err)
						clientConn.Close()
					}
					socksConnCache.SetDefault(socksId, clientConn)
					defer func() {
						socksConnCache.Delete(socksId)
						clientConn.Close()
						websocket.JSON.Send(ws, protocol.WsProtocol{SocksId: socksId, Op: protocol.CLOSE})
					}()
					io.Copy(protocol.WsWriteWrapper{ws, socksId}, clientConn)
				}()
			} else if protocol.DATA == wsRequest.Op {
				conn, found := socksConnCache.Get(socksId)
				if !found {
					log.Warn("SocksId not found in cache")
					continue
				}
				c := conn.(net.Conn)
				c.SetWriteDeadline(time.Now().Add(5 * time.Second))
				c.Write(wsRequest.Data)
			} else if protocol.CLOSE == wsRequest.Op {
				conn, found := socksConnCache.Get(socksId)
				if !found {
					log.Debug("socksId not found in cache,maybe closed")
					continue
				}
				socksConnCache.Delete(socksId)
				c := conn.(net.Conn)
				c.Close()
			} else {
				log.Error("Op not defined" + strconv.Itoa(wsRequest.Op))
				continue
			}
		}
		return
	}).ServeHTTP(c.Response(), c.Request())
	return nil
}

func Listen(port string) {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.GET("/ws", hello)
	e.Logger.Fatal(e.Start(":" + port))
}
