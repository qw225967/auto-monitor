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

func (p *PriceHistory) GetSlope(symbol, exchange string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := priceHistoryKey(symbol, exchange)
	points := p.histories[key]
	if len(points) < 2 {
		return 0
	}

	now := time.Now()
	cutoff := now.Add(-5 * time.Minute)

	var recentPoints []model.PricePoint
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Timestamp.After(cutoff) && points[i].Price > 0 {
			recentPoints = append([]model.PricePoint{points[i]}, recentPoints...)
		}
	}

	if len(recentPoints) < 2 {
		return 0
	}

	first := recentPoints[0]
	last := recentPoints[len(recentPoints)-1]

	duration := last.Timestamp.Sub(first.Timestamp).Minutes()
	if duration <= 0 {
		return 0
	}

	return (last.Price - first.Price) / first.Price / duration
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
