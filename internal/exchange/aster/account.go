package aster

import (
	"encoding/json"
	"fmt"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
)

// getAccountBalance 获取账户余额（默认查询现货余额）
func (a *aster) getAccountBalance() (*model.Balance, error) {
	return a.getSpotBalance()
}

// getSpotBalance 获取现货余额
func (a *aster) getSpotBalance() (*model.Balance, error) {
	apiKey, secretKey := a.getAPIKeys()
	
	// 构建请求参数
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("timestamp=%s", timestamp)
	
	// 签名
	signature := signRequest(params, secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s&signature=%s", 
		constants.AsterSpotRestBaseUrl, 
		constants.AsterSpotAccountPath,
		params,
		signature)
	
	headers := make(map[string]string)
	headers["X-MBX-APIKEY"] = apiKey
	
	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to get spot balance: %w", err)
	}
	
	// 检查是否返回 HTML（错误页面）
	if len(responseBody) > 0 && responseBody[0] == '<' {
		// 尝试提取更多错误信息
		errorPreview := responseBody[:min(500, len(responseBody))]
		return nil, fmt.Errorf("API returned HTML instead of JSON. This usually means:\n1. API endpoint is incorrect\n2. Authentication failed (check API key and signature)\n3. API URL configuration error\n\nRequest URL: %s\nResponse preview: %s", apiURL, errorPreview)
	}
	
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	var resp struct {
		Balances []struct {
			Asset     string `json:"asset"`
			Free      string `json:"free"`
			Locked    string `json:"locked"`
		} `json:"balances"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse spot balance response: %w", err)
	}
	
	// 返回第一个余额（通常是 USDT）
	if len(resp.Balances) > 0 {
		b := resp.Balances[0]
		free, _ := parseFloat(b.Free)
		locked, _ := parseFloat(b.Locked)
		total := free + locked
		
		return &model.Balance{
			Asset:      b.Asset,
			Total:      total,
			Available:  free,
			Locked:     locked,
			UpdateTime: time.Now(),
		}, nil
	}
	
	return &model.Balance{}, nil
}

// getFuturesBalance 获取合约余额
func (a *aster) getFuturesBalance() (*model.Balance, error) {
	apiKey, secretKey := a.getAPIKeys()
	
	// 构建请求参数
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("timestamp=%s", timestamp)
	
	// 签名
	signature := signRequest(params, secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s&signature=%s", 
		constants.AsterFuturesRestBaseUrl, 
		constants.AsterFuturesBalancePath,
		params,
		signature)
	
	headers := make(map[string]string)
	headers["X-MBX-APIKEY"] = apiKey
	
	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to get futures balance: %w", err)
	}
	
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	// 合约余额 v3 API 返回数组格式
	var resp []struct {
		Asset             string `json:"asset"`
		Balance           string `json:"balance"`           // 钱包余额
		CrossWalletBalance string `json:"crossWalletBalance"` // 交叉钱包余额
		CrossUnPnl        string `json:"crossUnPnl"`       // 交叉未实现盈亏
		AvailableBalance  string `json:"availableBalance"`  // 可用余额
		MaxWithdrawAmount  string `json:"maxWithdrawAmount"` // 最大提现金额
		UpdateTime         int64  `json:"updateTime"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse futures balance response: %w", err)
	}
	
	// 返回第一个余额（通常是 USDT）
	if len(resp) > 0 {
		b := resp[0]
		balance, _ := parseFloat(b.Balance)
		availableBalance, _ := parseFloat(b.AvailableBalance)
		
		// 计算锁定余额（总余额 - 可用余额）
		locked := balance - availableBalance
		if locked < 0 {
			locked = 0
		}
		
		return &model.Balance{
			Asset:      b.Asset,
			Total:      balance,
			Available:  availableBalance,
			Locked:     locked,
			UpdateTime: time.Unix(b.UpdateTime/1000, (b.UpdateTime%1000)*1000000),
		}, nil
	}
	
	return &model.Balance{}, nil
}

// getAllAccountBalances 获取所有余额（包括现货和合约）
func (a *aster) getAllAccountBalances() (map[string]*model.Balance, error) {
	balances := make(map[string]*model.Balance)
	
	// 获取现货余额
	spotBalance, err := a.getSpotBalance()
	if err != nil {
		// 如果现货余额查询失败，记录错误但继续查询合约余额
		logger.GetLoggerInstance().Named("aster").Sugar().Warnf("Failed to get spot balance: %v", err)
	} else if spotBalance != nil && spotBalance.Asset != "" {
		// 使用 "SPOT_" 前缀标识现货余额
		balances["SPOT_"+spotBalance.Asset] = spotBalance
	}
	
	// 获取合约余额
	futuresBalance, err := a.getFuturesBalance()
	if err != nil {
		// 如果合约余额查询失败，记录错误但返回已获取的现货余额
		logger.GetLoggerInstance().Named("aster").Sugar().Warnf("Failed to get futures balance: %v", err)
	} else if futuresBalance != nil && futuresBalance.Asset != "" {
		// 使用 "FUTURES_" 前缀标识合约余额
		balances["FUTURES_"+futuresBalance.Asset] = futuresBalance
	}
	
	// 如果没有获取到任何余额，返回错误
	if len(balances) == 0 {
		return nil, fmt.Errorf("failed to get any balance (spot or futures)")
	}
	
	return balances, nil
}

// getPositionBySymbol 获取指定交易对的持仓
func (a *aster) getPositionBySymbol(symbol string) (*model.Position, error) {
	positions, err := a.getAllPositions()
	if err != nil {
		return nil, err
	}
	
	for _, pos := range positions {
		if pos.Symbol == symbol {
			return pos, nil
		}
	}
	
	// 没有持仓，返回空持仓
	return &model.Position{
		Symbol:        symbol,
		Size:          0,
		EntryPrice:    0,
		MarkPrice:     0,
		UnrealizedPnl: 0,
		Leverage:      1,
		UpdateTime:    time.Now(),
	}, nil
}

// getAllPositions 获取所有持仓（仅合约有持仓）
func (a *aster) getAllPositions() ([]*model.Position, error) {
	apiKey, secretKey := a.getAPIKeys()
	
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	params := fmt.Sprintf("timestamp=%s", timestamp)
	signature := signRequest(params, secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s&signature=%s", 
		constants.AsterFuturesRestBaseUrl, 
		constants.AsterFuturesPositionPath,
		params,
		signature)
	
	headers := make(map[string]string)
	headers["X-MBX-APIKEY"] = apiKey
	
	responseBody, err := a.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}
	
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	// 使用中间结构体解析 API 响应（时间戳是数字）
	var apiPositions []struct {
		Symbol           string  `json:"symbol"`
		PositionAmt     string  `json:"positionAmt"`
		EntryPrice       string  `json:"entryPrice"`
		MarkPrice        string  `json:"markPrice"`
		UnRealizedProfit string  `json:"unRealizedProfit"`
		Leverage         string  `json:"leverage"`
		PositionSide     string  `json:"positionSide"`
		UpdateTime       int64   `json:"updateTime"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &apiPositions); err != nil {
		return nil, fmt.Errorf("failed to parse positions response: %w", err)
	}
	
	// 转换为我们的 Position 结构体
	positions := make([]*model.Position, 0, len(apiPositions))
	for _, apiPos := range apiPositions {
		// 解析持仓数量
		positionAmt, _ := parseFloat(apiPos.PositionAmt)
		
		// 如果持仓数量为 0，跳过
		if positionAmt == 0 {
			continue
		}
		
		// 确定持仓方向
		var side model.PositionSide
		if positionAmt > 0 {
			side = model.PositionSideLong
		} else {
			side = model.PositionSideShort
			positionAmt = -positionAmt // 转为正数
		}
		
		entryPrice, _ := parseFloat(apiPos.EntryPrice)
		markPrice, _ := parseFloat(apiPos.MarkPrice)
		unrealizedPnl, _ := parseFloat(apiPos.UnRealizedProfit)
		leverage, _ := parseFloat(apiPos.Leverage)
		
		positions = append(positions, &model.Position{
			Symbol:        apiPos.Symbol,
			Side:          side,
			Size:          positionAmt,
			EntryPrice:    entryPrice,
			MarkPrice:     markPrice,
			UnrealizedPnl: unrealizedPnl,
			Leverage:      int(leverage),
			UpdateTime:    time.Unix(apiPos.UpdateTime/1000, (apiPos.UpdateTime%1000)*1000000),
		})
	}
	
	return positions, nil
}

// parseFloat 辅助函数
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
