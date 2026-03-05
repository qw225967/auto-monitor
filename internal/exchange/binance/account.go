package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
)

// getUnifiedAccountBalance 获取统一账户余额
func (b *binance) getUnifiedAccountBalance() ([]*model.BinanceUnifiedAccountBalance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseUnifiedAccountUrl, constants.BinanceUnifiedAccountBalancePath, queryStr)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get unified account balance failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var balances []*model.BinanceUnifiedAccountBalance
	if err := json.Unmarshal([]byte(responseBody), &balances); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w, response: %s", err, responseBody)
	}

	return balances, nil
}

// getUnifiedAccountPositionRisk 获取统一账户持仓风险信息
func (b *binance) getUnifiedAccountPositionRisk(symbol string) ([]*model.BinanceFuturesPositionRisk, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()

	apiKey, secretKey := b.getAPIKeys()

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}
	if symbol != "" {
		params["symbol"] = symbol
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseUnifiedAccountUrl, constants.BinanceUnifiedAccountPositionRiskPath, queryStr)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get unified account position risk failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var positions []*model.BinanceFuturesPositionRisk
	if err := json.Unmarshal([]byte(responseBody), &positions); err != nil {
		return nil, fmt.Errorf("parse position risk response failed: %w, response: %s", err, responseBody)
	}

	return positions, nil
}

// getSpotAccountBalance 使用现货 API 获取现货账户余额
func (b *binance) getSpotAccountBalance() (map[string]*model.Balance, error) {
	b.mu.RLock()
	spotClient := b.restAPISpotClient
	b.mu.RUnlock()

	if spotClient == nil {
		return nil, fmt.Errorf("spot API client not initialized")
	}

	apiKey, secretKey := b.getAPIKeys()
	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("API keys not configured")
	}

	// 使用 binance-connector SDK 获取现货账户信息
	account, err := spotClient.NewGetAccountService().Do(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get spot account failed: %w", err)
	}

	balancesMap := make(map[string]*model.Balance)
	for _, bal := range account.Balances {
		free, _ := strconv.ParseFloat(bal.Free, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		total := free + locked

		// 只记录有余额的币种
		if total > 0 || free > 0 || locked > 0 {
			balancesMap[bal.Asset] = &model.Balance{
				Asset:      bal.Asset,
				Available:  free,
				Locked:     locked,
				Total:      total,
				UpdateTime: time.Now(),
			}
		}
	}

	return balancesMap, nil
}

// setLeverage 设置合约杠杆（统一账户 UM），在订阅合约 symbol 时调用
// Binance 使用 papi 统一账户(Portfolio Margin)，本身即为全仓，无需单独设置 marginType
// 注意：由 SubscribeTicker 在已持 b.mu 时调用，此处不再加锁
func (b *binance) setLeverage(symbol string, leverage int) error {
	restClient := b.restClient
	apiKey, secretKey := b.getAPIKeys()
	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"symbol":     symbol,
		"leverage":   strconv.Itoa(leverage),
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}
	params = b.buildSignedParams(params, secretKey)
	formData := buildFormData(params)
	apiURL := fmt.Sprintf("%s%s", constants.BinanceRestBaseUnifiedAccountUrl, constants.BinanceUnifiedAccountLeveragePath)
	headers := buildHeaders(apiKey)
	headers["Content-Type"] = "application/x-www-form-urlencoded"
	responseBody, err := restClient.DoPostWithHeaders(apiURL, formData, headers)
	if err != nil {
		return fmt.Errorf("set leverage failed: %w", err)
	}
	return checkAPIError(responseBody)
}

// convertPositionRiskToModel 将 Binance API 持仓风险数据转换为 model.Position
func convertPositionRiskToModel(pos *model.BinanceFuturesPositionRisk) *model.Position {
	positionAmt, _ := strconv.ParseFloat(pos.PositionAmt, 64)
	if positionAmt == 0 {
		return &model.Position{
			Symbol:        pos.Symbol,
			Side:          "",
			Size:          0,
			EntryPrice:    0,
			MarkPrice:     0,
			UnrealizedPnl: 0,
			Leverage:      0,
			UpdateTime:    time.Now(),
		}
	}

	var side model.PositionSide
	if positionAmt > 0 {
		side = model.PositionSideLong
	} else {
		side = model.PositionSideShort
		positionAmt = -positionAmt
	}

	entryPrice, _ := strconv.ParseFloat(pos.EntryPrice, 64)
	markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
	unrealizedPnl, _ := strconv.ParseFloat(pos.UnRealizedProfit, 64)
	leverage, _ := strconv.Atoi(pos.Leverage)

	updateTime := time.Unix(pos.UpdateTime/1000, 0)
	if pos.UpdateTime == 0 {
		updateTime = time.Now()
	}

	return &model.Position{
		Symbol:        pos.Symbol,
		Side:          side,
		Size:          positionAmt,
		EntryPrice:    entryPrice,
		MarkPrice:     markPrice,
		UnrealizedPnl: unrealizedPnl,
		Leverage:      leverage,
		UpdateTime:    updateTime,
	}
}
