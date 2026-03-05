package aggregator

import (
	"github.com/qw225967/auto-monitor/internal/model"
)

// Aggregate 按 symbol 分组并过滤低于阈值的项
func Aggregate(items []model.SpreadItem, threshold float64) model.AggregatedPaths {
	result := make(model.AggregatedPaths)
	for _, item := range items {
		if item.SpreadPercent < threshold {
			continue
		}
		path := model.PathItem{
			Symbol:        item.Symbol,
			BuyExchange:   item.BuyExchange,
			SellExchange:  item.SellExchange,
			SpreadPercent: item.SpreadPercent,
		}
		result[item.Symbol] = append(result[item.Symbol], path)
	}
	return result
}
