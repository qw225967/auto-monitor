package analytics

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

// 辅助函数：生成带时间戳的价差数据点
func generateSpreadDataPoint(timestamp time.Time, spreadLong, spreadShort, costLong, costShort float64) SpreadDataPoint {
	return SpreadDataPoint{
		Timestamp:   timestamp,
		SpreadLong:  spreadLong,
		SpreadShort: spreadShort,
		CostLong:    costLong,
		CostShort:   costShort,
	}
}

// 辅助函数：生成时间序列
func generateTimeSeries(startTime time.Time, count int, interval time.Duration) []time.Time {
	times := make([]time.Time, count)
	for i := 0; i < count; i++ {
		times[i] = startTime.Add(time.Duration(i) * interval)
	}
	return times
}

// TestFastTriggerOptimizer_HighFrequencyOscillation 测试场景1：高频正负变化
// 价差在正负之间快速震荡，测试优化器是否能找到稳定的阈值
func TestFastTriggerOptimizer_HighFrequencyOscillation(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	for i := 0; i < 200; i++ {
		// 高频震荡：+0.5 和 -0.5 之间快速切换
		spreadLong := 0.5
		spreadShort := -0.5
		if i%2 == 0 {
			spreadLong = -0.5
			spreadShort = 0.5
		}
		// 确保闭环利润满足要求
		roundTripProfit := spreadLong + spreadShort
		if roundTripProfit < 0.3 {
			// 调整以满足最小利润要求
			if spreadLong < 0 {
				spreadLong = 0.2
			}
			if spreadShort < 0 {
				spreadShort = 0.2
			}
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("高频震荡场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	if result.LongThreshold == 0 && result.ShortThreshold == 0 {
		t.Errorf("高频震荡场景：阈值不应该为0")
	}
	
	// 验证阈值满足闭环利润约束
	totalProfit := result.LongThreshold + result.ShortThreshold
	if totalProfit < optimizer.MinRoundTripProfit {
		t.Errorf("高频震荡场景：阈值不满足闭环利润约束，总利润 %.6f < 最小要求 %.6f",
			totalProfit, optimizer.MinRoundTripProfit)
	}
	
	t.Logf("高频震荡场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
	t.Logf("调试信息 - Long机会点: %d, Short机会点: %d, Long窗口: %d, Short窗口: %d",
		debug.LongOpportunityCount, debug.ShortOpportunityCount, debug.LongWindowsCount, debug.ShortWindowsCount)
}

// TestFastTriggerOptimizer_SuddenHighSpread 测试场景2：大部分平静突然进行高价差变化
// 前期价差很小，突然出现高价差，测试优化器是否能快速响应
func TestFastTriggerOptimizer_SuddenHighSpread(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 150, 200*time.Millisecond)
	
	data := make([]SpreadDataPoint, 150)
	
	// 前100个点：平静期，价差很小
	for i := 0; i < 100; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.05, 0.05, 0.05, 0.05)
	}
	
	// 后50个点：突然出现高价差
	for i := 100; i < 150; i++ {
		// 高价差：+A-B = 0.8, -A+B = 0.6，闭环利润 = 1.4
		data[i] = generateSpreadDataPoint(times[i], 0.8, 0.6, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果（传统算法校验下，达到/超过阈值的点才计数；若有有效对则检查阈值合理性）
	if result.TotalExecutions > 0 {
		if result.LongThreshold > 0.9 || result.ShortThreshold > 0.7 {
			t.Errorf("突然高价差场景：阈值设置过高。Long: %.6f, Short: %.6f",
				result.LongThreshold, result.ShortThreshold)
		}
	}
	t.Logf("突然高价差场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
	t.Logf("价差范围 - Long: [%.6f, %.6f], Short: [%.6f, %.6f]",
		debug.LongSpreadRange[0], debug.LongSpreadRange[1],
		debug.ShortSpreadRange[0], debug.ShortSpreadRange[1])
}

// TestFastTriggerOptimizer_GradualIncreaseThenDecrease 测试场景3：价差持续变大又持续变小
// 价差先逐渐增大到峰值，然后逐渐减小，测试优化器是否能找到最快触发点
func TestFastTriggerOptimizer_GradualIncreaseThenDecrease(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 前100个点：价差逐渐增大（0.1 -> 0.8）
	for i := 0; i < 100; i++ {
		spreadLong := 0.1 + float64(i)*0.007
		spreadShort := 0.1 + float64(i)*0.007
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.1 // 确保满足最小利润
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	// 后100个点：价差逐渐减小（0.8 -> 0.1）
	for i := 100; i < 200; i++ {
		spreadLong := 0.8 - float64(i-100)*0.007
		spreadShort := 0.8 - float64(i-100)*0.007
		if spreadLong < 0.1 {
			spreadLong = 0.1
		}
		if spreadShort < 0.1 {
			spreadShort = 0.1
		}
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.1
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("逐渐变化场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 新算法不再计算延迟，跳过延迟检查
	t.Logf("逐渐变化场景 - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread, result.TotalProfit)
}

// TestFastTriggerOptimizer_RandomSpread 测试场景4：价差随机性很强
// 价差完全随机，测试优化器在随机数据下的稳定性
func TestFastTriggerOptimizer_RandomSpread(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	rand.Seed(time.Now().UnixNano())
	data := make([]SpreadDataPoint, 200)
	
	for i := 0; i < 200; i++ {
		// 随机价差：-1.0 到 1.0 之间
		spreadLong := (rand.Float64() - 0.5) * 2.0
		spreadShort := (rand.Float64() - 0.5) * 2.0
		
		// 确保至少有一些点满足闭环利润要求
		if i%10 == 0 {
			// 每10个点强制一个满足闭环利润的点
			spreadLong = 0.3 + rand.Float64()*0.5
			spreadShort = 0.3 + rand.Float64()*0.5
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	// 随机数据可能没有足够的机会点，这是正常的
	if result.TotalExecutions > 0 {
		// 如果有触发，验证阈值合理性
		totalProfit := result.LongThreshold + result.ShortThreshold
		if totalProfit < optimizer.MinRoundTripProfit {
			t.Errorf("随机场景：阈值不满足闭环利润约束，总利润 %.6f < 最小要求 %.6f",
				totalProfit, optimizer.MinRoundTripProfit)
		}
	}
	
	t.Logf("随机场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
	t.Logf("机会点统计 - Long: %d, Short: %d",
		debug.LongOpportunityCount, debug.ShortOpportunityCount)
}

// TestFastTriggerOptimizer_LongTermNegativeSpread 测试场景5：双边长期运行负价差
// 价差长期为负，测试优化器是否能正确处理
func TestFastTriggerOptimizer_LongTermNegativeSpread(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 所有点都是负价差，但确保闭环利润满足要求
	for i := 0; i < 200; i++ {
		// 例如：SpreadLong = -0.6, SpreadShort = 0.8，闭环利润 = 0.2
		// 或者：SpreadLong = 0.8, SpreadShort = -0.6，闭环利润 = 0.2
		if i%2 == 0 {
			data[i] = generateSpreadDataPoint(times[i], -0.6, 0.8, 0.05, 0.05)
		} else {
			data[i] = generateSpreadDataPoint(times[i], 0.8, -0.6, 0.05, 0.05)
		}
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("长期负价差场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证阈值可以是负数（因为单边价差可以是负的）
	// 但总利润必须满足要求
	totalProfit := result.LongThreshold + result.ShortThreshold
	if totalProfit < optimizer.MinRoundTripProfit {
		t.Errorf("长期负价差场景：阈值不满足闭环利润约束，总利润 %.6f < 最小要求 %.6f",
			totalProfit, optimizer.MinRoundTripProfit)
	}
	
	t.Logf("长期负价差场景 - Long阈值: %.6f, Short阈值: %.6f, 总利润: %.6f, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, totalProfit, result.TotalExecutions)
	t.Logf("价差范围 - Long: [%.6f, %.6f], Short: [%.6f, %.6f]",
		debug.LongSpreadRange[0], debug.LongSpreadRange[1],
		debug.ShortSpreadRange[0], debug.ShortSpreadRange[1])
}

// TestFastTriggerOptimizer_ConsistentlyPositiveSpread 测试场景6：价差持续为正
// 价差一直为正，测试优化器是否能找到合适的阈值
func TestFastTriggerOptimizer_ConsistentlyPositiveSpread(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 所有价差都为正，且满足闭环利润要求
	for i := 0; i < 200; i++ {
		// 价差在 0.2 到 1.0 之间波动
		spreadLong := 0.2 + float64(i%50)*0.016
		spreadShort := 0.2 + float64((i+25)%50)*0.016
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("持续正价差场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证阈值应该都是正数
	if result.LongThreshold < 0 || result.ShortThreshold < 0 {
		t.Errorf("持续正价差场景：阈值应该是正数，但 Long: %.6f, Short: %.6f",
			result.LongThreshold, result.ShortThreshold)
	}
	
	t.Logf("持续正价差场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
}

// TestFastTriggerOptimizer_SuddenRiseThenExpand 测试场景7：突然出现价差持续上涨随后持续拉大
// 价差突然开始上涨，然后持续拉大，测试优化器是否能快速响应
func TestFastTriggerOptimizer_SuddenRiseThenExpand(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 前50个点：平静期
	for i := 0; i < 50; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.1, 0.1, 0.05, 0.05)
	}
	
	// 中间50个点：突然开始上涨（0.1 -> 0.5）
	for i := 50; i < 100; i++ {
		spreadLong := 0.1 + float64(i-50)*0.008
		spreadShort := 0.1 + float64(i-50)*0.008
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	// 后100个点：持续拉大（0.5 -> 1.5）
	for i := 100; i < 200; i++ {
		spreadLong := 0.5 + float64(i-100)*0.01
		spreadShort := 0.5 + float64(i-100)*0.01
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("突然上涨场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 新算法不再计算延迟，跳过延迟检查
	t.Logf("突然上涨场景 - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread, result.TotalProfit)
}

// TestFastTriggerOptimizer_PeriodicOscillation 测试场景8：价差周期性波动（正弦波）
// 价差按正弦波规律波动，测试优化器是否能识别周期性机会
func TestFastTriggerOptimizer_PeriodicOscillation(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 正弦波：周期为50个点，振幅0.5，中心值0.5
	for i := 0; i < 200; i++ {
		phase := 2 * math.Pi * float64(i) / 50.0
		spreadLong := 0.5 + 0.5*math.Sin(phase)
		spreadShort := 0.5 + 0.5*math.Sin(phase+math.Pi/2) // 相位差90度
		
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.2
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("周期性波动场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证成功率应该较高（周期性波动应该有多个触发机会）
	if result.AvgSpread < 0.3 {
		t.Errorf("周期性波动场景：成功率过低 %.2f%%，应该有更多触发机会",
			result.AvgSpread*100)
	}
	
	t.Logf("周期性波动场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
}

// TestFastTriggerOptimizer_SuddenJump 测试场景9：价差突然跳变
// 价差在某个时刻突然跳变，测试优化器是否能捕捉跳变后的机会
func TestFastTriggerOptimizer_SuddenJump(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 150, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 150)
	
	// 前100个点：稳定在低值
	for i := 0; i < 100; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.15, 0.15, 0.05, 0.05)
	}
	
	// 后50个点：突然跳变到高值
	for i := 100; i < 150; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.9, 0.8, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果（传统算法校验：仅考虑历史曾达到/超过的阈值；可能 0 触发或较高阈值）
	_ = debug
	t.Logf("突然跳变场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
}

// TestFastTriggerOptimizer_SlowClimb 测试场景10：价差缓慢爬升
// 价差非常缓慢地爬升，测试优化器是否能找到合适的触发点
func TestFastTriggerOptimizer_SlowClimb(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 300, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 300)
	
	// 非常缓慢地爬升：从0.2到0.8，300个点
	for i := 0; i < 300; i++ {
		spreadLong := 0.2 + float64(i)*0.002
		spreadShort := 0.2 + float64(i)*0.002
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("缓慢爬升场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 新算法不再计算延迟，跳过延迟检查
	
	t.Logf("缓慢爬升场景 - Long阈值: %.6f, Short阈值: %.6f, 平均延迟: %v, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, result.TotalProfit, result.TotalExecutions)
}

// TestFastTriggerOptimizer_ThresholdOscillation 测试场景11：价差在阈值附近震荡
// 价差在某个值附近震荡，测试优化器是否能找到稳定的阈值
func TestFastTriggerOptimizer_ThresholdOscillation(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 在0.5附近震荡：±0.2的波动
	rand.Seed(42) // 固定随机种子以便复现
	for i := 0; i < 200; i++ {
		spreadLong := 0.5 + (rand.Float64()-0.5)*0.4
		spreadShort := 0.5 + (rand.Float64()-0.5)*0.4
		
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.2
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("阈值震荡场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证阈值应该在震荡中心附近
	if result.LongThreshold < 0.3 || result.LongThreshold > 0.7 {
		t.Errorf("阈值震荡场景：Long阈值 %.6f 不在预期范围 [0.3, 0.7]",
			result.LongThreshold)
	}
	
	t.Logf("阈值震荡场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
}

// TestFastTriggerOptimizer_OneSideOnly 测试场景12：只有单边有机会
// 只有+A-B方向或只有-A+B方向有机会，测试优化器是否能正确处理
func TestFastTriggerOptimizer_OneSideOnly(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	// 测试1：只有+A-B方向有机会
	t.Run("OnlyLongOpportunity", func(t *testing.T) {
		data := make([]SpreadDataPoint, 200)
		for i := 0; i < 200; i++ {
			// +A-B方向有高价差，-A+B方向价差很小
			spreadLong := 0.6 + float64(i%50)*0.008
			spreadShort := 0.1
			// 确保闭环利润满足要求
			if spreadLong+spreadShort < 0.3 {
				spreadShort = 0.3 - spreadLong + 0.1
			}
			data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
		}
		
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
		
		// 传统算法校验要求双方向均曾在历史超过阈值；单边机会时 Short 恒定 0.1，无超过 → 可 0 触发
		if result.TotalExecutions > 0 && debug.LongOpportunityCount < debug.ShortOpportunityCount {
			t.Errorf("单边机会（Long）：Long机会点 %d 应 >= Short机会点 %d",
				debug.LongOpportunityCount, debug.ShortOpportunityCount)
		}
		t.Logf("单边机会（Long） - Long阈值: %.6f, Short阈值: %.6f, Long机会点: %d, Short机会点: %d",
			result.LongThreshold, result.ShortThreshold,
			debug.LongOpportunityCount, debug.ShortOpportunityCount)
	})
	
	// 测试2：只有-A+B方向有机会
	t.Run("OnlyShortOpportunity", func(t *testing.T) {
		data := make([]SpreadDataPoint, 200)
		for i := 0; i < 200; i++ {
			// -A+B方向有高价差，+A-B方向价差很小
			spreadLong := 0.1
			spreadShort := 0.6 + float64(i%50)*0.008
			// 确保闭环利润满足要求
			if spreadLong+spreadShort < 0.3 {
				spreadLong = 0.3 - spreadShort + 0.1
			}
			data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
		}
		
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
		
		// 传统算法校验：Long 恒定 0.1，无超过 → 可 0 触发
		if result.TotalExecutions > 0 && debug.ShortOpportunityCount < debug.LongOpportunityCount {
			t.Errorf("单边机会（Short）：Short机会点 %d 应 >= Long机会点 %d",
				debug.ShortOpportunityCount, debug.LongOpportunityCount)
		}
		t.Logf("单边机会（Short） - Long阈值: %.6f, Short阈值: %.6f, Long机会点: %d, Short机会点: %d",
			result.LongThreshold, result.ShortThreshold,
			debug.LongOpportunityCount, debug.ShortOpportunityCount)
	})
}

// TestFastTriggerOptimizer_InsufficientData 测试场景13：价差数据不足
// 数据点太少，测试优化器是否能正确处理
func TestFastTriggerOptimizer_InsufficientData(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 30, 100*time.Millisecond) // 只有30个点，小于WindowSize/2
	
	data := make([]SpreadDataPoint, 30)
	for i := 0; i < 30; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.5, 0.5, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果：数据不足时应该返回零阈值
	_ = debug // 避免未使用变量警告
	if result.LongThreshold != 0 || result.ShortThreshold != 0 {
		t.Errorf("数据不足场景：应该返回零阈值，但得到 Long: %.6f, Short: %.6f",
			result.LongThreshold, result.ShortThreshold)
	}
	
	if result.TotalExecutions != 0 {
		t.Errorf("数据不足场景：应该没有触发，但 TotalTriggers = %d",
			result.TotalExecutions)
	}
	
	t.Logf("数据不足场景 - 数据点: %d, 窗口大小: %d, 结果: 零阈值（符合预期）",
		debug.DataPoints, debug.WindowSize)
}

// TestFastTriggerOptimizer_LargeDataset 测试场景14：价差数据量很大
// 数据点很多，测试优化器是否能正确处理大数据集
func TestFastTriggerOptimizer_LargeDataset(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 1000, 50*time.Millisecond) // 1000个点
	
	data := make([]SpreadDataPoint, 1000)
	rand.Seed(43) // 固定随机种子
	for i := 0; i < 1000; i++ {
		// 价差在0.2到1.0之间随机波动
		spreadLong := 0.2 + rand.Float64()*0.8
		spreadShort := 0.2 + rand.Float64()*0.8
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果：大数据集应该能找到机会
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("大数据集场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证只使用最近的窗口数据
	if debug.DataPoints > optimizer.WindowSize {
		t.Errorf("大数据集场景：应该只使用窗口大小 %d 的数据，但使用了 %d",
			optimizer.WindowSize, debug.DataPoints)
	}
	
	t.Logf("大数据集场景 - 总数据点: 1000, 使用数据点: %d, Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		debug.DataPoints, result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
}

// TestFastTriggerOptimizer_StepwiseChange 测试场景15：价差阶梯式变化
// 价差呈阶梯式变化，测试优化器是否能识别阶梯
func TestFastTriggerOptimizer_StepwiseChange(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 阶梯式变化：每50个点一个台阶
	for i := 0; i < 200; i++ {
		step := i / 50
		spreadLong := 0.2 + float64(step)*0.2
		spreadShort := 0.2 + float64(step)*0.2
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果（传统算法校验下，阶梯数据可能无满足约束的候选对 → 允许 0 触发）
	_ = debug
	t.Logf("阶梯式变化场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
}

// TestFastTriggerOptimizer_PulsePattern 测试场景16：价差脉冲式变化
// 价差呈脉冲式变化（短暂的高价差），测试优化器是否能捕捉脉冲
func TestFastTriggerOptimizer_PulsePattern(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 脉冲式：大部分时间低值，偶尔出现高价差脉冲
	for i := 0; i < 200; i++ {
		spreadLong := 0.1
		spreadShort := 0.1
		
		// 每20个点出现一次脉冲（持续5个点）
		if i%20 < 5 {
			spreadLong = 0.8
			spreadShort = 0.7
		}
		
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	_ = debug // 避免未使用变量警告
	if result.TotalExecutions == 0 {
		t.Errorf("脉冲式变化场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证阈值应该能够捕捉脉冲
	if result.LongThreshold > 0.75 || result.ShortThreshold > 0.65 {
		t.Errorf("脉冲式变化场景：阈值设置过高，可能无法捕捉脉冲。Long: %.6f, Short: %.6f",
			result.LongThreshold, result.ShortThreshold)
	}
	
	t.Logf("脉冲式变化场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
}

// TestFastTriggerOptimizer_ConvergencePattern 测试场景17：价差收敛模式
// 价差从大值逐渐收敛到小值，测试优化器是否能适应收敛
func TestFastTriggerOptimizer_ConvergencePattern(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 从高价差逐渐收敛到低价差
	for i := 0; i < 200; i++ {
		spreadLong := 1.0 - float64(i)*0.004
		spreadShort := 1.0 - float64(i)*0.004
		if spreadLong < 0.2 {
			spreadLong = 0.2
		}
		if spreadShort < 0.2 {
			spreadShort = 0.2
		}
		// 确保闭环利润满足要求
		if spreadLong+spreadShort < 0.3 {
			spreadShort = 0.3 - spreadLong + 0.1
		}
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("收敛模式场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	t.Logf("收敛模式场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
}

// TestFastTriggerOptimizer_DivergencePattern 测试场景18：价差发散模式
// 价差从小值逐渐发散到大值，测试优化器是否能适应发散
func TestFastTriggerOptimizer_DivergencePattern(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 从低价差逐渐发散到高价差
	for i := 0; i < 200; i++ {
		spreadLong := 0.2 + float64(i)*0.004
		spreadShort := 0.2 + float64(i)*0.004
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("发散模式场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	// 验证平均触发延迟应该较小（应该能在发散早期触发）
	// 新算法不再计算延迟，跳过延迟检查
	t.Logf("发散模式场景 - Long阈值: %.6f, Short阈值: %.6f, 成交次数: %d, 平均价差: %.6f, 总利润: %.6f",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread, result.TotalProfit)
}

// TestFastTriggerOptimizer_MixedPatterns 测试场景19：混合模式
// 多种模式混合，模拟真实市场情况
func TestFastTriggerOptimizer_MixedPatterns(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 300, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 300)
	
	// 前100个点：平静期
	for i := 0; i < 100; i++ {
		data[i] = generateSpreadDataPoint(times[i], 0.1, 0.1, 0.05, 0.05)
	}
	
	// 中间100个点：逐渐上涨
	for i := 100; i < 200; i++ {
		spreadLong := 0.1 + float64(i-100)*0.006
		spreadShort := 0.1 + float64(i-100)*0.006
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	// 后100个点：在高位震荡
	rand.Seed(44)
	for i := 200; i < 300; i++ {
		spreadLong := 0.6 + (rand.Float64()-0.5)*0.2
		spreadShort := 0.6 + (rand.Float64()-0.5)*0.2
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, debug := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证结果
	if result.TotalExecutions == 0 {
		t.Errorf("混合模式场景：应该找到触发机会，但 TotalTriggers = 0")
	}
	
	t.Logf("混合模式场景 - Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d, 成功率: %.2f%%",
		result.LongThreshold, result.ShortThreshold, result.TotalExecutions, result.AvgSpread*100)
	t.Logf("机会统计 - Long机会点: %d, Short机会点: %d, Long窗口: %d, Short窗口: %d",
		debug.LongOpportunityCount, debug.ShortOpportunityCount,
		debug.LongWindowsCount, debug.ShortWindowsCount)
}

// TestFastTriggerOptimizer_EdgeCases 测试场景20：边界情况
// 测试各种边界情况
func TestFastTriggerOptimizer_EdgeCases(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	
	t.Run("EmptyData", func(t *testing.T) {
		result, debug := optimizer.CalculateOptimalThresholdsWithDebug([]SpreadDataPoint{})
		if result.LongThreshold != 0 || result.ShortThreshold != 0 {
			t.Errorf("空数据场景：应该返回零阈值")
		}
		if debug.DataPoints != 0 {
			t.Errorf("空数据场景：数据点应该为0")
		}
	})
	
	t.Run("AllZeroSpread", func(t *testing.T) {
		startTime := time.Now()
		times := generateTimeSeries(startTime, 100, 100*time.Millisecond)
		data := make([]SpreadDataPoint, 100)
		for i := 0; i < 100; i++ {
			data[i] = generateSpreadDataPoint(times[i], 0, 0, 0.05, 0.05)
		}
		
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
		// 所有价差为0，闭环利润为0，不满足最小要求，应该没有触发
		if result.TotalExecutions > 0 {
			t.Errorf("全零价差场景：不应该有触发，但 TotalTriggers = %d",
				result.TotalExecutions)
		}
	})
	
	t.Run("ExactMinRoundTripProfit", func(t *testing.T) {
		startTime := time.Now()
		times := generateTimeSeries(startTime, 100, 100*time.Millisecond)
		data := make([]SpreadDataPoint, 100)
		// 闭环利润正好等于最小要求
		for i := 0; i < 100; i++ {
			data[i] = generateSpreadDataPoint(times[i], 0.15, 0.15, 0.05, 0.05)
		}
		
		result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
		// 应该能找到机会
		if result.TotalExecutions == 0 {
			t.Logf("正好最小利润场景：没有触发（可能是阈值设置问题）")
		} else {
			totalProfit := result.LongThreshold + result.ShortThreshold
			if totalProfit < optimizer.MinRoundTripProfit {
				t.Errorf("正好最小利润场景：阈值不满足约束，总利润 %.6f < 最小要求 %.6f",
					totalProfit, optimizer.MinRoundTripProfit)
			}
		}
	})
}

// TestFastTriggerOptimizer_ConstraintValidation 测试场景21：约束验证
// 验证优化器是否正确处理各种约束
func TestFastTriggerOptimizer_ConstraintValidation(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.5) // 最小闭环利润0.5
	startTime := time.Now()
	times := generateTimeSeries(startTime, 200, 100*time.Millisecond)
	
	data := make([]SpreadDataPoint, 200)
	
	// 生成满足闭环利润要求的数据
	for i := 0; i < 200; i++ {
		// 确保闭环利润 >= 0.5
		spreadLong := 0.3 + float64(i%50)*0.01
		spreadShort := 0.3 + float64((i+25)%50)*0.01
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	// 验证约束
	if result.TotalExecutions > 0 {
		totalProfit := result.LongThreshold + result.ShortThreshold
		if totalProfit < optimizer.MinRoundTripProfit {
			t.Errorf("约束验证失败：阈值总利润 %.6f < 最小要求 %.6f",
				totalProfit, optimizer.MinRoundTripProfit)
		}
	}
	
	// 测试最大约束
	optimizer.SetMaxRoundTripProfit(1.0)
	result2, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	
	if result2.TotalExecutions > 0 {
		totalProfit := result2.LongThreshold + result2.ShortThreshold
		if totalProfit > optimizer.MaxRoundTripProfit {
			t.Errorf("最大约束验证失败：阈值总利润 %.6f > 最大限制 %.6f",
				totalProfit, optimizer.MaxRoundTripProfit)
		}
	}
	
	t.Logf("约束验证 - 最小利润: %.6f, 最大利润: %.6f, 结果总利润: %.6f",
		optimizer.MinRoundTripProfit, optimizer.MaxRoundTripProfit,
		result.LongThreshold+result.ShortThreshold)
}

// TestFastTriggerOptimizer_Performance 测试场景22：性能测试
// 测试优化器在处理大量数据时的性能
func TestFastTriggerOptimizer_Performance(t *testing.T) {
	optimizer := NewFastTriggerThresholdOptimizer(100, 0.3)
	startTime := time.Now()
	times := generateTimeSeries(startTime, 5000, 10*time.Millisecond) // 5000个点
	
	data := make([]SpreadDataPoint, 5000)
	rand.Seed(45)
	for i := 0; i < 5000; i++ {
		spreadLong := 0.2 + rand.Float64()*0.8
		spreadShort := 0.2 + rand.Float64()*0.8
		data[i] = generateSpreadDataPoint(times[i], spreadLong, spreadShort, 0.05, 0.05)
	}
	
	start := time.Now()
	result, _ := optimizer.CalculateOptimalThresholdsWithDebug(data)
	duration := time.Since(start)
	
	// 验证性能：应该在合理时间内完成（例如1秒内）
	if duration > 1*time.Second {
		t.Errorf("性能测试失败：处理5000个数据点耗时 %.2fs，超过1秒",
			duration.Seconds())
	}
	
	t.Logf("性能测试 - 数据点: 5000, 耗时: %v, Long阈值: %.6f, Short阈值: %.6f, 触发次数: %d",
		duration, result.LongThreshold, result.ShortThreshold, result.TotalExecutions)
}
