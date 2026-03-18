package opportunities

import (
	"math"

	"github.com/qw225967/auto-monitor/internal/model"
)

// 静态成本模型（v1）：单位 %
const (
	costCexCexTrade    = 0.20 // 买卖双边手续费
	costCexCexTransfer = 0.12 // 提现/充值/网络综合成本

	costCexDexCexTrade = 0.10
	costCexDexDexTrade = 0.30
	costCexDexBridge   = 0.20

	costDexDexDexTrade = 0.60 // 双边 DEX
	costDexDexBridge   = 0.25
)

func estimatedCostByType(oppType string) float64 {
	switch oppType {
	case model.OppTypeCexCex:
		return costCexCexTrade + costCexCexTransfer
	case model.OppTypeCexDex:
		return costCexDexCexTrade + costCexDexDexTrade + costCexDexBridge
	case model.OppTypeDexDex:
		return costDexDexDexTrade + costDexDexBridge
	default:
		return 0
	}
}

// EnrichEconomics 填充 gross/cost/net 字段；默认将 SpreadPercent 视为毛价差。
func EnrichEconomics(row model.OverviewRow) model.OverviewRow {
	gross := row.SpreadPercent
	if row.GrossSpreadPercent > 0 {
		gross = row.GrossSpreadPercent
	}
	cost := estimatedCostByType(row.Type)
	net := gross - cost
	row.GrossSpreadPercent = gross
	row.EstimatedCostPercent = cost
	row.NetSpreadPercent = net
	return row
}

// EffectiveSpreadForSort 优先按净价差排序；无净价差时回退到毛价差。
func EffectiveSpreadForSort(row model.OverviewRow) float64 {
	if row.NetSpreadPercent != 0 {
		return row.NetSpreadPercent
	}
	if row.GrossSpreadPercent != 0 {
		return row.GrossSpreadPercent
	}
	return row.SpreadPercent
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ComputeConfidence 规则版置信度 [0,1]：
// - 路径成功率（可用路径 / 详情路径）
// - 可用路径规模（越多越稳，3条封顶）
// - 净价差强度（2% 视为满分）
func ComputeConfidence(row model.OverviewRow) float64 {
	total := len(row.DetailPaths)
	available := row.AvailablePathCount
	successRate := 0.0
	if total > 0 {
		successRate = float64(available) / float64(total)
	}
	pathScale := clamp(float64(available)/3.0, 0, 1)
	netStrength := clamp(EffectiveSpreadForSort(row)/2.0, 0, 1)

	// 当没有详情路径时，保留较低基础置信度，避免完全为 0
	if total == 0 {
		base := 0.15 + 0.35*netStrength
		return math.Round(clamp(base, 0, 1)*1000) / 1000
	}
	score := 0.55*successRate + 0.30*pathScale + 0.15*netStrength
	return math.Round(clamp(score, 0, 1)*1000) / 1000
}

func EnrichConfidence(row model.OverviewRow) model.OverviewRow {
	row.ConfidenceScore = ComputeConfidence(row)
	return row
}

func EffectiveConfidenceForSort(row model.OverviewRow) float64 {
	if row.ConfidenceScore > 0 {
		return row.ConfidenceScore
	}
	return ComputeConfidence(row)
}
