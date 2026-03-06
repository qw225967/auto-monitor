package registry

import (
	"context"
	"strings"

	"github.com/qw225967/auto-monitor/internal/model"
)

// CompositeRegistry API 优先，无数据时回退到 USDT 同链（多数 ERC20/BEP20 与 USDT 同网）
type CompositeRegistry struct {
	api *APINetworkRegistry
	// usdtFallback: exchange -> chainIDs（API 无数据时，用 USDT 常见链兜底）
	usdtFallback map[string][]string
}

// NewCompositeRegistry 创建：API 优先，无数据时用 USDT 链兜底
func NewCompositeRegistry() *CompositeRegistry {
	return &CompositeRegistry{
		api: NewAPINetworkRegistry(),
		usdtFallback: map[string][]string{
			"bitget":  {"1", "56", "195", "137", "42161", "10"},
			"gate":    {"1", "56", "195", "137", "42161", "10", "43114", "8453"},
			"bybit":   {"1", "56", "195", "137", "42161", "10", "8453"},
			"binance": {"1", "56", "195", "137", "42161", "10", "43114", "8453"},
			"okex":    {"1", "56", "195", "137", "42161", "10", "8453"},
		},
	}
}

// Refresh 委托给 API 刷新
func (c *CompositeRegistry) Refresh(ctx context.Context, symbols []string) {
	c.api.Refresh(ctx, symbols)
}

// GetWithdrawNetworks API 有则用，无则用 USDT 链兜底
func (c *CompositeRegistry) GetWithdrawNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	nets, err := c.api.GetWithdrawNetworks(exchangeType, asset)
	if err == nil && len(nets) > 0 {
		return nets, nil
	}
	return c.fallbackNetworks(strings.ToLower(strings.TrimSpace(exchangeType)))
}

// GetDepositNetworks API 有则用，无则用 USDT 链兜底
func (c *CompositeRegistry) GetDepositNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	nets, err := c.api.GetDepositNetworks(exchangeType, asset)
	if err == nil && len(nets) > 0 {
		return nets, nil
	}
	return c.fallbackNetworks(strings.ToLower(strings.TrimSpace(exchangeType)))
}

func (c *CompositeRegistry) fallbackNetworks(ex string) ([]model.WithdrawNetworkInfo, error) {
	if ex == "okx" {
		ex = "okex"
	}
	chainIDs, ok := c.usdtFallback[ex]
	if !ok || len(chainIDs) == 0 {
		return nil, nil
	}
	out := make([]model.WithdrawNetworkInfo, 0, len(chainIDs))
	for _, cid := range chainIDs {
		out = append(out, model.WithdrawNetworkInfo{
			Network:        chainIDToName(cid),
			ChainID:        cid,
			WithdrawEnable: true,
		})
	}
	return out, nil
}

func chainIDToName(cid string) string {
	switch cid {
	case "1":
		return "ETH"
	case "56":
		return "BSC"
	case "195":
		return "TRON"
	case "137":
		return "Polygon"
	case "42161":
		return "Arbitrum"
	case "10":
		return "Optimism"
	case "43114":
		return "AVAX"
	case "8453":
		return "Base"
	default:
		return cid
	}
}
