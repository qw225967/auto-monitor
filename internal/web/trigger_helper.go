package web

import (
	"fmt"
	"strings"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/trigger"
	"auto-arbitrage/internal/trader"
)

// TraderTypeInfo traderType 解析结果
type TraderTypeInfo struct {
	Type      string // 类型（如 "binance", "onchain"）
	ChainId   string // 链ID（如果是链上类型）
	MarketType string // 市场类型（"spot" 或 "futures"，如果是交易所类型）
}

// resolveTraderTypes 解析并获取 traderType（从映射表或默认值）
// 优先使用请求中明确提供的值，只有在请求中未提供时才从 contract mapping 或默认值中获取
func (d *Dashboard) resolveTraderTypes(symbol, traderAType, traderBType string) (string, string, error) {
	// 如果请求中明确提供了两个 traderType，直接返回，不从 mapping 中读取
	if traderAType != "" && traderBType != "" {
		d.logger.Infof("Using provided trader types for %s: traderAType=%s, traderBType=%s", symbol, traderAType, traderBType)
		return traderAType, traderBType, nil
	}

	// 如果请求中只提供了部分值，尝试从合约映射表获取缺失的部分
	if mapping, err := d.contractMappingMgr.GetMapping(symbol); err == nil {
		d.logger.Infof("Found mapping for %s: traderAType=%s, traderBType=%s", symbol, mapping.TraderAType, mapping.TraderBType)
		// 只填充请求中未提供的部分
		if traderAType == "" {
			traderAType = mapping.TraderAType
		}
		if traderBType == "" {
			traderBType = mapping.TraderBType
		}
	} else {
		d.logger.Infof("No mapping found for %s, using defaults", symbol)
		// 如果映射表中不存在，使用默认值
		if traderAType == "" {
			traderAType = "onchain:56" // 默认 BSC 链
		}
		if traderBType == "" {
			traderBType = "binance:futures" // 默认 Binance 合约
		}
	}
	return traderAType, traderBType, nil
}

// parseTraderTypeInfo 解析 traderType 字符串为 TraderTypeInfo
func parseTraderTypeInfo(traderType string) (*TraderTypeInfo, error) {
	traderTypeStr, chainId, marketType, err := parseTraderType(traderType)
	if err != nil {
		return nil, err
	}
	return &TraderTypeInfo{
		Type:       traderTypeStr,
		ChainId:    chainId,
		MarketType: marketType,
	}, nil
}

// createTraderFromType 根据 traderType 创建 Trader 实例
func (d *Dashboard) createTraderFromType(traderType string) (trader.Trader, error) {
	info, err := parseTraderTypeInfo(traderType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse traderType: %w", err)
	}

	if info.Type == "onchain" {
		// 链上类型，返回 nil（会在 subscribeOnchainForSource 中创建）
		return nil, nil
	}

	// 交易所类型，获取对应的 Trader 实例
	exchangeType := d.mapExchangeType(info.Type)
	if exchangeType == "" {
		return nil, fmt.Errorf("unsupported exchange type: %s", info.Type)
	}

	traderInterface := d.triggerManager.GetExchangeSource(exchangeType)
	if traderInterface == nil {
		return nil, fmt.Errorf("trader not found for type: %s", info.Type)
	}

	t, ok := traderInterface.(trader.Trader)
	if !ok {
		return nil, fmt.Errorf("trader type assertion failed for type: %s", info.Type)
	}

	return t, nil
}

// mapExchangeType 将交易所类型字符串映射为 ExchangeType 常量
// 大小写不敏感，支持 "okex"、"OKX" 等
func (d *Dashboard) mapExchangeType(exchangeTypeStr string) constants.ExchangeType {
	s := strings.ToLower(strings.TrimSpace(exchangeTypeStr))
	switch s {
	case "binance":
		return constants.ExchangeBinance
	case "gate":
		return constants.ExchangeGate
	case "bybit":
		return constants.ExchangeByBit
	case "bitget":
		return constants.ExchangeBitGet
	case "okex", "okx":
		return constants.ExchangeOKEX
	case "aster":
		return constants.ExchangeAster
	case "hyperliquid":
		return constants.ExchangeHyperliquid
	case "lighter":
		return constants.ExchangeLighter
	default:
		return ""
	}
}

// buildTriggerResponse 构建创建 trigger 的返回响应
func buildTriggerResponse(symbol, traderAType, traderBType string, aInfo, bInfo *TraderTypeInfo) map[string]interface{} {
	// 确定返回的 exchangeType 和 marketType（优先使用 B 的，如果 B 不是交易所则使用 A 的）
	returnExchangeType := bInfo.Type
	returnMarketType := bInfo.MarketType
	if bInfo.Type == "onchain" && aInfo.Type != "onchain" {
		returnExchangeType = aInfo.Type
		returnMarketType = aInfo.MarketType
	}

	// 确定返回的 chainId（优先使用 A 的，如果 A 不是 onchain 则使用 B 的）
	returnChainId := aInfo.ChainId
	if aInfo.Type != "onchain" && bInfo.Type == "onchain" {
		returnChainId = bInfo.ChainId
	}

	return map[string]interface{}{
		"status":       "created",
		"symbol":       symbol,
		"traderAType":  traderAType,
		"traderBType":  traderBType,
		"chainId":      returnChainId,
		"exchangeType": returnExchangeType,
		"marketType":   returnMarketType,
		"note":         "Trigger created with Trader interface",
	}
}

// configureTrigger 配置 trigger 的类型和链ID
func configureTrigger(tg *trigger.Trigger, traderAType, traderBType string, aInfo, bInfo *TraderTypeInfo) {
	// 设置类型信息
	tg.SetTraderTypes(traderAType, traderBType)

	// 如果 A 或 B 是链上类型，设置链ID（优先使用 A 的 chainId，如果 A 不是 onchain 则使用 B 的）
	if aInfo.Type == "onchain" && aInfo.ChainId != "" {
		tg.SetChainId(aInfo.ChainId)
	} else if bInfo.Type == "onchain" && bInfo.ChainId != "" {
		tg.SetChainId(bInfo.ChainId)
	} else if aInfo.ChainId != "" {
		// 如果 A 不是 onchain 但 chainId 有值（可能是其他用途），也设置
		tg.SetChainId(aInfo.ChainId)
	}
}

