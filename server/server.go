package server

import (
	"context"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/net/websocket"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	SUCCESS = 0
	FAILURE = 1
)

type WsRequest struct {
	DstAddrType int    `json:"dstAddrType"`
	DstAddr     string `json:"dstAddr"`
	DstPort     int    `json:"dstPort"`
}

func (ws *WsRequest) Address() string {
	return ws.DstAddr + ":" + strconv.Itoa(ws.DstPort)
}

type WsResponse struct {
	CommandStatus int    `json:"commandStatus"`
	DstAddrType   int    `json:"dstAddrType"`
	DstAddr       string `json:"dstAddr"`
	DstPort       int    `json:"dstPort"`
}

func hello(c echo.Context) error {
	websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		// Read
		var wsRequest WsRequest
		err := websocket.JSON.Receive(ws, &wsRequest)
		if err != nil {
			c.Logger().Error(err)
			websocket.JSON.Send(ws, WsResponse{FAILURE, wsRequest.DstAddrType, wsRequest.DstAddr, wsRequest.DstPort})
			return
		}
		c.Logger().Info(wsRequest)
		clientConn, err := net.DialTimeout("tcp", wsRequest.Address(), 5*time.Second)
		if err != nil {
			c.Logger().Error(err)
			websocket.JSON.Send(ws, WsResponse{FAILURE, wsRequest.DstAddrType, wsRequest.DstAddr, wsRequest.DstPort})
			return
		}
		defer clientConn.Close()
		clientConnSuccess := WsResponse{SUCCESS, wsRequest.DstAddrType, wsRequest.DstAddr, wsRequest.DstPort}
		err = websocket.JSON.Send(ws, clientConnSuccess)
		if err != nil {
			c.Logger().Error(err)
			websocket.JSON.Send(ws, WsResponse{FAILURE, wsRequest.DstAddrType, wsRequest.DstAddr, wsRequest.DstPort})
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			_, _ = io.Copy(clientConn, ws)
			cancel()
		}()

		go func() {
			writer, err := ws.NewFrameWriter(websocket.BinaryFrame)
			if err != nil {
				cancel()
			}
			_, _ = io.Copy(writer, clientConn)
			cancel()
		}()

		<-ctx.Done()
		c.Logger().Info("wsClosed")
		return
	}).ServeHTTP(c.Response(), c.Request())
	return nil
}

func Listen() {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.GET("/ws", hello)
	e.Logger.Fatal(e.Start(":1323"))
}
