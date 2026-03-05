package analytics

import (
	"math"
	"sort"
	"time"

	"go.uber.org/zap"
)

// SpreadDataPoint 价差数据点（带时间戳）
type SpreadDataPoint struct {
	Timestamp   time.Time
	SpreadLong  float64 // +A-B: A买B卖的价差 (DiffAB)
	SpreadShort float64 // -A+B: A卖B买的价差 (DiffBA)
	CostLong    float64 // +A-B 方向的动态成本：A买入 + B卖出（百分比形式）
	CostShort   float64 // -A+B 方向的动态成本：A卖出 + B买入（百分比形式）
}

// FastTriggerResult 套利阈值优化结果
type FastTriggerResult struct {
	LongThreshold   float64       // +A-B 方向阈值
	ShortThreshold  float64       // -A+B 方向阈值
	TotalExecutions int           // 总成交次数（满足 spread >= threshold 且 spread - cost > 0）
	AvgSpread       float64       // 平均成交价差（所有成交点的平均价差）
	TotalProfit      float64       // 总利润（所有成交点的价差 - 成本之和）
}

// OpportunityWindow 机会窗口
type OpportunityWindow struct {
	StartTime time.Time
	EndTime   time.Time
	Points    []SpreadDataPoint
}

// FastTriggerThresholdOptimizer 套利阈值优化器
// 设计理念（重构后）：
// 1. 优化目标：闭环利润最大（只考虑闭环利润，不考虑单边）
// 2. 可执行性检查：只有 spread - cost > 0 的点才计入成交
// 3. 抗插针：使用稳健统计（IQR/截尾分位数）替代裸Min/Max，避免被极值影响
// 4. 套利闭环约束：LongThreshold + ShortThreshold >= max(MinRoundTripProfit, MinThreshold)
// 5. 套利闭环约束：LongThreshold + ShortThreshold <= min(MaxRoundTripProfit, MaxThreshold)（如果设置了）
// 6. 快速适应：检测到价差突增时，快速调整阈值范围
// 7. 支持正负、正正等多种阈值组合
type FastTriggerThresholdOptimizer struct {
	WindowSize         int           // 数据窗口大小
	MinRoundTripProfit float64       // 最小套利闭环利润：LongThreshold + ShortThreshold >= 此值
	MaxRoundTripProfit float64       // 最大套利闭环利润：LongThreshold + ShortThreshold <= 此值（0 表示不限制）
	MinThreshold       float64       // 最小闭环利润：LongThreshold + ShortThreshold >= 此值（用于约束两个阈值相加的结果）
	MaxThreshold       float64       // 最大闭环利润：LongThreshold + ShortThreshold <= 此值（用于约束两个阈值相加的结果，0 表示不限制）
	WeightDecay        float64       // 权重衰减系数：用于加权窗口，最近的数据权重更高（默认0.01）
	UseWeightedWindow  bool          // 是否使用加权窗口（默认true）
	ChangeDetectionThreshold float64 // 突变检测阈值：价差变化率超过此值认为发生突变（默认0.3）
	RecentDataWeight   float64       // 突变后数据权重：检测到突变后，最近数据的权重（默认0.9）
	OutlierPercentile  float64       // 异常值剔除分位数：使用 [OutlierPercentile, 1-OutlierPercentile] 范围（默认0.05，即5%-95%）
	logger             *zap.SugaredLogger // 日志记录器（可选）
}

// NewFastTriggerThresholdOptimizer 创建套利阈值优化器
// minRoundTripProfit: 最小套利闭环利润，即 LongThreshold + ShortThreshold >= 此值
// 例如：LongThreshold = 0.2, ShortThreshold = -0.1，则总利润 = 0.2 + (-0.1) = 0.1
func NewFastTriggerThresholdOptimizer(windowSize int, minRoundTripProfit float64) *FastTriggerThresholdOptimizer {
	return &FastTriggerThresholdOptimizer{
		WindowSize:         windowSize,
		MinRoundTripProfit: minRoundTripProfit,      // 最小套利闭环利润
		MaxRoundTripProfit: 0,                        // 默认不限制最大闭环利润
		MinThreshold:       -1e6,                     // 默认不限制最小闭环利润
		MaxThreshold:       0,                        // 默认不限制最大闭环利润
		WeightDecay:        0.01,                      // 权重衰减系数：默认0.01
		UseWeightedWindow:  true,                      // 默认使用加权窗口
		ChangeDetectionThreshold: 0.3,                // 突变检测阈值：默认0.3（30%）
		RecentDataWeight:   0.9,                       // 突变后数据权重：默认0.9
		OutlierPercentile:  0.05,                      // 异常值剔除：默认5%-95%范围
		logger:             nil,                       // 默认无日志记录器
	}
}

// SetLogger 设置日志记录器
func (opt *FastTriggerThresholdOptimizer) SetLogger(logger *zap.SugaredLogger) {
	opt.logger = logger
}

// logf 内部日志记录方法（如果 logger 存在则记录）
func (opt *FastTriggerThresholdOptimizer) logf(level string, format string, args ...interface{}) {
	if opt.logger == nil {
		return
	}
	switch level {
	case "debug":
		opt.logger.Debugf(format, args...)
	case "info":
		opt.logger.Infof(format, args...)
	case "warn":
		opt.logger.Warnf(format, args...)
	case "error":
		opt.logger.Errorf(format, args...)
	}
}

// CalculateOptimalThresholds 计算最优阈值（基于最快触发原则）
// 🔥 关键改进：分别为 Long 和 Short 方向独立优化阈值，避免阈值趋同
func (opt *FastTriggerThresholdOptimizer) CalculateOptimalThresholds(data []SpreadDataPoint) FastTriggerResult {
	result, _ := opt.CalculateOptimalThresholdsWithDebug(data)
	return result
}

// DebugInfo 调试信息
type DebugInfo struct {
	WindowSize              int        // 窗口大小
	DataPoints              int        // 数据点数量
	LongOpportunityCount    int        // Long 方向机会点数量（满足闭环利润且 SpreadLong > 0）
	ShortOpportunityCount   int        // Short 方向机会点数量（满足闭环利润且 SpreadShort > 0）
	LongSpreadRange         [2]float64 // Long 方向价差范围 [min, max]
	ShortSpreadRange        [2]float64 // Short 方向价差范围 [min, max]
	LongCandidatesCount     int        // Long 方向候选阈值数量
	ShortCandidatesCount    int        // Short 方向候选阈值数量
	LongCandidatesMin        float64    // Long 方向候选阈值最小值
	LongCandidatesMax        float64    // Long 方向候选阈值最大值
	ShortCandidatesMin       float64    // Short 方向候选阈值最小值
	ShortCandidatesMax       float64    // Short 方向候选阈值最大值
	LongWindowsCount        int        // Long 方向机会窗口数量
	ShortWindowsCount       int        // Short 方向机会窗口数量
	IsDefaultLongThreshold  bool       // 是否使用了默认 Long 阈值
	IsDefaultShortThreshold bool       // 是否使用了默认 Short 阈值
}

// CalculateOptimalThresholdsWithDebug 计算最优阈值并返回调试信息
func (opt *FastTriggerThresholdOptimizer) CalculateOptimalThresholdsWithDebug(data []SpreadDataPoint) (FastTriggerResult, DebugInfo) {
	opt.logf("info", "[快速阈值计算] 开始计算最优阈值 - 总数据点: %d, 窗口大小: %d, 最小闭环利润: %.6f",
		len(data), opt.WindowSize, opt.MinRoundTripProfit)

	debug := DebugInfo{
		WindowSize: opt.WindowSize,
		DataPoints: len(data),
	}

	// 降低数据点要求：至少需要 50 个数据点才能进行有效计算（而不是 WindowSize/2）
	minRequiredDataPoints := 50
	if opt.WindowSize/4 < minRequiredDataPoints {
		minRequiredDataPoints = opt.WindowSize / 4 // 至少窗口大小的 1/4
	}
	if len(data) < minRequiredDataPoints {
		opt.logf("warn", "[快速阈值计算] 数据点不足，跳过计算 - 数据点: %d, 需要: %d", len(data), minRequiredDataPoints)
		return FastTriggerResult{LongThreshold: 0, ShortThreshold: 0}, debug
	}

	// 使用最近的窗口数据
	windowStart := maxInt(0, len(data)-opt.WindowSize)
	dataWindow := data[windowStart:]
	debug.DataPoints = len(dataWindow)

	opt.logf("info", "[快速阈值计算] 使用窗口数据 - 窗口起始: %d, 窗口数据点: %d", windowStart, len(dataWindow))

	// 只统计闭环利润，不统计单边数据
	
	// 显示前5个数据点的闭环利润信息
	sampleCount := 5
	if len(dataWindow) < sampleCount {
		sampleCount = len(dataWindow)
	}
	opt.logf("info", "[快速阈值计算] 数据样本 (前%d个) -", sampleCount)
	for i := 0; i < sampleCount; i++ {
		d := dataWindow[i]
		roundTrip := d.SpreadLong + d.SpreadShort
		roundTripNet := roundTrip - (d.CostLong + d.CostShort)
		opt.logf("info", "[快速阈值计算]    [%d] 闭环利润=%.6f (成本=%.6f, 净=%.6f)",
			i, roundTrip, d.CostLong+d.CostShort, roundTripNet)
	}

	// 只统计闭环利润机会点，不统计单边机会点
	var roundTripOpportunityCount int
	for _, d := range dataWindow {
		roundTripProfit := d.SpreadLong + d.SpreadShort
		if roundTripProfit >= opt.MinRoundTripProfit {
			roundTripOpportunityCount++
		}
	}
	debug.LongOpportunityCount = roundTripOpportunityCount
	debug.ShortOpportunityCount = roundTripOpportunityCount

	// 统计闭环利润分布
	var roundTripProfits []float64
	var positiveRoundTripCount int
	for _, d := range dataWindow {
		roundTrip := d.SpreadLong + d.SpreadShort
		roundTripProfits = append(roundTripProfits, roundTrip)
		if roundTrip > 0 {
			positiveRoundTripCount++
		}
	}
	sort.Float64s(roundTripProfits)
	roundTripMedian := roundTripProfits[len(roundTripProfits)/2]
	roundTripMin := roundTripProfits[0]
	roundTripMax := roundTripProfits[len(roundTripProfits)-1]
	
	opt.logf("info", "[快速阈值计算] 闭环利润统计 - 正数点: %d/%d, 范围: [%.6f, %.6f], 中位数: %.6f, MinRoundTripProfit要求: %.6f, 满足要求的机会点: %d",
		positiveRoundTripCount, len(dataWindow), roundTripMin, roundTripMax, roundTripMedian, opt.MinRoundTripProfit, roundTripOpportunityCount)

	// 分别获取两个方向的价差范围
	longRange := opt.getSpreadRangeForDirection(dataWindow, true)
	shortRange := opt.getSpreadRangeForDirection(dataWindow, false)

	debug.LongSpreadRange = [2]float64{longRange.Min, longRange.Max}
	debug.ShortSpreadRange = [2]float64{shortRange.Min, shortRange.Max}

	opt.logf("info", "[快速阈值计算] 价差范围 - Long: [%.6f, %.6f], Short: [%.6f, %.6f]",
		longRange.Min, longRange.Max, shortRange.Min, shortRange.Max)

	// 分别为两个方向生成候选阈值
	longCandidates := opt.generateCandidatesForRange(longRange)
	shortCandidates := opt.generateCandidatesForRange(shortRange)
	debug.LongCandidatesCount = len(longCandidates)
	debug.ShortCandidatesCount = len(shortCandidates)
	
	// 🔥 调试：记录候选阈值范围（用于诊断）
	if len(longCandidates) > 0 {
		debug.LongCandidatesMin = longCandidates[0]
		debug.LongCandidatesMax = longCandidates[len(longCandidates)-1]
	}
	if len(shortCandidates) > 0 {
		debug.ShortCandidatesMin = shortCandidates[0]
		debug.ShortCandidatesMax = shortCandidates[len(shortCandidates)-1]
	}

	opt.logf("info", "[快速阈值计算] 候选阈值生成 - Long候选数: %d [%.6f, %.6f], Short候选数: %d [%.6f, %.6f]",
		len(longCandidates), debug.LongCandidatesMin, debug.LongCandidatesMax,
		len(shortCandidates), debug.ShortCandidatesMin, debug.ShortCandidatesMax)

	// 🔥 只检查闭环利润机会，不检查单边机会窗口
	hasRoundTripOpportunity := roundTripOpportunityCount > 0
	debug.LongWindowsCount = 0
	debug.ShortWindowsCount = 0
	if hasRoundTripOpportunity {
		debug.LongWindowsCount = 1
		debug.ShortWindowsCount = 1
	}

	opt.logf("info", "[快速阈值计算] 闭环利润机会 - 满足要求的机会点: %d/%d", roundTripOpportunityCount, len(dataWindow))

	// 🔥 联合优化两个方向的阈值，确保 LongThreshold + ShortThreshold >= MinRoundTripProfit
	bestLongThresh, bestShortThresh, combinedMetrics := opt.findBestThresholdPair(
		dataWindow, longCandidates, shortCandidates, hasRoundTripOpportunity,
	)

	// 检查是否使用了默认阈值
	defaultLongThreshold := opt.calculateDefaultThreshold(longRange)
	defaultShortThreshold := opt.calculateDefaultThreshold(shortRange)
	debug.IsDefaultLongThreshold = bestLongThresh == defaultLongThreshold
	debug.IsDefaultShortThreshold = bestShortThresh == defaultShortThreshold

	opt.logf("info", "[快速阈值计算] 计算完成 - Long阈值: %.6f (默认: %.6f, 使用默认: %v), Short阈值: %.6f (默认: %.6f, 使用默认: %v), 闭环利润: %.6f",
		bestLongThresh, defaultLongThreshold, debug.IsDefaultLongThreshold,
		bestShortThresh, defaultShortThreshold, debug.IsDefaultShortThreshold,
		bestLongThresh+bestShortThresh)
	opt.logf("info", "[快速阈值计算] 成交指标 - 总成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
		combinedMetrics.TotalExecutions, combinedMetrics.AvgSpread, combinedMetrics.TotalProfit)

	return FastTriggerResult{
		LongThreshold:   bestLongThresh,
		ShortThreshold:  bestShortThresh,
		TotalExecutions: combinedMetrics.TotalExecutions,
		AvgSpread:       combinedMetrics.AvgSpread,
		TotalProfit:     combinedMetrics.TotalProfit,
	}, debug
}

// countExceedsWithCooldown 计算价差超过阈值的次数，带冷却期（传统算法校验）
// 确保候选阈值在历史数据中曾被实际超过，避免输出“从未达到过”的阈值
const legacyCooldown = 5

func (opt *FastTriggerThresholdOptimizer) countExceedsWithCooldown(data []SpreadDataPoint, isLong bool, threshold float64, cooldown int) int {
	if len(data) == 0 {
		return 0
	}
	triggerCount := 0
	lastTriggerIdx := -cooldown - 1
	for i := range data {
		var spread float64
		if isLong {
			spread = data[i].SpreadLong
		} else {
			spread = data[i].SpreadShort
		}
		// 达到或超过阈值即计为一次触发（>=），与 findFirstTriggerTimeForDirection 一致
		if spread >= threshold && i-lastTriggerIdx > cooldown {
			triggerCount++
			lastTriggerIdx = i
		}
	}
	return triggerCount
}

// executionMetrics 成交指标
type executionMetrics struct {
	TotalExecutions int     // 总成交次数
	AvgSpread       float64 // 平均成交价差
	TotalProfit     float64 // 总利润（价差 - 成本）
}

// thresholdCandidate 候选阈值及其成交频率
type thresholdCandidate struct {
	Threshold      float64 // 阈值
	ExecutionCount int     // 成交次数（满足 spread >= threshold 且 spread - cost > 0）
	AvgSpread      float64 // 平均成交价差
	TotalProfit    float64 // 总利润
}

// findBestThresholdPair 联合优化两个方向的阈值
// 优化策略：评估每个候选阈值组合的闭环利润
// 优化目标：闭环利润最大（在满足约束的前提下）
// 约束：
//   1. LongThreshold + ShortThreshold >= max(MinRoundTripProfit, MinThreshold)
//   2. LongThreshold + ShortThreshold <= min(MaxRoundTripProfit, MaxThreshold)（如果设置了）
//   3. 只有 spread >= threshold 且 spread - cost > 0 的点才计入成交
func (opt *FastTriggerThresholdOptimizer) findBestThresholdPair(
	data []SpreadDataPoint,
	longCandidates, shortCandidates []float64,
	hasRoundTripOpportunity bool,
) (float64, float64, executionMetrics) {
	opt.logf("info", "[快速阈值计算-优化] 开始联合优化 - Long候选数: %d, Short候选数: %d",
		len(longCandidates), len(shortCandidates))

	// 计算默认阈值
	longRange := opt.getSpreadRangeForDirection(data, true)
	shortRange := opt.getSpreadRangeForDirection(data, false)
	
	defaultLongThreshold := opt.calculateDefaultThreshold(longRange)
	defaultShortThreshold := opt.calculateDefaultThreshold(shortRange)

	if !hasRoundTripOpportunity {
		opt.logf("warn", "[快速阈值计算-优化] 无闭环利润机会，返回默认阈值 - Long: %.6f, Short: %.6f",
			defaultLongThreshold, defaultShortThreshold)
		return defaultLongThreshold, defaultShortThreshold, executionMetrics{0, 0, 0}
	}

	// 记录约束参数用于诊断
	opt.logf("info", "[快速阈值计算-优化] 约束参数 - MinRoundTripProfit: %.6f, MaxRoundTripProfit: %.6f, MinThreshold: %.6f, MaxThreshold: %.6f",
		opt.MinRoundTripProfit, opt.MaxRoundTripProfit, opt.MinThreshold, opt.MaxThreshold)

	// 🔥 第一步：评估所有 Long 候选阈值的成交情况（用于计算总成交次数和总利润）
	longCandidatesWithMetrics := make([]thresholdCandidate, 0, len(longCandidates))
	opt.logf("info", "[快速阈值计算-优化] 开始评估 Long 候选阈值 - 总候选数: %d", len(longCandidates))
	
	for _, thresh := range longCandidates {
		// 评估 Long 方向的成交情况（用于后续计算总成交次数和总利润）
		var execCount int
		var totalSpread, totalProfit float64
		var thresholdNotMet, costNotMet int
		for _, point := range data {
			if point.SpreadLong >= thresh {
				// 计算净收益（Long方向的价差-成本）
				netProfit := point.SpreadLong - point.CostLong
				
				// 计算Long方向的成交情况（用于后续计算总成交次数和总利润）
				// 注意：评分只基于闭环利润，这里只是统计成交次数和利润
				if netProfit > 0 {
					execCount++
					totalSpread += point.SpreadLong
					totalProfit += netProfit
				} else {
					costNotMet++
				}
			} else {
				thresholdNotMet++
			}
		}
		
		avgSpread := 0.0
		if execCount > 0 {
			avgSpread = totalSpread / float64(execCount)
		}

		longCandidatesWithMetrics = append(longCandidatesWithMetrics, thresholdCandidate{
			Threshold:      thresh,
			ExecutionCount: execCount,
			AvgSpread:      avgSpread,
			TotalProfit:    totalProfit,
		})
	}
	
	opt.logf("info", "[快速阈值计算-优化] Long候选评估完成 - 总候选: %d, 有效候选: %d",
		len(longCandidates), len(longCandidatesWithMetrics))

	// 🔥 第二步：评估所有 Short 候选阈值的成交情况（用于计算总成交次数和总利润）
	shortCandidatesWithMetrics := make([]thresholdCandidate, 0, len(shortCandidates))
	opt.logf("info", "[快速阈值计算-优化] 开始评估 Short 候选阈值 - 总候选数: %d", len(shortCandidates))
	
	for _, thresh := range shortCandidates {
		// 评估 Short 方向的成交情况（用于后续计算总成交次数和总利润）
		// 注意：对于负数价差，判断条件应该是价差 >= 阈值（负数阈值）
		// 例如：SpreadShort = -0.40, thresh = -0.60，那么 -0.40 >= -0.60 是true
		var execCount int
		var totalSpread, totalProfit float64
		var thresholdNotMet, costNotMet int
		for _, point := range data {
			// 检查是否满足阈值条件（对于负数，-0.40 >= -0.60 是true）
			if point.SpreadShort >= thresh {
				// 计算净收益（Short方向的价差-成本）
				netProfit := point.SpreadShort - point.CostShort
				
				// 计算Short方向的成交情况（用于后续计算总成交次数和总利润）
				// 注意：评分只基于闭环利润，这里只是统计成交次数和利润
				if netProfit > 0 {
					execCount++
					totalSpread += point.SpreadShort
					totalProfit += netProfit
				} else {
					costNotMet++
				}
			} else {
				thresholdNotMet++
			}
		}
		
		avgSpread := 0.0
		if execCount > 0 {
			avgSpread = totalSpread / float64(execCount)
		}

		shortCandidatesWithMetrics = append(shortCandidatesWithMetrics, thresholdCandidate{
			Threshold:      thresh,
			ExecutionCount: execCount,
			AvgSpread:      avgSpread,
			TotalProfit:    totalProfit,
		})
	}
	
	opt.logf("info", "[快速阈值计算-优化] Short候选评估完成 - 总候选: %d, 有效候选: %d",
		len(shortCandidates), len(shortCandidatesWithMetrics))

	opt.logf("info", "[快速阈值计算-优化] 候选评估完成 - Long有效候选: %d (原始: %d), Short有效候选: %d (原始: %d)",
		len(longCandidatesWithMetrics), len(longCandidates),
		len(shortCandidatesWithMetrics), len(shortCandidates))

	// 🔥 第三步：评估所有组合，找到闭环利润最大的组合
	bestScore := -1e9
	bestLongThresh := defaultLongThreshold
	bestShortThresh := defaultShortThreshold
	bestMetrics := executionMetrics{0, 0, 0}

	totalEvaluated := 0
	skippedConstraints := 0
	skippedZeroExecutions := 0

	// 遍历所有 Long 和 Short 候选组合
	skippedRoundTripProfit := 0
	skippedMaxRoundTripProfit := 0
	topCandidatesCount := 0
	const maxTopCandidates = 10 // 只记录前10个最优候选
	
	opt.logf("info", "[快速阈值计算-优化] 开始组合匹配 - Long有效候选: %d, Short有效候选: %d, 总组合数: %d",
		len(longCandidatesWithMetrics), len(shortCandidatesWithMetrics),
		len(longCandidatesWithMetrics)*len(shortCandidatesWithMetrics))
	
	for i, longCand := range longCandidatesWithMetrics {
		for j, shortCand := range shortCandidatesWithMetrics {
			totalEvaluated++

			// 约束1：闭环利润约束（使用 MinRoundTripProfit 和 MinThreshold 中较大的值）
			roundTripProfit := longCand.Threshold + shortCand.Threshold
			minRequiredProfit := opt.MinRoundTripProfit
			// 🔥 修正：MinThreshold 用于约束闭环利润（两个阈值相加）
			if opt.MinThreshold > -1e5 && opt.MinThreshold > minRequiredProfit {
				minRequiredProfit = opt.MinThreshold
			}
			if roundTripProfit < minRequiredProfit {
				skippedRoundTripProfit++
				if totalEvaluated <= 10 {
					opt.logf("info", "[快速阈值计算-优化] 组合[%d,%d] 被闭环利润过滤 - Long: %.6f, Short: %.6f, 闭环利润: %.6f < 要求: %.6f (MinRoundTripProfit: %.6f, MinThreshold: %.6f)",
						i, j, longCand.Threshold, shortCand.Threshold, roundTripProfit, minRequiredProfit, opt.MinRoundTripProfit, opt.MinThreshold)
				}
				skippedConstraints++
				continue
			}
			
			// 🔥 约束2：最大闭环利润约束（使用 MaxRoundTripProfit 和 MaxThreshold 中较小的值）
			maxAllowedProfit := opt.MaxRoundTripProfit
			// 🔥 修正：MaxThreshold 用于约束闭环利润（两个阈值相加）
			if opt.MaxThreshold > 0 {
				if maxAllowedProfit <= 0 || opt.MaxThreshold < maxAllowedProfit {
					maxAllowedProfit = opt.MaxThreshold
				}
			}
			if maxAllowedProfit > 0 && roundTripProfit > maxAllowedProfit {
				skippedMaxRoundTripProfit++
				if totalEvaluated <= 10 {
					opt.logf("debug", "[快速阈值计算-优化] 组合[%d,%d] 被最大闭环利润过滤 - Long: %.6f, Short: %.6f, 闭环利润: %.6f > 要求: %.6f (MaxRoundTripProfit: %.6f, MaxThreshold: %.6f)",
						i, j, longCand.Threshold, shortCand.Threshold, roundTripProfit, maxAllowedProfit, opt.MaxRoundTripProfit, opt.MaxThreshold)
				}
				skippedConstraints++
				continue
			}

			// 如果两个方向都是0成交，跳过（无法产生任何交易）
			if longCand.ExecutionCount == 0 && shortCand.ExecutionCount == 0 {
				skippedZeroExecutions++
				if totalEvaluated <= 10 {
					opt.logf("info", "[快速阈值计算-优化] 组合[%d,%d] 被零成交过滤 - Long: %.6f (成交: %d), Short: %.6f (成交: %d), 闭环利润: %.6f",
						i, j, longCand.Threshold, longCand.ExecutionCount, shortCand.Threshold, shortCand.ExecutionCount, roundTripProfit)
				}
				continue
			}

			// 计算组合指标
			totalExecutions := longCand.ExecutionCount + shortCand.ExecutionCount
			totalProfit := longCand.TotalProfit + shortCand.TotalProfit
			
			// 计算平均价差（加权平均）
			avgSpread := 0.0
			if totalExecutions > 0 {
				totalSpreadSum := float64(longCand.ExecutionCount)*longCand.AvgSpread + float64(shortCand.ExecutionCount)*shortCand.AvgSpread
				avgSpread = totalSpreadSum / float64(totalExecutions)
			}

			// 🔥 评分只基于闭环利润：闭环利润越高，评分越高
			// 归一化闭环利润（相对于数据中的最大闭环利润）
			maxRoundTripInData := 0.0
			for _, point := range data {
				rt := point.SpreadLong + point.SpreadShort
				if rt > maxRoundTripInData {
					maxRoundTripInData = rt
				}
			}
			normalizedRoundTrip := 0.0
			if maxRoundTripInData > 0 {
				normalizedRoundTrip = roundTripProfit / maxRoundTripInData
			}
			
			// 归一化成交次数
			normalizedCount := float64(totalExecutions) / float64(len(data))

			// 综合评分：闭环利润权重0.6 + 成交次数权重0.4
			score := normalizedRoundTrip*0.6 + normalizedCount*0.4

			// 详细记录所有有效组合（前20个）
			if totalEvaluated <= 20 || score > bestScore {
				opt.logf("info", "[快速阈值计算-优化] 组合[%d,%d] 详情 - Long: %.6f (成交: %d), Short: %.6f (成交: %d), 闭环利润: %.6f, 总成交: %d, 归一化闭环利润: %.4f, 归一化成交: %.4f, 评分: %.6f",
					i, j,
					longCand.Threshold, longCand.ExecutionCount,
					shortCand.Threshold, shortCand.ExecutionCount,
					roundTripProfit, totalExecutions, normalizedRoundTrip, normalizedCount, score)
			}

			if score > bestScore {
				topCandidatesCount++
				opt.logf("info", "[快速阈值计算-优化] ⭐ 发现更优组合[%d] - Long: %.6f (成交: %d), Short: %.6f (成交: %d), 闭环利润: %.6f, 总成交: %d, 评分: %.6f",
					topCandidatesCount,
					longCand.Threshold, longCand.ExecutionCount,
					shortCand.Threshold, shortCand.ExecutionCount,
					roundTripProfit, totalExecutions, score)
				bestScore = score
				bestLongThresh = longCand.Threshold
				bestShortThresh = shortCand.Threshold
				bestMetrics = executionMetrics{
					TotalExecutions: totalExecutions,
					AvgSpread:       avgSpread,
					TotalProfit:     totalProfit,
				}
			}
		}
	}

	opt.logf("info", "[快速阈值计算-优化] 优化完成 - 评估组合: %d, 跳过约束: %d (闭环利润不足: %d, 超过最大闭环利润: %d), 跳过零成交: %d, 最优评分: %.6f",
		totalEvaluated, skippedConstraints, skippedRoundTripProfit, skippedMaxRoundTripProfit, skippedZeroExecutions, bestScore)
	
	if bestScore > -1e8 {
		opt.logf("info", "[快速阈值计算-优化] 🎯 最优组合结果 - Long阈值: %.6f, Short阈值: %.6f, 闭环利润: %.6f, 总成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
			bestLongThresh, bestShortThresh, bestLongThresh+bestShortThresh,
			bestMetrics.TotalExecutions, bestMetrics.AvgSpread, bestMetrics.TotalProfit)
	} else {
		opt.logf("warn", "[快速阈值计算-优化] ⚠️ 未找到有效组合，使用默认阈值 - Long: %.6f, Short: %.6f",
			bestLongThresh, bestShortThresh)
	}

	return bestLongThresh, bestShortThresh, bestMetrics
}

// calculateDefaultThreshold 计算默认阈值
func (opt *FastTriggerThresholdOptimizer) calculateDefaultThreshold(sr spreadRange) float64 {
	if sr.Min < 0 && sr.Max > 0 {
		// 跨0范围：使用范围中心
		return (sr.Min + sr.Max) / 2
	} else if sr.Max <= 0 {
		// 全负数范围：使用最大值（最接近0的负数）
		return sr.Max
	} else {
		// 全正数范围：使用 MinRoundTripProfit * 0.5
		return opt.MinRoundTripProfit * 0.5
	}
}

// evaluateExecutionMetrics 评估成交指标（带成本检查）
func (opt *FastTriggerThresholdOptimizer) evaluateExecutionMetrics(
	data []SpreadDataPoint,
	longThresh, shortThresh float64,
	longWindows, shortWindows []OpportunityWindow,
) executionMetrics {
	var totalExecutions int
	var totalSpreadSum float64
	var totalProfit float64

	// 评估 Long 方向成交
	for _, point := range data {
		// 检查是否满足阈值且可成交（spread >= threshold 且 spread - cost > 0）
		if point.SpreadLong >= longThresh && (point.SpreadLong-point.CostLong) > 0 {
			totalExecutions++
			totalSpreadSum += point.SpreadLong
			totalProfit += point.SpreadLong - point.CostLong
		}
	}

	// 评估 Short 方向成交
	for _, point := range data {
		// 检查是否满足阈值且可成交
		if point.SpreadShort >= shortThresh && (point.SpreadShort-point.CostShort) > 0 {
			totalExecutions++
			totalSpreadSum += point.SpreadShort
			totalProfit += point.SpreadShort - point.CostShort
		}
	}

	avgSpread := 0.0
	if totalExecutions > 0 {
		avgSpread = totalSpreadSum / float64(totalExecutions)
	}

	return executionMetrics{
		TotalExecutions: totalExecutions,
		AvgSpread:       avgSpread,
		TotalProfit:     totalProfit,
	}
}

// getSpreadRangeForDirection 获取指定方向的价差范围（抗插针版本）
// 使用稳健统计：IQR/截尾分位数替代裸Min/Max，避免被极值影响
func (opt *FastTriggerThresholdOptimizer) getSpreadRangeForDirection(data []SpreadDataPoint, isLong bool) spreadRange {
	if len(data) == 0 {
		return spreadRange{0, 0}
	}

	// 提取价差序列
	var spreads []float64
	for _, point := range data {
		if isLong {
			spreads = append(spreads, point.SpreadLong)
		} else {
			spreads = append(spreads, point.SpreadShort)
		}
	}

	// 如果启用加权窗口，使用加权稳健统计
	if opt.UseWeightedWindow && opt.WeightDecay > 0 {
		// 先检测是否有突变
		changePoint := opt.detectSpreadChange(data, isLong)
		if changePoint > 0 {
			// 检测到突变，使用分段加权稳健统计
			return opt.getSegmentedRobustRange(data, isLong, changePoint)
		}
		// 没有突变，使用加权稳健统计
		return opt.getWeightedRobustRange(spreads)
	}

	// 否则使用截尾分位数（抗插针）
	return opt.getRobustRange(spreads)
}

// getRobustRange 使用截尾分位数计算稳健范围（抗插针）
func (opt *FastTriggerThresholdOptimizer) getRobustRange(spreads []float64) spreadRange {
	if len(spreads) == 0 {
		return spreadRange{0, 0}
	}

	sorted := make([]float64, len(spreads))
	copy(sorted, spreads)
	sort.Float64s(sorted)

	// 使用截尾分位数：[OutlierPercentile, 1-OutlierPercentile]
	// 例如 OutlierPercentile=0.05，则使用 5%-95% 分位数
	lowerIdx := int(float64(len(sorted)) * opt.OutlierPercentile)
	upperIdx := int(float64(len(sorted)) * (1 - opt.OutlierPercentile))
	
	if lowerIdx < 0 {
		lowerIdx = 0
	}
	if upperIdx >= len(sorted) {
		upperIdx = len(sorted) - 1
	}

	return spreadRange{
		Min: sorted[lowerIdx],
		Max: sorted[upperIdx],
	}
}

// getWeightedRobustRange 使用加权截尾分位数计算稳健范围
func (opt *FastTriggerThresholdOptimizer) getWeightedRobustRange(spreads []float64) spreadRange {
	if len(spreads) == 0 {
		return spreadRange{0, 0}
	}

	// 计算加权价差
	var weightedSpreads []weightedSpread
	totalWeight := 0.0
	
	for i := 0; i < len(spreads); i++ {
		distance := float64(len(spreads) - 1 - i)
		weight := math.Exp(-opt.WeightDecay * distance)
		totalWeight += weight
		
		weightedSpreads = append(weightedSpreads, weightedSpread{
			spread: spreads[i],
			weight: weight,
		})
	}
	
	// 使用最近50%数据的加权分位数
	recentStart := len(weightedSpreads) / 2
	recentSpreads := weightedSpreads[recentStart:]
	recentTotalWeight := 0.0
	for _, ws := range recentSpreads {
		recentTotalWeight += ws.weight
	}
	
	// 按价差排序
	sort.Slice(recentSpreads, func(i, j int) bool {
		return recentSpreads[i].spread < recentSpreads[j].spread
	})
	
	// 使用截尾分位数
	lowerQuantile := opt.OutlierPercentile
	upperQuantile := 1 - opt.OutlierPercentile
	
	weightedMin := opt.calculateWeightedQuantile(recentSpreads, lowerQuantile, recentTotalWeight)
	weightedMax := opt.calculateWeightedQuantile(recentSpreads, upperQuantile, recentTotalWeight)
	
	return spreadRange{weightedMin, weightedMax}
}

// getSegmentedRobustRange 使用分段加权稳健统计（突变后）
func (opt *FastTriggerThresholdOptimizer) getSegmentedRobustRange(data []SpreadDataPoint, isLong bool, changePoint int) spreadRange {
	if len(data) == 0 {
		return spreadRange{0, 0}
	}
	
	// 分段：突变前和突变后
	beforeData := data[:changePoint]
	afterData := data[changePoint:]
	
	// 提取突变后的价差序列
	var afterSpreads []float64
	for _, point := range afterData {
		if isLong {
			afterSpreads = append(afterSpreads, point.SpreadLong)
		} else {
			afterSpreads = append(afterSpreads, point.SpreadShort)
		}
	}
	
	// 提取突变前的价差序列
	var beforeSpreads []float64
	for _, point := range beforeData {
		if isLong {
			beforeSpreads = append(beforeSpreads, point.SpreadLong)
		} else {
			beforeSpreads = append(beforeSpreads, point.SpreadShort)
		}
	}
	
	// 计算稳健范围
	afterRange := opt.getRobustRange(afterSpreads)
	beforeRange := opt.getRobustRange(beforeSpreads)
	
	// 加权合并：突变后的数据权重更高
	recentWeight := opt.RecentDataWeight
	oldWeight := 1.0 - recentWeight
	
	if len(afterSpreads) > 0 && len(beforeSpreads) > 0 {
		weightedMin := recentWeight*afterRange.Min + oldWeight*beforeRange.Min
		weightedMax := recentWeight*afterRange.Max + oldWeight*beforeRange.Max
		return spreadRange{weightedMin, weightedMax}
	} else if len(afterSpreads) > 0 {
		return afterRange
	} else {
		return beforeRange
	}
}


// detectSpreadChange 检测价差突变点
// 使用滑动窗口计算价差变化率，如果变化率超过阈值，认为发生突变
func (opt *FastTriggerThresholdOptimizer) detectSpreadChange(data []SpreadDataPoint, isLong bool) int {
	if len(data) < 100 {
		return -1 // 数据不足，不检测
	}
	
	windowSize := 100 // 增大滑动窗口大小，提高检测稳定性
	maxChange := 0.0
	changePoint := -1
	
	// 从后往前检测，找到最近的突变点
	for i := len(data) - windowSize; i >= windowSize; i-- {
		// 计算前后窗口的平均价差
		var prevAvg, nextAvg float64
		prevCount := 0
		nextCount := 0
		
		// 前窗口：i-windowSize 到 i
		for j := i - windowSize; j < i && j < len(data); j++ {
			var spread float64
			if isLong {
				spread = data[j].SpreadLong
			} else {
				spread = data[j].SpreadShort
			}
			prevAvg += spread
			prevCount++
		}
		if prevCount > 0 {
			prevAvg /= float64(prevCount)
		}
		
		// 后窗口：i 到 i+windowSize
		for j := i; j < i+windowSize && j < len(data); j++ {
			var spread float64
			if isLong {
				spread = data[j].SpreadLong
			} else {
				spread = data[j].SpreadShort
			}
			nextAvg += spread
			nextCount++
		}
		if nextCount > 0 {
			nextAvg /= float64(nextCount)
		}
		
		// 计算变化率（使用相对变化）
		var change float64
		if math.Abs(prevAvg) > 0.001 {
			change = math.Abs(nextAvg - prevAvg) / math.Abs(prevAvg)
		} else if math.Abs(nextAvg) > 0.001 {
			// 如果前窗口接近0，后窗口有值，认为发生了突变
			change = math.Abs(nextAvg) / 0.001
		} else {
			continue
		}
		
		if change > maxChange {
			maxChange = change
			changePoint = i
		}
	}
	
	// 如果变化率超过阈值，认为发生了突变
	if maxChange > opt.ChangeDetectionThreshold {
		return changePoint
	}
	
	return -1
}


// weightedSpread 带权重的价差
type weightedSpread struct {
	spread float64
	weight float64
}

// calculateWeightedQuantile 计算加权分位数
func (opt *FastTriggerThresholdOptimizer) calculateWeightedQuantile(weightedSpreads []weightedSpread, quantile float64, totalWeight float64) float64 {
	if len(weightedSpreads) == 0 {
		return 0
	}
	
	targetWeight := totalWeight * quantile
	accumulatedWeight := 0.0
	
	for _, ws := range weightedSpreads {
		accumulatedWeight += ws.weight
		if accumulatedWeight >= targetWeight {
			return ws.spread
		}
	}
	
	// 如果还没达到目标权重，返回最后一个值
	return weightedSpreads[len(weightedSpreads)-1].spread
}

// generateCandidatesForRange 根据范围生成候选阈值
// 基于实际机会点的价差范围生成候选阈值（支持正值和负值）
// 只考虑闭环利润：只要 LongThreshold + ShortThreshold >= MinRoundTripProfit 即可
func (opt *FastTriggerThresholdOptimizer) generateCandidatesForRange(sr spreadRange) []float64 {
	// 扩展搜索范围
	minSearch := sr.Min
	maxSearch := sr.Max

	// 🔥 修复：正确处理负数范围
	// 如果范围包含负数，需要确保候选阈值也包含负数
	hasNegative := minSearch < 0
	hasPositive := maxSearch > 0

	// 🔥 优化：如果价差范围很大（Max > Min * 5），说明有高价差
	// 但仅当 minSearch >= 0 时才提升 Min，避免丢失负值候选（对冲交易需要负阈值）
	// 策略：如果Max >= 0.5（高价差）且 minSearch >= 0，将Min提升到 Max * 0.3，确保候选阈值能覆盖高价差
	if maxSearch >= 0.5 && minSearch >= 0 && maxSearch > minSearch*3 {
		// 有高价差且原始范围为正，使用更高的Min值
		// 使用 Max * 0.3 作为Min，这样能确保候选阈值覆盖高价差范围
		// 例如：Max=0.8, Min=0.05 -> 新Min=0.24，这样候选阈值会从0.24开始
		newMin := maxSearch * 0.3
		if newMin > minSearch {
			minSearch = newMin
		}
		// 确保Min不会低于原始Min太多（至少是原始Min的2倍，但仅当原始Min为正时）
		if minSearch >= 0 && minSearch < sr.Min*2 {
			minSearch = sr.Min * 2
		}
	}

	// 🔥 修复：对于包含负数的范围，扩展时保留负数部分
	rangeSize := maxSearch - minSearch
	if rangeSize > 0 {
		// 扩展范围 20%，但确保保留原始范围
		expansion := rangeSize * 0.2
		minSearch = minSearch - expansion
		maxSearch = sr.Max + expansion
		
		// 🔥 重要：如果原始范围包含负数，确保扩展后的范围也包含负数
		if hasNegative && minSearch > sr.Min {
			// 如果扩展后minSearch变大了（更接近0），需要确保仍然包含原始的最小值
			// 但不要过度扩展负数部分，避免生成过多无意义的负阈值
			if minSearch > 0 && sr.Min < 0 {
				// 原始范围跨0，扩展后minSearch变成正数，需要保留负数部分
				minSearch = math.Min(minSearch, sr.Min*1.1) // 保留原始负数的10%扩展
			}
		}
	} else {
		// 🔥 修复：如果范围为 0（Min == Max），使用该值作为中心，扩展一定范围
		// 而不是使用默认范围，这样可以确保候选阈值覆盖高价差
		center := maxSearch // 或 minSearch，它们相等
		// 如果中心值 >= 0.5（高价差），扩展范围应该更大
		if center >= 0.5 {
			// 高价差：扩展范围为中心值的 50%
			rangeSize = center * 0.5
		} else if center <= -0.5 {
			// 🔥 新增：负价差：扩展范围为中心值的 50%（绝对值）
			rangeSize = math.Abs(center) * 0.5
		} else {
			// 低价差：使用默认范围
			rangeSize = opt.MinRoundTripProfit
		}
		minSearch = center - rangeSize
		maxSearch = center + rangeSize
		// 确保不会小于原始值
		if minSearch < sr.Min {
			minSearch = sr.Min
		}
		if maxSearch < sr.Max {
			maxSearch = sr.Max
		}
	}

	// 🔥 修复：确保范围足够大，但对于跨0的范围，需要特殊处理
	minRange := opt.MinRoundTripProfit
	if maxSearch-minSearch < minRange {
		center := (minSearch + maxSearch) / 2
		// 如果范围跨0，确保扩展后仍然包含0附近的区域
		if hasNegative && hasPositive {
			// 跨0范围：确保包含足够的负数部分
			minSearch = math.Min(center-minRange/2, sr.Min*1.1)
			maxSearch = math.Max(center+minRange/2, sr.Max*1.1)
		} else {
			// 单侧范围：正常扩展
			minSearch = center - minRange/2
			maxSearch = center + minRange/2
		}
	}

	return opt.generateSmartThresholds(minSearch, maxSearch)
}


// findOpportunityWindowsForDirection 识别指定方向的套利机会窗口
// isLong: true 表示 Long（+A-B）方向，false 表示 Short（-A+B）方向
//
// 🔥 关键修改：不再检查实时闭环利润
// 套利是跨时间的：开仓时只看当前方向的价差，平仓时才看另一个方向
// 机会判断条件：该方向的价差有变化（用于识别价差波动的时间窗口）
// 阈值约束（LongThreshold + ShortThreshold >= MinRoundTripProfit）在 findBestThresholdPair 中保证
func (opt *FastTriggerThresholdOptimizer) findOpportunityWindowsForDirection(data []SpreadDataPoint, isLong bool) []OpportunityWindow {
	if len(data) == 0 {
		return nil
	}

	var windows []OpportunityWindow
	var currentWindow *OpportunityWindow

	// 计算该方向价差的统计信息，用于识别"有意义"的价差变化
	var spreads []float64
	for _, point := range data {
		if isLong {
			spreads = append(spreads, point.SpreadLong)
		} else {
			spreads = append(spreads, point.SpreadShort)
		}
	}

	// 计算价差的中位数作为基准
	// 🔥 优化：检测是否有高价差，如果有，使用最近的数据
	var medianSpread float64
	
	// 检测最近200个点是否有高价差
	recentWindow := 200
	if len(data) > recentWindow {
		highSpreadCount := 0
		for i := len(data) - recentWindow; i < len(data); i++ {
			roundTripProfit := data[i].SpreadLong + data[i].SpreadShort
			if roundTripProfit >= 0.5 { // 高价差阈值
				highSpreadCount++
			}
		}
		
		// 🔥 优化：降低阈值到10%，更敏感地检测高价差
		// 如果最近200个点中，超过10%是高价值差，只使用最近的数据
		if float64(highSpreadCount)/float64(recentWindow) > 0.1 {
			recentSpreads := spreads[len(spreads)-recentWindow:]
			sortedRecent := make([]float64, len(recentSpreads))
			copy(sortedRecent, recentSpreads)
			sort.Float64s(sortedRecent)
			medianSpread = sortedRecent[len(sortedRecent)/2]
		} else if opt.UseWeightedWindow && len(spreads) > 100 {
			// 使用最近50%数据的中位数
			recentStart := len(spreads) / 2
			recentSpreads := spreads[recentStart:]
			sortedRecent := make([]float64, len(recentSpreads))
			copy(sortedRecent, recentSpreads)
			sort.Float64s(sortedRecent)
			medianSpread = sortedRecent[len(sortedRecent)/2]
		} else {
			// 使用所有数据的中位数
			sortedSpreads := make([]float64, len(spreads))
			copy(sortedSpreads, spreads)
			sort.Float64s(sortedSpreads)
			medianSpread = sortedSpreads[len(sortedSpreads)/2]
		}
	} else {
		// 数据不足，使用所有数据的中位数
		sortedSpreads := make([]float64, len(spreads))
		copy(sortedSpreads, spreads)
		sort.Float64s(sortedSpreads)
		medianSpread = sortedSpreads[len(sortedSpreads)/2]
	}

	for i, point := range data {
		var spread float64
		if isLong {
			spread = point.SpreadLong
		} else {
			spread = point.SpreadShort
		}

		// 🔥 修复：机会判断逻辑，支持负价差场景
		// 对于正价差：价差 >= 中位数（表示有套利机会）
		// 对于负价差：价差 <= 中位数（更负的价差也可能是机会）
		// 注意：单点闭环利润可能小于阈值，但通过组合（Long+Short）可以满足
		// 所以这里只判断价差是否"有意义"，闭环利润约束在 findBestThresholdPair 中保证
		roundTripProfit := point.SpreadLong + point.SpreadShort
		hasOpportunity := false
		
		// 关键：只要闭环利润满足要求，就认为有机会
		if roundTripProfit >= opt.MinRoundTripProfit {
			// 闭环利润满足要求，进一步判断价差是否"有意义"
			if medianSpread >= 0 {
				// 中位数为正或0：价差 >= 中位数
				hasOpportunity = spread >= medianSpread
			} else {
				// 中位数为负：对于负数，更负（更小）也可能是机会；对于正数，肯定是机会
				if spread < 0 {
					// 负价差：价差 <= 中位数（更负）
					hasOpportunity = spread <= medianSpread
				} else {
					// 正价差：肯定是机会
					hasOpportunity = true
				}
			}
		} else {
			// 闭环利润不满足要求，但如果是高价差（绝对值大），也可能是机会
			// 因为可以通过组合其他点来满足闭环利润要求
			if math.Abs(spread) >= 0.5 {
				hasOpportunity = true
			}
		}

		if hasOpportunity {
			if currentWindow == nil {
				startTime := point.Timestamp
				if i > 0 {
					startTime = data[i-1].Timestamp
				}

				currentWindow = &OpportunityWindow{
					StartTime: startTime,
					EndTime:   point.Timestamp,
					Points:    []SpreadDataPoint{point},
				}
			} else {
				currentWindow.EndTime = point.Timestamp
				currentWindow.Points = append(currentWindow.Points, point)
			}
		} else {
			if currentWindow != nil {
				windows = append(windows, *currentWindow)
				currentWindow = nil
			}
		}
	}

	if currentWindow != nil {
		windows = append(windows, *currentWindow)
	}

	return windows
}


// generateSmartThresholds 生成智能阈值候选（均匀采样，支持正负）
func (opt *FastTriggerThresholdOptimizer) generateSmartThresholds(minVal, maxVal float64) []float64 {
	if minVal >= maxVal {
		return []float64{minVal}
	}

	// 检测范围特征
	crossesZero := minVal < 0 && maxVal > 0

	// 生成候选阈值：均匀采样，确保覆盖整个范围
	// 总采样数：根据范围大小动态调整
	rangeSize := maxVal - minVal
	candidateCount := 150 // 默认150个候选
	
	// 如果范围很大，增加采样数
	if rangeSize > 1.0 {
		candidateCount = 200
	} else if rangeSize < 0.1 {
		candidateCount = 100
	}

	var thresholds []float64

	if crossesZero {
		// 跨0范围：分别处理负数部分和正数部分
		negativeRange := 0.0 - minVal
		positiveRange := maxVal - 0.0
		
		// 负数部分采样
		if negativeRange > 0.001 {
			negativeCount := int(float64(candidateCount) * negativeRange / rangeSize)
			if negativeCount < 5 {
				negativeCount = 5
			}
			step := negativeRange / float64(negativeCount-1)
			for i := 0; i < negativeCount; i++ {
				val := minVal + float64(i)*step
				thresholds = append(thresholds, val)
			}
		}
		
		// 正数部分采样
		if positiveRange > 0.001 {
			positiveCount := int(float64(candidateCount) * positiveRange / rangeSize)
			if positiveCount < 5 {
				positiveCount = 5
			}
			step := positiveRange / float64(positiveCount-1)
			for i := 1; i <= positiveCount; i++ {
				val := float64(i) * step
				thresholds = append(thresholds, val)
			}
		}
	} else {
		// 单侧范围：均匀采样
		step := rangeSize / float64(candidateCount-1)
		for i := 0; i < candidateCount; i++ {
			val := minVal + float64(i)*step
				thresholds = append(thresholds, val)
		}
	}

	// 去重并排序
	sort.Float64s(thresholds)
	unique := []float64{}
	for i, val := range thresholds {
		if i == 0 || val != thresholds[i-1] {
			unique = append(unique, val)
		}
	}

	return unique
}

// spreadRange 价差范围
type spreadRange struct {
	Min float64
	Max float64
}

// 辅助函数
func calculateAverageDelay(delays []time.Duration) time.Duration {
	if len(delays) == 0 {
		return 0
	}

	total := time.Duration(0)
	for _, delay := range delays {
		total += delay
	}

	return time.Duration(int64(total) / int64(len(delays)))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SetMinThreshold 设置最小闭环利润（LongThreshold + ShortThreshold >= 此值）
func (opt *FastTriggerThresholdOptimizer) SetMinThreshold(minThreshold float64) {
	opt.MinThreshold = minThreshold
}

// GetMinThreshold 获取最小闭环利润
func (opt *FastTriggerThresholdOptimizer) GetMinThreshold() float64 {
	return opt.MinThreshold
}

// SetMaxThreshold 设置最大闭环利润（LongThreshold + ShortThreshold <= 此值）
func (opt *FastTriggerThresholdOptimizer) SetMaxThreshold(maxThreshold float64) {
	opt.MaxThreshold = maxThreshold
}

// GetMaxThreshold 获取最大闭环利润
func (opt *FastTriggerThresholdOptimizer) GetMaxThreshold() float64 {
	return opt.MaxThreshold
}

// SetOutlierPercentile 设置异常值剔除分位数
func (opt *FastTriggerThresholdOptimizer) SetOutlierPercentile(percentile float64) {
	if percentile > 0 && percentile < 0.5 {
		opt.OutlierPercentile = percentile
	}
}

// SetMinRoundTripProfit 设置最小套利闭环利润
func (opt *FastTriggerThresholdOptimizer) SetMinRoundTripProfit(profit float64) {
	opt.MinRoundTripProfit = profit
}

// GetMinRoundTripProfit 获取最小套利闭环利润
func (opt *FastTriggerThresholdOptimizer) GetMinRoundTripProfit() float64 {
	return opt.MinRoundTripProfit
}


// SetMaxRoundTripProfit 设置最大套利闭环利润
// 优化出的阈值必须满足：开仓价差 + 平仓价差 <= 此值（0 表示不限制）
func (opt *FastTriggerThresholdOptimizer) SetMaxRoundTripProfit(profit float64) {
	opt.MaxRoundTripProfit = profit
}

// GetMaxRoundTripProfit 获取最大套利闭环利润
func (opt *FastTriggerThresholdOptimizer) GetMaxRoundTripProfit() float64 {
	return opt.MaxRoundTripProfit
}

