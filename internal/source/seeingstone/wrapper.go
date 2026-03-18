package seeingstone

import (
	"context"

	"github.com/qw225967/auto-monitor/internal/model"
)

// ToSpreadItems 将 DataSource.Fetch 结果转为 []SpreadItem
func ToSpreadItems(v interface{}) ([]model.SpreadItem, error) {
	if v == nil {
		return nil, nil
	}
	items, ok := v.([]model.SpreadItem)
	if !ok {
		return nil, nil
	}
	return items, nil
}

// FetchSpread 便捷方法：拉取并返回 []SpreadItem
func (a *Adapter) FetchSpread(ctx context.Context) ([]model.SpreadItem, error) {
	v, err := a.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	return ToSpreadItems(v)
}
