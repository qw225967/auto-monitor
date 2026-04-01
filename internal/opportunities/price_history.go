package opportunities

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

func priceHistoryKey(symbol, exchange string) string {
	return symbol + ":" + strings.ToLower(exchange)
}

// PriceHistory 维护价格历史，用于斜率计算和交易量统计
type PriceHistory struct {
	mu         sync.RWMutex
	histories  map[string][]model.PricePoint
	maxPoints  int
	windowSize time.Duration
}

func NewPriceHistory(maxPoints int, windowSize time.Duration) *PriceHistory {
	return &PriceHistory{
		histories:  make(map[string][]model.PricePoint),
		maxPoints:  maxPoints,
		windowSize: windowSize,
	}
}

func (p *PriceHistory) Record(symbol, exchange string, price, volume float64) {
	p.RecordAt(symbol, exchange, price, volume, time.Now())
}

// RecordAt 在指定时间戳记录（K 线用此接口只填 volume，price 填 0）
func (p *PriceHistory) RecordAt(symbol, exchange string, price, volume float64, ts time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := priceHistoryKey(symbol, exchange)

	p.histories[key] = append(p.histories[key], model.PricePoint{
		Price:     price,
		Timestamp: ts,
		Volume:    volume,
	})

	now := time.Now()
	cutoff := now.Add(-p.windowSize)
	points := p.histories[key]
	i := 0
	for ; i < len(points); i++ {
		if points[i].Timestamp.After(cutoff) {
			break
		}
	}
	if i > 0 {
		p.histories[key] = points[i:]
	}

	if len(p.histories[key]) > p.maxPoints {
		p.histories[key] = p.histories[key][len(p.histories[key])-p.maxPoints:]
	}
}

// StatsCount 返回全局统计：总 key 数、5 分钟内有 Price>0 的 key 数（用于诊断）
func (p *PriceHistory) StatsCount() (totalKeys, keysWithPriceIn5m int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)
	for _, points := range p.histories {
		totalKeys++
		hasPrice := false
		for _, pt := range points {
			if pt.Timestamp.After(cutoff) && pt.Price > 0 {
				hasPrice = true
				break
			}
		}
		if hasPrice {
			keysWithPriceIn5m++
		}
	}
	return totalKeys, keysWithPriceIn5m
}

// CountPoints 返回总点数及 Price>0 的点数（用于诊断）
func (p *PriceHistory) CountPoints(symbol, exchange string) (total, withPrice int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key := priceHistoryKey(symbol, exchange)
	points := p.histories[key]
	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)
	for _, pt := range points {
		if pt.Timestamp.After(cutoff) {
			total++
			if pt.Price > 0 {
				withPrice++
			}
		}
	}
	return total, withPrice
}

// recordRaw 通用时序数据存储（内部方法，直接用 key 存储，不做 symbol:exchange 转换）
func (p *PriceHistory) recordRaw(key string, value float64, ts time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.histories[key] = append(p.histories[key], model.PricePoint{
		Price:     value,
		Timestamp: ts,
	})

	now := time.Now()
	cutoff := now.Add(-p.windowSize)
	points := p.histories[key]
	i := 0
	for ; i < len(points); i++ {
		if points[i].Timestamp.After(cutoff) {
			break
		}
	}
	if i > 0 {
		p.histories[key] = points[i:]
	}

	if len(p.histories[key]) > p.maxPoints {
		p.histories[key] = p.histories[key][len(p.histories[key])-p.maxPoints:]
	}
}

// GetSlopeInWindowByKey 按原始 key 在指定时间窗口内计算斜率（OLS 最小二乘法）
func (p *PriceHistory) GetSlopeInWindowByKey(key string, window time.Duration) (slope float64, hasData bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	points := p.histories[key]
	now := time.Now()
	cutoff := now.Add(-window)

	var windowPoints []model.PricePoint
	for _, pt := range points {
		if pt.Timestamp.After(cutoff) && pt.Price > 0 {
			windowPoints = append(windowPoints, pt)
		}
	}

	if len(windowPoints) < 2 {
		return 0, false
	}

	basePrice := windowPoints[0].Price
	baseTime := windowPoints[0].Timestamp

	var sumX, sumY, sumXX, sumXY float64
	n := float64(len(windowPoints))

	for _, pt := range windowPoints {
		x := pt.Timestamp.Sub(baseTime).Minutes()
		y := (pt.Price - basePrice) / basePrice
		sumX += x
		sumY += y
		sumXX += x * x
		sumXY += x * y
	}

	denominator := n*sumXX - sumX*sumX
	if denominator == 0 {
		return 0, false
	}

	return (n*sumXY - sumX*sumY) / denominator, true
}

// RecordOrderbookDepth 记录 top50 挂单量（USDT 计价）
func (p *PriceHistory) RecordOrderbookDepth(symbol string, totalQty float64) {
	p.recordRaw("depth:"+symbol, totalQty, time.Now())
}

// GetPriceSlopeAccel 计算价格斜率加速比（短窗口5min / 长窗口30min）。
// 判断"价格不下跌且突然上涨"：短斜率 > 0 且 短/长 >= 阈值。
// hasData=false 时表示数据不足，调用方必须过滤（不放行）。
func (p *PriceHistory) GetPriceSlopeAccel(symbol, exchange string) (accel float64, hasData bool) {
	key := priceHistoryKey(symbol, exchange)
	shortSlope, hasShort := p.GetSlopeInWindowByKey(key, 5*time.Minute)
	longSlope, hasLong := p.GetSlopeInWindowByKey(key, 30*time.Minute)

	if !hasShort || !hasLong {
		return 0, false // 数据不足，调用方必须过滤
	}
	// 短斜率 <= 0 表示价格下跌或横盘，不符合"突然上涨"条件
	if shortSlope <= 0 {
		return 0, true
	}
	if longSlope == 0 {
		return 2.0, true // 长期横盘突然上涨，视为加速
	}
	if longSlope < 0 {
		// 长期下跌但短期上涨，加速比取绝对值（方向反转）
		return shortSlope / math.Abs(longSlope), true
	}
	return shortSlope / longSlope, true
}

// GetDepthSlopeAccel 计算挂单量斜率加速比（短窗口5min / 长窗口30min）。
// 数据源：第一档 bid 挂单量（一手量，非 USDT 总深度）。
// hasData=false 时表示数据不足，调用方必须过滤（不放行）。
func (p *PriceHistory) GetDepthSlopeAccel(symbol string) (accel float64, hasData bool) {
	key := "depth:" + symbol
	shortSlope, hasShort := p.GetSlopeInWindowByKey(key, 5*time.Minute)
	longSlope, hasLong := p.GetSlopeInWindowByKey(key, 30*time.Minute)

	if !hasShort || !hasLong {
		return 0, false // 数据不足，调用方必须过滤
	}
	if shortSlope <= 0 {
		return 0, true
	}
	if longSlope == 0 {
		return 2.0, true // 长期横盘突然上涨，视为加速
	}
	if longSlope < 0 {
		return shortSlope / math.Abs(longSlope), true
	}
	return shortSlope / longSlope, true
}
