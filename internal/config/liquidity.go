package config

import "sync"

var (
	liquidityThreshold   float64 // USDT，0 表示不限制，使用默认 500 询价
	liquidityThresholdMu sync.RWMutex
)

// SetLiquidityThreshold 设置流动性阈值（USDT），仅存内存
// 0 或负数表示不限制，询价使用 500 USDT；>0 时使用该金额询价，失败（如流动性不足）则过滤
func SetLiquidityThreshold(threshold float64) {
	liquidityThresholdMu.Lock()
	defer liquidityThresholdMu.Unlock()
	liquidityThreshold = threshold
}

// GetLiquidityThreshold 获取当前流动性阈值
func GetLiquidityThreshold() float64 {
	liquidityThresholdMu.RLock()
	defer liquidityThresholdMu.RUnlock()
	return liquidityThreshold
}

// GetQuoteAmountUSDT 获取询价使用的 USDT 金额
// 若设置了流动性阈值且 >0，则用该值；否则用默认 500
func GetQuoteAmountUSDT() float64 {
	t := GetLiquidityThreshold()
	if t > 0 {
		return t
	}
	return 500
}
