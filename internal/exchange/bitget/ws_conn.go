package bitget

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/utils/logger"

	"github.com/gorilla/websocket"
)

// simpleWebSocketConn WebSocket 连接的简单实现
type simpleWebSocketConn struct {
	conn              *websocket.Conn
	url               string
	onMessage         func(string)
	onDisconnected    func()
	sendMutex         sync.Mutex
	isConnected       bool
	stopChan          chan struct{}
	pingStopChan      chan struct{}
}

// newSimpleWebSocketConn 创建新的 WebSocket 连接（支持代理）
func newSimpleWebSocketConn(wsURL string) (*simpleWebSocketConn, error) {
	log := logger.GetLoggerInstance().Named("bitget.ws").Sugar()
	
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	// 获取代理配置
	proxyConfig := config.GetProxyConfig()
	if proxyConfig.IsProxyEnabled() {
		proxyURL := proxyConfig.GetProxyURL()
		if proxyURL != nil {
			dialer.Proxy = http.ProxyURL(proxyURL)
			log.Infof("Using proxy for WebSocket: %s", proxyURL.String())
		}
	}

	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to websocket: %w", err)
	}

	ws := &simpleWebSocketConn{
		conn:         conn,
		url:          wsURL,
		isConnected:  true,
		stopChan:     make(chan struct{}),
		pingStopChan: make(chan struct{}),
	}

	// 启动消息读取循环
	go ws.readLoop()

	// 启动 ping 循环（Bitget 需要定期发送 ping 保持连接）
	go ws.startPing()

	log.Infof("WebSocket connected to %s", wsURL)
	return ws, nil
}

// Send 发送消息
func (ws *simpleWebSocketConn) Send(message string) error {
	ws.sendMutex.Lock()
	defer ws.sendMutex.Unlock()

	if !ws.isConnected {
		return fmt.Errorf("websocket not connected")
	}

	err := ws.conn.WriteMessage(websocket.TextMessage, []byte(message))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

// Close 关闭连接
func (ws *simpleWebSocketConn) Close() error {
	ws.isConnected = false
	
	// 停止 ping 循环
	select {
	case <-ws.pingStopChan:
		// 已经关闭
	default:
		close(ws.pingStopChan)
	}
	
	// 停止读取循环
	select {
	case <-ws.stopChan:
		// 已经关闭
	default:
		close(ws.stopChan)
	}

	err := ws.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	if err != nil {
		return err
	}

	return ws.conn.Close()
}

// startPing 启动 ping 循环，定期发送 ping 保持连接
// Bitget WebSocket 需要定期发送 "ping" 文本消息，服务端会回复 "pong"
func (ws *simpleWebSocketConn) startPing() {
	log := logger.GetLoggerInstance().Named("bitget.ws.ping").Sugar()
	
	// 每 20 秒发送一次 ping（Bitget 建议 30 秒内发送，这里用 20 秒更安全）
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ws.pingStopChan:
			log.Debug("Ping loop stopped")
			return
		case <-ws.stopChan:
			log.Debug("Ping loop stopped (connection closed)")
			return
		case <-ticker.C:
			if !ws.isConnected {
				log.Debug("Skipping ping, not connected")
				continue
			}
			
			// 发送 ping 消息
			if err := ws.Send("ping"); err != nil {
				log.Warnf("Failed to send ping: %v", err)
			} else {
				log.Debug("Ping sent")
			}
		}
	}
}

// OnMessage 设置消息回调
func (ws *simpleWebSocketConn) OnMessage(handler func(message string)) {
	ws.onMessage = handler
}

// OnDisconnected 设置断开连接回调
func (ws *simpleWebSocketConn) OnDisconnected(handler func()) {
	ws.onDisconnected = handler
}

// readLoop 读取消息循环
func (ws *simpleWebSocketConn) readLoop() {
	log := logger.GetLoggerInstance().Named("bitget.ws").Sugar()

	defer func() {
		ws.isConnected = false
		if ws.onDisconnected != nil {
			ws.onDisconnected()
		}
	}()

	for {
		select {
		case <-ws.stopChan:
			return
		default:
			messageType, message, err := ws.conn.ReadMessage()
			if err != nil {
				log.Errorf("Error reading websocket message: %v", err)
				return
			}

			if messageType == websocket.TextMessage {
				if ws.onMessage != nil {
					ws.onMessage(string(message))
				}
			} else if messageType == websocket.PingMessage {
				// 响应 ping
				ws.sendMutex.Lock()
				_ = ws.conn.WriteMessage(websocket.PongMessage, nil)
				ws.sendMutex.Unlock()
			}
		}
	}
}
