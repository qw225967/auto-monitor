package opportunities

import (
	"math"
	"sort"
	"strings"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// maxSpreadAnomaly 价差超过此阈值视为非同一币种（单位/代币不匹配），过滤掉
const maxSpreadAnomaly = 500.0

// ComputeCexDex 从价差数据和链上价格计算 CEX-DEX 套利机会
// chainPrices: key "asset:chainID" -> price
// 1) 若 SpreadItem 有 BuyPrice/SellPrice，直接计算
// 2) 若无价格，用 DEX 价 + spread_percent 估算 CEX 价（假设 DEX≈mid，buy≈dex/(1+spread/200)）
// 3) 价差 > maxCexDexSpread 视为异常数据（如单位不匹配），过滤
func ComputeCexDex(items []model.SpreadItem, chainPrices map[string]float64, threshold float64) []model.OverviewRow {
	var rows []model.OverviewRow
	seen := make(map[string]bool)
	for _, it := range items {
		if it.BuyExchange == it.SellExchange {
			continue
		}
		base, _ := tokenregistry.SymbolToAsset(it.Symbol)
		if base == "" {
			continue
		}
		hasExplicitPrice := it.BuyPrice > 0 || it.SellPrice > 0
		for key, dexPrice := range chainPrices {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) != 2 || parts[0] != base {
				continue
			}
			chainID := parts[1]
			if dexPrice <= 0 {
				continue
			}
			if hasExplicitPrice {
				if it.BuyPrice > 0 {
					spread := math.Abs(dexPrice-it.BuyPrice) / it.BuyPrice * 100
					if spread >= threshold && spread <= maxSpreadAnomaly {
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
					if spread >= threshold && spread <= maxSpreadAnomaly {
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
			} else if it.SpreadPercent > 0 {
				// 无显式价格时估算：CEX 价差 S% 时，CEX-DEX 约 S/2%（每 CEX 对只展示一条）
				estSpread := it.SpreadPercent / 2
				if estSpread >= threshold {
					k := it.Symbol + ":est:" + it.BuyExchange + ":" + it.SellExchange
					if !seen[k] {
						seen[k] = true
						rows = append(rows, model.OverviewRow{
							Type:          model.OppTypeCexDex,
							Symbol:        it.Symbol,
							PathDisplay:   it.BuyExchange + " ↔ " + it.SellExchange + " ↔ 链(估)",
							BuyExchange:   it.BuyExchange,
							SellExchange:  "Chain",
							SpreadPercent: estSpread,
						})
					}
				}
			}
		}
	}
	return rows
}

// ComputeDexDex 从链上价格计算 DEX-DEX 套利机会（同资产不同链）
// 价差公式: |p1-p2|/min(p1,p2)*100，显示买低卖高方向
func ComputeDexDex(chainPrices map[string]float64, threshold float64) []model.OverviewRow {
	type pair struct {
		asset, buyChain, sellChain string // buyChain 价低，sellChain 价高
		spread                     float64
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
			if spread >= threshold && spread <= maxSpreadAnomaly {
				kk := asset + ":" + c1 + ":" + c2
				if c1 > c2 {
					kk = asset + ":" + c2 + ":" + c1
				}
				if !seen[kk] {
					seen[kk] = true
					buyChain, sellChain := c1, c2
					if p1 > p2 {
						buyChain, sellChain = c2, c1
					}
					pairs = append(pairs, pair{asset: asset, buyChain: buyChain, sellChain: sellChain, spread: spread})
				}
			}
		}
	}
	var rows []model.OverviewRow
	for _, p := range pairs {
		rows = append(rows, model.OverviewRow{
			Type:          model.OppTypeDexDex,
			Symbol:        p.asset + "USDT",
			PathDisplay:   "Chain_" + p.buyChain + "(低) → Chain_" + p.sellChain + "(高)",
			BuyExchange:   "Chain_" + p.buyChain,
			SellExchange:  "Chain_" + p.sellChain,
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
