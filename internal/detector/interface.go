package detector

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// Detector 路由探测接口
type Detector interface {
	// DetectRoutes 探测 symbol 从 buyExchange 到 sellExchange 的所有物理通路
	DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error)
}

// RegistryRefresher 可选接口：探测前用价差 symbol 刷新充提网络（每 30s）
type RegistryRefresher interface {
	RefreshNetworks(ctx context.Context, symbols []string)
}
