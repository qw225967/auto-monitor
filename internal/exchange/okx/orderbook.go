package okx

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
)

const okxOrderBookLimit = 400

// GetSpotOrderBook 获取现货订单簿
func (o *okx) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}
	instId := ToOKXSpotInstId(symbol)
	return o.getOrderBook(instId)
}

// GetFuturesOrderBook 获取合约订单簿
func (o *okx) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, nil, exchange.ErrNotInitialized
	}
	instId := ToOKXSwapInstId(symbol)
	return o.getOrderBook(instId)
}

// getOrderBook 请求 GET /api/v5/market/books?instId=xxx&sz=400（公开接口，无需签名）
func (o *okx) getOrderBook(instId string) ([][]string, [][]string, error) {
	requestPath := constants.OkexPathMarketBooks + "?instId=" + url.QueryEscape(instId) + "&sz=" + fmt.Sprintf("%d", okxOrderBookLimit)
	apiURL := constants.OkexBaseUrl + requestPath

	o.mu.RLock()
	restClient := o.restClient
	o.mu.RUnlock()

	// 公开行情接口，无需签名
	resp, err := restClient.DoGetWithHeaders(apiURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("okx order book request: %w", err)
	}

	var out struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Bids [][]interface{} `json:"bids"`
			Asks [][]interface{} `json:"asks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return nil, nil, fmt.Errorf("okx order book parse: %w", err)
	}
	if err := CheckOKXAPIError(resp); err != nil {
		return nil, nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil, fmt.Errorf("okx order book: no data")
	}

	bids := okxBookLevelsToStrings(out.Data[0].Bids)
	asks := okxBookLevelsToStrings(out.Data[0].Asks)
	return bids, asks, nil
}

// okxBookLevelsToStrings 将 OKX [[price, sz, ...], ...] 转为 [][]string，每项 [price, qty]
func okxBookLevelsToStrings(levels [][]interface{}) [][]string {
	result := make([][]string, 0, len(levels))
	for _, row := range levels {
		if len(row) < 2 {
			continue
		}
		priceStr, _ := interfaceToString(row[0])
		qtyStr, _ := interfaceToString(row[1])
		if priceStr != "" && qtyStr != "" {
			result = append(result, []string{priceStr, qtyStr})
		}
	}
	return result
}

func interfaceToString(v interface{}) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return fmt.Sprintf("%v", x), true
	default:
		return "", false
	}
}
