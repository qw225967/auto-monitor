package hyperliquid

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type websocketConn struct {
	mu               sync.RWMutex
	conn             *websocket.Conn
	walletAddress    string
	privateKey       string
	logger           *zap.SugaredLogger
	tickerCallback   func(string, *model.Ticker)
	isConnected      bool
	subscribedCoins  map[string]bool
	stopCh           chan struct{}
	done             chan struct{}
}

func newWebsocketConn(walletAddress, privateKey string, logger *zap.SugaredLogger) *websocketConn {
	return &websocketConn{
		walletAddress:   walletAddress,
		privateKey:      privateKey,
		logger:          logger,
		subscribedCoins: make(map[string]bool),
		stopCh:          make(chan struct{}),
		done:            make(chan struct{}),
	}
}

func (w *websocketConn) setTickerCallback(callback func(string, *model.Ticker)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tickerCallback = callback
}

func (w *websocketConn) connect() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.isConnected {
		return nil
	}

	// 配置代理
	dialer := websocket.DefaultDialer
	if proxyConfig := config.GetProxyConfig(); proxyConfig != nil {
		if proxyURL := proxyConfig.GetProxyURL(); proxyURL != nil {
			dialer.Proxy = func(*http.Request) (*url.URL, error) {
				return proxyURL, nil
			}
			w.logger.Infof("Using proxy for WebSocket: %s", proxyConfig.GetProxyURLString())
		}
	}

	// 连接 WebSocket
	conn, _, err := dialer.Dial(constants.HyperliquidWsUrl, nil)
	if err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	w.conn = conn
	w.isConnected = true
	w.logger.Infof("WebSocket connected to %s", constants.HyperliquidWsUrl)

	// 启动消息读取循环
	go w.readLoop()

	return nil
}

func (w *websocketConn) subscribe(symbols []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isConnected || w.conn == nil {
		return fmt.Errorf("WebSocket not connected")
	}

	// 订阅所有交易对的 L2 订单簿（用于 ticker 数据）
	for _, symbol := range symbols {
		coin := normalizeSymbol(symbol, true)
		
		subscribeMsg := map[string]interface{}{
			"method": "subscribe",
			"subscription": map[string]interface{}{
				"type": "l2Book",
				"coin": coin,
			},
		}

		if err := w.conn.WriteJSON(subscribeMsg); err != nil {
			w.logger.Errorf("Failed to subscribe %s: %v", coin, err)
			continue
		}

		w.subscribedCoins[coin] = true
		w.logger.Debugf("Subscribed to %s", coin)
	}

	return nil
}

func (w *websocketConn) unsubscribe(symbols []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.isConnected || w.conn == nil {
		return nil
	}

	for _, symbol := range symbols {
		coin := normalizeSymbol(symbol, true)
		
		unsubscribeMsg := map[string]interface{}{
			"method": "unsubscribe",
			"subscription": map[string]interface{}{
				"type": "l2Book",
				"coin": coin,
			},
		}

		if err := w.conn.WriteJSON(unsubscribeMsg); err != nil {
			w.logger.Errorf("Failed to unsubscribe %s: %v", coin, err)
			continue
		}

		delete(w.subscribedCoins, coin)
	}

	return nil
}

func (w *websocketConn) readLoop() {
	defer close(w.done)

	const maxReconnectAttempts = 20
	const baseBackoff = 2 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-w.stopCh:
			return
		default:
		}

		_, message, err := w.conn.ReadMessage()
		if err != nil {
			select {
			case <-w.stopCh:
				return
			default:
			}

			w.logger.Warnf("WebSocket read error, will reconnect: %v", err)
			w.mu.Lock()
			w.isConnected = false
			w.mu.Unlock()

			for attempt := 0; attempt < maxReconnectAttempts; attempt++ {
				select {
				case <-w.stopCh:
					return
				default:
				}

				backoff := baseBackoff * time.Duration(1<<uint(attempt))
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
				w.logger.Infof("Reconnecting WebSocket (attempt %d/%d) in %v...", attempt+1, maxReconnectAttempts, backoff)
				time.Sleep(backoff)

				if err := w.reconnect(); err != nil {
					w.logger.Errorf("Reconnect failed: %v", err)
					continue
				}

				w.logger.Infof("WebSocket reconnected successfully")
				break
			}

			w.mu.RLock()
			connected := w.isConnected
			w.mu.RUnlock()
			if !connected {
				w.logger.Errorf("WebSocket: max reconnect attempts reached, giving up")
				return
			}
			continue
		}

		w.handleMessage(message)
	}
}

// reconnect 重新建立 WebSocket 连接并重新订阅
func (w *websocketConn) reconnect() error {
	w.mu.Lock()
	if w.conn != nil {
		_ = w.conn.Close()
	}
	w.mu.Unlock()

	if err := w.connect(); err != nil {
		return err
	}

	w.mu.RLock()
	coins := make([]string, 0, len(w.subscribedCoins))
	for coin := range w.subscribedCoins {
		coins = append(coins, coin)
	}
	w.mu.RUnlock()

	if len(coins) > 0 {
		if err := w.subscribe(coins); err != nil {
			w.logger.Warnf("Re-subscribe failed: %v", err)
		}
	}

	return nil
}

func (w *websocketConn) handleMessage(message []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(message, &msg); err != nil {
		w.logger.Errorf("Failed to parse WebSocket message: %v", err)
		return
	}

	// 检查消息类型
	channel, ok := msg["channel"].(string)
	if !ok {
		return
	}

	// 处理不同类型的消息
	switch channel {
	case "l2Book":
		w.handleL2BookUpdate(msg)
	case "trades":
		w.handleTradeUpdate(msg)
	}
}

func (w *websocketConn) handleL2BookUpdate(msg map[string]interface{}) {
	data, ok := msg["data"].(map[string]interface{})
	if !ok {
		return
	}

	coin, ok := data["coin"].(string)
	if !ok {
		return
	}

	// 获取最优买卖价
	levels, ok := data["levels"].([]interface{})
	if !ok || len(levels) < 2 {
		return
	}

	// levels[0] = bids, levels[1] = asks
	bids, ok1 := levels[0].([]interface{})
	asks, ok2 := levels[1].([]interface{})
	if !ok1 || !ok2 || len(bids) == 0 || len(asks) == 0 {
		return
	}

	// 提取最优价格
	bidLevel, ok1 := bids[0].(map[string]interface{})
	askLevel, ok2 := asks[0].(map[string]interface{})
	if !ok1 || !ok2 {
		return
	}

	bidPrice := parseFloat64(bidLevel["px"])
	bidQty := parseFloat64(bidLevel["sz"])
	askPrice := parseFloat64(askLevel["px"])
	askQty := parseFloat64(askLevel["sz"])

	// 计算最新价格（中间价）
	lastPrice := (bidPrice + askPrice) / 2

	// 构建 Ticker
	ticker := &model.Ticker{
		Symbol:    denormalizeSymbol(coin),
		BidPrice:  bidPrice,
		BidQty:    bidQty,
		AskPrice:  askPrice,
		AskQty:    askQty,
		LastPrice: lastPrice,
		Timestamp: time.Now(),
	}

	// 调用回调
	w.mu.RLock()
	callback := w.tickerCallback
	w.mu.RUnlock()

	if callback != nil {
		callback(ticker.Symbol, ticker)
	}
}

func (w *websocketConn) handleTradeUpdate(msg map[string]interface{}) {
	// TODO: 处理成交更新（如果需要）
}

func (w *websocketConn) close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.conn != nil {
		close(w.stopCh)
		w.conn.Close()
		<-w.done
		w.conn = nil
		w.isConnected = false
		w.logger.Info("WebSocket connection closed")
	}
}
