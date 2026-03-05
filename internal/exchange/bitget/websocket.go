package bitget

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

// handleWebSocketMessage 处理 WebSocket 消息
func (b *bitgetExchange) handleWebSocketMessage(message string) {
	log := logger.GetLoggerInstance().Named("bitget.websocket").Sugar()

	// 更新最后消息时间
	b.mu.Lock()
	b.lastMessageTime = time.Now()
	b.mu.Unlock()

	// 处理 pong 响应（Bitget 返回 "pong" 文本）
	if message == "pong" {
		log.Debug("Received pong")
		return
	}

	// V2 API 使用不同的消息格式
	// 尝试解析 V2 格式 (action + arg + data)
	var v2Msg struct {
		Action string          `json:"action"` // V2 使用 action 而不是 event
		Arg    json.RawMessage `json:"arg"`
		Data   json.RawMessage `json:"data"`
	}
	
	if err := json.Unmarshal([]byte(message), &v2Msg); err != nil {
		log.Debugf("Failed to unmarshal message: %v", err)
		return
	}

	// 也尝试解析旧格式 (event) 以兼容
	var v1Msg struct {
		Event string          `json:"event"`
		Arg   json.RawMessage `json:"arg"`
		Data  json.RawMessage `json:"data"`
	}
	json.Unmarshal([]byte(message), &v1Msg)

	// 处理 V2 消息
	if v2Msg.Action != "" {
		switch v2Msg.Action {
		case "snapshot", "update":
			// ticker 数据更新（V2 可能是 snapshot 或 update）
			b.handleTickerUpdate(v2Msg.Data)
		default:
			log.Debugf("Received unknown V2 action: %s", v2Msg.Action)
		}
		return
	}

	// 处理 V1 消息（兼容旧格式）
	switch v1Msg.Event {
	case "login":
		log.Info("WebSocket login successful")
	case "subscribe":
		log.Info("WebSocket subscription successful")
	case "update":
		b.handleTickerUpdate(v1Msg.Data)
	default:
		if v1Msg.Event != "" {
			log.Debugf("Received unknown V1 event: %s", v1Msg.Event)
		}
	}
}

// handleDisconnected 处理断开连接
func (b *bitgetExchange) handleDisconnected() {
	log := logger.GetLoggerInstance().Named("bitget.websocket").Sugar()
	log.Warn("WebSocket disconnected, triggering reconnect")

	b.mu.Lock()
	b.isWsConnected = false
	b.mu.Unlock()

	// 触发重连
	go b.reconnect()
}

// handleTickerUpdate 处理 ticker 更新
func (b *bitgetExchange) handleTickerUpdate(data json.RawMessage) {
	log := logger.GetLoggerInstance().Named("bitget.ticker").Sugar()

	// V2 ticker 数据使用新的结构
	var tickers []BitgetTicker
	if err := json.Unmarshal(data, &tickers); err != nil {
		log.Errorf("❌ Failed to unmarshal ticker data: %v", err)
		log.Debugf("Raw data: %s", string(data))
		return
	}

	log.Debugf("✅ Parsed %d tickers", len(tickers))

	for _, ticker := range tickers {
		// 转换为统一格式
		modelTicker := convertBitgetV2TickerToModel(&ticker)
		if modelTicker == nil {
			log.Warnf("⚠️  Failed to convert ticker %s", ticker.InstId)
			continue
		}

		// 判断是 spot 还是 futures（根据订阅的 symbol 列表）
		b.mu.RLock()
		callback := b.tickerCallback
		marketType := "futures" // 默认使用 futures
		if _, isSpot := b.subscribedSpotSymbols[modelTicker.Symbol]; isSpot {
			marketType = "spot"
		} else if _, isFutures := b.subscribedFuturesSymbols[modelTicker.Symbol]; isFutures {
			marketType = "futures"
		}
		b.mu.RUnlock()

		if callback != nil {
			callback(modelTicker.Symbol, modelTicker, marketType)
		}

		log.Debugf("✅ Received ticker for %s: bid=%.4f, ask=%.4f, last=%.4f",
			modelTicker.Symbol, modelTicker.BidPrice, modelTicker.AskPrice, modelTicker.LastPrice)
	}
}

// convertBitgetToTicker 将 Bitget OrderBook 数据转换为统一格式（保留用于 orderbook 频道）
func convertBitgetToTicker(ticker *BitgetBookTicker) *model.Ticker {
	if ticker == nil || len(ticker.Bids) == 0 || len(ticker.Asks) == 0 {
		return nil
	}

	// Bitget 的 bids/asks 格式：[[price, qty], ...]
	bidPrice, err := strconv.ParseFloat(ticker.Bids[0][0], 64)
	if err != nil {
		return nil
	}
	askPrice, err := strconv.ParseFloat(ticker.Asks[0][0], 64)
	if err != nil {
		return nil
	}

	bidQty, _ := strconv.ParseFloat(ticker.Bids[0][1], 64)
	askQty, _ := strconv.ParseFloat(ticker.Asks[0][1], 64)

	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    ticker.InstId,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: time.Unix(ticker.Ts/1000, 0),
	}
}

// convertBitgetV2TickerToModel 将 Bitget V2 Ticker 数据转换为统一格式
func convertBitgetV2TickerToModel(ticker *BitgetTicker) *model.Ticker {
	if ticker == nil {
		return nil
	}

	// 解析价格和数量
	bidPrice, err := strconv.ParseFloat(ticker.BidPr, 64)
	if err != nil {
		return nil
	}
	askPrice, err := strconv.ParseFloat(ticker.AskPr, 64)
	if err != nil {
		return nil
	}
	lastPrice, err := strconv.ParseFloat(ticker.LastPr, 64)
	if err != nil {
		// 如果没有 lastPr，使用中间价
		lastPrice = (bidPrice + askPrice) / 2.0
	}

	bidQty, _ := strconv.ParseFloat(ticker.BidSz, 64)
	askQty, _ := strconv.ParseFloat(ticker.AskSz, 64)

	// 解析时间戳（V2 是字符串，毫秒级）
	var timestamp time.Time
	if ticker.Ts != "" {
		if ts, err := strconv.ParseInt(ticker.Ts, 10, 64); err == nil {
			timestamp = time.Unix(0, ts*int64(time.Millisecond))
		}
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	return &model.Ticker{
		Symbol:    ticker.InstId,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: timestamp,
	}
}

// BitgetBookTicker Bitget BookTicker 数据结构（用于 orderbook）
type BitgetBookTicker struct {
	InstId string     `json:"instId"`
	Bids   [][]string `json:"bids"` // [[price, qty], ...]
	Asks   [][]string `json:"asks"` // [[price, qty], ...]
	Ts     int64      `json:"ts"`
}

// BitgetTicker Bitget V2 Ticker 数据结构（用于 ticker 频道）
type BitgetTicker struct {
	InstId   string `json:"instId"`   // 交易对
	LastPr   string `json:"lastPr"`   // 最新成交价
	BidPr    string `json:"bidPr"`    // 买一价
	AskPr    string `json:"askPr"`    // 卖一价
	BidSz    string `json:"bidSz"`    // 买一量
	AskSz    string `json:"askSz"`    // 卖一量
	Open24h  string `json:"open24h"`  // 24h开盘价
	High24h  string `json:"high24h"`  // 24h最高价
	Low24h   string `json:"low24h"`   // 24h最低价
	Change24h string `json:"change24h"` // 24h涨跌幅
	Ts       string `json:"ts"`       // 时间戳（V2 是字符串）
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
