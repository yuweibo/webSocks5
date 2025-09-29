package server

import (
	_ "embed"
	"encoding/json"
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
			log.Info("wsClosed")
			ws.Close()
		}()
		for {
			// Read
			var wsRequest protocol.WsProtocol
			err := websocket.JSON.Receive(ws, &wsRequest)
			if err != nil {
				log.Error(err)
				return
			}
			rstr, _ := json.Marshal(wsRequest)
			log.Info(string(rstr))
			socksId := wsRequest.SocksId
			if protocol.OPEN == wsRequest.Op {
				go func() {
					clientConn, err := net.DialTimeout("tcp", wsRequest.Address(), 1*time.Second)
					if err != nil {
						log.Error(socksId+":connect err:", err)
						websocket.JSON.Send(ws, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, OpStatus: protocol.FAILURE})
						return
					}
					socksConnCache.SetDefault(socksId, clientConn)
					log.Info(socksId + ":send success")
					err = websocket.JSON.Send(ws, protocol.WsProtocol{SocksId: socksId, Op: protocol.OPEN, OpStatus: protocol.SUCCESS})
					if err != nil {
						log.Error(socksId+":connect send err:", err)
						clientConn.Close()
					}
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
					return
				}
				log.Info(".........")
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

//go:embed webSocks5.yaml
var webSocks5Config []byte

func clash(c echo.Context) error {
	c.Response().Write(webSocks5Config)
	return nil
}

func Listen(port string) {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.GET("/ws", hello)
	e.GET("/clash.yaml", clash)
	e.GET("/*", func(c echo.Context) error {
		return c.String(200, "Hello, World!")
	})
	e.Logger.Fatal(e.Start(":" + port))
}
