package analytics

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
)

// 长时间测试配置
const (
	longTestInterval      = 200 * time.Millisecond // 每个数据点间隔200ms
	longTestDuration      = 1 * time.Hour          // 测试时长1小时
	longTestOutputInterval = 5 * time.Second        // 每5秒输出一次阈值
)

// getLongTestDuration 可被 FAST_TRIGGER_LONG_DURATION 覆盖（如 "2m"），用于报告等缩短运行
func getLongTestDuration() time.Duration {
	if s := os.Getenv("FAST_TRIGGER_LONG_DURATION"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return longTestDuration
}

// calculateLongTestPoints 计算长时间测试的数据点数量
func calculateLongTestPoints() int {
	return int(getLongTestDuration() / longTestInterval)
}

// calculateOutputIntervalPoints 计算输出间隔对应的数据点数量
func calculateOutputIntervalPoints() int {
	return int(longTestOutputInterval / longTestInterval) // 25个点
}

// TestFastTriggerOptimizer_HighFrequencyOscillation_Long 长时间测试：高频正负变化
func TestFastTriggerOptimizer_HighFrequencyOscillation_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v (%d个点)",
		totalPoints, getLongTestDuration(), longTestOutputInterval, outputIntervalPoints)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 生成数据：高频震荡
	for i := 0; i < totalPoints; i++ {
		spreadLong := 0.5
		spreadShort := -0.5
		if i%2 == 0 {
			spreadLong = -0.5
			spreadShort = 0.5
		}
		// 确保闭环利润满足要求
		roundTripProfit := spreadLong + spreadShort
		if roundTripProfit < 0.3 {
			if spreadLong < 0 {
				spreadLong = 0.2
			}
			if spreadShort < 0 {
				spreadShort = 0.2
			}
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	// 每5秒（25个点）输出一次阈值
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		// 使用到当前时间为止的数据
		currentData := data[:i]
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread, result.TotalProfit)
		
		if debug.DataPoints > 0 {
			t.Logf("      [调试] Long机会点: %d, Short机会点: %d, Long窗口: %d, Short窗口: %d",
				debug.LongOpportunityCount, debug.ShortOpportunityCount,
				debug.LongWindowsCount, debug.ShortWindowsCount)
		}
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_SuddenHighSpread_Long 长时间测试：大部分平静突然进行高价差变化
func TestFastTriggerOptimizer_SuddenHighSpread_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 前80%平静，后20%高价差
	calmPeriod := int(float64(totalPoints) * 0.8)
	
	for i := 0; i < totalPoints; i++ {
		if i < calmPeriod {
			data[i] = generateSpreadDataPoint(times[i], 0.05, 0.05, 0.05, 0.05)
		} else {
			data[i] = generateSpreadDataPoint(times[i], 0.8, 0.6, 0.05, 0.05)
		}
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread)
		
		// 🔥 调试信息：在突变点附近输出详细信息
		if i >= 14400 && i <= 15000 && outputCount%5 == 0 {
			t.Logf("  [调试] Long范围: [%.6f, %.6f], Short范围: [%.6f, %.6f]",
				debug.LongSpreadRange[0], debug.LongSpreadRange[1],
				debug.ShortSpreadRange[0], debug.ShortSpreadRange[1])
			t.Logf("  [调试] Long候选: [%.6f, %.6f] (%d个), Short候选: [%.6f, %.6f] (%d个)",
				debug.LongCandidatesMin, debug.LongCandidatesMax, debug.LongCandidatesCount,
				debug.ShortCandidatesMin, debug.ShortCandidatesMax, debug.ShortCandidatesCount)
			t.Logf("  [调试] Long窗口: %d, Short窗口: %d",
				debug.LongWindowsCount, debug.ShortWindowsCount)
		}
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_GradualIncreaseThenDecrease_Long 长时间测试：价差持续变大又持续变小
func TestFastTriggerOptimizer_GradualIncreaseThenDecrease_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 前50%逐渐增大，后50%逐渐减小
	midPoint := totalPoints / 2
	
	for i := 0; i < totalPoints; i++ {
		var spreadLong, spreadShort float64
		if i < midPoint {
			// 逐渐增大：0.1 -> 0.8
			spreadLong = 0.1 + float64(i)*0.7/float64(midPoint)
			spreadShort = 0.1 + float64(i)*0.7/float64(midPoint)
		} else {
			// 逐渐减小：0.8 -> 0.1
			spreadLong = 0.8 - float64(i-midPoint)*0.7/float64(midPoint)
			spreadShort = 0.8 - float64(i-midPoint)*0.7/float64(midPoint)
		}
		
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.1
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 总利润: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.TotalProfit)
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_RandomSpread_Long 长时间测试：价差随机性很强
func TestFastTriggerOptimizer_RandomSpread_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	rand.Seed(time.Now().UnixNano())
	
	for i := 0; i < totalPoints; i++ {
		spreadLong := (rand.Float64() - 0.5) * 2.0
		spreadShort := (rand.Float64() - 0.5) * 2.0
		
		// 每10个点强制一个满足闭环利润的点
		if i%10 == 0 {
			spreadLong = 0.3 + rand.Float64()*0.5
			spreadShort = 0.3 + rand.Float64()*0.5
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread)
		
		if debug.DataPoints > 0 {
			t.Logf("      [调试] Long机会点: %d, Short机会点: %d",
				debug.LongOpportunityCount, debug.ShortOpportunityCount)
		}
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_LongTermNegativeSpread_Long 长时间测试：双边长期运行负价差
func TestFastTriggerOptimizer_LongTermNegativeSpread_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	for i := 0; i < totalPoints; i++ {
		if i%2 == 0 {
			data[i] = generateSpreadDataPoint(times[i], -0.6, 0.8, 0.05, 0.05)
		} else {
			data[i] = generateSpreadDataPoint(times[i], 0.8, -0.6, 0.05, 0.05)
		}
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		totalProfit := result.LongThreshold + result.ShortThreshold
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 总利润: %.6f, 成交次数: %d",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold, totalProfit,
			result.TotalExecutions)
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_ConsistentlyPositiveSpread_Long 长时间测试：价差持续为正
func TestFastTriggerOptimizer_ConsistentlyPositiveSpread_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	for i := 0; i < totalPoints; i++ {
		spreadLong := 0.2 + float64(i%50)*0.016
		spreadShort := 0.2 + float64((i+25)%50)*0.016
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread)
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_SuddenRiseThenExpand_Long 长时间测试：突然出现价差持续上涨随后持续拉大
func TestFastTriggerOptimizer_SuddenRiseThenExpand_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 前25%平静，中间25%突然上涨，后50%持续拉大
	phase1End := totalPoints / 4
	phase2End := totalPoints / 2
	
	for i := 0; i < totalPoints; i++ {
		var spreadLong, spreadShort float64
		if i < phase1End {
			// 平静期
			spreadLong = 0.1
			spreadShort = 0.1
		} else if i < phase2End {
			// 突然上涨：0.1 -> 0.5
			progress := float64(i-phase1End) / float64(phase2End-phase1End)
			spreadLong = 0.1 + progress*0.4
			spreadShort = 0.1 + progress*0.4
		} else {
			// 持续拉大：0.5 -> 1.5
			progress := float64(i-phase2End) / float64(totalPoints-phase2End)
			spreadLong = 0.5 + progress*1.0
			spreadShort = 0.5 + progress*1.0
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 总利润: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.TotalProfit)
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_PeriodicOscillation_Long 长时间测试：价差周期性波动（正弦波）
func TestFastTriggerOptimizer_PeriodicOscillation_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 正弦波：周期为50个点，振幅0.5，中心值0.5
	period := 50.0
	for i := 0; i < totalPoints; i++ {
		phase := 2 * math.Pi * float64(i) / period
		spreadLong := 0.5 + 0.5*math.Sin(phase)
		spreadShort := 0.5 + 0.5*math.Sin(phase+math.Pi/2)
		
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.2
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread)
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// TestFastTriggerOptimizer_MixedPatterns_Long 长时间测试：混合模式
func TestFastTriggerOptimizer_MixedPatterns_Long(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v",
		totalPoints, getLongTestDuration(), longTestOutputInterval)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := make([]SpreadDataPoint, totalPoints)
	
	// 前33%平静，中间33%逐渐上涨，后34%高位震荡
	phase1End := totalPoints / 3
	phase2End := totalPoints * 2 / 3
	
	rand.Seed(44)
	for i := 0; i < totalPoints; i++ {
		var spreadLong, spreadShort float64
		if i < phase1End {
			// 平静期
			spreadLong = 0.1
			spreadShort = 0.1
		} else if i < phase2End {
			// 逐渐上涨
			progress := float64(i-phase1End) / float64(phase2End-phase1End)
			spreadLong = 0.1 + progress*0.5
			spreadShort = 0.1 + progress*0.5
		} else {
			// 高位震荡
			spreadLong = 0.6 + (rand.Float64()-0.5)*0.2
			spreadShort = 0.6 + (rand.Float64()-0.5)*0.2
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	outputCount := 0
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		
		t.Logf("[%03d] 时间: %v (数据点: %d/%d) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f",
			outputCount, elapsedTime, i, totalPoints,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread)
		
		if debug.DataPoints > 0 {
			t.Logf("      [调试] Long机会点: %d, Short机会点: %d, Long窗口: %d, Short窗口: %d",
				debug.LongOpportunityCount, debug.ShortOpportunityCount,
				debug.LongWindowsCount, debug.ShortWindowsCount)
		}
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}

// runLongTestWithOutput 通用长时间测试函数
func runLongTestWithOutput(t *testing.T, testName string, dataGenerator func([]time.Time, int) []SpreadDataPoint) {
	optimizer := NewFastTriggerThresholdOptimizer(600, 0.3)
	startTime := time.Now()
	
	totalPoints := calculateLongTestPoints()
	outputIntervalPoints := calculateOutputIntervalPoints()
	
	t.Logf("=== %s ===", testName)
	t.Logf("开始长时间测试 - 总数据点: %d, 测试时长: %v, 输出间隔: %v (%d个点)",
		totalPoints, getLongTestDuration(), longTestOutputInterval, outputIntervalPoints)
	
	times := generateTimeSeries(startTime, totalPoints, longTestInterval)
	data := dataGenerator(times, totalPoints)
	
	outputCount := 0
	lastOutputTime := time.Now()
	
	for i := outputIntervalPoints; i <= totalPoints; i += outputIntervalPoints {
		currentData := data[:i]
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(currentData)
		
		elapsedTime := time.Duration(i) * longTestInterval
		outputCount++
		now := time.Now()
		realElapsed := now.Sub(lastOutputTime)
		lastOutputTime = now
		
		// 格式化输出（同时输出到stdout和t.Log）
		outputLine := fmt.Sprintf("[%03d] 时间: %v (数据点: %d/%d, 实际耗时: %v) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 平均延迟: %v",
			outputCount, elapsedTime, i, totalPoints, realElapsed,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread,
			result.TotalProfit)
		
		fmt.Printf("[%s] %s\n", testName, outputLine)
		t.Logf("[%03d] 时间: %v (数据点: %d/%d, 实际耗时: %v) - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 平均延迟: %v",
			outputCount, elapsedTime, i, totalPoints, realElapsed,
			result.LongThreshold, result.ShortThreshold,
			result.TotalExecutions, result.AvgSpread,
			result.TotalProfit)
		
		if debug.DataPoints > 0 && outputCount%10 == 0 {
			t.Logf("      [调试] Long机会点: %d, Short机会点: %d, Long窗口: %d, Short窗口: %d",
				debug.LongOpportunityCount, debug.ShortOpportunityCount,
				debug.LongWindowsCount, debug.ShortWindowsCount)
		}
	}
	
	t.Logf("长时间测试完成 - 共输出 %d 次阈值结果", outputCount)
}
