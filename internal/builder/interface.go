package builder

import "context"

// AggregatedInput 聚合后的输入数据（供不同 builder 消费）
type AggregatedInput struct {
	Paths      map[string][]PathItemWithRoutes
	SpreadThreshold float64
}

// PathItemWithRoutes 带探测结果的路径项
type PathItemWithRoutes struct {
	Symbol        string
	BuyExchange   string
	SellExchange  string
	SpreadPercent float64
	PhysicalPaths []PhysicalPathRow
}

// PhysicalPathRow 物理路径行
type PhysicalPathRow struct {
	PathID       string
	PhysicalFlow string
	Status       string
}

// TableBuilder 表格构建器接口（便于后续新增监控表格）
type TableBuilder interface {
	Type() string
	Build(ctx context.Context, input *AggregatedInput) (interface{}, error)
}
