package detector

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// Detector 路由探测接口
// TODO: 待迁移 - 接入实际的路由探测模块（HTTP 或本地包）
type Detector interface {
	// DetectRoutes 探测 symbol 从 buyExchange 到 sellExchange 的所有物理通路
	DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error)
}
