package aster

import (
	"github.com/qw225967/auto-monitor/internal/model"
	"time"
)

// handleTickerUpdate 处理ticker更新事件
func (a *aster) handleTickerUpdate(symbol string, ticker *model.Ticker) {
	a.mu.RLock()
	callback := a.tickerCallback
	a.mu.RUnlock()

	if callback == nil {
		return
	}

	// Aster 是 DEX，默认使用 futures
	callback(symbol, ticker, "futures")
}

// convertBookTickerToTicker 将 BookTicker 数据转换为 model.Ticker
func convertBookTickerToTicker(symbol string, bidPrice, askPrice, bidQty, askQty float64) *model.Ticker {
	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    symbol,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: time.Now(),
	}
}
