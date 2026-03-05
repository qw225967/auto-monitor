package runner

import (
	"context"
	"log"
	"sync"

	"github.com/qw225967/auto-monitor/internal/aggregator"
	"github.com/qw225967/auto-monitor/internal/builder"
	"github.com/qw225967/auto-monitor/internal/builder/arbitrage"
	"github.com/qw225967/auto-monitor/internal/detector"
	"github.com/qw225967/auto-monitor/internal/model"
	"golang.org/x/sync/errgroup"
)

// Runner 主流程编排：聚合 → 探测 → 表格组装
type Runner struct {
	det       detector.Detector
	threshold float64
}

// New 创建 Runner
func New(det detector.Detector, threshold float64) *Runner {
	return &Runner{
		det:       det,
		threshold: threshold,
	}
}

// RunDetect 执行一轮：聚合 + 全 symbol 通路探测 + 表格组装
func (r *Runner) RunDetect(ctx context.Context, items []model.SpreadItem) (*model.OverviewResponse, error) {
	agg := aggregator.Aggregate(items, r.threshold)
	if len(agg) == 0 {
		return &model.OverviewResponse{Overview: []model.OverviewRow{}}, nil
	}

	// 收集所有 (symbol, buyEx, sellEx) 用于探测
	type pathKey struct {
		symbol, buy, sell string
	}
	pathSet := make(map[pathKey]model.PathItem)
	for symbol, paths := range agg {
		for _, p := range paths {
			k := pathKey{symbol, p.BuyExchange, p.SellExchange}
			pathSet[k] = p
		}
	}

	// 并发探测
	type result struct {
		key  pathKey
		item model.PathItem
		phys []model.PhysicalPath
	}
	var mu sync.Mutex
	results := make([]result, 0, len(pathSet))

	g, gctx := errgroup.WithContext(ctx)
	for k, item := range pathSet {
		k, item := k, item
		g.Go(func() error {
			paths, err := r.det.DetectRoutes(gctx, k.symbol, k.buy, k.sell)
			if err != nil {
				log.Printf("[Runner] detect %s %s->%s: %v", k.symbol, k.buy, k.sell, err)
				return nil // 单路失败不中断
			}
			mu.Lock()
			results = append(results, result{key: k, item: item, phys: paths})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	// 组装 AggregatedInput
	input := &builder.AggregatedInput{
		Paths:           make(map[string][]builder.PathItemWithRoutes),
		SpreadThreshold: r.threshold,
	}
	for _, res := range results {
		var physRows []builder.PhysicalPathRow
		for _, p := range res.phys {
			physRows = append(physRows, builder.PhysicalPathRow{
				PathID:       p.PathID,
				PhysicalFlow: p.PhysicalFlow(),
				Status:       p.OverallStatus,
			})
		}
		pwr := builder.PathItemWithRoutes{
			Symbol:        res.item.Symbol,
			BuyExchange:   res.item.BuyExchange,
			SellExchange:  res.item.SellExchange,
			SpreadPercent: res.item.SpreadPercent,
			PhysicalPaths: physRows,
		}
		input.Paths[res.key.symbol] = append(input.Paths[res.key.symbol], pwr)
	}

	tb := arbitrage.New()
	out, err := tb.Build(ctx, input)
	if err != nil {
		return nil, err
	}
	return out.(*model.OverviewResponse), nil
}
