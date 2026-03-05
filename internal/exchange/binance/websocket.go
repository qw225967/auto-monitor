package binance

import (
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"

	binance_connector "github.com/binance/binance-connector-go"
)

// handleTickerUpdate 处理ticker更新事件
func (b *binance) handleTickerUpdate(event *binance_connector.WsBookTickerEvent, marketType string) {
	b.mu.RLock()
	callback := b.tickerCallback
	b.mu.RUnlock()

	if callback == nil {
		return
	}

	ticker := convertBookTickerToTicker(event, marketType)
	if ticker == nil {
		return
	}

	callback(ticker.Symbol, ticker, marketType)
}

// handleTickerError 处理ticker更新错误
func (b *binance) handleTickerError(err error) {
	if err == nil {
		return
	}
	log := logger.GetLoggerInstance()
	log.Error("Binance ticker update error", zap.Error(err))

	// 检测断链错误，触发重连
	errStr := err.Error()
	if strings.Contains(errStr, "close 1006") || // abnormal closure
		strings.Contains(errStr, "unexpected EOF") ||
		strings.Contains(errStr, "connection closed") ||
		strings.Contains(errStr, "broken pipe") {
		log.Warn("检测到 WebSocket 断链错误，将在 doneCh 触发时自动重连", zap.Error(err))
		// 注意：实际的重连会在 watchConnection 中通过 doneCh 触发
	}
}

// convertBookTickerToTicker 将币安的 BookTicker 事件转换为 model.Ticker
func convertBookTickerToTicker(event *binance_connector.WsBookTickerEvent, marketType string) *model.Ticker {
	if event == nil {
		return nil
	}

	bidPrice, _ := strconv.ParseFloat(event.BestBidPrice, 64)
	bidQty, _ := strconv.ParseFloat(event.BestBidQty, 64)
	askPrice, _ := strconv.ParseFloat(event.BestAskPrice, 64)
	askQty, _ := strconv.ParseFloat(event.BestAskQty, 64)

	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    event.Symbol,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: time.Now(),
	}
}
