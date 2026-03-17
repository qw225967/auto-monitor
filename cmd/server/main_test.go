package main

import (
	"testing"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

func TestAssetPriorityTrackerTopAssets(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	tracker := newAssetPriorityTracker(0.9, 1*time.Hour)

	// BTC 高频 + 高 spread
	tracker.Observe([]model.SpreadItem{
		{Symbol: "BTCUSDT", SpreadPercent: 1.1},
		{Symbol: "BTCUSDT", SpreadPercent: 2.8},
		{Symbol: "ETHUSDT", SpreadPercent: 2.5},
	}, now)
	// 第二次观测强化 BTC 频次
	tracker.Observe([]model.SpreadItem{
		{Symbol: "BTCUSDT", SpreadPercent: 2.2},
		{Symbol: "SOLUSDT", SpreadPercent: 1.7},
	}, now.Add(5*time.Minute))

	top := tracker.TopAssets(2, now.Add(10*time.Minute))
	if len(top) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(top))
	}
	if top[0] != "BTC" {
		t.Fatalf("expected BTC ranked first, got %v", top)
	}
}
