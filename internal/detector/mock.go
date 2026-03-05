package detector

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// MockDetector 模拟路由探测（待迁移时替换为真实实现）
var _ Detector = (*MockDetector)(nil)

type MockDetector struct{}

func NewMock() *MockDetector {
	return &MockDetector{}
}

// DetectRoutes 返回模拟的物理通路
func (m *MockDetector) DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error) {
	_ = ctx
	_ = symbol
	paths := []model.PhysicalPath{
		{
			PathID: "Path_01",
			Hops: []model.Hop{
				{FromNode: buyExchange, EdgeDesc: "提现BSC", ToNode: "BSC链", Status: "ok"},
				{FromNode: "BSC链", EdgeDesc: "跨链桥A", ToNode: "ETH链", Status: "ok"},
				{FromNode: "ETH链", EdgeDesc: "充值ETH", ToNode: sellExchange, Status: "ok"},
			},
			OverallStatus: "ok",
		},
		{
			PathID: "Path_02",
			Hops: []model.Hop{
				{FromNode: buyExchange, EdgeDesc: "提现TRC20", ToNode: "TRON链", Status: "maintenance"},
				{FromNode: "TRON链", EdgeDesc: "充值TRON", ToNode: sellExchange, Status: "maintenance"},
			},
			OverallStatus: "maintenance",
		},
	}
	return paths, nil
}
