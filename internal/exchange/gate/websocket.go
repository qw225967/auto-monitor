package gate

import (
	"encoding/json"
	"strconv"
	"time"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"

	gate "github.com/gateio/gatews/go"
)

// FlexibleSize 灵活的 size 类型，支持字符串和整数
type FlexibleSize string

// UnmarshalJSON 支持解析字符串或整数
func (f *FlexibleSize) UnmarshalJSON(data []byte) error {
	// 先尝试解析为字符串
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = FlexibleSize(s)
		return nil
	}
	// 如果失败，尝试解析为整数
	var i int64
	if err := json.Unmarshal(data, &i); err == nil {
		*f = FlexibleSize(strconv.FormatInt(i, 10))
		return nil
	}
	// 如果都失败，尝试解析为浮点数
	var fl float64
	if err := json.Unmarshal(data, &fl); err == nil {
		*f = FlexibleSize(strconv.FormatFloat(fl, 'f', -1, 64))
		return nil
	}
	return json.Unmarshal(data, (*string)(f))
}

// handleSpotTickerUpdate 处理现货 ticker 更新
func (g *gateExchange) handleSpotTickerUpdate(msg *gate.UpdateMsg) {
	log := logger.GetLoggerInstance().Named("gate.websocket").Sugar()

	var ticker GateSpotBookTicker
	if err := json.Unmarshal(msg.Result, &ticker); err != nil {
		log.Debugf("Failed to unmarshal spot ticker: %v", err)
		return
	}

	if ticker.Bid == "" || ticker.Ask == "" {
		return
	}

	// 转换为统一格式
	modelTicker := convertGateSpotToTicker(&ticker)
	if modelTicker == nil {
		return
	}

	// 将 Gate 格式的 symbol 转换回原始格式
	g.mu.RLock()
	callback := g.tickerCallback
	originalSymbol := modelTicker.Symbol
	if mappedSymbol, ok := g.gateSymbolToOriginal[modelTicker.Symbol]; ok {
		originalSymbol = mappedSymbol
		// 更新 ticker 中的 symbol 为原始格式
		modelTicker.Symbol = originalSymbol
	}
	g.mu.RUnlock()

	if callback != nil {
		callback(originalSymbol, modelTicker, "spot")
	}

	log.Debugf("Received spot ticker for %s: bid=%.4f, ask=%.4f",
		modelTicker.Symbol, modelTicker.BidPrice, modelTicker.AskPrice)
}

// handleFuturesTickerUpdate 处理合约 ticker 更新
func (g *gateExchange) handleFuturesTickerUpdate(msg *gate.UpdateMsg) {
	log := logger.GetLoggerInstance().Named("gate.websocket").Sugar()

	var ticker GateFuturesBookTicker
	if err := json.Unmarshal(msg.Result, &ticker); err != nil {
		log.Debugf("Failed to unmarshal futures ticker: %v", err)
		return
	}

	if ticker.Bid == "" || ticker.Ask == "" {
		return
	}

	// 转换为统一格式
	modelTicker := convertGateFuturesToTicker(&ticker)
	if modelTicker == nil {
		return
	}

	// 将 Gate 格式的 symbol 转换回原始格式
	g.mu.RLock()
	callback := g.tickerCallback
	originalSymbol := modelTicker.Symbol
	if mappedSymbol, ok := g.gateSymbolToOriginal[modelTicker.Symbol]; ok {
		originalSymbol = mappedSymbol
		// 更新 ticker 中的 symbol 为原始格式
		modelTicker.Symbol = originalSymbol
	}
	g.mu.RUnlock()

	if callback != nil {
		callback(originalSymbol, modelTicker, "futures")
	}

	log.Debugf("Received futures ticker for %s: bid=%.4f, ask=%.4f",
		modelTicker.Symbol, modelTicker.BidPrice, modelTicker.AskPrice)
}

// convertGateSpotToTicker 将 Gate.io 现货数据转换为统一格式
func convertGateSpotToTicker(ticker *GateSpotBookTicker) *model.Ticker {
	if ticker == nil || ticker.Bid == "" || ticker.Ask == "" {
		return nil
	}

	bidPrice, err := strconv.ParseFloat(ticker.Bid, 64)
	if err != nil {
		return nil
	}
	askPrice, err := strconv.ParseFloat(ticker.Ask, 64)
	if err != nil {
		return nil
	}

	bidQty, _ := strconv.ParseFloat(string(ticker.BidSize), 64)
	askQty, _ := strconv.ParseFloat(string(ticker.AskSize), 64)

	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    ticker.Symbol,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: time.Now(),
	}
}

// convertGateFuturesToTicker 将 Gate.io 合约数据转换为统一格式
func convertGateFuturesToTicker(ticker *GateFuturesBookTicker) *model.Ticker {
	if ticker == nil || ticker.Bid == "" || ticker.Ask == "" {
		return nil
	}

	bidPrice, err := strconv.ParseFloat(ticker.Bid, 64)
	if err != nil {
		return nil
	}
	askPrice, err := strconv.ParseFloat(ticker.Ask, 64)
	if err != nil {
		return nil
	}

	bidQty, _ := strconv.ParseFloat(string(ticker.BidSize), 64)
	askQty, _ := strconv.ParseFloat(string(ticker.AskSize), 64)

	lastPrice := (bidPrice + askPrice) / 2.0

	return &model.Ticker{
		Symbol:    ticker.Symbol,
		LastPrice: lastPrice,
		BidPrice:  bidPrice,
		AskPrice:  askPrice,
		BidQty:    bidQty,
		AskQty:    askQty,
		Volume:    0,
		Timestamp: time.Now(),
	}
}

// GateSpotBookTicker Gate.io 现货 BookTicker 数据结构
type GateSpotBookTicker struct {
	Symbol  string `json:"s"`
	Bid     string `json:"b"`
	BidSize string `json:"B"`
	Ask     string `json:"a"`
	AskSize string `json:"A"`
	T       int64  `json:"t"`
	U       int64  `json:"u"`
}

// GateFuturesBookTicker Gate.io 合约 BookTicker 数据结构
type GateFuturesBookTicker struct {
	Symbol  string       `json:"s"`
	Bid     string       `json:"b"`
	BidSize FlexibleSize `json:"B"` // 支持字符串或整数
	Ask     string       `json:"a"`
	AskSize FlexibleSize `json:"A"` // 支持字符串或整数
	T       int64        `json:"t"`
	U       int64        `json:"u"`
}
