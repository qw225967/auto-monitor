package arbitrage

import (
	"context"
	"sort"

	"github.com/qw225967/auto-monitor/internal/builder"
	"github.com/qw225967/auto-monitor/internal/model"
)

const TableType = "arbitrage_overview"

type ArbitrageBuilder struct{}

func New() *ArbitrageBuilder {
	return &ArbitrageBuilder{}
}

func (b *ArbitrageBuilder) Type() string {
	return TableType
}

func (b *ArbitrageBuilder) Build(ctx context.Context, input *builder.AggregatedInput) (interface{}, error) {
	_ = ctx
	if input == nil || input.Paths == nil {
		return &model.OverviewResponse{Overview: []model.OverviewRow{}}, nil
	}

	var rows []model.OverviewRow
	for symbol, items := range input.Paths {
		if len(items) == 0 {
			continue
		}
		// 取价差最大的作为主表展示
		best := items[0]
		for _, item := range items[1:] {
			if item.SpreadPercent > best.SpreadPercent {
				best = item
			}
		}

		availableCount := 0
		var detailPaths []model.DetailPathRow
		for _, pp := range best.PhysicalPaths {
			if pp.Status == "ok" {
				availableCount++
			}
			detailPaths = append(detailPaths, model.DetailPathRow{
				PathID:       pp.PathID,
				PhysicalFlow: pp.PhysicalFlow,
				Status:       pp.Status,
			})
		}
		sort.Slice(detailPaths, func(i, j int) bool { return detailPaths[i].PathID < detailPaths[j].PathID })

		rows = append(rows, model.OverviewRow{
			Type:               model.OppTypeCexCex,
			Symbol:             symbol,
			PathDisplay:        best.BuyExchange + " → " + best.SellExchange,
			BuyExchange:        best.BuyExchange,
			SellExchange:       best.SellExchange,
			SpreadPercent:      best.SpreadPercent,
			AvailablePathCount: availableCount,
			DetailPaths:        detailPaths,
		})
	}

	// 按价差降序
	sort.Slice(rows, func(i, j int) bool { return rows[i].SpreadPercent > rows[j].SpreadPercent })

	return &model.OverviewResponse{Overview: rows}, nil
}
