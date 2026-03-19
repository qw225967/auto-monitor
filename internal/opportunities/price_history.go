package opportunities

import (
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

// GetSlope 返回短窗口（5分钟）内的价格斜率，使用最小二乘法拟合。
// 斜率单位：归一化价格变化率 / 分钟。
// hasData 为 false 表示窗口内有效数据点不足（< 2），调用方应跳过该维度的阈值校验。
func (p *PriceHistory) GetSlope(symbol, exchange string) (slope float64, hasData bool) {
	return p.GetSlopeInWindow(symbol, exchange, 5*time.Minute)
}

// GetSlopeLong 返回长窗口（1小时）内的价格斜率，使用最小二乘法拟合。
// hasData 为 false 表示窗口内有效数据点不足（< 2），调用方应跳过该维度的阈值校验。
func (p *PriceHistory) GetSlopeLong(symbol, exchange string) (slope float64, hasData bool) {
	return p.GetSlopeInWindow(symbol, exchange, 60*time.Minute)
}

// GetSlopeInWindow 在指定时间窗口内，用最小二乘法拟合价格序列，返回归一化斜率（变化率/分钟）。
// 归一化方式：以窗口内第一个有效价格为基准，计算相对涨跌幅后再做 OLS 拟合。
// hasData 为 false 时表示数据不足，斜率值无意义，调用方应跳过阈值校验（而非过滤）。
// 这样保证"有多少数据用多少数据计算，随时间积累越来越准确"的语义。
func (p *PriceHistory) GetSlopeInWindow(symbol, exchange string, window time.Duration) (slope float64, hasData bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := priceHistoryKey(symbol, exchange)
	points := p.histories[key]

	now := time.Now()
	cutoff := now.Add(-window)

	// 收集窗口内有效价格点（不要求满窗口，有多少用多少）
	var windowPoints []model.PricePoint
	for _, pt := range points {
		if pt.Timestamp.After(cutoff) && pt.Price > 0 {
			windowPoints = append(windowPoints, pt)
		}
	}

	// 至少需要 2 个点才能拟合直线
	if len(windowPoints) < 2 {
		return 0, false
	}

	// 以第一个点的价格和时间为基准，构造 OLS 输入
	basePrice := windowPoints[0].Price
	baseTime := windowPoints[0].Timestamp

	// OLS: y = a + b*x，求 b（斜率）
	// x = 距基准时间的分钟数，y = 相对于基准价格的归一化涨跌幅
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
		// 所有点时间戳相同，无法拟合
		return 0, false
	}

	return (n*sumXY - sumX*sumY) / denominator, true
}

func (p *PriceHistory) GetVolumeSpike(symbol, exchange string, threshold float64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := priceHistoryKey(symbol, exchange)
	points := p.histories[key]
	if len(points) < 2 {
		return false
	}

	now := time.Now()
	oneMinAgo := now.Add(-1 * time.Minute)
	sixMinAgo := now.Add(-6 * time.Minute)

	var recentVol, olderVol float64
	var recentCount, olderCount int

	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Timestamp.After(oneMinAgo) {
			recentVol += points[i].Volume
			recentCount++
		} else if points[i].Timestamp.After(sixMinAgo) {
			olderVol += points[i].Volume
			olderCount++
		}
	}

	if recentCount == 0 || olderCount == 0 {
		return false
	}

	recentAvg := recentVol / float64(recentCount)
	olderAvg := olderVol / float64(olderCount)

	return recentAvg/olderAvg > threshold
}
