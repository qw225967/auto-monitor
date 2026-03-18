package seeingstone

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// MockSpreadData 开发用模拟价差数据（无 API 时使用）
var MockSpreadData []model.SpreadItem

// FetchMock 返回模拟数据
func FetchMock(ctx context.Context) ([]model.SpreadItem, error) {
	_ = ctx
	return MockSpreadData, nil
}
