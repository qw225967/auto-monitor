package opportunities

import (
	"testing"

	"github.com/qw225967/auto-monitor/internal/model"
)

func TestEnrichEconomics(t *testing.T) {
	row := model.OverviewRow{
		Type:          model.OppTypeCexDex,
		SpreadPercent: 1.2,
	}
	out := EnrichEconomics(row)
	if out.GrossSpreadPercent != 1.2 {
		t.Fatalf("expected gross=1.2, got %v", out.GrossSpreadPercent)
	}
	if out.EstimatedCostPercent <= 0 {
		t.Fatalf("expected cost>0, got %v", out.EstimatedCostPercent)
	}
	if out.NetSpreadPercent != out.GrossSpreadPercent-out.EstimatedCostPercent {
		t.Fatalf("unexpected net spread, got %v", out.NetSpreadPercent)
	}
}

func TestMergeAndSortByNetSpread(t *testing.T) {
	// rowA 毛价差高，但净价差低；rowB 毛价差略低但净价差更高
	rowA := model.OverviewRow{
		Type:               model.OppTypeDexDex,
		Symbol:             "BTCUSDT",
		BuyExchange:        "Chain_1",
		SellExchange:       "Chain_56",
		SpreadPercent:      1.00,
		GrossSpreadPercent: 1.00,
		NetSpreadPercent:   0.10,
	}
	rowB := model.OverviewRow{
		Type:               model.OppTypeCexDex,
		Symbol:             "ETHUSDT",
		BuyExchange:        "BITGET",
		SellExchange:       "Chain_1",
		SpreadPercent:      0.95,
		GrossSpreadPercent: 0.95,
		NetSpreadPercent:   0.30,
	}
	out := MergeAndSort([]model.OverviewRow{rowA}, []model.OverviewRow{rowB}, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0].Symbol != "ETHUSDT" {
		t.Fatalf("expected ETHUSDT ranked first by net spread, got %s", out[0].Symbol)
	}
}

func TestMergeAndSortTieBreakByConfidence(t *testing.T) {
	rowA := model.OverviewRow{
		Type:               model.OppTypeCexDex,
		Symbol:             "AAAUSDT",
		BuyExchange:        "A",
		SellExchange:       "B",
		NetSpreadPercent:   0.50,
		AvailablePathCount: 1,
		DetailPaths:        []model.DetailPathRow{{Status: "ok"}, {Status: "maintenance"}},
	}
	rowB := model.OverviewRow{
		Type:               model.OppTypeCexDex,
		Symbol:             "BBBUSDT",
		BuyExchange:        "C",
		SellExchange:       "D",
		NetSpreadPercent:   0.50,
		AvailablePathCount: 2,
		DetailPaths:        []model.DetailPathRow{{Status: "ok"}, {Status: "ok"}},
	}
	out := MergeAndSort([]model.OverviewRow{rowA, rowB}, nil, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	if out[0].Symbol != "BBBUSDT" {
		t.Fatalf("expected BBBUSDT first by confidence tie-breaker, got %s", out[0].Symbol)
	}
}

func TestComputeConfidenceBounds(t *testing.T) {
	row := model.OverviewRow{
		Type:               model.OppTypeDexDex,
		NetSpreadPercent:   0.8,
		AvailablePathCount: 2,
		DetailPaths:        []model.DetailPathRow{{Status: "ok"}, {Status: "ok"}, {Status: "maintenance"}},
	}
	c := ComputeConfidence(row)
	if c < 0 || c > 1 {
		t.Fatalf("confidence out of range: %v", c)
	}
}
