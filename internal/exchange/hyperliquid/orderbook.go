package hyperliquid

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qw225967/auto-monitor/constants"
)

// getOrderBook 获取订单簿
func (h *hyperliquidExchange) getOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error) {
	// 标准化符号
	normalizedSymbol := normalizeSymbol(symbol, isFutures)
	
	// 构建请求
	request := buildInfoRequest(constants.HyperliquidQueryTypeL2Book, map[string]interface{}{
		"coin": normalizedSymbol,
	})
	
	requestBody, _ := json.Marshal(request)
	
	// 发送请求
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidInfoPath)
	headers := buildHeaders()
	
	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, nil, fmt.Errorf("get orderbook failed: %w", err)
	}
	
	// 解析响应
	return h.parseOrderBook(responseBody)
}

// parseOrderBook 解析订单簿响应
func (h *hyperliquidExchange) parseOrderBook(responseBody string) (bids [][]string, asks [][]string, err error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, nil, err
	}
	
	var resp struct {
		Levels [][]struct {
			Px string `json:"px"` // 价格
			Sz string `json:"sz"` // 数量
			N  int    `json:"n"`  // 订单数
		} `json:"levels"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse orderbook response failed: %w", err)
	}
	
	if len(resp.Levels) < 2 {
		return nil, nil, fmt.Errorf("invalid orderbook response: insufficient data")
	}
	
	// Hyperliquid 返回格式：[[bids], [asks]]
	// bids 是买单（降序），asks 是卖单（升序）
	bidLevels := resp.Levels[0]
	askLevels := resp.Levels[1]
	
	// 转换为标准格式
	bids = make([][]string, len(bidLevels))
	for i, level := range bidLevels {
		bids[i] = []string{level.Px, level.Sz}
	}
	
	asks = make([][]string, len(askLevels))
	for i, level := range askLevels {
		asks[i] = []string{level.Px, level.Sz}
	}
	
	return bids, asks, nil
}

// calculateSlippage 计算滑点（内部实现）
func calculateSlippage(orderBook [][]string, amount float64, isBuy bool) (slippage float64, maxSize float64) {
	if len(orderBook) == 0 {
		return 0, 0
	}
	
	var totalSize float64
	var totalValue float64
	
	for _, level := range orderBook {
		if len(level) < 2 {
			continue
		}
		
		price, _ := strconv.ParseFloat(level[0], 64)
		size, _ := strconv.ParseFloat(level[1], 64)
		
		if totalSize+size >= amount {
			// 这一档足够完成订单
			remainingSize := amount - totalSize
			totalValue += remainingSize * price
			totalSize = amount
			break
		}
		
		totalSize += size
		totalValue += size * price
	}
	
	if totalSize == 0 {
		return 0, 0
	}
	
	avgPrice := totalValue / totalSize
	bestPrice, _ := strconv.ParseFloat(orderBook[0][0], 64)
	
	if bestPrice > 0 {
		slippage = (avgPrice - bestPrice) / bestPrice * 100
		if !isBuy {
			slippage = -slippage
		}
	}
	
	return slippage, totalSize
}
