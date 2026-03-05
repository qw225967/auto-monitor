package okx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"

	"github.com/gorilla/websocket"
)

const (
	okxWsPingInterval = 25 * time.Second
	okxWsReadDeadline = 60 * time.Second
)

// okxWsConn 封装 OKX 公开行情的 WebSocket 连接（现货+合约共用一条连接）
type okxWsConn struct {
	conn     *websocket.Conn
	url      string
	sendMu   sync.Mutex
	stopCh   chan struct{}
	closed   bool
	onTicker func(instId, instType string, d *okxTickerData)
}

// okxTickerData OKX tickers channel 单条数据
type okxTickerData struct {
	InstType string `json:"instType"` // SPOT, SWAP
	InstId   string `json:"instId"`   // BTC-USDT, BTC-USDT-SWAP
	Last     string `json:"last"`
	BidPx    string `json:"bidPx"`
	AskPx    string `json:"askPx"`
	BidSz    string `json:"bidSz"`
	AskSz    string `json:"askSz"`
	Vol24h   string `json:"vol24h"`
	Ts       string `json:"ts"` // 毫秒时间戳
}

// okxWsMsg 通用 WS 消息（event / arg + data）
type okxWsMsg struct {
	Event string `json:"event"`
	Arg   *struct {
		Channel string `json:"channel"`
		InstId  string `json:"instId"`
	} `json:"arg"`
	Data json.RawMessage `json:"data"`
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
}

// newOkxWsConn 建立 OKX 公开行情 WebSocket 连接（支持代理）
func newOkxWsConn(wsURL string) (*okxWsConn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	proxyConfig := config.GetProxyConfig()
	if proxyConfig.IsProxyEnabled() {
		if u := proxyConfig.GetProxyURL(); u != nil {
			dialer.Proxy = http.ProxyURL(u)
			logger.GetLoggerInstance().Named("okx.ws").Sugar().Infof("Using proxy: %s", u.String())
		}
	}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("okx ws dial: %w", err)
	}
	c := &okxWsConn{
		conn:   conn,
		url:    wsURL,
		stopCh: make(chan struct{}),
	}
	return c, nil
}

// send 发送文本帧
func (c *okxWsConn) send(msg string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.closed {
		return fmt.Errorf("ws closed")
	}
	return c.conn.WriteMessage(websocket.TextMessage, []byte(msg))
}

// subscribe 发送订阅请求，args 为 instId 列表，channel 固定为 tickers
func (c *okxWsConn) subscribe(instIds []string) error {
	if len(instIds) == 0 {
		return nil
	}
	args := make([]map[string]string, 0, len(instIds))
	for _, id := range instIds {
		args = append(args, map[string]string{"channel": "tickers", "instId": id})
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"op":   "subscribe",
		"args": args,
	})
	return c.send(string(payload))
}

// close 关闭连接
func (c *okxWsConn) close() {
	c.sendMu.Lock()
	if c.closed {
		c.sendMu.Unlock()
		return
	}
	c.closed = true
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
	_ = c.conn.Close()
	c.sendMu.Unlock()
}

// runReadLoop 在 goroutine 中调用，读取消息并解析 ticker，收到 stop 或错误后返回
func (c *okxWsConn) runReadLoop() {
	log := logger.GetLoggerInstance().Named("okx.ws").Sugar()
	defer c.close()

	c.conn.SetReadDeadline(time.Now().Add(okxWsReadDeadline))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(okxWsReadDeadline))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !c.closed {
				log.Warnf("okx ws read: %v", err)
			}
			return
		}
		c.conn.SetReadDeadline(time.Now().Add(okxWsReadDeadline))

		var msg okxWsMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg.Event != "" {
			switch msg.Event {
			case "subscribe":
				log.Debugf("okx subscribe ack: arg=%+v", msg.Arg)
			case "error":
				log.Warnf("okx ws error: code=%s msg=%s", msg.Code, msg.Msg)
			}
			continue
		}

		if len(msg.Data) == 0 || msg.Arg == nil || msg.Arg.Channel != "tickers" {
			continue
		}

		var tickers []okxTickerData
		if err := json.Unmarshal(msg.Data, &tickers); err != nil {
			continue
		}
		if c.onTicker != nil {
			for i := range tickers {
				instType := tickers[i].InstType
				if instType == "" && msg.Arg != nil {
					instType = "SPOT"
					if strings.HasSuffix(msg.Arg.InstId, "-SWAP") {
						instType = "SWAP"
					}
				}
				c.onTicker(tickers[i].InstId, instType, &tickers[i])
			}
		}
	}
}

// startPing 在 goroutine 中调用，定期发送 ping
func (c *okxWsConn) startPing() {
	ticker := time.NewTicker(okxWsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.sendMu.Lock()
			if c.closed {
				c.sendMu.Unlock()
				return
			}
			_ = c.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second))
			c.sendMu.Unlock()
		}
	}
}

// runWsPublic 建立公开行情连接并订阅 instIds，在 goroutine 中运行；断线后自动重连
func (o *okx) runWsPublic(instIds []string) {
	if len(instIds) == 0 {
		return
	}
	log := logger.GetLoggerInstance().Named("okx.ws").Sugar()

	const maxReconnectAttempts = 30
	const baseBackoff = 2 * time.Second
	const maxBackoff = 30 * time.Second

	for attempt := 0; ; attempt++ {
		// 检查 context 是否已取消（全局停止）
		o.mu.RLock()
		ctx := o.wsCtx
		o.mu.RUnlock()
		if ctx != nil && ctx.Err() != nil {
			log.Infof("okx ws context cancelled, stop reconnecting")
			return
		}

		// 重新读取当前订阅列表（可能在断线期间有变化）
		if attempt > 0 {
			o.mu.RLock()
			instIds = o.buildTickerInstIdsLocked()
			o.mu.RUnlock()
			if len(instIds) == 0 {
				log.Infof("okx ws: no subscriptions, stop reconnecting")
				return
			}
		}

		conn, err := newOkxWsConn(constants.OkexWsPublicUrl)
		if err != nil {
			log.Errorf("okx ws connect (attempt %d): %v", attempt+1, err)
			if attempt >= maxReconnectAttempts {
				log.Errorf("okx ws: max reconnect attempts reached, giving up")
				return
			}
			backoff := baseBackoff * time.Duration(1<<uint(attempt))
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			time.Sleep(backoff)
			continue
		}

		conn.onTicker = func(instId, instType string, d *okxTickerData) {
			if d == nil {
				return
			}
			o.handleTickerUpdate(instId, instType, d)
		}

		if err := conn.subscribe(instIds); err != nil {
			log.Errorf("okx ws subscribe: %v", err)
			conn.close()
			time.Sleep(baseBackoff)
			continue
		}

		o.mu.Lock()
		o.wsConn = conn
		o.mu.Unlock()

		go conn.startPing()
		conn.runReadLoop() // 阻塞直到断线

		o.mu.Lock()
		o.wsConn = nil
		o.mu.Unlock()

		// 正常断开（context 取消）则不重连
		if ctx != nil && ctx.Err() != nil {
			return
		}

		log.Warnf("okx ws disconnected, will reconnect in %v (attempt %d)", baseBackoff, attempt+1)
		attempt = 0 // 成功连接过，重置计数
		time.Sleep(baseBackoff)
	}
}

// handleTickerUpdate 将 OKX ticker 转为 model.Ticker 并回调
func (o *okx) handleTickerUpdate(instId, instType string, d *okxTickerData) {
	o.mu.RLock()
	callback := o.tickerCallback
	o.mu.RUnlock()
	if callback == nil {
		return
	}
	ticker := okxTickerDataToModel(d)
	if ticker == nil {
		return
	}
	marketType := spotFlag
	if strings.EqualFold(instType, "SWAP") {
		marketType = futuresFlag
	}
	callback(ticker.Symbol, ticker, marketType)
}

func okxTickerDataToModel(d *okxTickerData) *model.Ticker {
	if d == nil {
		return nil
	}
	last, _ := strconv.ParseFloat(d.Last, 64)
	bidPx, _ := strconv.ParseFloat(d.BidPx, 64)
	askPx, _ := strconv.ParseFloat(d.AskPx, 64)
	bidSz, _ := strconv.ParseFloat(d.BidSz, 64)
	askSz, _ := strconv.ParseFloat(d.AskSz, 64)
	vol, _ := strconv.ParseFloat(d.Vol24h, 64)
	ts := time.Now()
	if d.Ts != "" {
		if ms, err := strconv.ParseInt(d.Ts, 10, 64); err == nil {
			ts = time.UnixMilli(ms)
		}
	}
	return &model.Ticker{
		Symbol:    FromOKXInstId(d.InstId),
		LastPrice: last,
		BidPrice:  bidPx,
		AskPrice:  askPx,
		BidQty:    bidSz,
		AskQty:    askSz,
		Volume:    vol,
		Timestamp: ts,
	}
}
