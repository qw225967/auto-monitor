package aster

import (
	"encoding/json"
	"fmt"

	"github.com/qw225967/auto-monitor/constants"
)

// getOrderBook 获取订单簿
func (a *aster) getOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error) {
	var baseURL, apiPath string
	if isFutures {
		baseURL = constants.AsterFuturesRestBaseUrl
		apiPath = constants.AsterFuturesOrderBookPath
	} else {
		baseURL = constants.AsterSpotRestBaseUrl
		apiPath = constants.AsterSpotOrderBookPath
	}
	
	apiURL := fmt.Sprintf("%s%s?symbol=%s&limit=20", 
		baseURL, 
		apiPath,
		symbol)
	
	headers := make(map[string]string)
	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get orderbook: %w", err)
	}
	
	if err := checkAPIError(responseBody); err != nil {
		return nil, nil, err
	}
	
	var resp struct {
		Bids [][]interface{} `json:"bids"` // [[price, quantity], ...]
		Asks [][]interface{} `json:"asks"` // [[price, quantity], ...]
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, nil, fmt.Errorf("failed to parse orderbook response: %w", err)
	}
	
	// 转换为字符串数组
	bids = convertOrderBookSide(resp.Bids)
	asks = convertOrderBookSide(resp.Asks)
	
	return bids, asks, nil
}

// convertOrderBookSide 转换订单簿一侧的数据
func convertOrderBookSide(side [][]interface{}) [][]string {
	result := make([][]string, 0, len(side))
	for _, level := range side {
		if len(level) >= 2 {
			price := fmt.Sprintf("%v", level[0])
			quantity := fmt.Sprintf("%v", level[1])
			result = append(result, []string{price, quantity})
		}
	}
	return result
}
