package opportunities

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// maxSpreadAnomaly 价差超过此阈值视为非同一币种（单位/代币不匹配），过滤掉
const maxSpreadAnomaly = 50.0

// chainDisplayName 链 ID 转展示名
var chainDisplayName = map[string]string{
	"1": "ETH", "56": "BSC", "195": "TRON", "137": "Polygon", "42161": "Arbitrum",
	"10": "OP", "8453": "Base", "43114": "AVAX",
}

func formatLiquidityUsd(v float64) string {
	if v <= 0 {
		return ""
	}
	if v >= 1e8 {
		return fmt.Sprintf("%.0f亿", v/1e8)
	}
	if v >= 1e4 {
		return fmt.Sprintf("%.0f万", v/1e4)
	}
	return fmt.Sprintf("%.0f", v)
}

// ComputeCexDex 从价差数据和链上价格计算 CEX-DEX 套利机会
// chainPrices: key "asset:chainID" -> price
// liquidity: key "asset:chainID" -> reserve_usd，用于流动性阈值过滤（阈值>0 时）
func ComputeCexDex(items []model.SpreadItem, chainPrices map[string]float64, threshold float64, liquidity map[string]float64) []model.OverviewRow {
	liqThreshold := config.GetLiquidityThreshold()
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
			// 流动性阈值过滤：仅当有数据且低于阈值时跳过；无数据(!ok)时不过滤，避免误杀
			if liqThreshold > 0 && liquidity != nil && len(liquidity) > 0 {
				if r, ok := liquidity[base+":"+chainID]; ok && r < liqThreshold {
					continue
				}
			}
			if hasExplicitPrice {
				if it.BuyPrice > 0 {
					spread := math.Abs(dexPrice-it.BuyPrice) / it.BuyPrice * 100
					if spread >= threshold && spread <= maxSpreadAnomaly {
						k := it.Symbol + ":" + it.BuyExchange + ":Chain_" + chainID
						if !seen[k] {
							seen[k] = true
							liqStr := ""
							if liquidity != nil {
								if r, ok := liquidity[base+":"+chainID]; ok && r > 0 {
									name := chainDisplayName[chainID]
									if name == "" {
										name = "Chain_" + chainID
									}
									liqStr = name + ": " + formatLiquidityUsd(r)
								}
							}
							rows = append(rows, model.OverviewRow{
								Type:           model.OppTypeCexDex,
								Symbol:         it.Symbol,
								PathDisplay:    it.BuyExchange + " ↔ Chain_" + chainID,
								ChainLiquidity: liqStr,
								BuyExchange:    it.BuyExchange,
								SellExchange:   "Chain_" + chainID,
								SpreadPercent:  spread,
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
							liqStr := ""
							if liquidity != nil {
								if r, ok := liquidity[base+":"+chainID]; ok && r > 0 {
									name := chainDisplayName[chainID]
									if name == "" {
										name = "Chain_" + chainID
									}
									liqStr = name + ": " + formatLiquidityUsd(r)
								}
							}
							rows = append(rows, model.OverviewRow{
								Type:           model.OppTypeCexDex,
								Symbol:         it.Symbol,
								PathDisplay:   "Chain_" + chainID + " ↔ " + it.SellExchange,
								ChainLiquidity: liqStr,
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
func ComputeDexDex(chainPrices map[string]float64, threshold float64, liquidity map[string]float64) []model.OverviewRow {
	liqThreshold := config.GetLiquidityThreshold()
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
			// 流动性阈值过滤：仅当有数据且低于阈值时跳过；无数据时不过滤
			if liqThreshold > 0 && liquidity != nil && len(liquidity) > 0 {
				if r1, ok := liquidity[asset+":"+c1]; ok && r1 < liqThreshold {
					continue
				}
				if r2, ok := liquidity[asset+":"+c2]; ok && r2 < liqThreshold {
					continue
				}
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
		liqStr := ""
		if liquidity != nil {
			var parts []string
			if r1, ok := liquidity[p.asset+":"+p.buyChain]; ok && r1 > 0 {
				name := chainDisplayName[p.buyChain]
				if name == "" {
					name = "Chain_" + p.buyChain
				}
				parts = append(parts, name+"(低): "+formatLiquidityUsd(r1))
			}
			if r2, ok := liquidity[p.asset+":"+p.sellChain]; ok && r2 > 0 {
				name := chainDisplayName[p.sellChain]
				if name == "" {
					name = "Chain_" + p.sellChain
				}
				parts = append(parts, name+"(高): "+formatLiquidityUsd(r2))
			}
			if len(parts) > 0 {
				liqStr = strings.Join(parts, " | ")
			}
		}
		rows = append(rows, model.OverviewRow{
			Type:           model.OppTypeDexDex,
			Symbol:        p.asset + "USDT",
			PathDisplay:   "Chain_" + p.buyChain + "(低) → Chain_" + p.sellChain + "(高)",
			ChainLiquidity: liqStr,
			BuyExchange:   "Chain_" + p.buyChain,
			SellExchange:  "Chain_" + p.sellChain,
			SpreadPercent: p.spread,
		})
	}
	return rows
}

// MergeAndSort 合并 CEX-CEX、CEX-DEX、DEX-DEX，按价差降序
// 过滤正反重复：同一标的 (A,B) 与 (B,A) 只保留一条（价差高的为正向，反向负价差过滤）
func MergeAndSort(cexCex, cexDex, dexDex []model.OverviewRow) []model.OverviewRow {
	var all []model.OverviewRow
	all = append(all, cexCex...)
	all = append(all, cexDex...)
	all = append(all, dexDex...)

	// 按 (symbol, 有序对) 去重，只保留价差更高的一条（正向）
	type pairKey struct {
		symbol string
		a, b   string
	}
	best := make(map[pairKey]model.OverviewRow)
	for _, row := range all {
		a, b := row.BuyExchange, row.SellExchange
		if a > b {
			a, b = b, a
		}
		k := pairKey{row.Symbol, a, b}
		if cur, ok := best[k]; !ok || row.SpreadPercent > cur.SpreadPercent {
			best[k] = row
		}
	}
	var out []model.OverviewRow
	for _, row := range best {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpreadPercent > out[j].SpreadPercent })
	return out
}
