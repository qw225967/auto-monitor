package opportunities

import (
	"math"
	"sort"
	"strings"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// ComputeCexDex 从价差数据和链上价格计算 CEX-DEX 套利机会
// chainPrices: key "asset:chainID" -> price
// 仅当 SpreadItem 有 BuyPrice/SellPrice 时计算
func ComputeCexDex(items []model.SpreadItem, chainPrices map[string]float64, threshold float64) []model.OverviewRow {
	var rows []model.OverviewRow
	seen := make(map[string]bool)
	for _, it := range items {
		base, _ := tokenregistry.SymbolToAsset(it.Symbol)
		if base == "" {
			continue
		}
		if it.BuyPrice <= 0 && it.SellPrice <= 0 {
			continue
		}
		for key, dexPrice := range chainPrices {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) != 2 || parts[0] != base {
				continue
			}
			chainID := parts[1]
			if dexPrice <= 0 {
				continue
			}
			if it.BuyPrice > 0 {
				spread := math.Abs(dexPrice-it.BuyPrice) / it.BuyPrice * 100
				if spread >= threshold {
					k := it.Symbol + ":" + it.BuyExchange + ":Chain_" + chainID
					if !seen[k] {
						seen[k] = true
						rows = append(rows, model.OverviewRow{
							Type:          model.OppTypeCexDex,
							Symbol:        it.Symbol,
							PathDisplay:   it.BuyExchange + " ↔ Chain_" + chainID,
							BuyExchange:   it.BuyExchange,
							SellExchange:  "Chain_" + chainID,
							SpreadPercent: spread,
						})
					}
				}
			}
			if it.SellPrice > 0 {
				spread := math.Abs(dexPrice-it.SellPrice) / it.SellPrice * 100
				if spread >= threshold {
					k := it.Symbol + ":Chain_" + chainID + ":" + it.SellExchange
					if !seen[k] {
						seen[k] = true
						rows = append(rows, model.OverviewRow{
							Type:          model.OppTypeCexDex,
							Symbol:        it.Symbol,
							PathDisplay:   "Chain_" + chainID + " ↔ " + it.SellExchange,
							BuyExchange:   "Chain_" + chainID,
							SellExchange:  it.SellExchange,
							SpreadPercent: spread,
						})
					}
				}
			}
		}
	}
	return rows
}

// ComputeDexDex 从链上价格计算 DEX-DEX 套利机会（同资产不同链）
func ComputeDexDex(chainPrices map[string]float64, threshold float64) []model.OverviewRow {
	type pair struct {
		asset, c1, c2 string
		spread         float64
	}
	var pairs []pair
	seen := make(map[string]bool)
	for key, p1 := range chainPrices {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 || p1 <= 0 {
			continue
		}
		asset, c1 := parts[0], parts[1]
		for key2, p2 := range chainPrices {
			parts2 := strings.SplitN(key2, ":", 2)
			if len(parts2) != 2 || parts2[0] != asset || parts2[1] == c1 {
				continue
			}
			c2 := parts2[1]
			if p2 <= 0 {
				continue
			}
			minP := math.Min(p1, p2)
			spread := math.Abs(p1-p2) / minP * 100
			if spread >= threshold {
				kk := asset + ":" + c1 + ":" + c2
				if c1 > c2 {
					kk = asset + ":" + c2 + ":" + c1
				}
				if !seen[kk] {
					seen[kk] = true
					pairs = append(pairs, pair{asset: asset, c1: c1, c2: c2, spread: spread})
				}
			}
		}
	}
	var rows []model.OverviewRow
	for _, p := range pairs {
		rows = append(rows, model.OverviewRow{
			Type:          model.OppTypeDexDex,
			Symbol:        p.asset + "USDT",
			PathDisplay:   "Chain_" + p.c1 + " ↔ Chain_" + p.c2,
			BuyExchange:   "Chain_" + p.c1,
			SellExchange:  "Chain_" + p.c2,
			SpreadPercent: p.spread,
		})
	}
	return rows
}

// MergeAndSort 合并 CEX-CEX、CEX-DEX、DEX-DEX，按价差降序
func MergeAndSort(cexCex, cexDex, dexDex []model.OverviewRow) []model.OverviewRow {
	var all []model.OverviewRow
	all = append(all, cexCex...)
	all = append(all, cexDex...)
	all = append(all, dexDex...)
	sort.Slice(all, func(i, j int) bool { return all[i].SpreadPercent > all[j].SpreadPercent })
	return all
}
