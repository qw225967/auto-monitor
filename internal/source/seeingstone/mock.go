package seeingstone

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// MockSpreadData 开发用模拟价差数据（无 API 时使用）
var MockSpreadData = []model.SpreadItem{
	{Symbol: "POWERUSDT", BuyExchange: "BITGET", SellExchange: "GATE", SpreadPercent: 20.38, UpdatedAt: ""},
	{Symbol: "POWERUSDT", BuyExchange: "BINANCE", SellExchange: "OKX", SpreadPercent: 15.2, UpdatedAt: ""},
	{Symbol: "ETHUSDT", BuyExchange: "BITGET", SellExchange: "GATE", SpreadPercent: 2.5, UpdatedAt: ""},
}

// FetchMock 返回模拟数据
func FetchMock(ctx context.Context) ([]model.SpreadItem, error) {
	_ = ctx
	return MockSpreadData, nil
}
