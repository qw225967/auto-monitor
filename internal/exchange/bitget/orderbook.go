package bitget

import (
	"encoding/json"
	"fmt"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
)

// GetSpotOrderBook 获取现货订单簿 (V2 API)
func (b *bitgetExchange) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	b.mu.RLock()
	restClient := b.restClient
	apiKey, secretKey, passphrase := b.getAPIKeys()
	b.mu.RUnlock()

	if !b.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	normalizedSymbol := normalizeBitgetSymbol(symbol, false)
	// V2 API 参数格式：symbol, type (step0-step4), limit
	queryString := fmt.Sprintf("symbol=%s&type=step0&limit=100", normalizedSymbol)
	
	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetSpotOrderBookPath, queryString, "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetSpotOrderBookPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("get spot orderbook failed: %w", err)
	}

	// V2 API 响应格式
	var resp struct {
		Code        string `json:"code"`
		Msg         string `json:"msg"`
		RequestTime int64  `json:"requestTime"` // V2 新增字段
		Data        struct {
			Asks [][]string `json:"asks"` // [[price, quantity], ...]
			Bids [][]string `json:"bids"` // [[price, quantity], ...]
			Ts   string     `json:"ts"`   // V2 返回字符串格式的时间戳
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse spot orderbook response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	return resp.Data.Bids, resp.Data.Asks, nil
}

// GetFuturesOrderBook 获取合约订单簿 (V2 API)
func (b *bitgetExchange) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	b.mu.RLock()
	restClient := b.restClient
	apiKey, secretKey, passphrase := b.getAPIKeys()
	b.mu.RUnlock()

	if !b.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}

	normalizedSymbol := normalizeBitgetSymbol(symbol, true)
	// V2 API 参数：productType (USDT-FUTURES/USDC-FUTURES/COIN-FUTURES), symbol, limit
	// 默认使用 USDT-FUTURES
	queryString := fmt.Sprintf("productType=USDT-FUTURES&symbol=%s&limit=100", normalizedSymbol)
	
	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetFuturesOrderBookPath, queryString, "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetFuturesOrderBookPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("get futures orderbook failed: %w", err)
	}

	// V2 API 响应格式
	var resp struct {
		Code        string `json:"code"`
		Msg         string `json:"msg"`
		RequestTime int64  `json:"requestTime"` // V2 新增字段
		Data        struct {
			Asks [][]string `json:"asks"` // [[price, quantity], ...]
			Bids [][]string `json:"bids"` // [[price, quantity], ...]
			Ts   string     `json:"ts"`   // V2 返回字符串格式的时间戳
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse futures orderbook response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	return resp.Data.Bids, resp.Data.Asks, nil
}
