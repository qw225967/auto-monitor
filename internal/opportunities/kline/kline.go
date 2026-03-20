package kline

import (
	"sort"
	"sync"
	"time"
)

// KlinePoint 单根 K 线（OHLCV）
type KlinePoint struct {
	Timestamp time.Time
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
}

// Store K 线存储，按 symbol:exchange 索引
type Store struct {
	mu       sync.RWMutex
	data     map[string][]KlinePoint
	maxBars  int
	interval time.Duration // 1m
}

// NewStore 创建 K 线存储
func NewStore(maxBars int) *Store {
	return &Store{
		data:    make(map[string][]KlinePoint),
		maxBars: maxBars,
	}
}

func key(symbol, exchange string) string {
	return symbol + ":" + exchange
}

// Append 追加 K 线（去重按时间戳）
func (s *Store) Append(symbol, exchange string, bars []KlinePoint) {
	if len(bars) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(symbol, exchange)
	existing := s.data[k]
	seen := make(map[int64]bool)
	for _, b := range existing {
		seen[b.Timestamp.Unix()] = true
	}
	for _, b := range bars {
		ts := b.Timestamp.Unix()
		if !seen[ts] {
			seen[ts] = true
			existing = append(existing, b)
		}
	}
	sort.Slice(existing, func(i, j int) bool {
		return existing[i].Timestamp.Before(existing[j].Timestamp)
	})
	if len(existing) > s.maxBars {
		existing = existing[len(existing)-s.maxBars:]
	}
	s.data[k] = existing
}

// GetBars 获取最近 N 根 K 线
func (s *Store) GetBars(symbol, exchange string, n int) []KlinePoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := key(symbol, exchange)
	bars := s.data[k]
	if len(bars) <= n {
		return bars
	}
	return bars[len(bars)-n:]
}

// GetClose 获取最近一根的收盘价
func (s *Store) GetClose(symbol, exchange string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := key(symbol, exchange)
	bars := s.data[k]
	if len(bars) == 0 {
		return 0, false
	}
	return bars[len(bars)-1].Close, true
}

// GetVolumeInWindow 获取时间窗口内的成交量之和
func (s *Store) GetVolumeInWindow(symbol, exchange string, since time.Time) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k := key(symbol, exchange)
	bars := s.data[k]
	var vol float64
	for _, b := range bars {
		if b.Timestamp.After(since) {
			vol += b.Volume
		}
	}
	return vol
}
