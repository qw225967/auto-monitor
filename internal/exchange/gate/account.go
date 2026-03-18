package gate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// GetBalance 查询账户余额（实现）
func (g *gateExchange) GetBalance() (*model.Balance, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateAccountBalancePath, "", "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GateAccountBalancePath)
	headers := buildHeaders(apiKey, signature, timestamp)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get balance failed: %w", err)
	}

	// Gate.io 可能返回错误对象，先检查是否是错误
	var errorResp struct {
		Label   string `json:"label"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(responseBody), &errorResp) == nil && errorResp.Label != "" {
		return nil, fmt.Errorf("gate.io API error: %s - %s", errorResp.Label, errorResp.Message)
	}

	// 解析响应（Gate.io 返回数组）
	var balances []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}

	if err := json.Unmarshal([]byte(responseBody), &balances); err != nil {
		// 打印原始响应便于调试
		return nil, fmt.Errorf("parse balance response failed: %w, response: %s", err, responseBody)
	}

	// 查找 USDT 余额
	for _, balance := range balances {
		if balance.Currency == "USDT" {
			available, _ := strconv.ParseFloat(balance.Available, 64)
			locked, _ := strconv.ParseFloat(balance.Locked, 64)
			total := available + locked

			return &model.Balance{
				Asset:      "USDT",
				Available:  available,
				Locked:     locked,
				Total:      total,
				UpdateTime: time.Now(),
			}, nil
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
func (g *gateExchange) GetAllBalances() (map[string]*model.Balance, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GateAccountBalancePath, "", "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GateAccountBalancePath)
	headers := buildHeaders(apiKey, signature, timestamp)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get all balances failed: %w", err)
	}

	// 解析响应
	var balances []struct {
		Currency  string `json:"currency"`
		Available string `json:"available"`
		Locked    string `json:"locked"`
	}

	if err := json.Unmarshal([]byte(responseBody), &balances); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}

	balancesMap := make(map[string]*model.Balance)
	for _, bal := range balances {
		available, _ := strconv.ParseFloat(bal.Available, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		total := available + locked

		// 只记录有余额的币种
		if total > 0 {
			balancesMap[bal.Currency] = &model.Balance{
				Asset:      bal.Currency,
				Available:  available,
				Locked:     locked,
				Total:      total,
				UpdateTime: time.Now(),
			}
		}
	}

	return balancesMap, nil
}

// GetPosition 查询持仓（实现）
func (g *gateExchange) GetPosition(symbol string) (*model.Position, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	if symbol == "" {
		return nil, fmt.Errorf("invalid symbol")
	}

	timestamp := getCurrentTimestamp()
	queryString := fmt.Sprintf("contract=%s", normalizeGateSymbol(symbol))
	signature := signRequest("GET", constants.GatePositionPath, queryString, "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, constants.GatePositionPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get position failed: %w", err)
	}

	// 检查是否是错误响应
	var errorResp struct {
		Label   string `json:"label"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(responseBody), &errorResp) == nil && errorResp.Label != "" {
		return nil, fmt.Errorf("gate.io API error: %s - %s", errorResp.Label, errorResp.Message)
	}

	// 解析响应
	var positions []struct {
		User         int64  `json:"user"`
		Contract     string `json:"contract"`
		Size         int64  `json:"size"`
		Leverage     string `json:"leverage"`
		RiskLimit    string `json:"risk_limit"`
		LeverageMax  string `json:"leverage_max"`
		Value        string `json:"value"`
		EntryPrice   string `json:"entry_price"`
		LiqPrice     string `json:"liq_price"`
		MarkPrice    string `json:"mark_price"`
		UnrealisedPnl string `json:"unrealised_pnl"`
		RealisedPnl  string `json:"realised_pnl"`
		UpdateTime   int64  `json:"update_time"`
	}

	if err := json.Unmarshal([]byte(responseBody), &positions); err != nil {
		return nil, fmt.Errorf("parse position response failed: %w, response: %s", err, responseBody)
	}

	// 尝试从映射中获取原始 symbol 格式
	g.mu.RLock()
	symbolMap := g.gateSymbolToOriginal
	g.mu.RUnlock()

	normalizedSymbol := normalizeGateSymbol(symbol)
	// 查找指定 symbol 的持仓
	for _, pos := range positions {
		if pos.Contract == normalizedSymbol {
			size := float64(pos.Size)
			if size == 0 {
				continue
			}

			entryPrice, _ := strconv.ParseFloat(pos.EntryPrice, 64)
			markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
			unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPnl, 64)
			leverage, _ := strconv.Atoi(pos.Leverage)

			var side model.PositionSide
			if size > 0 {
				side = model.PositionSideLong
				size = size // 正数表示多头
			} else {
				side = model.PositionSideShort
				size = -size // 负数表示空头，转为正数
			}

			// 尝试将 Gate.io 格式的 symbol 转换为原始格式
			returnSymbol := symbol // 默认使用传入的 symbol
			if originalSymbol, exists := symbolMap[pos.Contract]; exists {
				returnSymbol = originalSymbol
			} else {
				// 如果没有映射，尝试将 _ 替换为 ""（BTC_USDT -> BTCUSDT）
				returnSymbol = strings.ReplaceAll(pos.Contract, "_", "")
			}

			return &model.Position{
				Symbol:        returnSymbol,
				Side:          side,
				Size:          size,
				EntryPrice:    entryPrice,
				MarkPrice:     markPrice,
				UnrealizedPnl: unrealizedPnl,
				Leverage:      leverage,
				UpdateTime:    time.Unix(pos.UpdateTime, 0),
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
func (g *gateExchange) GetPositions() ([]*model.Position, error) {
	g.mu.RLock()
	restClient := g.restClient
	g.mu.RUnlock()
	
	apiKey, secretKey := g.getAPIKeys()

	if !g.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest("GET", constants.GatePositionPath, "", "", secretKey, timestamp)
	
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, constants.GatePositionPath)
	headers := buildHeaders(apiKey, signature, timestamp)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get positions failed: %w", err)
	}

	// 检查是否是错误响应
	var errorResp struct {
		Label   string `json:"label"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(responseBody), &errorResp) == nil && errorResp.Label != "" {
		return nil, fmt.Errorf("gate.io API error: %s - %s", errorResp.Label, errorResp.Message)
	}

	// 解析响应
	var positionsResp []struct {
		User         int64  `json:"user"`
		Contract     string `json:"contract"`
		Size         int64  `json:"size"`
		Leverage     string `json:"leverage"`
		RiskLimit    string `json:"risk_limit"`
		LeverageMax  string `json:"leverage_max"`
		Value        string `json:"value"`
		EntryPrice   string `json:"entry_price"`
		LiqPrice     string `json:"liq_price"`
		MarkPrice    string `json:"mark_price"`
		UnrealisedPnl string `json:"unrealised_pnl"`
		RealisedPnl  string `json:"realised_pnl"`
		UpdateTime   int64  `json:"update_time"`
	}

	if err := json.Unmarshal([]byte(responseBody), &positionsResp); err != nil {
		return nil, fmt.Errorf("parse positions response failed: %w, response: %s", err, responseBody)
	}

	// 尝试从映射中获取原始 symbol 格式
	g.mu.RLock()
	symbolMap := g.gateSymbolToOriginal
	g.mu.RUnlock()

	positions := make([]*model.Position, 0)
	for _, pos := range positionsResp {
		size := float64(pos.Size)
		if size == 0 {
			continue
		}

		entryPrice, _ := strconv.ParseFloat(pos.EntryPrice, 64)
		markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
		unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPnl, 64)
		leverage, _ := strconv.Atoi(pos.Leverage)

		var side model.PositionSide
		if size > 0 {
			side = model.PositionSideLong
		} else {
			side = model.PositionSideShort
			size = -size
		}

		// 尝试将 Gate.io 格式的 symbol 转换为原始格式
		// 例如：BTC_USDT -> BTCUSDT
		symbol := pos.Contract
		if originalSymbol, exists := symbolMap[pos.Contract]; exists {
			symbol = originalSymbol
		} else {
			// 如果没有映射，尝试将 _ 替换为 ""（BTC_USDT -> BTCUSDT）
			symbol = strings.ReplaceAll(pos.Contract, "_", "")
		}

		positions = append(positions, &model.Position{
			Symbol:        symbol,
			Side:          side,
			Size:          size,
			EntryPrice:    entryPrice,
			MarkPrice:     markPrice,
			UnrealizedPnl: unrealizedPnl,
			Leverage:      leverage,
			UpdateTime:    time.Unix(pos.UpdateTime, 0),
		})
	}

	return positions, nil
}
