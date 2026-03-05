package lighter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// refreshToken 刷新认证 Token
func (l *lighter) refreshToken() error {
	// 构建认证请求
	nonce := l.getNextNonce()
	signatureData := fmt.Sprintf("%s%d", l.apiKey, nonce)
	signature := signRequest(signatureData, l.apiSecret)

	requestBody := map[string]interface{}{
		"api_key":   l.apiKey,
		"nonce":     nonce,
		"signature": signature,
	}

	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}
	apiURL := fmt.Sprintf("%s%s", constants.LighterRestBaseUrl, constants.LighterAuthPath)

	headers := make(map[string]string)
	headers["Content-Type"] = "application/json"

	responseBody, err := l.restClient.DoPostWithHeaders(apiURL, string(bodyJSON), headers)
	if err != nil {
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return err
	}

	var resp struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"` // seconds
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return fmt.Errorf("failed to parse token response: %w", err)
	}

	// 验证 Token 是否有效
	if resp.Token == "" {
		return fmt.Errorf("received empty token from API")
	}
	if resp.ExpiresIn <= 0 {
		return fmt.Errorf("invalid expires_in value: %d", resp.ExpiresIn)
	}

	// 更新 token 和过期时间（需要写锁）
	l.mu.Lock()
	l.token = resp.Token
	l.tokenExpiry = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	l.mu.Unlock()

	return nil
}

// getAccountBalance 获取账户余额
func (l *lighter) getAccountBalance() (*model.Balance, error) {
	nonce := l.getNextNonce()
	headers := buildAuthHeaders(l.apiKey, l.token, nonce)

	apiURL := fmt.Sprintf("%s%s", constants.LighterRestBaseUrl, constants.LighterBalancesPath)

	responseBody, err := l.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Balances []struct {
			Asset     string `json:"asset"`
			Total     string `json:"total"`
			Available string `json:"available"`
			Locked    string `json:"locked"`
		} `json:"balances"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse balance response: %w", err)
	}

	// 返回第一个余额（通常是 USDT）
	if len(resp.Balances) > 0 {
		b := resp.Balances[0]
		total, _ := parseFloat(b.Total)
		available, _ := parseFloat(b.Available)
		locked, _ := parseFloat(b.Locked)

		return &model.Balance{
			Asset:      b.Asset,
			Total:      total,
			Available:  available,
			Locked:     locked,
			UpdateTime: time.Now(),
		}, nil
	}

	return &model.Balance{}, nil
}

// getAllAccountBalances 获取所有余额
func (l *lighter) getAllAccountBalances() (map[string]*model.Balance, error) {
	// 简化实现
	balance, err := l.getAccountBalance()
	if err != nil {
		return nil, err
	}

	balances := make(map[string]*model.Balance)
	balances[balance.Asset] = balance

	return balances, nil
}

// getPositionBySymbol 获取指定交易对的持仓
func (l *lighter) getPositionBySymbol(symbol string) (*model.Position, error) {
	positions, err := l.getAllPositions()
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

// getAllPositions 获取所有持仓
func (l *lighter) getAllPositions() ([]*model.Position, error) {
	nonce := l.getNextNonce()
	headers := buildAuthHeaders(l.apiKey, l.token, nonce)

	apiURL := fmt.Sprintf("%s%s", constants.LighterRestBaseUrl, constants.LighterPositionsPath)

	responseBody, err := l.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Positions []struct {
			Symbol        string `json:"symbol"`
			Size          string `json:"size"`
			EntryPrice    string `json:"entryPrice"`
			MarkPrice     string `json:"markPrice"`
			UnrealizedPnl string `json:"unrealizedPnl"`
			Leverage      int    `json:"leverage"`
		} `json:"positions"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse positions response: %w", err)
	}

	positions := make([]*model.Position, 0, len(resp.Positions))
	for _, p := range resp.Positions {
		size, _ := parseFloat(p.Size)
		entryPrice, _ := parseFloat(p.EntryPrice)
		markPrice, _ := parseFloat(p.MarkPrice)
		pnl, _ := parseFloat(p.UnrealizedPnl)

		side := model.PositionSideLong
		if size < 0 {
			side = model.PositionSideShort
			size = -size
		}

		positions = append(positions, &model.Position{
			Symbol:        denormalizeSymbol(p.Symbol),
			Side:          side,
			Size:          size,
			EntryPrice:    entryPrice,
			MarkPrice:     markPrice,
			UnrealizedPnl: pnl,
			Leverage:      p.Leverage,
			UpdateTime:    time.Now(),
		})
	}

	return positions, nil
}

// parseFloat 辅助函数：解析浮点数
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
