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
	"github.com/qw225967/auto-monitor/internal/opportunities"
	"golang.org/x/sync/errgroup"
)

// pathKey 探测路径键
type pathKey struct {
	symbol, buy, sell string
}

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
// chainPrices 可选，key "asset:chainID"，用于 CEX-DEX、DEX-DEX 机会计算
func (r *Runner) RunDetect(ctx context.Context, items []model.SpreadItem, chainPrices map[string]float64) (*model.OverviewResponse, error) {
	agg := aggregator.Aggregate(items, r.threshold)
	cexDex := opportunities.ComputeCexDex(items, chainPrices, r.threshold)
	dexDex := opportunities.ComputeDexDex(chainPrices, r.threshold)

	if len(agg) == 0 && len(cexDex) == 0 && len(dexDex) == 0 {
		return &model.OverviewResponse{Overview: []model.OverviewRow{}}, nil
	}
	if len(agg) == 0 && len(chainPrices) > 0 {
		// 仅 CEX-DEX/DEX-DEX，需探测后附加路径
		return r.runDetectCexDexDexOnly(ctx, cexDex, dexDex)
	}

	// 用价差 symbol 刷新充提网络（跟随 30s 探测周期，从交易所 API 实时获取）
	if refresher, ok := r.det.(detector.RegistryRefresher); ok {
		symbols := extractSymbolsFromItems(items)
		refresher.RefreshNetworks(ctx, symbols)
	}

	// 收集所有 (symbol, buyEx, sellEx) 用于探测：CEX-CEX + CEX-DEX
	pathSet := make(map[pathKey]model.PathItem)
	for symbol, paths := range agg {
		for _, p := range paths {
			k := pathKey{symbol, p.BuyExchange, p.SellExchange}
			pathSet[k] = p
		}
	}
	for _, row := range cexDex {
		k := pathKey{row.Symbol, row.BuyExchange, row.SellExchange}
		if _, ok := pathSet[k]; !ok {
			pathSet[k] = model.PathItem{
				Symbol:        row.Symbol,
				BuyExchange:   row.BuyExchange,
				SellExchange:  row.SellExchange,
				SpreadPercent: row.SpreadPercent,
			}
		}
	}
	for _, row := range dexDex {
		k := pathKey{row.Symbol, row.BuyExchange, row.SellExchange}
		if _, ok := pathSet[k]; !ok {
			pathSet[k] = model.PathItem{
				Symbol:        row.Symbol,
				BuyExchange:   row.BuyExchange,
				SellExchange:  row.SellExchange,
				SpreadPercent: row.SpreadPercent,
			}
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

	// 组装 AggregatedInput（仅 CEX-CEX，builder 只处理交易所间）
	aggKeys := make(map[pathKey]bool)
	for symbol, paths := range agg {
		for _, p := range paths {
			aggKeys[pathKey{symbol, p.BuyExchange, p.SellExchange}] = true
		}
	}
	input := &builder.AggregatedInput{
		Paths:           make(map[string][]builder.PathItemWithRoutes),
		SpreadThreshold: r.threshold,
	}
	for _, res := range results {
		if !aggKeys[res.key] {
			continue
		}
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
	resp := out.(*model.OverviewResponse)

	// 合并 CEX-DEX、DEX-DEX，并附加探测路径
	resultMap := make(map[pathKey][]builder.PhysicalPathRow)
	for _, res := range results {
		var physRows []builder.PhysicalPathRow
		for _, p := range res.phys {
			physRows = append(physRows, builder.PhysicalPathRow{
				PathID:       p.PathID,
				PhysicalFlow: p.PhysicalFlow(),
				Status:       p.OverallStatus,
			})
		}
		resultMap[res.key] = physRows
	}
	cexDexWithPaths := attachPathsToRows(cexDex, resultMap)
	dexDexWithPaths := attachPathsToRows(dexDex, resultMap)
	resp.Overview = opportunities.MergeAndSort(resp.Overview, cexDexWithPaths, dexDexWithPaths)
	return resp, nil
}

// runDetectCexDexDexOnly 仅 CEX-DEX/DEX-DEX 时的探测与组装
func (r *Runner) runDetectCexDexDexOnly(ctx context.Context, cexDex, dexDex []model.OverviewRow) (*model.OverviewResponse, error) {
	pathSet := make(map[pathKey]model.OverviewRow)
	var symbols []string
	seen := make(map[string]bool)
	for _, row := range cexDex {
		k := pathKey{row.Symbol, row.BuyExchange, row.SellExchange}
		pathSet[k] = row
		if row.Symbol != "" && !seen[row.Symbol] {
			seen[row.Symbol] = true
			symbols = append(symbols, row.Symbol)
		}
	}
	for _, row := range dexDex {
		k := pathKey{row.Symbol, row.BuyExchange, row.SellExchange}
		pathSet[k] = row
		if row.Symbol != "" && !seen[row.Symbol] {
			seen[row.Symbol] = true
			symbols = append(symbols, row.Symbol)
		}
	}
	if len(pathSet) == 0 {
		return &model.OverviewResponse{Overview: opportunities.MergeAndSort(nil, cexDex, dexDex)}, nil
	}
	if refresher, ok := r.det.(detector.RegistryRefresher); ok {
		refresher.RefreshNetworks(ctx, symbols)
	}

	type result struct {
		key  pathKey
		phys []model.PhysicalPath
	}
	var mu sync.Mutex
	var results []result
	g, gctx := errgroup.WithContext(ctx)
	for k := range pathSet {
		k := k
		g.Go(func() error {
			paths, err := r.det.DetectRoutes(gctx, k.symbol, k.buy, k.sell)
			if err != nil {
				log.Printf("[Runner] detect %s %s->%s: %v", k.symbol, k.buy, k.sell, err)
				return nil
			}
			mu.Lock()
			results = append(results, result{key: k, phys: paths})
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	resultMap := make(map[pathKey][]builder.PhysicalPathRow)
	for _, res := range results {
		var physRows []builder.PhysicalPathRow
		for _, p := range res.phys {
			physRows = append(physRows, builder.PhysicalPathRow{
				PathID:       p.PathID,
				PhysicalFlow: p.PhysicalFlow(),
				Status:       p.OverallStatus,
			})
		}
		resultMap[res.key] = physRows
	}
	cexDexWithPaths := attachPathsToRows(cexDex, resultMap)
	dexDexWithPaths := attachPathsToRows(dexDex, resultMap)
	return &model.OverviewResponse{
		Overview: opportunities.MergeAndSort(nil, cexDexWithPaths, dexDexWithPaths),
	}, nil
}

// extractSymbolsFromItems 从价差数据提取去重 symbol
func extractSymbolsFromItems(items []model.SpreadItem) []string {
	seen := make(map[string]bool)
	for _, it := range items {
		if it.Symbol != "" {
			seen[it.Symbol] = true
		}
	}
	var out []string
	for s := range seen {
		out = append(out, s)
	}
	return out
}

// attachPathsToRows 为 CEX-DEX/DEX-DEX 行附加探测路径
func attachPathsToRows(rows []model.OverviewRow, resultMap map[pathKey][]builder.PhysicalPathRow) []model.OverviewRow {
	var out []model.OverviewRow
	for _, row := range rows {
		k := pathKey{row.Symbol, row.BuyExchange, row.SellExchange}
		physRows, ok := resultMap[k]
		if !ok {
			out = append(out, row)
			continue
		}
		availableCount := 0
		var detailPaths []model.DetailPathRow
		for _, pp := range physRows {
			if pp.Status == "ok" {
				availableCount++
			}
			detailPaths = append(detailPaths, model.DetailPathRow{
				PathID:       pp.PathID,
				PhysicalFlow: pp.PhysicalFlow,
				Status:       pp.Status,
			})
		}
		row.AvailablePathCount = availableCount
		row.DetailPaths = detailPaths
		out = append(out, row)
	}
	return out
}
