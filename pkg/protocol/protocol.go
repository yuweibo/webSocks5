package protocol

import (
	"golang.org/x/net/websocket"
	"strconv"
)

// WsProtocol 自定义webSocket数据通信协议
type WsProtocol struct {
	SocksId     string `json:"sId"` //SocksId
	Op          int    `json:"op"`  //动作
	OpStatus    int    `json:"os"`  // 动作执行结果
	DstAddrType int    `json:"dstAddrType"`
	TargetAddr  string `json:"a"` //target addr
	TargetPort  int    `json:"p"` //target port
	Data        []byte `json:"d"` //op == data,byte array data
}

func (ws *WsProtocol) Address() string {
	return ws.TargetAddr + ":" + strconv.Itoa(ws.TargetPort)
}

const (
	// SUCCESS OpStatus
	SUCCESS = 0
	FAILURE = 1
	// OPEN Op
	// 1 connection OPEN,传输ip 端口信息
	// 2 DATA 传输data
	OPEN  = 1
	CLOSE = 2
	DATA  = 3
)

// WsWriteWrapper 自定义 Write
type WsWriteWrapper struct {
	WsConn  *websocket.Conn
	SocksId string
}

func (writer WsWriteWrapper) Write(p []byte) (n int, err error) {
	e := websocket.JSON.Send(writer.WsConn, WsProtocol{SocksId: writer.SocksId, Op: DATA, Data: p})
	if e != nil {
		return 0, e
	}
	return len(p), nil
}
