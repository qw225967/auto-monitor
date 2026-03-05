package lighter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type WebSocketClient struct {
	conn            *websocket.Conn
	mu              sync.RWMutex
	apiKey          string
	token           string
	tickerCallback  exchange.TickerCallback
	logger          *zap.SugaredLogger
	isConnected     bool
	subscribedCoins map[string]bool
	stopCh          chan struct{}
	done            chan struct{}
}

func NewWebSocketClient(apiKey, token string, callback exchange.TickerCallback) *WebSocketClient {
	return &WebSocketClient{
		apiKey:          apiKey,
		token:           token,
		tickerCallback:  callback,
		logger:          getLogger().Named("lighter.websocket"),
		subscribedCoins: make(map[string]bool),
		stopCh:          make(chan struct{}),
		done:            make(chan struct{}),
	}
}

func (wsc *WebSocketClient) Connect() error {
	wsc.mu.Lock()
	defer wsc.mu.Unlock()

	if wsc.isConnected {
		return nil
	}

	// 配置代理
	dialer := websocket.DefaultDialer
	if proxyConfig := config.GetProxyConfig(); proxyConfig != nil {
		if proxyURL := proxyConfig.GetProxyURL(); proxyURL != nil {
			dialer.Proxy = func(*http.Request) (*url.URL, error) {
				return proxyURL, nil
			}
			wsc.logger.Infof("Using proxy for WebSocket: %s", proxyConfig.GetProxyURLString())
		}
	}

	// 连接 WebSocket（带认证参数）
	wsURL := fmt.Sprintf("%s?api_key=%s&token=%s", constants.LighterWsUrl, wsc.apiKey, wsc.token)
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect WebSocket: %w", err)
	}

	wsc.conn = conn
	wsc.isConnected = true
	wsc.logger.Infof("WebSocket connected to %s", constants.LighterWsUrl)

	// 启动消息处理
	go wsc.handleMessages()

	return nil
}

func (wsc *WebSocketClient) Disconnect() {
	wsc.mu.Lock()
	defer wsc.mu.Unlock()

	if !wsc.isConnected {
		return
	}

	close(wsc.stopCh)
	if wsc.conn != nil {
		wsc.conn.Close()
	}
	wsc.isConnected = false
}

func (wsc *WebSocketClient) Subscribe(symbols []string) error {
	if err := wsc.Connect(); err != nil {
		return err
	}

	wsc.mu.Lock()
	defer wsc.mu.Unlock()

	for _, symbol := range symbols {
		normalizedSymbol := normalizeLighterSymbol(symbol)
		
		subscribeMsg := map[string]interface{}{
			"type":    "subscribe",
			"channel": "ticker",
			"symbol":  normalizedSymbol,
		}

		msgJSON, _ := json.Marshal(subscribeMsg)
		if err := wsc.conn.WriteMessage(websocket.TextMessage, msgJSON); err != nil {
			return fmt.Errorf("failed to subscribe %s: %w", symbol, err)
		}

		wsc.subscribedCoins[normalizedSymbol] = true
		wsc.logger.Infof("Subscribed to ticker: %s", normalizedSymbol)
	}

	return nil
}

func (wsc *WebSocketClient) handleMessages() {
	defer close(wsc.done)

	const maxReconnectAttempts = 20
	const baseBackoff = 2 * time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-wsc.stopCh:
			return
		default:
			_, message, err := wsc.conn.ReadMessage()
			if err != nil {
				select {
				case <-wsc.stopCh:
					return
				default:
				}

				wsc.logger.Warnf("WebSocket read error, will reconnect: %v", err)
				wsc.mu.Lock()
				wsc.isConnected = false
				wsc.mu.Unlock()

				for attempt := 0; attempt < maxReconnectAttempts; attempt++ {
					select {
					case <-wsc.stopCh:
						return
					default:
					}

					backoff := baseBackoff * time.Duration(1<<uint(attempt))
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
					wsc.logger.Infof("Reconnecting WebSocket (attempt %d/%d) in %v...", attempt+1, maxReconnectAttempts, backoff)
					time.Sleep(backoff)

					if err := wsc.reconnectAndResubscribe(); err != nil {
						wsc.logger.Errorf("Reconnect failed: %v", err)
						continue
					}
					wsc.logger.Infof("WebSocket reconnected successfully")
					break
				}

				wsc.mu.RLock()
				connected := wsc.isConnected
				wsc.mu.RUnlock()
				if !connected {
					wsc.logger.Errorf("WebSocket: max reconnect attempts reached, giving up")
					return
				}
				continue
			}

			wsc.processMessage(message)
		}
	}
}

// reconnectAndResubscribe 重新建立连接并重新订阅已有 symbol
func (wsc *WebSocketClient) reconnectAndResubscribe() error {
	wsc.mu.Lock()
	if wsc.conn != nil {
		_ = wsc.conn.Close()
	}
	wsc.isConnected = false
	coins := make([]string, 0, len(wsc.subscribedCoins))
	for coin := range wsc.subscribedCoins {
		coins = append(coins, coin)
	}
	wsc.subscribedCoins = make(map[string]bool)
	wsc.mu.Unlock()

	if err := wsc.Connect(); err != nil {
		return err
	}

	if err := wsc.Subscribe(coins); err != nil {
		return fmt.Errorf("re-subscribe failed: %w", err)
	}

	return nil
}

func (wsc *WebSocketClient) processMessage(message []byte) {
	var msg struct {
		Channel string `json:"channel"`
		Data    struct {
			Symbol    string `json:"symbol"`
			BidPrice  string `json:"bid_price"`
			AskPrice  string `json:"ask_price"`
			LastPrice string `json:"last_price"`
			BidQty    string `json:"bid_qty"`
			AskQty    string `json:"ask_qty"`
		} `json:"data"`
	}

	if err := json.Unmarshal(message, &msg); err != nil {
		wsc.logger.Debugf("Failed to parse message: %v", err)
		return
	}

	// 处理 ticker 数据
	if msg.Channel == "ticker" && wsc.tickerCallback != nil {
		bidPrice, _ := parseFloat(msg.Data.BidPrice)
		askPrice, _ := parseFloat(msg.Data.AskPrice)
		lastPrice, _ := parseFloat(msg.Data.LastPrice)
		bidQty, _ := parseFloat(msg.Data.BidQty)
		askQty, _ := parseFloat(msg.Data.AskQty)

		ticker := &model.Ticker{
			Symbol:    denormalizeSymbol(msg.Data.Symbol),
			BidPrice:  bidPrice,
			AskPrice:  askPrice,
			LastPrice: lastPrice,
			BidQty:    bidQty,
			AskQty:    askQty,
			Timestamp: time.Now(),
		}

		// Lighter 是 DEX，默认使用 futures
		wsc.tickerCallback(ticker.Symbol, ticker, "futures")
	}
}

func (wsc *WebSocketClient) SetCallback(callback exchange.TickerCallback) {
	wsc.mu.Lock()
	defer wsc.mu.Unlock()
	wsc.tickerCallback = callback
}

func getLogger() *zap.SugaredLogger {
	return zap.NewNop().Sugar() // 简化实现
}
