package bybit

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
)

// GetBalance 查询账户余额（实现）
func (b *bybit) GetBalance() (*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	
	params := map[string]string{
		"accountType": "UNIFIED",
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	// 发送 HTTP GET 请求
	apiURL := fmt.Sprintf("%s%s?%s", 
		constants.BybitRestBaseUrl, 
		constants.BybitAccountBalancePath, 
		queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get balance failed: %w", err)
	}

	// 解析响应
	var balanceResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Coin []struct {
					Coin                string `json:"coin"`
					WalletBalance       string `json:"walletBalance"`
					AvailableToWithdraw string `json:"availableToWithdraw"`
					Locked              string `json:"locked"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &balanceResp); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}

	if balanceResp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", balanceResp.RetCode, balanceResp.RetMsg)
	}

	// 查找 USDT 余额
	for _, account := range balanceResp.Result.List {
		for _, coinInfo := range account.Coin {
			if coinInfo.Coin == "USDT" {
				walletBalance, _ := strconv.ParseFloat(coinInfo.WalletBalance, 64)
				available, _ := strconv.ParseFloat(coinInfo.AvailableToWithdraw, 64)
				locked, _ := strconv.ParseFloat(coinInfo.Locked, 64)

				return &model.Balance{
					Asset:      "USDT",
					Available:  available,
					Locked:     locked,
					Total:      walletBalance,
					UpdateTime: time.Now(),
				}, nil
			}
		}
	}

	// 如果没有找到 USDT，返回零余额
	return &model.Balance{
		Asset:      "USDT",
		Available:  0,
		Locked:     0,
		Total:      0,
		UpdateTime: time.Now(),
	}, nil
}

// GetAllBalances 获取所有币种的余额（实现）
func (b *bybit) GetAllBalances() (map[string]*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	
	params := map[string]string{
		"accountType": "UNIFIED",
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	// 发送 HTTP GET 请求
	apiURL := fmt.Sprintf("%s%s?%s",
		constants.BybitRestBaseUrl,
		constants.BybitAccountBalancePath,
		queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get all balances failed: %w", err)
	}

	// 解析响应
	var balanceResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Coin []struct {
					Coin                string `json:"coin"`
					WalletBalance       string `json:"walletBalance"`
					AvailableToWithdraw string `json:"availableToWithdraw"`
					Locked              string `json:"locked"`
				} `json:"coin"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &balanceResp); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}

	if balanceResp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", balanceResp.RetCode, balanceResp.RetMsg)
	}

	// 构建所有币种的余额映射
	balancesMap := make(map[string]*model.Balance)
	for _, account := range balanceResp.Result.List {
		for _, coinInfo := range account.Coin {
			walletBalance, _ := strconv.ParseFloat(coinInfo.WalletBalance, 64)
			available, _ := strconv.ParseFloat(coinInfo.AvailableToWithdraw, 64)
			locked, _ := strconv.ParseFloat(coinInfo.Locked, 64)

			// 只记录有余额的币种
			if walletBalance > 0 {
				balancesMap[coinInfo.Coin] = &model.Balance{
					Asset:      coinInfo.Coin,
					Available:  available,
					Locked:     locked,
					Total:      walletBalance,
					UpdateTime: time.Now(),
				}
			}
		}
	}

	return balancesMap, nil
}

// GetPosition 查询持仓（实现）
func (b *bybit) GetPosition(symbol string) (*model.Position, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	if symbol == "" {
		return nil, fmt.Errorf("invalid symbol")
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	
	params := map[string]string{
		"category": "linear",
		"symbol":   symbol,
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	// 发送 HTTP GET 请求
	apiURL := fmt.Sprintf("%s%s?%s",
		constants.BybitRestBaseUrl,
		constants.BybitPositionInfoPath,
		queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get position failed: %w", err)
	}

	// 解析响应
	var posResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol        string `json:"symbol"`
				Side          string `json:"side"`
				Size          string `json:"size"`
				PositionValue string `json:"positionValue"`
				EntryPrice    string `json:"entryPrice"`
				MarkPrice     string `json:"markPrice"`
				UnrealisedPnl string `json:"unrealisedPnl"`
				Leverage      string `json:"leverage"`
				UpdatedTime   string `json:"updatedTime"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &posResp); err != nil {
		return nil, fmt.Errorf("parse position response failed: %w", err)
	}

	if posResp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", posResp.RetCode, posResp.RetMsg)
	}

	// 查找指定 symbol 的持仓
	for _, pos := range posResp.Result.List {
		if pos.Symbol == symbol {
			size, _ := strconv.ParseFloat(pos.Size, 64)
			if size == 0 {
				continue // 跳过零持仓
			}

			entryPrice, _ := strconv.ParseFloat(pos.EntryPrice, 64)
			markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
			unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPnl, 64)
			leverage, _ := strconv.Atoi(pos.Leverage)
			updatedTime, _ := strconv.ParseInt(pos.UpdatedTime, 10, 64)

			var side model.PositionSide
			if pos.Side == "Buy" {
				side = model.PositionSideLong
			} else {
				side = model.PositionSideShort
			}

			return &model.Position{
				Symbol:        pos.Symbol,
				Side:          side,
				Size:          size,
				EntryPrice:    entryPrice,
				MarkPrice:     markPrice,
				UnrealizedPnl: unrealizedPnl,
				Leverage:      leverage,
				UpdateTime:    time.Unix(updatedTime/1000, 0),
			}, nil
		}
	}

	// 如果没有找到，返回零持仓
	return &model.Position{
		Symbol:        symbol,
		Side:          "",
		Size:          0,
		EntryPrice:    0,
		MarkPrice:     0,
		UnrealizedPnl: 0,
		Leverage:      0,
		UpdateTime:    time.Now(),
	}, nil
}

// GetPositions 查询所有持仓（实现）
func (b *bybit) GetPositions() ([]*model.Position, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	// 构建请求参数
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	
	params := map[string]string{
		"category":  "linear",
		"settleCoin": "USDT",  // Bybit V5 要求必须提供 settleCoin
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)

	// 发送 HTTP GET 请求
	apiURL := fmt.Sprintf("%s%s?%s",
		constants.BybitRestBaseUrl,
		constants.BybitPositionInfoPath,
		queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get positions failed: %w", err)
	}

	// 解析响应
	var posResp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			List []struct {
				Symbol        string `json:"symbol"`
				Side          string `json:"side"`
				Size          string `json:"size"`
				PositionValue string `json:"positionValue"`
				EntryPrice    string `json:"entryPrice"`
				MarkPrice     string `json:"markPrice"`
				UnrealisedPnl string `json:"unrealisedPnl"`
				Leverage      string `json:"leverage"`
				UpdatedTime   string `json:"updatedTime"`
			} `json:"list"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &posResp); err != nil {
		return nil, fmt.Errorf("parse positions response failed: %w", err)
	}

	if posResp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", posResp.RetCode, posResp.RetMsg)
	}

	positions := make([]*model.Position, 0)
	for _, pos := range posResp.Result.List {
		size, _ := strconv.ParseFloat(pos.Size, 64)
		if size == 0 {
			continue // 跳过零持仓
		}

		entryPrice, _ := strconv.ParseFloat(pos.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
		unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPnl, 64)
		leverage, _ := strconv.Atoi(pos.Leverage)
		updatedTime, _ := strconv.ParseInt(pos.UpdatedTime, 10, 64)

		var side model.PositionSide
		if pos.Side == "Buy" {
			side = model.PositionSideLong
		} else {
			side = model.PositionSideShort
		}

		positions = append(positions, &model.Position{
			Symbol:        pos.Symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    entryPrice,
			MarkPrice:     markPrice,
			UnrealizedPnl: unrealizedPnl,
			Leverage:      leverage,
			UpdateTime:    time.Unix(updatedTime/1000, 0),
		})
	}

	return positions, nil
}

// setLeverage 设置合约杠杆且强制全仓，在订阅合约 symbol 时调用
// 使用 switch-isolated：tradeMode=0 为全仓，1 为逐仓；同时设置 buyLeverage/sellLeverage
// 注意：由 SubscribeTicker 在已持 b.mu 时调用，此处不再加锁
func (b *bybit) setLeverage(symbol string, leverage int) error {
	restClient := b.restClient
	apiKey, secretKey := b.getAPIKeys()

	levStr := strconv.Itoa(leverage)
	body := map[string]interface{}{
		"category":     "linear",
		"symbol":       normalizeBybitSymbol(symbol),
		"tradeMode":    0, // 0=全仓(Cross) 1=逐仓(Isolated)
		"buyLeverage":  levStr,
		"sellLeverage": levStr,
	}
	jsonBody, _ := json.Marshal(body)
	requestBody := string(jsonBody)

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"
	signature := signRequest(timestamp, apiKey, recvWindow, requestBody, secretKey)
	apiURL := fmt.Sprintf("%s%s", constants.BybitRestBaseUrl, constants.BybitSwitchIsolatedPath)
	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, requestBody, headers)
	if err != nil {
		return fmt.Errorf("set leverage(cross) failed: %w", err)
	}
	return checkAPIError(responseBody)
}
