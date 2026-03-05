package analytics

import (
	"errors"
	"sync"
	"time"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/utils/logger"

	"go.uber.org/zap"
)

const maxPriceDiffsArraySize = 600

// SlippageData 滑点数据结构
type SlippageData struct {
	ExchangeBuy  float64 // 交易所买入滑点（百分比）（已废弃，使用 ABuy/BBuy）
	ExchangeSell float64 // 交易所卖出滑点（百分比）（已废弃，使用 ASell/BSell）
	OnChainBuy   float64 // 链上买入滑点（百分比）（已废弃，使用 ABuy/BBuy）
	OnChainSell  float64 // 链上卖出滑点（百分比）（已废弃，使用 ASell/BSell）

	// A 和 B 的滑点（新的统一字段）
	ABuy  float64 // A 买入滑点（百分比）
	ASell float64 // A 卖出滑点（百分比）
	BBuy  float64 // B 买入滑点（百分比）
	BSell float64 // B 卖出滑点（百分比）

	// MaxSize 数据（平滑后的）
	ExchangeBuyMaxSize  float64 // 交易所买入最大 size（平滑后）
	ExchangeSellMaxSize float64 // 交易所卖出最大 size（平滑后）
}

// ThresholdIntervalConfig 价差阈值区间配置
type ThresholdIntervalConfig struct {
	mu           sync.RWMutex
	minThreshold float64 // 最小阈值（优化出的阈值必须 >= 此值）
	maxThreshold float64 // 最大阈值（优化出的阈值必须 <= 此值）
}

// GetTargetThresholdInterval 获取目标价差阈值区间（向后兼容，返回 minThreshold）
// Deprecated: 请使用 GetMinThreshold 和 GetMaxThreshold
func (c *ThresholdIntervalConfig) GetTargetThresholdInterval() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.minThreshold
}

// SetTargetThresholdInterval 设置目标价差阈值区间（向后兼容，设置 minThreshold）
// Deprecated: 请使用 SetMinThreshold 和 SetMaxThreshold
func (c *ThresholdIntervalConfig) SetTargetThresholdInterval(targetThresholdInterval float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.minThreshold = targetThresholdInterval
}

// GetMinThreshold 获取最小阈值
func (c *ThresholdIntervalConfig) GetMinThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.minThreshold
}

// SetMinThreshold 设置最小阈值
func (c *ThresholdIntervalConfig) SetMinThreshold(minThreshold float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.minThreshold = minThreshold
}

// GetMaxThreshold 获取最大阈值
func (c *ThresholdIntervalConfig) GetMaxThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.maxThreshold
}

// SetMaxThreshold 设置最大阈值
func (c *ThresholdIntervalConfig) SetMaxThreshold(maxThreshold float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxThreshold = maxThreshold
}

// GetThresholdRange 获取阈值区间
func (c *ThresholdIntervalConfig) GetThresholdRange() (minThreshold, maxThreshold float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.minThreshold, c.maxThreshold
}

// SetThresholdRange 设置阈值区间
func (c *ThresholdIntervalConfig) SetThresholdRange(minThreshold, maxThreshold float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.minThreshold = minThreshold
	c.maxThreshold = maxThreshold
}

// Analyzer 分析器接口
type Analyzer interface {
	OnPriceDiff(priceDiff *model.DiffResult)
	GetThreshold() (thresholdAB, thresholdBA float64, err error)
	GetSize() (float64, error)
	GetOptimalThresholds() *OptimalThresholds
	CalculateSlippage(t trader.Trader, symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64)
	CalculateOnchainSlippage(onchainTrader trader.OnchainTrader, side model.OrderSide) float64
	GetSlippageData() *SlippageData
	GetMaxSize(direction string) float64
	CalculateCost(direction string, size float64, exchangePrice, onchainPrice float64) *CostData
	CalculateCostForDirection(direction string, size float64, exchangePriceData, onchainPriceData *model.PriceData) *CostData
	GetCost(direction string, size float64, exchangePrice, onchainPrice float64) *CostData
}

// Analytics 分析器实现
type Analytics struct {
	currentSymbol string

	// 价差阈值区间配置
	thresholdIntervalConfig *ThresholdIntervalConfig

	// 价差统计信息
	minDiffAB float64
	minDiffBA float64
	maxDiffAB float64
	maxDiffBA float64

	// 当前价差
	AAsk float64
	ABid float64
	BAsk float64
	BBid float64

	// 价差数据（旧格式，保留兼容性）
	priceDiffs        [][]float64
	priceDiffsMu      sync.RWMutex // 保护 priceDiffs 的读写
	optimalThresholds *OptimalThresholds
	logger            *zap.SugaredLogger

	// 带时间戳的价差数据（用于快速触发优化器）
	spreadDataPoints   []SpreadDataPoint
	spreadDataPointsMu sync.RWMutex

	// 当前动态成本（百分比形式，由外部动态设置）
	// AB: +A-B 方向（A买入 + B卖出）
	// BA: -A+B 方向（A卖出 + B买入）
	currentCostAB float64
	currentCostBA float64
	currentCostMu sync.RWMutex

	// 快速触发阈值优化器
	fastTriggerOptimizer *FastTriggerThresholdOptimizer

	// 滑点数据（使用结构体封装）
	slippageData *SlippageData
	slippageMu   sync.RWMutex

	// 用户配置的 maxSize（Web 设置的 triggerABSize/triggerBASize）；>0 时 GetMaxSize 优先返回此值，CalculateSlippage 不覆盖
	configuredMaxSizeAB float64
	configuredMaxSizeBA float64

	// MaxSize 平滑参数（指数移动平均的 alpha 值，0-1之间，越大越敏感）
	maxSizeSmoothingAlpha float64

	// Telegram 通知回调函数（用于检查是否应该发送通知）
	shouldNotify func() bool

	// 最后成交时间（用于检测成交停滞，超过30秒无成交则重置阈值数据）
	lastTradeTime   time.Time
	lastTradeTimeMu sync.RWMutex

	// 阈值稳定性控制
	thresholdStabilityConfig *ThresholdStabilityConfig
	thresholdStabilityMu     sync.RWMutex
}

// NewAnalytics 创建新的分析器实例
// targetThresholdInterval: 固定阈值下限，优化出的阈值必须大于此值
func NewAnalytics(targetThresholdInterval, maxSize float64, symbol string) *Analytics {
	// 默认最大阈值为 0，表示不限制
	return NewAnalyticsWithRange(targetThresholdInterval, 0, maxSize, symbol)
}

// ThresholdStabilityConfig 阈值稳定性配置
type ThresholdStabilityConfig struct {
	// 平滑系数（0-1）：0.1 表示新阈值权重10%，旧阈值权重90%（更稳定）
	// 0.5 表示新阈值权重50%，旧阈值权重50%（中等稳定）
	// 1.0 表示完全使用新阈值（不稳定，不推荐）
	SmoothingAlpha float64

	// 最小变化比例：只有新阈值与旧阈值的差异超过此比例才更新
	// 例如 0.1 表示变化超过10%才更新
	MinChangeRatio float64

	// 最小变化间隔：两次阈值更新之间的最小时间间隔
	MinUpdateInterval time.Duration

	// 最小置信度：只有满足以下条件才更新阈值
	// - 成功率 >= MinSuccessRate
	// - 触发次数 >= MinTriggerCount
	MinSuccessRate  float64
	MinTriggerCount int

	// 最后更新时间
	LastUpdateTime time.Time
}

// NewThresholdStabilityConfig 创建默认的阈值稳定性配置
func NewThresholdStabilityConfig() *ThresholdStabilityConfig {
	return &ThresholdStabilityConfig{
		SmoothingAlpha:    0.2,                      // 默认20%新值，80%旧值（较稳定）
		MinChangeRatio:    0.15,                     // 变化超过15%才更新
		MinUpdateInterval: 10 * time.Second,         // 最小更新间隔10秒
		MinSuccessRate:    0.5,                      // 最小成功率50%
		MinTriggerCount:   5,                        // 最小触发次数5次
		LastUpdateTime:    time.Time{},              // 初始化为零值
	}
}

// NewAnalyticsWithRange 创建新的分析器实例（带阈值区间）
// minThreshold: 最小阈值，优化出的阈值必须 >= 此值
// maxThreshold: 最大阈值，优化出的阈值必须 <= 此值（0 表示不限制）
func NewAnalyticsWithRange(minThreshold, maxThreshold, maxSize float64, symbol string) *Analytics {
	a := &Analytics{
		currentSymbol: symbol,
		thresholdIntervalConfig: &ThresholdIntervalConfig{
			minThreshold: minThreshold,
			maxThreshold: maxThreshold,
		},
		priceDiffs: [][]float64{
			make([]float64, 0, maxPriceDiffsArraySize),
			make([]float64, 0, maxPriceDiffsArraySize),
		},
		spreadDataPoints: make([]SpreadDataPoint, 0, maxPriceDiffsArraySize),
		// 创建快速触发优化器，传入固定阈值下限
		fastTriggerOptimizer:     NewFastTriggerThresholdOptimizer(maxPriceDiffsArraySize, minThreshold),
		slippageData:             &SlippageData{ExchangeBuyMaxSize: maxSize, ExchangeSellMaxSize: maxSize}, // 使用配置的 size，否则 GetMaxSize 为 0
		maxSizeSmoothingAlpha:    0.3, // 默认平滑系数（30% 新值，70% 旧值）
		logger:                   logger.GetLoggerInstance().Named("Analytics").Sugar(),
		shouldNotify:             func() bool { return true }, // 默认允许通知
		thresholdStabilityConfig: NewThresholdStabilityConfig(),
	}
	// 为快速触发优化器设置 logger
	a.fastTriggerOptimizer.SetLogger(a.logger)
	return a
}

// SetNotificationCallback 设置通知回调函数
func (a *Analytics) SetNotificationCallback(callback func() bool) {
	a.shouldNotify = callback
}

// OnPriceDiff 输入价差
func (a *Analytics) OnPriceDiff(priceDiff *model.DiffResult) {
	now := time.Now()

	a.priceDiffsMu.Lock()
	if len(a.priceDiffs[0]) > maxPriceDiffsArraySize &&
		len(a.priceDiffs[1]) > maxPriceDiffsArraySize {
		a.priceDiffs[0] = a.priceDiffs[0][1:]
		a.priceDiffs[1] = a.priceDiffs[1][1:]
	}

	a.AAsk = priceDiff.AAsk
	a.BAsk = priceDiff.BAsk
	a.ABid = priceDiff.ABid
	a.BBid = priceDiff.BBid

	a.priceDiffs[0] = append(a.priceDiffs[0], priceDiff.DiffAB)
	a.priceDiffs[1] = append(a.priceDiffs[1], priceDiff.DiffBA)
	abLen := len(a.priceDiffs[0])
	baLen := len(a.priceDiffs[1])
	a.priceDiffsMu.Unlock()

	// 同时存储带时间戳的数据（用于快速触发优化器）
	a.spreadDataPointsMu.Lock()
	if len(a.spreadDataPoints) >= maxPriceDiffsArraySize {
		a.spreadDataPoints = a.spreadDataPoints[1:]
	}
	// 使用对应方向的动态成本
	a.currentCostMu.RLock()
	costAB := a.currentCostAB // +A-B 方向：A买入 + B卖出
	costBA := a.currentCostBA // -A+B 方向：A卖出 + B买入
	a.currentCostMu.RUnlock()
	a.spreadDataPoints = append(a.spreadDataPoints, SpreadDataPoint{
		Timestamp:   now,
		SpreadLong:  priceDiff.DiffAB, // +A-B
		SpreadShort: priceDiff.DiffBA, // -A+B
		CostLong:    costAB,           // +A-B 方向成本
		CostShort:   costBA,           // -A+B 方向成本
	})
	a.spreadDataPointsMu.Unlock()

	a.logger.Debugf("价差数据已添加 - AB数组长度: %d, BA数组长度: %d, 成本AB: %.6f, 成本BA: %.6f", abLen, baLen, costAB, costBA)
}

// UpdateLastTradeTime 更新最后成交时间（成交时调用）
func (a *Analytics) UpdateLastTradeTime() {
	a.lastTradeTimeMu.Lock()
	defer a.lastTradeTimeMu.Unlock()
	a.lastTradeTime = time.Now()
	a.logger.Debugf("最后成交时间已更新: %v", a.lastTradeTime)
}

// GetLastTradeTime 获取最后成交时间
func (a *Analytics) GetLastTradeTime() time.Time {
	a.lastTradeTimeMu.RLock()
	defer a.lastTradeTimeMu.RUnlock()
	return a.lastTradeTime
}

// ClearPriceDiffs 清空历史价差数据
func (a *Analytics) ClearPriceDiffs() {
	a.priceDiffsMu.Lock()
	abLen := len(a.priceDiffs[0])
	baLen := len(a.priceDiffs[1])

	// 清空价差数据
	a.priceDiffs[0] = a.priceDiffs[0][:0]
	a.priceDiffs[1] = a.priceDiffs[1][:0]

	// 重置统计信息
	a.minDiffAB = 0
	a.minDiffBA = 0
	a.maxDiffAB = 0
	a.maxDiffBA = 0

	// 清空最优阈值（因为它是基于历史数据计算的）
	a.optimalThresholds = nil
	a.priceDiffsMu.Unlock()

	// 清空带时间戳的价差数据
	a.spreadDataPointsMu.Lock()
	spreadDataLen := len(a.spreadDataPoints)
	a.spreadDataPoints = a.spreadDataPoints[:0]
	a.spreadDataPointsMu.Unlock()

	a.logger.Infof("已清空历史价差数据 - AB: %d 条, BA: %d 条, 时间戳数据: %d 条，最优阈值已重置", abLen, baLen, spreadDataLen)
}

// CalculateOptimalThresholds 计算最优阈值
func (a *Analytics) CalculateOptimalThresholds() {
	a.updateStats()
	a.analyzeOptimalThresholds()
}

// GetThreshold 获取价差阈值
func (a *Analytics) GetThreshold() (thresholdAB, thresholdBA float64, err error) {
	if a.optimalThresholds == nil {
		return 0, 0, errors.New("optimalThresholds is not ready")
	}
	thresholdAB = a.optimalThresholds.ThresholdAB
	thresholdBA = a.optimalThresholds.ThresholdBA
	a.logger.Debugf("获取价差阈值 - AB阈值: %.6f, BA阈值: %.6f", thresholdAB, thresholdBA)
	return thresholdAB, thresholdBA, nil
}

// GetSize 获取交易单次size
func (a *Analytics) GetSize() (float64, error) {
	if a.optimalThresholds == nil {
		a.logger.Warn("最优阈值未找到，返回默认size 0")
		return 0, errors.New("optimalThresholds is not ready")
	}
	size := float64(a.optimalThresholds.TotalTrades)
	a.logger.Debugf("获取交易size - Size: %.0f (总触发次数: %d)",
		size, a.optimalThresholds.TotalTrades)
	return size, nil
}

// GetOptimalThresholds 获取最优阈值结果
func (a *Analytics) GetOptimalThresholds() *OptimalThresholds {
	return a.optimalThresholds
}

// GetSlippageData 获取滑点数据
func (a *Analytics) GetSlippageData() *SlippageData {
	a.slippageMu.RLock()
	defer a.slippageMu.RUnlock()

	// 返回副本，避免外部修改
	return &SlippageData{
		ExchangeBuy:         a.slippageData.ExchangeBuy,
		ExchangeSell:        a.slippageData.ExchangeSell,
		OnChainBuy:          a.slippageData.OnChainBuy,
		OnChainSell:         a.slippageData.OnChainSell,
		ABuy:                a.slippageData.ABuy,
		ASell:               a.slippageData.ASell,
		BBuy:                a.slippageData.BBuy,
		BSell:               a.slippageData.BSell,
		ExchangeBuyMaxSize:  a.slippageData.ExchangeBuyMaxSize,
		ExchangeSellMaxSize: a.slippageData.ExchangeSellMaxSize,
	}
}

// SetSlippageForSource 设置指定源的滑点（用于统一设置 A 或 B 的滑点）
// source: "A" 或 "B"
// side: model.OrderSideBuy 或 model.OrderSideSell
// slippage: 滑点百分比
func (a *Analytics) SetSlippageForSource(source string, side model.OrderSide, slippage float64) {
	a.slippageMu.Lock()
	defer a.slippageMu.Unlock()

	if source == "A" {
		if side == model.OrderSideBuy {
			a.slippageData.ABuy = slippage
		} else {
			a.slippageData.ASell = slippage
		}
	} else if source == "B" {
		if side == model.OrderSideBuy {
			a.slippageData.BBuy = slippage
		} else {
			a.slippageData.BSell = slippage
		}
	}
}

// GetMaxSize 获取最大 size；若用户已通过 SetMaxSize 配置则优先返回配置值，否则返回滑点/订单簿平滑结果
// direction: "AB" 表示 +A-B（链上买入，交易所卖出），"BA" 表示 -A+B（链上卖出，交易所买入）
func (a *Analytics) GetMaxSize(direction string) float64 {
	a.slippageMu.RLock()
	defer a.slippageMu.RUnlock()

	if direction == "AB" {
		if a.configuredMaxSizeAB > 0 {
			return a.configuredMaxSizeAB
		}
		return a.slippageData.ExchangeSellMaxSize
	}
	if direction == "BA" {
		if a.configuredMaxSizeBA > 0 {
			return a.configuredMaxSizeBA
		}
		return a.slippageData.ExchangeBuyMaxSize
	}
	return 0
}

// SetMaxSize 设置指定方向的最大 size（与 Web 的 triggerABSize/triggerBASize 同步，保证 swapInfo.Amount 不被订单簿结果覆盖）
// 设置后 GetMaxSize 优先返回此值，且 CalculateSlippage 不会用订单簿结果覆盖
// direction: "AB" 或 "BA"；size > 0 时生效
func (a *Analytics) SetMaxSize(direction string, size float64) {
	if size <= 0 {
		return
	}
	a.slippageMu.Lock()
	defer a.slippageMu.Unlock()
	if direction == "AB" {
		a.configuredMaxSizeAB = size
		a.slippageData.ExchangeSellMaxSize = size
	} else if direction == "BA" {
		a.configuredMaxSizeBA = size
		a.slippageData.ExchangeBuyMaxSize = size
	}
}

// GetTargetThresholdInterval 获取目标价差阈值区间
func (a *Analytics) GetTargetThresholdInterval() float64 {
	if a.thresholdIntervalConfig == nil {
		return 0
	}
	return a.thresholdIntervalConfig.GetTargetThresholdInterval()
}

// SetTargetThresholdInterval 设置目标价差阈值区间（固定阈值下限）
// 同时更新快速触发优化器的 MinThreshold
func (a *Analytics) SetTargetThresholdInterval(targetThresholdInterval float64) {
	a.SetMinThreshold(targetThresholdInterval)
}

// GetMinThreshold 获取最小阈值
func (a *Analytics) GetMinThreshold() float64 {
	if a.thresholdIntervalConfig == nil {
		return 0
	}
	return a.thresholdIntervalConfig.GetMinThreshold()
}

// SetMinThreshold 设置最小阈值
func (a *Analytics) SetMinThreshold(minThreshold float64) {
	if a.thresholdIntervalConfig == nil {
		a.thresholdIntervalConfig = &ThresholdIntervalConfig{}
	}
	oldValue := a.thresholdIntervalConfig.GetTargetThresholdInterval()
	a.thresholdIntervalConfig.SetTargetThresholdInterval(minThreshold)

	// 同步更新快速触发优化器的 MinThreshold
	if a.fastTriggerOptimizer != nil {
		a.fastTriggerOptimizer.SetMinThreshold(minThreshold)
	}

	a.logger.Infof("最小阈值已更新: %.6f -> %.6f (同步更新优化器 MinThreshold)", oldValue, minThreshold)
}

// GetMaxThreshold 获取最大阈值
func (a *Analytics) GetMaxThreshold() float64 {
	if a.thresholdIntervalConfig == nil {
		return 0
	}
	return a.thresholdIntervalConfig.GetMaxThreshold()
}

// SetMaxThreshold 设置最大阈值
// 同时更新快速触发优化器的 MaxThreshold
func (a *Analytics) SetMaxThreshold(maxThreshold float64) {
	if a.thresholdIntervalConfig == nil {
		a.thresholdIntervalConfig = &ThresholdIntervalConfig{}
	}
	oldValue := a.thresholdIntervalConfig.GetMaxThreshold()
	a.thresholdIntervalConfig.SetMaxThreshold(maxThreshold)

	// 同步更新快速触发优化器的 MaxThreshold
	if a.fastTriggerOptimizer != nil {
		a.fastTriggerOptimizer.SetMaxThreshold(maxThreshold)
	}

	a.logger.Infof("最大阈值已更新: %.6f -> %.6f (同步更新优化器 MaxThreshold)", oldValue, maxThreshold)
}

// GetFastTriggerOptimizer 获取快速触发优化器
func (a *Analytics) GetFastTriggerOptimizer() *FastTriggerThresholdOptimizer {
	return a.fastTriggerOptimizer
}

// SetThresholdStabilityConfig 设置阈值稳定性配置
func (a *Analytics) SetThresholdStabilityConfig(config *ThresholdStabilityConfig) {
	a.thresholdStabilityMu.Lock()
	defer a.thresholdStabilityMu.Unlock()
	a.thresholdStabilityConfig = config
	a.logger.Infof("阈值稳定性配置已更新 - 平滑系数: %.2f, 最小变化比例: %.2f%%, 最小更新间隔: %v, 最小成功率: %.2f%%, 最小触发次数: %d",
		config.SmoothingAlpha, config.MinChangeRatio*100, config.MinUpdateInterval,
		config.MinSuccessRate*100, config.MinTriggerCount)
}

// GetThresholdStabilityConfig 获取阈值稳定性配置
func (a *Analytics) GetThresholdStabilityConfig() *ThresholdStabilityConfig {
	a.thresholdStabilityMu.RLock()
	defer a.thresholdStabilityMu.RUnlock()
	return a.thresholdStabilityConfig
}

// SetThresholdSmoothingAlpha 设置阈值平滑系数（便捷方法）
// alpha: 0.0-1.0，越小越稳定（0.1 表示新值权重10%，旧值权重90%）
func (a *Analytics) SetThresholdSmoothingAlpha(alpha float64) {
	if alpha < 0 || alpha > 1 {
		a.logger.Warnf("平滑系数必须在 0-1 之间，当前值: %.2f，已忽略", alpha)
		return
	}
	a.thresholdStabilityMu.Lock()
	defer a.thresholdStabilityMu.Unlock()
	if a.thresholdStabilityConfig == nil {
		a.thresholdStabilityConfig = NewThresholdStabilityConfig()
	}
	a.thresholdStabilityConfig.SmoothingAlpha = alpha
	a.logger.Infof("阈值平滑系数已设置为: %.2f (新值权重 %.2f%%, 旧值权重 %.2f%%)",
		alpha, alpha*100, (1-alpha)*100)
}

// SetThresholdMinChangeRatio 设置最小变化比例（便捷方法）
// ratio: 0.0-1.0，例如 0.15 表示变化超过15%才更新
func (a *Analytics) SetThresholdMinChangeRatio(ratio float64) {
	if ratio < 0 || ratio > 1 {
		a.logger.Warnf("最小变化比例必须在 0-1 之间，当前值: %.2f，已忽略", ratio)
		return
	}
	a.thresholdStabilityMu.Lock()
	defer a.thresholdStabilityMu.Unlock()
	if a.thresholdStabilityConfig == nil {
		a.thresholdStabilityConfig = NewThresholdStabilityConfig()
	}
	a.thresholdStabilityConfig.MinChangeRatio = ratio
	a.logger.Infof("阈值最小变化比例已设置为: %.2f%%", ratio*100)
}

// SetThresholdMinUpdateInterval 设置最小更新间隔（便捷方法）
func (a *Analytics) SetThresholdMinUpdateInterval(interval time.Duration) {
	if interval < 0 {
		a.logger.Warnf("最小更新间隔不能为负，当前值: %v，已忽略", interval)
		return
	}
	a.thresholdStabilityMu.Lock()
	defer a.thresholdStabilityMu.Unlock()
	if a.thresholdStabilityConfig == nil {
		a.thresholdStabilityConfig = NewThresholdStabilityConfig()
	}
	a.thresholdStabilityConfig.MinUpdateInterval = interval
	a.logger.Infof("阈值最小更新间隔已设置为: %v", interval)
}

// GetSpreadDataPoints 获取带时间戳的价差数据副本
func (a *Analytics) GetSpreadDataPoints() []SpreadDataPoint {
	a.spreadDataPointsMu.RLock()
	defer a.spreadDataPointsMu.RUnlock()

	result := make([]SpreadDataPoint, len(a.spreadDataPoints))
	copy(result, a.spreadDataPoints)
	return result
}

// GetSpreadDataPointsCount 获取带时间戳的价差数据数量
func (a *Analytics) GetSpreadDataPointsCount() int {
	a.spreadDataPointsMu.RLock()
	defer a.spreadDataPointsMu.RUnlock()
	return len(a.spreadDataPoints)
}

// SetCurrentCost 设置指定方向的动态成本（百分比形式，如 0.15 表示 0.15%）
// direction: "AB" 表示 +A-B（A买入 + B卖出），"BA" 表示 -A+B（A卖出 + B买入）
// 此成本用于快速触发优化器判断净利润
func (a *Analytics) SetCurrentCost(direction string, cost float64) {
	a.currentCostMu.Lock()
	defer a.currentCostMu.Unlock()
	if direction == "AB" {
		a.currentCostAB = cost
	} else if direction == "BA" {
		a.currentCostBA = cost
	}
}

// GetCurrentCost 获取指定方向的动态成本
// direction: "AB" 表示 +A-B，"BA" 表示 -A+B
func (a *Analytics) GetCurrentCost(direction string) float64 {
	a.currentCostMu.RLock()
	defer a.currentCostMu.RUnlock()
	if direction == "AB" {
		return a.currentCostAB
	} else if direction == "BA" {
		return a.currentCostBA
	}
	return 0
}

// SetCurrentCosts 同时设置两个方向的动态成本
// costAB: +A-B 方向成本（A买入 + B卖出）
// costBA: -A+B 方向成本（A卖出 + B买入）
func (a *Analytics) SetCurrentCosts(costAB, costBA float64) {
	a.currentCostMu.Lock()
	defer a.currentCostMu.Unlock()
	a.currentCostAB = costAB
	a.currentCostBA = costBA
}

// GetCurrentCosts 获取两个方向的动态成本
// 返回: costAB (+A-B), costBA (-A+B)
func (a *Analytics) GetCurrentCosts() (costAB, costBA float64) {
	a.currentCostMu.RLock()
	defer a.currentCostMu.RUnlock()
	return a.currentCostAB, a.currentCostBA
}
