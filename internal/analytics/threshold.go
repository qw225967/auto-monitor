package analytics

import (
	"math"
	"time"
)

// OptimalThresholds 最优阈值结果
type OptimalThresholds struct {
	ThresholdAB           float64       // +A-B 方向阈值
	ThresholdBA           float64       // -A+B 方向阈值
	TotalTrades           int           // 总触发次数
	ABTradeCount          int           // AB 方向触发次数（传统算法）
	BATradeCount          int           // BA 方向触发次数（传统算法）
	AvgTriggerDelay       time.Duration // 平均触发延迟
	SuccessRate           float64       // 触发成功率
	LongOpportunityCount  int           // Long 方向机会点数量
	ShortOpportunityCount int           // Short 方向机会点数量
	UseFastTrigger        bool          // 是否使用快速触发算法（保留用于兼容）
}

// updateStats 更新价差统计信息
func (a *Analytics) updateStats() {
	a.priceDiffsMu.RLock()
	defer a.priceDiffsMu.RUnlock()

	if len(a.priceDiffs[0]) == 0 || len(a.priceDiffs[1]) == 0 {
		a.logger.Debugf("[阈值更新价差] 数据为空，跳过更新 - AB长度: %d, BA长度: %d", len(a.priceDiffs[0]), len(a.priceDiffs[1]))
		return
	}

	oldMinAB := a.minDiffAB
	oldMaxAB := a.maxDiffAB
	oldMinBA := a.minDiffBA
	oldMaxBA := a.maxDiffBA

	a.minDiffAB = a.priceDiffs[0][0]
	a.maxDiffAB = a.priceDiffs[0][0]
	a.minDiffBA = a.priceDiffs[1][0]
	a.maxDiffBA = a.priceDiffs[1][0]

	for i := 0; i < len(a.priceDiffs[0]); i++ {
		if a.priceDiffs[0][i] < a.minDiffAB {
			a.minDiffAB = a.priceDiffs[0][i]
		}
		if a.priceDiffs[0][i] > a.maxDiffAB {
			a.maxDiffAB = a.priceDiffs[0][i]
		}
	}

	for i := 0; i < len(a.priceDiffs[1]); i++ {
		if a.priceDiffs[1][i] < a.minDiffBA {
			a.minDiffBA = a.priceDiffs[1][i]
		}
		if a.priceDiffs[1][i] > a.maxDiffBA {
			a.maxDiffBA = a.priceDiffs[1][i]
		}
	}

	// 详细日志：记录更新前后的变化
	a.logger.Infof("[阈值更新价差] 价差统计信息已更新 - AB: [%.6f, %.6f] (变化: min=%.6f, max=%.6f), BA: [%.6f, %.6f] (变化: min=%.6f, max=%.6f), 数据点数量: AB=%d, BA=%d",
		a.minDiffAB, a.maxDiffAB, a.minDiffAB-oldMinAB, a.maxDiffAB-oldMaxAB,
		a.minDiffBA, a.maxDiffBA, a.minDiffBA-oldMinBA, a.maxDiffBA-oldMaxBA,
		len(a.priceDiffs[0]), len(a.priceDiffs[1]))
}

// analyzeOptimalThresholds 分析最优阈值（优先快速触发优化器，失败时回退传统遍历算法）
func (a *Analytics) analyzeOptimalThresholds() {
	// 🔥 快速阈值计算效果差，已注释，直接使用传统算法
	// 获取带时间戳的价差数据
	a.spreadDataPointsMu.RLock()
	dataLen := len(a.spreadDataPoints)
	a.spreadDataPointsMu.RUnlock()

	if dataLen == 0 {
		a.logger.Debug("价差数据为空，跳过阈值分析")
		return
	}

	// 直接使用传统遍历算法
	a.logger.Debugf("开始分析最优阈值（使用传统算法） - 数据长度: %d, AB范围: [%.6f, %.6f], BA范围: [%.6f, %.6f]",
		dataLen, a.minDiffAB, a.maxDiffAB, a.minDiffBA, a.maxDiffBA)
	a.analyzeOptimalThresholdsLegacy()

	/* 🔥 快速阈值计算已注释（效果差）
	targetInterval := a.thresholdIntervalConfig.GetTargetThresholdInterval()
	a.logger.Debugf("开始分析最优阈值（快速触发算法） - 数据长度: %d, AB范围: [%.6f, %.6f], BA范围: [%.6f, %.6f], 成本阈值: %.6f",
		dataLen, a.minDiffAB, a.maxDiffAB, a.minDiffBA, a.maxDiffBA, targetInterval)

	// 获取价差数据副本
	spreadData := a.GetSpreadDataPoints()
	if len(spreadData) == 0 {
		a.logger.Debug("价差数据为空，跳过阈值分析")
		return
	}

	// 使用快速触发优化器计算最优阈值
	result, debug := a.fastTriggerOptimizer.CalculateOptimalThresholdsWithDebug(spreadData)

	// 🔥 判断快速阈值是否有效：
	// 1. 总成交次数 > 0（有实际成交）
	// 2. 阈值不是默认值（LongThreshold 和 ShortThreshold 都不是默认值，说明找到了有效组合）
	// 检查是否使用了默认阈值
	isDefaultLong := debug.IsDefaultLongThreshold
	isDefaultShort := debug.IsDefaultShortThreshold
	hasValidThresholds := result.TotalExecutions > 0 && !isDefaultLong && !isDefaultShort

	if hasValidThresholds {
		a.logger.Infof("[阈值计算] 快速阈值计算成功 - Long: %.6f, Short: %.6f, 成交次数: %d, 使用快速阈值",
			result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
		a.updateThresholdsWithStability(&result, &debug)
		return
	}

	// 快速阈值未找到有效结果，回退传统遍历算法
	if result.TotalExecutions == 0 {
		a.logger.Infof("[阈值计算] 快速阈值计算无成交（成交次数=0），回退传统算法")
	} else if isDefaultLong || isDefaultShort {
		a.logger.Infof("[阈值计算] 快速阈值计算使用了默认值（Long默认: %v, Short默认: %v），回退传统算法", isDefaultLong, isDefaultShort)
	}
	a.analyzeOptimalThresholdsLegacy()
	*/
}

// countWithCooldownFromSlice 计算超过阈值的次数，带冷却期（使用传入的切片，不需要加锁）
// 为了提高命中率，在比较时对每个价差点统一减去固定 0.05，这样能保证命中率。
func (a *Analytics) countWithCooldownFromSlice(diffs []float64, threshold float64, cooldown int) int {
	triggerCount := 0
	lastTriggerIdx := -cooldown - 1
	const fixedDiscount = 0.05
	for i, val := range diffs {
		adjustedVal := val - fixedDiscount
		if adjustedVal >= threshold && i-lastTriggerIdx > cooldown {
			triggerCount++
			lastTriggerIdx = i
		}
	}
	return triggerCount
}

// findOptimalThresholdLegacy 找出最优阈值组合（传统算法，基于最大利润，遍历 min~max step 0.05）
// 为了提高命中率，在计算时对每个价差点统一减去固定 0.05。
func (a *Analytics) findOptimalThresholdLegacy(minAB, maxAB, minBA, maxBA, targetThresholdInterval float64, priceDiffs [][]float64) (*float64, *float64, int, int) {
	a.logger.Infof("[传统阈值计算] 开始计算 - AB范围: [%.6f, %.6f], BA范围: [%.6f, %.6f], 目标闭环利润: %.6f, 数据点: AB=%d, BA=%d, 价差固定减: 0.05（提高命中率）",
		minAB, maxAB, minBA, maxBA, targetThresholdInterval, len(priceDiffs[0]), len(priceDiffs[1]))

	if len(priceDiffs[0]) == 0 || len(priceDiffs[1]) == 0 {
		a.logger.Warnf("[传统阈值计算] 数据为空，无法计算 - AB长度: %d, BA长度: %d", len(priceDiffs[0]), len(priceDiffs[1]))
		return nil, nil, 0, 0
	}

	const step = 0.05

	type candidate struct {
		thresholdAB float64
		thresholdBA float64
		countAB     int
		countBA     int
		profit      float64
	}

	var candidates []candidate
	totalCombinations := 0

	// 获取最大阈值限制（如果设置了）
	maxThreshold := a.thresholdIntervalConfig.GetMaxThreshold()
	if maxThreshold > 0 {
		a.logger.Infof("[传统阈值计算] 最大阈值限制: %.6f (闭环利润必须 <= 此值)", maxThreshold)
	}

	for abThreshold := minAB; abThreshold <= maxAB; abThreshold += step {
		minBAThreshold := targetThresholdInterval - abThreshold

		for baThreshold := minBAThreshold; baThreshold <= maxBA; baThreshold += step {
			totalCombinations++
			countAB := a.countWithCooldownFromSlice(priceDiffs[0], abThreshold, 5)
			countBA := a.countWithCooldownFromSlice(priceDiffs[1], baThreshold, 5)

			if countAB > 0 && countBA > 0 {
				totalPriceDiff := abThreshold + baThreshold
				
				// 检查最大阈值限制（如果设置了）
				if maxThreshold > 0 && totalPriceDiff > maxThreshold {
					// 超过最大阈值限制，跳过此候选
					continue
				}
				
				minCount := countAB
				if countBA < minCount {
					minCount = countBA
				}
				profit := float64(minCount) * totalPriceDiff

				candidates = append(candidates, candidate{
					thresholdAB: abThreshold,
					thresholdBA: baThreshold,
					countAB:     countAB,
					countBA:     countBA,
					profit:      profit,
				})
			}
		}
	}

	a.logger.Infof("[传统阈值计算] 遍历完成 - 总组合数: %d, 有效候选数: %d", totalCombinations, len(candidates))

	if len(candidates) == 0 {
		a.logger.Warnf("[传统阈值计算] 未找到满足 AB + BA >= %.6f 且双方向均有触发的阈值组合", targetThresholdInterval)
		return nil, nil, 0, 0
	}

	// 按 profit 降序，同 profit 按 total 触发次数降序
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			shouldSwap := false
			if candidates[i].profit < candidates[j].profit {
				shouldSwap = true
			} else if candidates[i].profit == candidates[j].profit {
				totalI := candidates[i].countAB + candidates[i].countBA
				totalJ := candidates[j].countAB + candidates[j].countBA
				if totalI < totalJ {
					shouldSwap = true
				}
			}
			if shouldSwap {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	best := candidates[0]
	a.logger.Infof("[传统阈值计算] 最优阈值结果 - AB阈值: %.6f (触发: %d次), BA阈值: %.6f (触发: %d次), 总利润: %.6f, 闭环利润: %.6f",
		best.thresholdAB, best.countAB, best.thresholdBA, best.countBA, best.profit, best.thresholdAB+best.thresholdBA)

	return &best.thresholdAB, &best.thresholdBA, best.countAB, best.countBA
}

// analyzeOptimalThresholdsLegacy 使用传统遍历算法分析最优阈值（作为快速触发失败时的备选）
func (a *Analytics) analyzeOptimalThresholdsLegacy() {
	a.priceDiffsMu.RLock()
	abLen := len(a.priceDiffs[0])
	baLen := len(a.priceDiffs[1])
	a.priceDiffsMu.RUnlock()

	if abLen == 0 || baLen == 0 {
		a.logger.Debug("价差数据为空，跳过传统阈值分析")
		return
	}

	dataLen := abLen
	if baLen < dataLen {
		dataLen = baLen
	}
	if dataLen == 0 {
		return
	}

	targetInterval := a.thresholdIntervalConfig.GetTargetThresholdInterval()
	a.logger.Debugf("传统算法 - 数据长度: %d, AB范围: [%.6f, %.6f], BA范围: [%.6f, %.6f], 目标价差区间: %.6f",
		dataLen, a.minDiffAB, a.maxDiffAB, a.minDiffBA, a.maxDiffBA, targetInterval)

	a.priceDiffsMu.RLock()
	priceDiffsCopy := make([][]float64, 2)
	priceDiffsCopy[0] = make([]float64, len(a.priceDiffs[0]))
	priceDiffsCopy[1] = make([]float64, len(a.priceDiffs[1]))
	copy(priceDiffsCopy[0], a.priceDiffs[0])
	copy(priceDiffsCopy[1], a.priceDiffs[1])
	a.priceDiffsMu.RUnlock()

	thresholdAB, thresholdBA, countAB, countBA := a.findOptimalThresholdLegacy(
		a.minDiffAB, a.maxDiffAB, a.minDiffBA, a.maxDiffBA, targetInterval, priceDiffsCopy)

	if thresholdAB != nil && thresholdBA != nil {
		a.optimalThresholds = &OptimalThresholds{
			ThresholdAB:    *thresholdAB,
			ThresholdBA:    *thresholdBA,
			ABTradeCount:   countAB,
			BATradeCount:   countBA,
			TotalTrades:    countAB + countBA,
			UseFastTrigger: false,
		}
		a.logger.Infof("传统阈值分析完成 - +A-B: %.6f (触发: %d), -A+B: %.6f (触发: %d)",
			*thresholdAB, countAB, *thresholdBA, countBA)
	} else {
		a.logger.Debug("传统算法未找到满足条件的最优阈值")
	}
}

// updateThresholdsWithStability 使用稳定性机制更新阈值
func (a *Analytics) updateThresholdsWithStability(result *FastTriggerResult, debug *DebugInfo) {
	a.thresholdStabilityMu.RLock()
	stabilityConfig := a.thresholdStabilityConfig
	a.thresholdStabilityMu.RUnlock()

	if stabilityConfig == nil {
		// 如果没有配置稳定性，使用默认配置
		stabilityConfig = NewThresholdStabilityConfig()
	}

	// 1. 置信度检查：只有满足最小要求才考虑更新
	// 使用成交次数替代成功率检查
	if result.TotalExecutions < stabilityConfig.MinTriggerCount {
		a.logger.Debugf("阈值更新被拒绝：成交次数 %d < 最小要求 %d",
			result.TotalExecutions, stabilityConfig.MinTriggerCount)
		return
	}

	// 2. 时间间隔检查：只有距离上次更新足够久才考虑更新
	now := time.Now()
	if !stabilityConfig.LastUpdateTime.IsZero() {
		timeSinceLastUpdate := now.Sub(stabilityConfig.LastUpdateTime)
		if timeSinceLastUpdate < stabilityConfig.MinUpdateInterval {
			a.logger.Debugf("阈值更新被拒绝：距离上次更新仅 %v < 最小间隔 %v",
				timeSinceLastUpdate, stabilityConfig.MinUpdateInterval)
			return
		}
	}

	// 3. 变化幅度检查：只有变化足够大才更新
	var newLongThreshold, newShortThreshold float64
	var shouldUpdate bool

	if a.optimalThresholds == nil {
		// 如果没有旧阈值，直接使用新阈值
		newLongThreshold = result.LongThreshold
		newShortThreshold = result.ShortThreshold
		shouldUpdate = true
		a.logger.Infof("首次设置阈值 - +A-B阈值: %.6f, -A+B阈值: %.6f",
			newLongThreshold, newShortThreshold)
	} else {
		// 计算变化比例
		oldLong := a.optimalThresholds.ThresholdAB
		oldShort := a.optimalThresholds.ThresholdBA

		longChangeRatio := math.Abs(result.LongThreshold-oldLong) / math.Max(math.Abs(oldLong), 0.001)
		shortChangeRatio := math.Abs(result.ShortThreshold-oldShort) / math.Max(math.Abs(oldShort), 0.001)

		// 如果任一方向的变化超过阈值，则更新
		if longChangeRatio >= stabilityConfig.MinChangeRatio || shortChangeRatio >= stabilityConfig.MinChangeRatio {
			// 应用平滑：新阈值 = alpha * 新值 + (1-alpha) * 旧值
			alpha := stabilityConfig.SmoothingAlpha
			newLongThreshold = alpha*result.LongThreshold + (1-alpha)*oldLong
			newShortThreshold = alpha*result.ShortThreshold + (1-alpha)*oldShort
			shouldUpdate = true

			a.logger.Infof("阈值更新（平滑后） - +A-B: %.6f -> %.6f (变化: %.2f%%), -A+B: %.6f -> %.6f (变化: %.2f%%)",
				oldLong, newLongThreshold, longChangeRatio*100,
				oldShort, newShortThreshold, shortChangeRatio*100)
		} else {
			// 变化太小，不更新
			a.logger.Debugf("阈值更新被拒绝：变化幅度太小 - Long: %.2f%%, Short: %.2f%% < 最小要求 %.2f%%",
				longChangeRatio*100, shortChangeRatio*100, stabilityConfig.MinChangeRatio*100)
			return
		}
	}

	// 4. 更新阈值
	if shouldUpdate {
		a.optimalThresholds = &OptimalThresholds{
			ThresholdAB:           newLongThreshold,
			ThresholdBA:           newShortThreshold,
			AvgTriggerDelay:       0, // 新算法不再计算延迟
			SuccessRate:           0, // 新算法不再计算成功率
			TotalTrades:           result.TotalExecutions,
			LongOpportunityCount:  debug.LongOpportunityCount,
			ShortOpportunityCount: debug.ShortOpportunityCount,
			UseFastTrigger:        true,
		}

		// 更新最后更新时间
		a.thresholdStabilityMu.Lock()
		a.thresholdStabilityConfig.LastUpdateTime = now
		a.thresholdStabilityMu.Unlock()

		a.logger.Infof("套利阈值分析完成 - +A-B阈值: %.6f, -A+B阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 总利润: %.6f, Long机会点: %d, Short机会点: %d",
			newLongThreshold, newShortThreshold, result.TotalExecutions,
			result.AvgSpread, result.TotalProfit,
			debug.LongOpportunityCount, debug.ShortOpportunityCount)
	}
}


