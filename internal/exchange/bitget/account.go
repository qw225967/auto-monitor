package bitget

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// GetBalance 查询账户余额（实现）
func (b *bitgetExchange) GetBalance() (*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey, passphrase := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetAccountBalancePath, "", "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetAccountBalancePath)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get balance failed: %w", err)
	}

	// 解析响应
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Coin      string `json:"coin"`
			Available string `json:"available"`
			Frozen    string `json:"frozen"`
			Locked    string `json:"locked"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	// 查找 USDT 余额
	for _, balance := range resp.Data {
		if balance.Coin == "USDT" {
			available, _ := strconv.ParseFloat(balance.Available, 64)
			locked, _ := strconv.ParseFloat(balance.Locked, 64)
			frozen, _ := strconv.ParseFloat(balance.Frozen, 64)
			total := available + locked + frozen

			return &model.Balance{
				Asset:      "USDT",
				Available:  available,
				Locked:     locked + frozen,
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
func (b *bitgetExchange) GetAllBalances() (map[string]*model.Balance, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey, passphrase := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetAccountBalancePath, "", "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetAccountBalancePath)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get all balances failed: %w", err)
	}

	// 解析响应
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Coin      string `json:"coin"`
			Available string `json:"available"`
			Frozen    string `json:"frozen"`
			Locked    string `json:"locked"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	balancesMap := make(map[string]*model.Balance)
	for _, bal := range resp.Data {
		available, _ := strconv.ParseFloat(bal.Available, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		frozen, _ := strconv.ParseFloat(bal.Frozen, 64)
		total := available + locked + frozen

		// 只记录有余额的币种
		if total > 0 {
			balancesMap[bal.Coin] = &model.Balance{
				Asset:      bal.Coin,
				Available:  available,
				Locked:     locked + frozen,
				Total:      total,
				UpdateTime: time.Now(),
			}
		}
	}

	return balancesMap, nil
}

// GetPosition 查询持仓（实现）
func (b *bitgetExchange) GetPosition(symbol string) (*model.Position, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey, passphrase := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	if symbol == "" {
		return nil, fmt.Errorf("invalid symbol")
	}

	normalizedSymbol := normalizeBitgetSymbol(symbol, true)
	// V2 API 需要 productType, symbol, marginCoin
	queryString := fmt.Sprintf("productType=USDT-FUTURES&symbol=%s&marginCoin=USDT", normalizedSymbol)
	
	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetPositionPath, queryString, "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetPositionPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get position failed: %w", err)
	}

	// 解析响应
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Symbol        string `json:"symbol"`
			MarginCoin    string `json:"marginCoin"`
			HoldSide      string `json:"holdSide"`
			Total         string `json:"total"`
			Available     string `json:"available"`
			Locked        string `json:"locked"`
			AverageOpenPrice string `json:"averageOpenPrice"`
			Leverage      string `json:"leverage"`
			UnrealisedPL  string `json:"unrealisedPL"`
			MarkPrice     string `json:"markPrice"`
			CTime         string `json:"cTime"`
			UTime         string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse position response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	// 查找指定 symbol 的持仓
	for _, pos := range resp.Data {
		if pos.Symbol == normalizedSymbol {
			total, _ := strconv.ParseFloat(pos.Total, 64)
			if total == 0 {
				continue
			}

			entryPrice, _ := strconv.ParseFloat(pos.AverageOpenPrice, 64)
			markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
			unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPL, 64)
			leverage, _ := strconv.Atoi(pos.Leverage)
			updateTime, _ := strconv.ParseInt(pos.UTime, 10, 64)

			var side model.PositionSide
			if pos.HoldSide == "long" {
				side = model.PositionSideLong
			} else {
				side = model.PositionSideShort
			}

			return &model.Position{
				Symbol:        pos.Symbol,
				Side:          side,
				Size:          total,
				EntryPrice:    entryPrice,
				MarkPrice:     markPrice,
				UnrealizedPnl: unrealizedPnl,
				Leverage:      leverage,
				UpdateTime:    time.Unix(updateTime/1000, 0),
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
func (b *bitgetExchange) GetPositions() ([]*model.Position, error) {
	b.mu.RLock()
	restClient := b.restClient
	b.mu.RUnlock()
	
	apiKey, secretKey, passphrase := b.getAPIKeys()

	if !b.isInitialized {
		return nil, fmt.Errorf("exchange not initialized")
	}

	// V2 API 参数：productType, marginCoin (可选)
	queryString := "productType=USDT-FUTURES&marginCoin=USDT"
	
	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", constants.BitgetAllPositionsPath, queryString, "", secretKey)
	
	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, constants.BitgetAllPositionsPath, queryString)
	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get positions failed: %w", err)
	}

	// 解析响应
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Symbol        string `json:"symbol"`
			MarginCoin    string `json:"marginCoin"`
			HoldSide      string `json:"holdSide"`
			Total         string `json:"total"`
			Available     string `json:"available"`
			Locked        string `json:"locked"`
			AverageOpenPrice string `json:"averageOpenPrice"`
			Leverage      string `json:"leverage"`
			UnrealisedPL  string `json:"unrealisedPL"`
			MarkPrice     string `json:"markPrice"`
			CTime         string `json:"cTime"`
			UTime         string `json:"uTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse positions response failed: %w", err)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	positions := make([]*model.Position, 0)
	for _, pos := range resp.Data {
		total, _ := strconv.ParseFloat(pos.Total, 64)
		if total == 0 {
			continue
		}

		entryPrice, _ := strconv.ParseFloat(pos.AverageOpenPrice, 64)
		markPrice, _ := strconv.ParseFloat(pos.MarkPrice, 64)
		unrealizedPnl, _ := strconv.ParseFloat(pos.UnrealisedPL, 64)
		leverage, _ := strconv.Atoi(pos.Leverage)
		updateTime, _ := strconv.ParseInt(pos.UTime, 10, 64)

		var side model.PositionSide
		if pos.HoldSide == "long" {
			side = model.PositionSideLong
		} else {
			side = model.PositionSideShort
		}

		positions = append(positions, &model.Position{
			Symbol:        pos.Symbol,
			Side:          side,
			Size:          total,
			EntryPrice:    entryPrice,
			MarkPrice:     markPrice,
			UnrealizedPnl: unrealizedPnl,
			Leverage:      leverage,
			UpdateTime:    time.Unix(updateTime/1000, 0),
		})
	}

	return positions, nil
}

// setLeverage 设置合约杠杆且强制全仓，在订阅合约 symbol 时调用
// 先 setMarginMode(crossed)，再 setLeverage；注意：由 SubscribeTicker 在已持 b.mu 时调用，此处不再加锁
func (b *bitgetExchange) setLeverage(symbol string, leverage int) error {
	restClient := b.restClient
	apiKey, secretKey, passphrase := b.getAPIKeys()

	norm := normalizeBitgetSymbol(symbol, true)

	// 1. 全仓模式
	modeBody := map[string]interface{}{
		"symbol":      norm,
		"productType": "umcbl",
		"marginCoin":  "USDT",
		"marginMode":  "crossed",
	}
	modeReq, _ := json.Marshal(modeBody)
	ts := getCurrentTimestamp()
	sig := signRequest(ts, "POST", constants.BitgetSetMarginModePath, "", string(modeReq), secretKey)
	headers := buildHeaders(apiKey, sig, ts, passphrase)
	if _, err := restClient.DoPostWithHeaders(fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetSetMarginModePath), string(modeReq), headers); err != nil {
		// 若有持仓/挂单可能切换失败，仅记录后继续设置杠杆
		_ = err
	}

	// 2. 杠杆
	body := map[string]interface{}{
		"symbol":      norm,
		"productType": "umcbl",
		"marginCoin":  "USDT",
		"leverage":    strconv.Itoa(leverage),
	}
	requestBody, _ := json.Marshal(body)

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "POST", constants.BitgetSetLeveragePath, "", string(requestBody), secretKey)
	apiURL := fmt.Sprintf("%s%s", constants.BitgetRestBaseUrl, constants.BitgetSetLeveragePath)
	headers = buildHeaders(apiKey, signature, timestamp, passphrase)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return fmt.Errorf("set leverage failed: %w", err)
	}
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err == nil && resp.Code != "00000" {
		return fmt.Errorf("bitget set leverage: %s", resp.Msg)
	}
	return nil
}
