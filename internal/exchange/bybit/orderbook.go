package bybit

import (
	"encoding/json"
	"fmt"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
)

// GetSpotOrderBook 获取现货订单簿
func (b *bybit) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	if !b.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	apiURL := fmt.Sprintf("%s%s?category=spot&symbol=%s&limit=500",
		constants.BybitRestBaseUrl,
		constants.BybitSpotOrderBookPath,
		symbol)

	responseBody, err := restClient.DoGet(constants.ConnectTypeBybit, apiURL, "", "", "", "", "")
	if err != nil {
		return nil, nil, fmt.Errorf("get spot orderbook failed: %w", err)
	}

	var orderBookResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Symbol string     `json:"s"`
			Bids   [][]string `json:"b"` // [[price, qty], ...]
			Asks   [][]string `json:"a"` // [[price, qty], ...]
			Ts     int64      `json:"ts"`
			U      int64      `json:"u"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderBookResp); err != nil {
		return nil, nil, fmt.Errorf("parse orderbook response failed: %w", err)
	}

	if orderBookResp.RetCode != 0 {
		return nil, nil, fmt.Errorf("bybit API error: code=%d, msg=%s", orderBookResp.RetCode, orderBookResp.RetMsg)
	}

	return orderBookResp.Result.Bids, orderBookResp.Result.Asks, nil
}

// GetFuturesOrderBook 获取合约订单簿
func (b *bybit) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	if !b.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	apiURL := fmt.Sprintf("%s%s?category=linear&symbol=%s&limit=500",
		constants.BybitRestBaseUrl,
		constants.BybitFuturesOrderBookPath,
		symbol)

	responseBody, err := restClient.DoGet(constants.ConnectTypeBybit, apiURL, "", "", "", "", "")
	if err != nil {
		return nil, nil, fmt.Errorf("get futures orderbook failed: %w", err)
	}

	var orderBookResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Symbol string     `json:"s"`
			Bids   [][]string `json:"b"` // [[price, qty], ...]
			Asks   [][]string `json:"a"` // [[price, qty], ...]
			Ts     int64      `json:"ts"`
			U      int64      `json:"u"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &orderBookResp); err != nil {
		return nil, nil, fmt.Errorf("parse orderbook response failed: %w", err)
	}

	if orderBookResp.RetCode != 0 {
		return nil, nil, fmt.Errorf("bybit API error: code=%d, msg=%s", orderBookResp.RetCode, orderBookResp.RetMsg)
	}

	return orderBookResp.Result.Bids, orderBookResp.Result.Asks, nil
}
