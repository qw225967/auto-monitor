package bybit

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

// handleTickerUpdate 处理 ticker 更新事件（WebSocket 回调）
func (b *bybit) handleTickerUpdate(message string, marketType string) {
	if message == "" {
		return
	}

	log := logger.GetLoggerInstance().Named("bybit.websocket").Sugar()

	// 解析 Bybit OrderbookSnapshot
	var snap BybitOrderbookSnapshot
	if err := json.Unmarshal([]byte(message), &snap); err != nil {
		log.Debugf("Failed to unmarshal bybit message: %v, message: %s", err, message)
		return
	}

	// 验证数据有效性
	if snap.Topic == "" || snap.Data.Symbol == "" {
		return
	}

	if len(snap.Data.Bids) == 0 || len(snap.Data.Asks) == 0 {
		log.Debugf("Empty bids or asks for symbol %s", snap.Data.Symbol)
		return
	}

	// 转换为统一格式
	ticker := convertBybitToTicker(&snap, marketType)
	if ticker == nil {
		return
	}

	// 触发回调
	b.mu.RLock()
	callback := b.tickerCallback
	b.mu.RUnlock()

	if callback != nil {
		callback(ticker.Symbol, ticker, marketType)
	}

	log.Debugf("Received %s ticker for %s: bid=%.4f, ask=%.4f", 
		marketType, ticker.Symbol, ticker.BidPrice, ticker.AskPrice)
}

// convertBybitToTicker 将 Bybit 数据转换为统一的 Ticker 格式
func convertBybitToTicker(snap *BybitOrderbookSnapshot, marketType string) *model.Ticker {
	if snap == nil || len(snap.Data.Bids) == 0 || len(snap.Data.Asks) == 0 {
		return nil
	}

	// 解析买一价和卖一价
	bidPrice, err := strconv.ParseFloat(snap.Data.Bids[0][0], 64)
	if err != nil {
		return nil
	}
	askPrice, err := strconv.ParseFloat(snap.Data.Asks[0][0], 64)
	if err != nil {
		return nil
	}

	// 解析买一量和卖一量
	bidQty, _ := strconv.ParseFloat(snap.Data.Bids[0][1], 64)
	askQty, _ := strconv.ParseFloat(snap.Data.Asks[0][1], 64)

	// 计算最新价（买一价和卖一价的中间值）
	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    snap.Data.Symbol,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0, // Bybit orderbook 数据不包含成交量
		Timestamp: time.Now(),
	}
}

// BybitOrderbookSnapshot Bybit 订单簿快照数据结构
type BybitOrderbookSnapshot struct {
	Topic string `json:"topic"`
	Type  string `json:"type"`
	Ts    int64  `json:"ts"`
	Data  struct {
		Symbol string     `json:"s"`
		Bids   [][]string `json:"b"` // [[price, qty], ...]
		Asks   [][]string `json:"a"` // [[price, qty], ...]
		U      int64      `json:"u"` // Update ID
		Seq    int64      `json:"seq"`
	} `json:"data"`
}
