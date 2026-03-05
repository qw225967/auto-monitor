package gate

import (
	"encoding/json"
	"fmt"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
)

// GetSpotOrderBook 获取现货订单簿
func (g *gateExchange) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	normalizedSymbol := normalizeGateSymbol(symbol)
	queryString := fmt.Sprintf("currency_pair=%s&limit=500", normalizedSymbol)
	
	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateSpotOrderBookPath, queryString, "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, constants.GateSpotOrderBookPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("get spot orderbook failed: %w", err)
	}

	var orderBookResp struct {
		Id      int64      `json:"id"`
		Current float64    `json:"current"` // Gate.io 返回浮点数时间戳
		Update  float64    `json:"update"`  // Gate.io 返回浮点数时间戳
		Asks    [][]string `json:"asks"`    // [[price, amount], ...]
		Bids    [][]string `json:"bids"`    // [[price, amount], ...]
	}

	if err := json.Unmarshal([]byte(responseBody), &orderBookResp); err != nil {
		return nil, nil, fmt.Errorf("parse spot orderbook response failed: %w", err)
	}

	return orderBookResp.Bids, orderBookResp.Asks, nil
}

// GetFuturesOrderBook 获取合约订单簿
func (g *gateExchange) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	normalizedSymbol := normalizeGateSymbol(symbol)
	queryString := fmt.Sprintf("contract=%s&limit=300", normalizedSymbol)
	
	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateFuturesOrderBookPath, queryString, "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, constants.GateFuturesOrderBookPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("get futures orderbook failed: %w", err)
	}

	// Gate.io 合约订单簿响应结构与现货不同
	var orderBookResp struct {
		Current float64 `json:"current"` // Gate.io 返回浮点数时间戳
		Update  float64 `json:"update"`  // Gate.io 返回浮点数时间戳
		Asks    []struct {
			P string `json:"p"` // price
			S int    `json:"s"` // size
		} `json:"asks"`
		Bids []struct {
			P string `json:"p"` // price
			S int    `json:"s"` // size
		} `json:"bids"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderBookResp); err != nil {
		return nil, nil, fmt.Errorf("parse futures orderbook response failed: %w", err)
	}

	// 转换为统一格式 [[price, amount], ...]
	bids := make([][]string, len(orderBookResp.Bids))
	for i, bid := range orderBookResp.Bids {
		bids[i] = []string{bid.P, fmt.Sprintf("%d", bid.S)}
	}

	asks := make([][]string, len(orderBookResp.Asks))
	for i, ask := range orderBookResp.Asks {
		asks[i] = []string{ask.P, fmt.Sprintf("%d", ask.S)}
	}

	return bids, asks, nil
}
