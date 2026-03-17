package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/qw225967/auto-monitor/internal/model"
)

func TestGetOverviewIncludesFreshnessMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New()
	h.UpdateOverview(&model.OverviewResponse{
		Overview: []model.OverviewRow{{Symbol: "BTCUSDT", SpreadPercent: 1.2}},
	})
	h.UpdateChainPrices(map[string]float64{"BTC:1": 100000})
	h.UpdateLiquidity(map[string]float64{"BTC:1": 123456})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	h.GetOverview(c)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp model.OverviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Overview) != 1 || resp.Overview[0].Symbol != "BTCUSDT" {
		t.Fatalf("unexpected overview payload: %+v", resp.Overview)
	}
	if resp.OverviewUpdatedAt == "" || resp.ChainPricesUpdatedAt == "" || resp.LiquidityUpdatedAt == "" {
		t.Fatalf("expected non-empty updated_at fields: %+v", resp)
	}
	if resp.OverviewAgeSec < 0 || resp.ChainPricesAgeSec < 0 || resp.LiquidityAgeSec < 0 {
		t.Fatalf("expected non-negative age fields: %+v", resp)
	}
}
