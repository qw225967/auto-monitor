package hyperliquid

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
)

// assetIndexCache 缓存 symbol->asset 索引，避免重复请求 meta
var assetIndexCache struct {
	sync.Mutex
	m  map[string]int
	ok bool
}

// getAccountBalance 获取账户余额
func (h *hyperliquidExchange) getAccountBalance() (*model.Balance, error) {
	walletAddress, _ := h.getWalletCredentials()
	
	// 构建请求
	request := buildInfoRequest(constants.HyperliquidQueryTypeClearinghouse, map[string]interface{}{
		"user": walletAddress,
	})
	
	requestBody, _ := json.Marshal(request)
	
	// 发送请求
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidInfoPath)
	headers := buildHeaders()
	
	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("get account balance failed: %w", err)
	}
	
	// 解析响应
	return h.parseAccountBalance(responseBody)
}

// parseAccountBalance 解析账户余额响应
func (h *hyperliquidExchange) parseAccountBalance(responseBody string) (*model.Balance, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	var resp struct {
		MarginSummary struct {
			AccountValue string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
			TotalNtlPos string `json:"totalNtlPos"`
			TotalRawUsd string `json:"totalRawUsd"`
		} `json:"marginSummary"`
		CrossMarginSummary struct {
			AccountValue string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
		} `json:"crossMarginSummary"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse balance response failed: %w", err)
	}
	
	accountValue := parseFloat64(resp.MarginSummary.AccountValue)
	marginUsed := parseFloat64(resp.MarginSummary.TotalMarginUsed)
	available := accountValue - marginUsed
	
	return &model.Balance{
		Asset:      "USDT",
		Available:  available,
		Locked:     marginUsed,
		Total:      accountValue,
		UpdateTime: time.Now(),
	}, nil
}

// getAllAccountBalances 获取所有币种的余额
func (h *hyperliquidExchange) getAllAccountBalances() (map[string]*model.Balance, error) {
	// Hyperliquid 主要使用 USDT 作为保证金
	balance, err := h.getAccountBalance()
	if err != nil {
		return nil, err
	}
	
	result := make(map[string]*model.Balance)
	result["USDT"] = balance
	
	return result, nil
}

// getPositionBySymbol 获取指定交易对的持仓
func (h *hyperliquidExchange) getPositionBySymbol(symbol string) (*model.Position, error) {
	walletAddress, _ := h.getWalletCredentials()
	
	// 构建请求
	request := buildInfoRequest(constants.HyperliquidQueryTypeClearinghouse, map[string]interface{}{
		"user": walletAddress,
	})
	
	requestBody, _ := json.Marshal(request)
	
	// 发送请求
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidInfoPath)
	headers := buildHeaders()
	
	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("get position failed: %w", err)
	}
	
	// 解析响应并查找指定交易对的持仓
	return h.parsePositionBySymbol(responseBody, symbol)
}

// getAllPositions 获取所有持仓
func (h *hyperliquidExchange) getAllPositions() ([]*model.Position, error) {
	walletAddress, _ := h.getWalletCredentials()
	
	// 构建请求
	request := buildInfoRequest(constants.HyperliquidQueryTypeClearinghouse, map[string]interface{}{
		"user": walletAddress,
	})
	
	requestBody, _ := json.Marshal(request)
	
	// 发送请求
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidInfoPath)
	headers := buildHeaders()
	
	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), headers)
	if err != nil {
		return nil, fmt.Errorf("get positions failed: %w", err)
	}
	
	// 解析响应
	return h.parseAllPositions(responseBody)
}

// parsePositionBySymbol 解析指定交易对的持仓
func (h *hyperliquidExchange) parsePositionBySymbol(responseBody string, targetSymbol string) (*model.Position, error) {
	positions, err := h.parseAllPositions(responseBody)
	if err != nil {
		return nil, err
	}
	
	// 标准化目标符号
	normalizedTarget := normalizeSymbol(targetSymbol, true)
	
	// 查找匹配的持仓
	for _, pos := range positions {
		if pos.Symbol == normalizedTarget || pos.Symbol == targetSymbol {
			return pos, nil
		}
	}
	
	// 如果没有找到持仓，返回空持仓
	return &model.Position{
		Symbol:        targetSymbol,
		Size:          0,
		EntryPrice:    0,
		MarkPrice:     0,
		UnrealizedPnl: 0,
		Leverage:      1,
		UpdateTime:    time.Now(),
	}, nil
}

// parseAllPositions 解析所有持仓
func (h *hyperliquidExchange) parseAllPositions(responseBody string) ([]*model.Position, error) {
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	
	var resp struct {
		AssetPositions []struct {
			Position struct {
				Coin string `json:"coin"`
				Szi  string `json:"szi"` // Size (正数为多头，负数为空头)
				EntryPx string `json:"entryPx"` // 入场价格
				PositionValue string `json:"positionValue"`
				UnrealizedPnl string `json:"unrealizedPnl"`
				ReturnOnEquity string `json:"returnOnEquity"`
				Leverage struct {
					Type  string `json:"type"`
					Value int    `json:"value"`
				} `json:"leverage"`
			} `json:"position"`
			Type string `json:"type"` // "oneWay" or "hedge"
		} `json:"assetPositions"`
	}
	
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse positions response failed: %w", err)
	}
	
	var positions []*model.Position
	
	for _, assetPos := range resp.AssetPositions {
		pos := assetPos.Position
		
		size := parseFloat64(pos.Szi)
		if size == 0 {
			continue // 跳过空持仓
		}
		
		entryPrice := parseFloat64(pos.EntryPx)
		pnl := parseFloat64(pos.UnrealizedPnl)
		leverage := float64(pos.Leverage.Value)
		if leverage == 0 {
			leverage = 1
		}
		
		// 确定方向
		side := model.PositionSideLong
		absSize := size
		if size < 0 {
			side = model.PositionSideShort
			absSize = -size
		}
		
		// 反标准化符号（转回系统格式）
		symbol := denormalizeSymbol(pos.Coin)
		
		positions = append(positions, &model.Position{
			Symbol:        symbol,
			Side:          side,
			Size:          absSize,
			EntryPrice:    entryPrice,
			MarkPrice:     0, // 需要从其他接口获取
			UnrealizedPnl: pnl,
			Leverage:      int(leverage),
			UpdateTime:    time.Now(),
		})
	}
	
	return positions, nil
}

// getAssetIndex 从 meta 获取永续合约的 asset 索引，用于 updateLeverage 等
func (h *hyperliquidExchange) getAssetIndex(symbol string) (int, error) {
	coin := normalizeSymbol(symbol, true)

	assetIndexCache.Lock()
	defer assetIndexCache.Unlock()

	if !assetIndexCache.ok {
		request := buildInfoRequest(constants.HyperliquidQueryTypeMeta, nil)
		requestBody, _ := json.Marshal(request)
		apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidInfoPath)
		responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), buildHeaders())
		if err != nil {
			return 0, fmt.Errorf("fetch meta: %w", err)
		}
		var meta struct {
			Universe []struct {
				Name string `json:"name"`
			} `json:"universe"`
		}
		if err := json.Unmarshal([]byte(responseBody), &meta); err != nil {
			return 0, fmt.Errorf("parse meta: %w", err)
		}
		assetIndexCache.m = make(map[string]int)
		for i, u := range meta.Universe {
			assetIndexCache.m[u.Name] = i
		}
		assetIndexCache.ok = true
	}

	if idx, ok := assetIndexCache.m[coin]; ok {
		return idx, nil
	}
	return 0, fmt.Errorf("asset not found for symbol %s (coin %s)", symbol, coin)
}

// setLeverage 设置合约杠杆，在订阅合约 symbol 时调用
func (h *hyperliquidExchange) setLeverage(symbol string, leverage int) error {
	walletAddress, privateKey := h.getWalletCredentials()
	walletAddress = strings.ToLower(walletAddress)

	asset, err := h.getAssetIndex(symbol)
	if err != nil {
		return err
	}

	// UpdateLeverageAction 与 signOrderWithEIP712 兼容（msgpack 序列化）
	type UpdateLeverageAction struct {
		Type     string `msgpack:"type"`
		Asset    int    `msgpack:"asset"`
		IsCross  bool   `msgpack:"isCross"`
		Leverage int    `msgpack:"leverage"`
	}
	action := UpdateLeverageAction{
		Type:     constants.HyperliquidActionUpdateLeverage,
		Asset:    asset,
		IsCross:  true, // 全仓
		Leverage: leverage,
	}

	timestamp := getCurrentNonce()
	signatureObj, err := signOrderWithEIP712(privateKey, action, timestamp)
	if err != nil {
		return fmt.Errorf("sign updateLeverage: %w", err)
	}

	request := buildExchangeRequestWithEIP712(constants.HyperliquidActionUpdateLeverage, walletAddress, signatureObj, action, timestamp)
	requestBody, _ := json.Marshal(request)
	apiURL := fmt.Sprintf("%s%s", constants.HyperliquidRestBaseUrl, constants.HyperliquidExchangePath)

	responseBody, err := h.restClient.DoPostWithHeaders(apiURL, string(requestBody), buildHeaders())
	if err != nil {
		return fmt.Errorf("updateLeverage request: %w", err)
	}
	return checkAPIError(responseBody)
}
