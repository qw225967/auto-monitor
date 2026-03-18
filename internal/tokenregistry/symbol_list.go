package tokenregistry

import (
	"context"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/internal/source/seeingstone"
)

// SymbolToAsset 从 symbol 解析资产（如 POWERUSDT -> POWER, USDT）
// 返回 base 和 quote，套利场景通常用 quote(USDT) 或 base
func SymbolToAsset(symbol string) (base, quote string) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	// 常见 quote: USDT, USDC, BUSD
	for _, q := range []string{"USDT", "USDC", "BUSD", "ETH", "BTC"} {
		if strings.HasSuffix(symbol, q) && len(symbol) > len(q) {
			return strings.TrimSuffix(symbol, q), q
		}
	}
	return symbol, ""
}

// ExtractAssetsFromSymbols 从 symbol 列表提取去重后的资产列表
// 同时包含 base 和 quote（如 POWERUSDT -> POWER, USDT）
func ExtractAssetsFromSymbols(symbols []string) []string {
	seen := make(map[string]bool)
	for _, s := range symbols {
		base, quote := SymbolToAsset(s)
		if base != "" {
			seen[base] = true
		}
		if quote != "" {
			seen[quote] = true
		}
	}
	var out []string
	for a := range seen {
		out = append(out, a)
	}
	return out
}

// AssetsFromSeeingStone 从 SeeingStone API 拉取价差数据，提取资产列表（不过滤阈值）
// timeout=0 时使用 60s
func AssetsFromSeeingStone(ctx context.Context, baseURL, token string, timeout time.Duration) ([]string, error) {
	return AssetsFromSeeingStoneWithThreshold(ctx, baseURL, token, 0, timeout)
}

// AssetsFromSeeingStoneWithThreshold 从 SeeingStone 拉取价差，仅保留符合阈值的 symbol，再提取资产
// threshold=0 表示不过滤；timeout=0 时使用 60s（数据量大时需更长）
func AssetsFromSeeingStoneWithThreshold(ctx context.Context, baseURL, token string, threshold float64, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	adapter := seeingstone.New(seeingstone.Config{
		BaseURL:        baseURL,
		Token:          token,
		RequestTimeout: timeout,
	})
	raw, err := adapter.Fetch(ctx)
	if err != nil {
		return nil, err
	}
	items, err := seeingstone.ToSpreadItems(raw)
	if err != nil {
		return nil, err
	}
	var symbols []string
	for _, it := range items {
		if threshold > 0 && it.SpreadPercent < threshold {
			continue
		}
		symbols = append(symbols, it.Symbol)
	}
	return ExtractAssetsFromSymbols(symbols), nil
}
