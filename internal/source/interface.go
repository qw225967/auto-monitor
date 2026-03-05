package source

import "context"

// DataSource 数据源接口（输出类型不限于价差）
// 支持多数据源扩展：价差、symbol 列表等
type DataSource interface {
	Name() string
	DataType() string
	Fetch(ctx context.Context) (interface{}, error)
}
