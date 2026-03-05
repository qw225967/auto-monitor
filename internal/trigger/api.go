package trigger

import (
	"fmt"
	"strings"
	"time"

	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/trader"
)

// 确保 TriggerManager 实现 proto.TriggerManager 接口
var _ proto.TriggerManager = (*TriggerManager)(nil)

// 确保 Trigger 实现 proto.Trigger 接口
var _ proto.Trigger = (*Trigger)(nil)

// NewTrigger 实现 proto.TriggerManager 接口
// mode: 0 表示 ModeInstant, 1 表示 ModeScheduled
func (tm *TriggerManager) NewTrigger(symbol string, sourceA, sourceB interface{}, mode int) proto.Trigger {
	var triggerMode TriggerMode
	if mode == 0 {
		triggerMode = ModeInstant
	} else {
		triggerMode = ModeScheduled
	}

	// 转换 interface{} 为 trader.Trader
	var traderA, traderB trader.Trader
	if sourceA != nil {
		if t, ok := sourceA.(trader.Trader); ok {
			traderA = t
		} else if exch, ok := sourceA.(exchange.Exchange); ok {
			// 向后兼容：如果是 Exchange，创建 CexTrader 或 DexTrader
			exchType := exch.GetType()
			if exchType == "aster" || exchType == "hyperliquid" || exchType == "lighter" {
				traderA = trader.NewDexTrader(exch)
			} else {
				traderA = trader.NewCexTrader(exch)
			}
		} else if onchainClient, ok := sourceA.(onchain.OnchainClient); ok {
			// 向后兼容：如果是 OnchainClient，创建 OnchainTrader
			traderA = trader.NewOnchainTrader(onchainClient, "onchain:56")
		}
	}
	if sourceB != nil {
		if t, ok := sourceB.(trader.Trader); ok {
			traderB = t
		} else if exch, ok := sourceB.(exchange.Exchange); ok {
			// 向后兼容：如果是 Exchange，创建 CexTrader 或 DexTrader
			exchType := exch.GetType()
			if exchType == "aster" || exchType == "hyperliquid" || exchType == "lighter" {
				traderB = trader.NewDexTrader(exch)
			} else {
				traderB = trader.NewCexTrader(exch)
			}
		} else if onchainClient, ok := sourceB.(onchain.OnchainClient); ok {
			// 向后兼容：如果是 OnchainClient，创建 OnchainTrader
			traderB = trader.NewOnchainTrader(onchainClient, "onchain:56")
		}
	}

	return tm.NewTriggerWithMode(symbol, traderA, traderB, triggerMode)
}

// ============================================================================
// API 方法：以下方法仅用于暴露给 Web Dashboard，不涉及核心逻辑
// ============================================================================

// GetID 获取 Trigger ID
func (t *Trigger) GetID() uint64 {
	return t.ID
}

// GetSymbol 获取交易对符号
func (t *Trigger) GetSymbol() string {
	return t.symbol
}

// GetStatus 获取 trigger 的运行状态
func (t *Trigger) GetStatus() string {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	if t.isRunning {
		return "running"
	}
	return "stopped"
}

// IsRunning 检查 trigger 是否在运行
func (t *Trigger) IsRunning() bool {
	t.statusMu.RLock()
	defer t.statusMu.RUnlock()
	return t.isRunning
}

// GetOnChainClient 获取链上客户端实例（向后兼容，从 sourceA 或 sourceB 获取）
func (t *Trigger) GetOnChainClient() onchain.OnchainClient {
	onchainTrader := t.getOnchainTrader()
	if onchainTrader == nil {
		return nil
	}
	// 通过 OnchainTrader 获取底层 client
	if impl, ok := onchainTrader.(*trader.OnchainTraderImpl); ok {
		return impl.GetOnchainClient()
	}
	return nil
}

// GetAnalytics 返回具体的 Analytics 实例
func (t *Trigger) GetAnalytics() *analytics.Analytics {
	if v, ok := any(t.analytics).(*analytics.Analytics); ok {
		return v
	}
	return nil
}

// GetPriceData 获取当前价格数据
func (t *Trigger) GetPriceData() (directionAB, directionBA *DirectionConfig) {
	return t.directionAB, t.directionBA
}

// GetOnChainData 获取链上数据
func (t *Trigger) GetOnChainData() *OnChainData {
	return t.onChainData
}

// GetTargetThresholdInterval 获取目标价差阈值区间（向后兼容，返回 minInterval）
// Deprecated: 请使用 GetThresholdRange
func (t *Trigger) GetTargetThresholdInterval() float64 {
	return t.GetMinThreshold()
}

// SetTargetThresholdInterval 设置目标价差阈值区间（向后兼容，设置 minInterval）
// Deprecated: 请使用 SetThresholdRange
func (t *Trigger) SetTargetThresholdInterval(targetThresholdInterval float64) error {
	return t.SetMinThreshold(targetThresholdInterval)
}

// GetMinThreshold 获取最小阈值
func (t *Trigger) GetMinThreshold() float64 {
	if t.analytics == nil {
		return 0
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		return v.GetMinThreshold()
	}
	return 0
}

// SetMinThreshold 设置最小阈值
func (t *Trigger) SetMinThreshold(minThreshold float64) error {
	if minThreshold < 0 {
		return fmt.Errorf("min threshold must be >= 0")
	}
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.SetMinThreshold(minThreshold)
		t.logger.Infof("Trigger %s 的最小阈值已设置为: %.6f", t.symbol, minThreshold)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// GetMaxThreshold 获取最大阈值
func (t *Trigger) GetMaxThreshold() float64 {
	if t.analytics == nil {
		return 0
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		return v.GetMaxThreshold()
	}
	return 0
}

// SetMaxThreshold 设置最大阈值
func (t *Trigger) SetMaxThreshold(maxThreshold float64) error {
	if maxThreshold < 0 {
		return fmt.Errorf("max threshold must be >= 0")
	}
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.SetMaxThreshold(maxThreshold)
		t.logger.Infof("Trigger %s 的最大阈值已设置为: %.6f", t.symbol, maxThreshold)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// ==================== 快速触发优化器参数设置 ====================

// GetFastTriggerSpeedWeight 获取快速触发速度权重
// Deprecated: 重构后算法不再使用速度权重，返回默认值 0.6 以保持兼容性
func (t *Trigger) GetFastTriggerSpeedWeight() float64 {
	return 0.6 // 默认值（已废弃，仅用于兼容）
}

// SetFastTriggerSpeedWeight 设置快速触发速度权重
// Deprecated: 重构后算法不再使用速度权重，此方法已废弃，仅返回 nil 以保持兼容性
func (t *Trigger) SetFastTriggerSpeedWeight(weight float64) error {
	if weight < 0 || weight > 1 {
		return fmt.Errorf("speed weight must be between 0 and 1")
	}
	// 已废弃：新算法不再使用速度权重
	t.logger.Warnf("Trigger %s: SetFastTriggerSpeedWeight 已废弃，新算法不再使用速度权重", t.symbol)
	return nil
}

// GetFastTriggerQuantileLevel 获取快速触发分位数水平
// Deprecated: 重构后算法不再使用分位数水平，返回默认值 0.3 以保持兼容性
func (t *Trigger) GetFastTriggerQuantileLevel() float64 {
	return 0.3 // 默认值（已废弃，仅用于兼容）
}

// SetFastTriggerQuantileLevel 设置快速触发分位数水平
// Deprecated: 重构后算法不再使用分位数水平，此方法已废弃，仅返回 nil 以保持兼容性
func (t *Trigger) SetFastTriggerQuantileLevel(level float64) error {
	if level < 0.1 || level > 0.5 {
		return fmt.Errorf("quantile level must be between 0.1 and 0.5")
	}
	// 已废弃：新算法不再使用分位数水平
	t.logger.Warnf("Trigger %s: SetFastTriggerQuantileLevel 已废弃，新算法不再使用分位数水平", t.symbol)
	return nil
}

// GetFastTriggerMaxAcceptableDelay 获取快速触发最大可接受延迟（毫秒）
// Deprecated: 重构后算法不再使用延迟，返回默认值 1000 以保持兼容性
func (t *Trigger) GetFastTriggerMaxAcceptableDelay() int64 {
	return 1000 // 默认值（已废弃，仅用于兼容）
}

// SetFastTriggerMaxAcceptableDelay 设置快速触发最大可接受延迟（毫秒）
// Deprecated: 重构后算法不再使用延迟，此方法已废弃，仅返回 nil 以保持兼容性
func (t *Trigger) SetFastTriggerMaxAcceptableDelay(delayMs int64) error {
	if delayMs < 100 || delayMs > 10000 {
		return fmt.Errorf("max acceptable delay must be between 100ms and 10000ms")
	}
	// 已废弃：新算法不再使用延迟
	t.logger.Warnf("Trigger %s: SetFastTriggerMaxAcceptableDelay 已废弃，新算法不再使用延迟", t.symbol)
	return nil
}

// GetFastTriggerMinValidTriggers 获取快速触发最小有效触发次数
// Deprecated: 重构后算法不再使用最小有效触发次数，此方法保留以保持 API 兼容性，返回默认值 5
func (t *Trigger) GetFastTriggerMinValidTriggers() int {
	// 重构后算法优化目标是成交次数和价差，不再使用最小有效触发次数
	// 返回默认值以保持 API 兼容性
	return 5
}

// SetFastTriggerMinValidTriggers 设置快速触发最小有效触发次数
// Deprecated: 重构后算法不再使用最小有效触发次数，此方法保留以保持 API 兼容性，不执行任何操作
func (t *Trigger) SetFastTriggerMinValidTriggers(count int) error {
	// 重构后算法不再使用最小有效触发次数，此方法保留以保持 API 兼容性
	t.logger.Warnf("SetFastTriggerMinValidTriggers 已废弃：重构后算法不再使用最小有效触发次数，请使用新的配置参数")
	return nil
}

// GetFastTriggerConfig 获取快速触发优化器的所有配置
func (t *Trigger) GetFastTriggerConfig() map[string]interface{} {
	config := map[string]interface{}{
		// 已废弃的配置（保留以保持 API 兼容性）
		"speedWeight":        0.6,  // Deprecated: 重构后不再使用
		"quantileLevel":      0.3,  // Deprecated: 重构后不再使用
		"maxAcceptableDelay": int64(1000),  // Deprecated: 重构后不再使用
		"minValidTriggers":   5,  // Deprecated: 重构后不再使用
		
		// 当前使用的配置
		"minRoundTripProfit": 0.5,
		"maxRoundTripProfit": 0.0,
		"minThreshold": -1e6,
		"maxThreshold": 0.0,
		"weightDecay": 0.01,
		"useWeightedWindow": true,
		"changeDetectionThreshold": 0.3,
		"recentDataWeight": 0.9,
		"outlierPercentile": 0.05,
		
		// 阈值稳定性配置
		"thresholdSmoothingAlpha":   0.2,
		"thresholdMinChangeRatio":   0.15,
		"thresholdMinUpdateInterval": int64(10000),
		"thresholdMinSuccessRate":   0.5,
		"thresholdMinTriggerCount":  5,
	}

	if t.analytics == nil {
		return config
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		opt := v.GetFastTriggerOptimizer()
		if opt != nil {
			// 当前使用的配置
			config["minRoundTripProfit"] = opt.MinRoundTripProfit
			config["maxRoundTripProfit"] = opt.MaxRoundTripProfit
			config["minThreshold"] = opt.MinThreshold
			config["maxThreshold"] = opt.MaxThreshold
			config["weightDecay"] = opt.WeightDecay
			config["useWeightedWindow"] = opt.UseWeightedWindow
			config["changeDetectionThreshold"] = opt.ChangeDetectionThreshold
			config["recentDataWeight"] = opt.RecentDataWeight
			config["outlierPercentile"] = opt.OutlierPercentile
		}

		// 获取阈值稳定性配置
		stabilityConfig := v.GetThresholdStabilityConfig()
		if stabilityConfig != nil {
			config["thresholdSmoothingAlpha"] = stabilityConfig.SmoothingAlpha
			config["thresholdMinChangeRatio"] = stabilityConfig.MinChangeRatio
			config["thresholdMinUpdateInterval"] = stabilityConfig.MinUpdateInterval.Milliseconds()
			config["thresholdMinSuccessRate"] = stabilityConfig.MinSuccessRate
			config["thresholdMinTriggerCount"] = stabilityConfig.MinTriggerCount
		}
	}
	return config
}

// SetThresholdSmoothingAlpha 设置阈值平滑系数
func (t *Trigger) SetThresholdSmoothingAlpha(alpha float64) error {
	if alpha < 0 || alpha > 1 {
		return fmt.Errorf("smoothing alpha must be between 0 and 1")
	}
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.SetThresholdSmoothingAlpha(alpha)
		t.logger.Infof("Trigger %s 的阈值平滑系数已设置为: %.2f", t.symbol, alpha)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// GetThresholdSmoothingAlpha 获取阈值平滑系数
func (t *Trigger) GetThresholdSmoothingAlpha() float64 {
	if t.analytics == nil {
		return 0.2 // 默认值
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		config := v.GetThresholdStabilityConfig()
		if config != nil {
			return config.SmoothingAlpha
		}
	}
	return 0.2
}

// SetThresholdMinChangeRatio 设置阈值最小变化比例
func (t *Trigger) SetThresholdMinChangeRatio(ratio float64) error {
	if ratio < 0 || ratio > 1 {
		return fmt.Errorf("min change ratio must be between 0 and 1")
	}
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.SetThresholdMinChangeRatio(ratio)
		t.logger.Infof("Trigger %s 的阈值最小变化比例已设置为: %.2f%%", t.symbol, ratio*100)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// GetThresholdMinChangeRatio 获取阈值最小变化比例
func (t *Trigger) GetThresholdMinChangeRatio() float64 {
	if t.analytics == nil {
		return 0.15 // 默认值
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		config := v.GetThresholdStabilityConfig()
		if config != nil {
			return config.MinChangeRatio
		}
	}
	return 0.15
}

// SetThresholdMinUpdateInterval 设置阈值最小更新间隔（毫秒）
func (t *Trigger) SetThresholdMinUpdateInterval(intervalMs int64) error {
	if intervalMs < 0 {
		return fmt.Errorf("min update interval must be >= 0")
	}
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.SetThresholdMinUpdateInterval(time.Duration(intervalMs) * time.Millisecond)
		t.logger.Infof("Trigger %s 的阈值最小更新间隔已设置为: %dms", t.symbol, intervalMs)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// GetThresholdMinUpdateInterval 获取阈值最小更新间隔（毫秒）
func (t *Trigger) GetThresholdMinUpdateInterval() int64 {
	if t.analytics == nil {
		return 10000 // 默认10秒
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		config := v.GetThresholdStabilityConfig()
		if config != nil {
			return config.MinUpdateInterval.Milliseconds()
		}
	}
	return 10000
}

// SetDirectionEnabled 设置方向的订单执行启用状态（实现 proto.Trigger 接口）
// direction: 0 表示 DirectionAB, 1 表示 DirectionBA
func (t *Trigger) SetDirectionEnabled(direction int, enabled bool) {
	if direction == 0 {
		t.SetDirectionEnabledOrder(DirectionAB, enabled)
	} else if direction == 1 {
		t.SetDirectionEnabledOrder(DirectionBA, enabled)
	}
}

// SetDirectionEnabledOrder 设置方向的订单执行启用状态（使用 OrderDirection 类型）
func (t *Trigger) SetDirectionEnabledOrder(direction OrderDirection, enabled bool) {
	if direction == DirectionAB {
		t.directionAB.OrderExecutionEnabled = enabled
	} else if direction == DirectionBA {
		t.directionBA.OrderExecutionEnabled = enabled
	}
}

// GetDirectionEnabled 获取方向的订单执行启用状态（实现 proto.Trigger 接口）
// direction: 0 表示 DirectionAB, 1 表示 DirectionBA
func (t *Trigger) GetDirectionEnabled(direction int) bool {
	if direction == 0 {
		return t.GetDirectionEnabledOrder(DirectionAB)
	} else if direction == 1 {
		return t.GetDirectionEnabledOrder(DirectionBA)
	}
	return false
}

// GetDirectionEnabledOrder 获取方向的订单执行启用状态（使用 OrderDirection 类型）
func (t *Trigger) GetDirectionEnabledOrder(direction OrderDirection) bool {
	if direction == DirectionAB {
		return t.directionAB.OrderExecutionEnabled
	} else if direction == DirectionBA {
		return t.directionBA.OrderExecutionEnabled
	}
	return false
}

// SetTelegramNotificationEnabled 设置 Telegram 通知启用状态
func (t *Trigger) SetTelegramNotificationEnabled(enabled bool) {
	t.telegramNotificationMu.Lock()
	defer t.telegramNotificationMu.Unlock()
	t.telegramNotificationEnabled = enabled
}

// GetTelegramNotificationEnabled 获取 Telegram 通知启用状态
func (t *Trigger) GetTelegramNotificationEnabled() bool {
	t.telegramNotificationMu.RLock()
	defer t.telegramNotificationMu.RUnlock()
	return t.telegramNotificationEnabled
}

// GetSlippageData 获取滑点计算结果
func (t *Trigger) GetSlippageData() map[string]interface{} {
	result := make(map[string]interface{})

	if t.analytics == nil {
		t.logger.Debug("analytics 为空，返回空的滑点数据")
		result["aBuy"] = 0.0
		result["aSell"] = 0.0
		result["bBuy"] = 0.0
		result["bSell"] = 0.0
		result["aName"] = ""
		result["bName"] = ""
		// 向后兼容
		result["exchangeBuy"] = 0.0
		result["exchangeSell"] = 0.0
		result["onchainBuy"] = 0.0
		result["onchainSell"] = 0.0
		return result
	}

	// 从 analytics 获取滑点数据
	slippageData := t.analytics.GetSlippageData()
	if slippageData == nil {
		result["aBuy"] = 0.0
		result["aSell"] = 0.0
		result["bBuy"] = 0.0
		result["bSell"] = 0.0
		result["aName"] = ""
		result["bName"] = ""
		// 向后兼容
		result["exchangeBuy"] = 0.0
		result["exchangeSell"] = 0.0
		result["onchainBuy"] = 0.0
		result["onchainSell"] = 0.0
		return result
	}

	// 获取 A 和 B 的显示名称
	aName := t.getTraderDisplayName(t.traderAType, true)
	bName := t.getTraderDisplayName(t.traderBType, false)

	// 返回 A 和 B 的滑点数据
	result["aBuy"] = slippageData.ABuy
	result["aSell"] = slippageData.ASell
	result["bBuy"] = slippageData.BBuy
	result["bSell"] = slippageData.BSell
	result["aName"] = aName
	result["bName"] = bName

	// 向后兼容：保留旧的键
	result["exchangeBuy"] = slippageData.ExchangeBuy
	result["exchangeSell"] = slippageData.ExchangeSell
	result["onchainBuy"] = slippageData.OnChainBuy
	result["onchainSell"] = slippageData.OnChainSell

	t.logger.Debugf("获取滑点数据 - %s买入: %.4f%%, %s卖出: %.4f%%, %s买入: %.4f%%, %s卖出: %.4f%%",
		aName, slippageData.ABuy, aName, slippageData.ASell,
		bName, slippageData.BBuy, bName, slippageData.BSell)

	return result
}

// getTraderDisplayName 获取 trader 的显示名称
// 例如："binance:futures" -> "Binance"，"onchain:56" -> "Onchain"
func (t *Trigger) getTraderDisplayName(traderType string, isSourceA bool) string {
	if traderType == "" {
		// 如果 traderType 为空，尝试从 source 推断
		var source trader.Trader
		if isSourceA {
			source = t.sourceA
		} else {
			source = t.sourceB
		}
		if source != nil {
			traderType = source.GetType()
		}
	}

	if traderType == "" {
		if isSourceA {
			return "A"
		}
		return "B"
	}

	// 解析 traderType：格式为 "type:value"（如 "binance:futures" 或 "onchain:56"）
	parts := strings.Split(traderType, ":")
	if len(parts) != 2 {
		return traderType // 如果格式不对，直接返回原值
	}

	traderTypeStr := parts[0]
	if traderTypeStr == "onchain" {
		return "Onchain"
	}

	// 交易所类型，首字母大写
	if len(traderTypeStr) > 0 {
		return strings.ToUpper(traderTypeStr[:1]) + traderTypeStr[1:]
	}
	return traderTypeStr
}

// GetOptimalThresholds 获取最优阈值区间
func (t *Trigger) GetOptimalThresholds() map[string]interface{} {
	result := make(map[string]interface{})

	if t.analytics == nil {
		t.logger.Debug("analytics 为空，返回空的最优阈值数据")
		result["thresholdAB"] = nil
		result["thresholdBA"] = nil
		result["longOpportunityCount"] = 0
		result["shortOpportunityCount"] = 0
		result["totalTrades"] = 0
		return result
	}

	optimalThresholds := t.analytics.GetOptimalThresholds()
	if optimalThresholds == nil {
		t.logger.Debug("最优阈值未计算，返回空数据")
		result["thresholdAB"] = nil
		result["thresholdBA"] = nil
		result["longOpportunityCount"] = 0
		result["shortOpportunityCount"] = 0
		result["totalTrades"] = 0
		return result
	}

	t.logger.Debugf("获取最优阈值 - AB: %.6f, BA: %.6f, 总触发次数: %d, 平均延迟: %v, 成功率: %.2f%%, Long机会点: %d, Short机会点: %d",
		optimalThresholds.ThresholdAB, optimalThresholds.ThresholdBA,
		optimalThresholds.TotalTrades, optimalThresholds.AvgTriggerDelay,
		optimalThresholds.SuccessRate*100,
		optimalThresholds.LongOpportunityCount, optimalThresholds.ShortOpportunityCount)

	result["thresholdAB"] = optimalThresholds.ThresholdAB
	result["thresholdBA"] = optimalThresholds.ThresholdBA
	result["longOpportunityCount"] = optimalThresholds.LongOpportunityCount   // Long 机会点数量
	result["shortOpportunityCount"] = optimalThresholds.ShortOpportunityCount // Short 机会点数量
	result["totalTrades"] = optimalThresholds.TotalTrades
	result["avgTriggerDelay"] = optimalThresholds.AvgTriggerDelay.Milliseconds() // 平均触发延迟（毫秒）
	result["successRate"] = optimalThresholds.SuccessRate                        // 触发成功率

	return result
}

// ClearPriceDiffs 清空历史价差数据
func (t *Trigger) ClearPriceDiffs() error {
	if t.analytics == nil {
		return fmt.Errorf("analytics is nil")
	}
	if v, ok := t.analytics.(*analytics.Analytics); ok {
		v.ClearPriceDiffs()
		t.logger.Infof("Trigger %s 的历史价差数据已清空", t.symbol)
		return nil
	}
	return fmt.Errorf("analytics type assertion failed")
}

// SetCleanupPriceDiffsInterval 设置清理价差数据间隔
// interval: 清理间隔，0 表示不自动清理
func (t *Trigger) SetCleanupPriceDiffsInterval(interval time.Duration) error {
	if t.intervalOpt == nil {
		return fmt.Errorf("intervalOpt is nil")
	}
	t.intervalOpt.cleanupPriceDiffs = interval
	if interval > 0 {
		t.logger.Infof("Trigger %s 的清理价差数据间隔已设置为: %v", t.symbol, interval)
	} else {
		t.logger.Infof("Trigger %s 的自动清理价差数据已禁用", t.symbol)
	}
	return nil
}

// GetCleanupPriceDiffsInterval 获取清理价差数据间隔
func (t *Trigger) GetCleanupPriceDiffsInterval() time.Duration {
	if t.intervalOpt == nil {
		return 0
	}
	return t.intervalOpt.cleanupPriceDiffs
}

// IsBundlerEnabled 检查是否启用了 Bundler
func (t *Trigger) IsBundlerEnabled() bool {
	client := t.GetOnChainClient()
	if client == nil {
		return false
	}
	return onchain.IsBundlerEnabledForClient(client)
}

// EnableBundler 启用 Bundler
func (t *Trigger) EnableBundler() error {
	client := t.GetOnChainClient()
	if client == nil {
		t.logger.Debugf("Trigger %s 无链上客户端，跳过 EnableBundler", t.symbol)
		return nil // 无链上时为 no-op，避免保存配置报错
	}
	onchain.EnableBundlerForClient(client)
	t.logger.Infof("Trigger %s Bundler 已启用", t.symbol)
	return nil
}

// DisableBundler 禁用 Bundler
func (t *Trigger) DisableBundler() error {
	client := t.GetOnChainClient()
	if client == nil {
		t.logger.Debugf("Trigger %s 无链上客户端，跳过 DisableBundler", t.symbol)
		return nil // 无链上时为 no-op，避免保存配置报错
	}
	onchain.DisableBundlerForClient(client)
	t.logger.Infof("Trigger %s Bundler 已禁用", t.symbol)
	return nil
}

// GetOnChainSlippage 获取链上滑点配置
func (t *Trigger) GetOnChainSlippage() string {
	client := t.GetOnChainClient()
	if client == nil {
		return ""
	}
	swapInfo := client.GetSwapInfo()
	if swapInfo == nil {
		return ""
	}
	return swapInfo.Slippage
}

// SetOnChainSlippage 设置链上滑点配置
func (t *Trigger) SetOnChainSlippage(slippage string) error {
	client := t.GetOnChainClient()
	if client == nil {
		t.logger.Debugf("Trigger %s 无链上客户端，跳过 SetOnChainSlippage", t.symbol)
		return nil // 无链上时为 no-op，避免保存配置报错
	}
	client.UpdateSwapInfoSlippage(slippage)
	t.logger.Infof("Trigger %s 链上滑点已设置为: %s", t.symbol, slippage)
	return nil
}

// GetGasMultiplier 获取链上 gas 乘数配置
func (t *Trigger) GetGasMultiplier() float64 {
	client := t.GetOnChainClient()
	if client == nil {
		return 1.0
	}
	return client.GetGasMultiplier()
}

// SetGasMultiplier 设置链上 gas 乘数配置
func (t *Trigger) SetGasMultiplier(multiplier float64) error {
	client := t.GetOnChainClient()
	if client == nil {
		t.logger.Debugf("Trigger %s 无链上客户端，跳过 SetGasMultiplier", t.symbol)
		return nil
	}
	if multiplier <= 0 {
		multiplier = 1.0
	}
	client.SetGasMultiplier(multiplier)
	t.logger.Infof("Trigger %s Gas 乘数已设置为: %.2f", t.symbol, multiplier)
	return nil
}

// GetOnChainGasLimit 获取链上 GasLimit 配置
func (t *Trigger) GetOnChainGasLimit() string {
	client := t.GetOnChainClient()
	if client == nil {
		return ""
	}
	swapInfo := client.GetSwapInfo()
	if swapInfo == nil {
		return ""
	}
	return swapInfo.GasLimit
}

// SetOnChainGasLimit 设置链上 GasLimit 配置
func (t *Trigger) SetOnChainGasLimit(gasLimit string) error {
	client := t.GetOnChainClient()
	if client == nil {
		t.logger.Debugf("Trigger %s 无链上客户端，跳过 SetOnChainGasLimit", t.symbol)
		return nil
	}
	client.UpdateSwapInfoGasLimit(gasLimit)
	t.logger.Infof("Trigger %s 链上 GasLimit 已设置为: %s", t.symbol, gasLimit)
	return nil
}

// GetTraderAType 获取 A 的类型（如 "onchain:56"）
// 优先使用存储的 traderAType，如果为空则从 onChainClient 推断
func (t *Trigger) GetTraderAType() string {
	// 优先使用存储的类型信息
	if t.traderAType != "" {
		return t.traderAType
	}
	// 向后兼容：从 sourceA 推断
	if t.sourceA != nil {
		if _, ok := t.sourceA.(trader.OnchainTrader); ok {
			chainId := t.GetChainId()
			if chainId == "" {
				chainId = "56" // 默认 BSC 链
			}
			return "onchain:" + chainId
		}
	}
	return ""
}

// GetTraderBType 获取 B 的类型（如 "binance:futures"）
// 优先使用存储的 traderBType，如果为空则从 sourceB 推断
func (t *Trigger) GetTraderBType() string {
	// 优先使用存储的类型信息
	if t.traderBType != "" {
		return t.traderBType
	}
	// 向后兼容：从 sourceB 推断
	if t.sourceB != nil {
		traderType := t.sourceB.GetType()
		// 如果已经是完整格式（如 "binance:futures"），直接返回
		if strings.Contains(traderType, ":") {
			return traderType
		}
		// 否则添加默认市场类型
		marketType := "futures"
		return traderType + ":" + marketType
	}
	return ""
}

// GetChainId 获取链ID
func (t *Trigger) GetChainId() string {
	if t.onChainData != nil && t.onChainData.ChainIndex != "" {
		return t.onChainData.ChainIndex
	}
	return "56" // 默认 BSC 链
}

// GetExchangeType 获取交易所类型（如 "binance"）
func (t *Trigger) GetExchangeType() string {
	if t.sourceB != nil {
		traderType := t.sourceB.GetType()
		// 如果包含冒号，提取交易所类型部分
		if idx := strings.Index(traderType, ":"); idx > 0 {
			return traderType[:idx]
		}
		return traderType
	}
	return ""
}

// SetTraderTypes 设置类型信息
func (t *Trigger) SetTraderTypes(traderAType, traderBType string) {
	t.traderAType = traderAType
	t.traderBType = traderBType
}

// SetChainId 设置链ID（在 onChainData.ChainIndex 中）
func (t *Trigger) SetChainId(chainId string) {
	if t.onChainData == nil {
		t.onChainData = &OnChainData{
			BuyTx:      "",
			SellTx:     "",
			ChainIndex: chainId,
		}
	} else {
		t.onChainData.ChainIndex = chainId
	}
}

