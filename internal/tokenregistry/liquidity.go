package tokenregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// chainIDToOnchainNetwork CoinGecko onchain API 使用的 network ID
// 支持 EVM 链 ID（数字）及部分 CoinGecko platform ID（如 solana、ton）
var chainIDToOnchainNetwork = map[string]string{
	"1":     "eth",
	"56":    "bsc",
	"137":   "polygon_pos",
	"42161": "arbitrum_one",
	"10":    "optimism",
	"43114": "avalanche",
	"8453":  "base",
	"324":   "zksync",
	"5000":  "mantle",
	"59144": "linea",
	"534352": "scroll",
	"195":   "tron",
	"1101":  "polygon_zkevm",
	"66":    "okc",
	"250":   "fantom",
	"solana": "solana",
	"the-open-network": "ton",
	"ronin": "ronin",
}

// chainSupportedForLiquidity 该链是否支持流动性查询（onchain API 支持）
func chainSupportedForLiquidity(chainID string) bool {
	_, ok := chainIDToOnchainNetwork[chainID]
	return ok
}

// LiquidityFetcher 从 CoinGecko onchain API 获取池子流动性
type LiquidityFetcher struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewLiquidityFetcher 创建，usePro=true 时用 pro-api.coingecko.com
func NewLiquidityFetcher(apiKey string, usePro bool) *LiquidityFetcher {
	base := "https://api.coingecko.com/api/v3/onchain"
	if usePro {
		base = "https://pro-api.coingecko.com/api/v3/onchain"
	}
	return &LiquidityFetcher{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: base,
		apiKey:  apiKey,
	}
}

// poolsResponse CoinGecko onchain pools 响应（JSON:API 格式）
type poolsResponse struct {
	Data []struct {
		Attributes struct {
			ReserveUSD   interface{} `json:"reserve_usd"`
			ReserveInUSD interface{} `json:"reserve_in_usd"`
		} `json:"attributes"`
	} `json:"data"`
}

func parseReserve(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case string:
		var f float64
		if _, err := fmt.Sscanf(x, "%f", &f); err == nil {
			return f
		}
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	}
	return 0
}

// FetchReserveUSD 获取某代币在某链上的流动性（USDT 计价）
// 取该代币所有池子的 reserve 之和（或最大池子），单位 USD
func (f *LiquidityFetcher) FetchReserveUSD(ctx context.Context, chainID, tokenAddress string) (float64, error) {
	network := chainIDToOnchainNetwork[chainID]
	if network == "" {
		return 0, fmt.Errorf("chain %s not supported by onchain API", chainID)
	}
	addr := strings.TrimSpace(tokenAddress)
	if addr == "" {
		return 0, fmt.Errorf("empty token address")
	}
	// EVM 链需要 0x 前缀；Solana/TON 等非 EVM 保持原样
	nonEVM := map[string]bool{"solana": true, "ton": true, "the-open-network": true, "ronin": true}
	if !nonEVM[chainID] && chainID != "195" && !strings.HasPrefix(strings.ToLower(addr), "0x") {
		addr = "0x" + addr
	}

	u := fmt.Sprintf("%s/networks/%s/tokens/%s/pools", f.baseURL, network, url.PathEscape(addr))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	if f.apiKey != "" {
		req.Header.Set("x-cg-pro-api-key", f.apiKey)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	var pr poolsResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, fmt.Errorf("decode: %w", err)
	}
	var total float64
	for _, d := range pr.Data {
		v := parseReserve(d.Attributes.ReserveUSD)
		if v <= 0 {
			v = parseReserve(d.Attributes.ReserveInUSD)
		}
		if v > 0 {
			total += v
		}
	}
	return total, nil
}

// LiquiditySyncConfig 流动性同步配置
type LiquiditySyncConfig struct {
	RegistryPath    string
	CoinGeckoAPIKey string
	CoinGeckoPro    bool
	DelayPerRequest time.Duration // 每次请求间隔，避免限流
}

// LiquidityMapFromRegistry 从已加载的 registry 构建 liquidity map（用于启动时从文件加载）
func LiquidityMapFromRegistry(rd *RegistryData) map[string]float64 {
	out := make(map[string]float64)
	for asset, chains := range rd.Assets {
		for chainID, info := range chains {
			if info.ReserveUSD > 0 {
				out[asset+":"+chainID] = info.ReserveUSD
			}
		}
	}
	return out
}

// RunLiquiditySync 全表流动性更新：遍历 registry 中所有 (asset, chainID)，从 CoinGecko onchain 拉取并更新
// 返回更新后的 liquidity map（key: "asset:chainID" -> reserve_usd），供 handler 使用
func RunLiquiditySync(ctx context.Context, cfg LiquiditySyncConfig) (map[string]float64, error) {
	store := NewStorage(cfg.RegistryPath)
	rd, err := store.Load()
	if err != nil {
		return nil, err
	}
	if cfg.DelayPerRequest == 0 {
		cfg.DelayPerRequest = 1 * time.Second
	}
	fetcher := NewLiquidityFetcher(cfg.CoinGeckoAPIKey, cfg.CoinGeckoPro)
	liquidityMap := make(map[string]float64)
	updated := 0
	now := time.Now().Format(time.RFC3339)

	for asset, chains := range rd.Assets {
		for chainID, info := range chains {
			if info.Address == "" {
				continue
			}
			if !chainSupportedForLiquidity(chainID) {
				// 不支持的链（如 provenance、aptos、cardano）静默跳过，不占 API 配额
				if info.ReserveUSD > 0 {
					liquidityMap[asset+":"+chainID] = info.ReserveUSD
				}
				continue
			}
			select {
			case <-ctx.Done():
				return liquidityMap, ctx.Err()
			default:
			}
			reserve, err := fetcher.FetchReserveUSD(ctx, chainID, info.Address)
			if err != nil {
				// 404/无池子等常见情况不打印，仅保留原值
				errStr := err.Error()
				if !strings.Contains(errStr, "status 404") && !strings.Contains(errStr, "not supported") {
					log.Printf("[LiquiditySync] %s %s: %v", asset, chainID, err)
				}
				if info.ReserveUSD > 0 {
					liquidityMap[asset+":"+chainID] = info.ReserveUSD
				}
			} else {
				liquidityMap[asset+":"+chainID] = reserve
				if rd.Assets[asset] == nil {
					rd.Assets[asset] = make(map[string]TokenChainInfo)
				}
				info.ReserveUSD = reserve
				info.UpdatedAt = now
				rd.Assets[asset][chainID] = info
				updated++
			}
			time.Sleep(cfg.DelayPerRequest)
		}
	}

	if updated > 0 {
		if err := store.Save(rd); err != nil {
			log.Printf("[LiquiditySync] 保存失败: %v", err)
		} else {
			log.Printf("[LiquiditySync] 更新 %d 条流动性，保存至 %s", updated, cfg.RegistryPath)
		}
	}
	return liquidityMap, nil
}
