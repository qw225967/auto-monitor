package tokenregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
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
	header  string // x-cg-demo-api-key 或 x-cg-pro-api-key
}

// NewLiquidityFetcher 创建，usePro=true 时用 pro-api.coingecko.com
func NewLiquidityFetcher(apiKey string, usePro bool) *LiquidityFetcher {
	base := "https://api.coingecko.com/api/v3/onchain"
	header := "x-cg-demo-api-key"
	if usePro {
		base = "https://pro-api.coingecko.com/api/v3/onchain"
		header = "x-cg-pro-api-key"
	}
	return &LiquidityFetcher{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: base,
		apiKey:  apiKey,
		header:  header,
	}
}

func (f *LiquidityFetcher) setAuth(req *http.Request) {
	if f.apiKey != "" && f.header != "" {
		req.Header.Set(f.header, f.apiKey)
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
	f.setAuth(req)
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
	MaxRetries      int           // 每次请求的最大重试次数（不含首次）
	BackoffBase     time.Duration // 指数退避基础时长
	BackoffMax      time.Duration // 指数退避最大时长
	BackoffJitter   float64       // 抖动百分比，如 20 表示 ±20%
	NegativeTTL     time.Duration // 负缓存 TTL（404/无池）
	BudgetPath      string
	BudgetEnabled   bool
	BudgetMonthlyLimit int
	IncludeAssets   []string      // 仅同步这些资产；为空表示全量
	MaxRequests     int           // 本轮最多请求数；<=0 表示不限制
}

func parseNegativeUntil(s string) (time.Time, bool) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func shouldSkipByNegativeCache(info TokenChainInfo, now time.Time) bool {
	until, ok := parseNegativeUntil(info.LiquidityNegativeUntil)
	if !ok {
		return false
	}
	return now.Before(until)
}

func classifyNegativeReason(err error, reserve float64) (reason string, cacheable bool) {
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "status 404") {
			return "status_404", true
		}
		return "", false
	}
	if reserve <= 0 {
		return "no_pool", true
	}
	return "", false
}

func setNegativeCache(info *TokenChainInfo, reason string, now time.Time, ttl time.Duration) {
	if info == nil || ttl <= 0 || reason == "" {
		return
	}
	info.LiquidityNegativeReason = reason
	info.LiquidityNegativeUntil = now.Add(ttl).Format(time.RFC3339)
}

func clearNegativeCache(info *TokenChainInfo) {
	if info == nil {
		return
	}
	info.LiquidityNegativeReason = ""
	info.LiquidityNegativeUntil = ""
}

func shouldRetryLiquidityError(err error) bool {
	if err == nil {
		return false
	}
	// 主 context 取消时不重试
	if errors.Is(err, context.Canceled) {
		return false
	}
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "status 429") {
		return true
	}
	// 5xx 服务端错误
	if strings.Contains(errStr, "status 5") {
		return true
	}
	// 网络抖动类错误
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "temporary") {
		return true
	}
	return false
}

func calcBackoffDelay(attempt int, base, max time.Duration, jitterPercent float64) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if max <= 0 {
		max = 5 * time.Second
	}
	if attempt < 0 {
		attempt = 0
	}
	d := base
	for i := 0; i < attempt; i++ {
		if d >= max/2 {
			d = max
			break
		}
		d *= 2
	}
	if d > max {
		d = max
	}
	if jitterPercent <= 0 {
		return d
	}
	j := jitterPercent / 100.0
	if j > 0.95 {
		j = 0.95
	}
	factor := (1.0 - j) + rand.Float64()*(2.0*j)
	out := time.Duration(float64(d) * factor)
	if out < 0 {
		return 0
	}
	return out
}

func fetchReserveWithRetry(ctx context.Context, f *LiquidityFetcher, chainID, address string, cfg LiquiditySyncConfig) (float64, error) {
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		reserve, err := f.FetchReserveUSD(ctx, chainID, address)
		if err == nil {
			return reserve, nil
		}
		lastErr = err
		if !shouldRetryLiquidityError(err) || attempt == cfg.MaxRetries {
			break
		}
		delay := calcBackoffDelay(attempt, cfg.BackoffBase, cfg.BackoffMax, cfg.BackoffJitter)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(delay):
		}
	}
	return 0, lastErr
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
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.BackoffBase == 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 5 * time.Second
	}
	if cfg.BackoffJitter == 0 {
		cfg.BackoffJitter = 20
	}
	if cfg.NegativeTTL == 0 {
		cfg.NegativeTTL = 24 * time.Hour
	}
	fetcher := NewLiquidityFetcher(cfg.CoinGeckoAPIKey, cfg.CoinGeckoPro)
	liquidityMap := LiquidityMapFromRegistry(rd)
	updated := 0
	negativeSkipped := 0
	budgetDenied := 0
	requested := 0
	now := time.Now()
	budget := GetCoinGeckoBudget(cfg.BudgetPath, cfg.BudgetEnabled, cfg.BudgetMonthlyLimit)
	includeSet := make(map[string]bool)
	for _, a := range cfg.IncludeAssets {
		a = strings.ToUpper(strings.TrimSpace(a))
		if a != "" {
			includeSet[a] = true
		}
	}
	stop := false

	for asset, chains := range rd.Assets {
		if len(includeSet) > 0 && !includeSet[strings.ToUpper(strings.TrimSpace(asset))] {
			continue
		}
		for chainID, info := range chains {
			if cfg.MaxRequests > 0 && requested >= cfg.MaxRequests {
				stop = true
				break
			}
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
			if shouldSkipByNegativeCache(info, now) {
				negativeSkipped++
				if info.ReserveUSD > 0 {
					liquidityMap[asset+":"+chainID] = info.ReserveUSD
				}
				continue
			}
			allowed, reason, snap, bErr := budget.TryConsume(1, time.Now())
			if bErr != nil {
				log.Printf("[LiquiditySync] budget error: %v", bErr)
			}
			if !allowed {
				budgetDenied++
				if budgetDenied <= 3 {
					log.Printf("[LiquiditySync] budget deny(%s): used=%d/%d remaining=%d today=%d/%d",
						reason, snap.Used, snap.MonthlyLimit, snap.Remaining, snap.TodayUsed, snap.TodayCap)
				}
				if info.ReserveUSD > 0 {
					liquidityMap[asset+":"+chainID] = info.ReserveUSD
				}
				continue
			}
			requested++
			select {
			case <-ctx.Done():
				return liquidityMap, ctx.Err()
			default:
			}
			reserve, err := fetchReserveWithRetry(ctx, fetcher, chainID, info.Address, cfg)
			if err != nil {
				// 404/无池子等常见情况不打印，仅保留原值
				errStr := err.Error()
				if !strings.Contains(errStr, "status 404") && !strings.Contains(errStr, "not supported") {
					log.Printf("[LiquiditySync] %s %s: %v", asset, chainID, err)
				}
				if info.ReserveUSD > 0 {
					liquidityMap[asset+":"+chainID] = info.ReserveUSD
				}
				if reason, ok := classifyNegativeReason(err, 0); ok {
					setNegativeCache(&info, reason, now, cfg.NegativeTTL)
					if rd.Assets[asset] == nil {
						rd.Assets[asset] = make(map[string]TokenChainInfo)
					}
					rd.Assets[asset][chainID] = info
					updated++
				}
			} else {
				liquidityMap[asset+":"+chainID] = reserve
				if rd.Assets[asset] == nil {
					rd.Assets[asset] = make(map[string]TokenChainInfo)
				}
				info.ReserveUSD = reserve
				if reason, ok := classifyNegativeReason(nil, reserve); ok {
					setNegativeCache(&info, reason, now, cfg.NegativeTTL)
				} else {
					clearNegativeCache(&info)
				}
				rd.Assets[asset][chainID] = info
				updated++
			}
			select {
			case <-ctx.Done():
				return liquidityMap, ctx.Err()
			case <-time.After(cfg.DelayPerRequest):
			}
		}
		if stop {
			break
		}
	}

	if updated > 0 {
		if err := store.Save(rd); err != nil {
			log.Printf("[LiquiditySync] 保存失败: %v", err)
		} else {
			log.Printf("[LiquiditySync] 更新 %d 条流动性，保存至 %s", updated, cfg.RegistryPath)
		}
	}
	if negativeSkipped > 0 {
		log.Printf("[LiquiditySync] 负缓存跳过 %d 条请求", negativeSkipped)
	}
	if budgetDenied > 0 {
		log.Printf("[LiquiditySync] 因预算跳过 %d 条请求", budgetDenied)
	}
	if len(includeSet) > 0 {
		log.Printf("[LiquiditySync] 优先同步模式: assets=%d requested=%d", len(includeSet), requested)
	}
	return liquidityMap, nil
}
