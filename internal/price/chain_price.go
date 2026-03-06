package price

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/onchain"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// parseQuoteResponse 解析 OKX v6 quote 响应，支持 routerResult.toTokenAmount 或 dexRouterList[].toToken.tokenUnitPrice
func parseQuoteResponse(resp string, tokenDecimals int) (float64, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(resp), &raw); err != nil {
		return 0, fmt.Errorf("parse: %w", err)
	}
	if code, _ := raw["code"].(string); code != "0" {
		msg, _ := raw["msg"].(string)
		return 0, fmt.Errorf("api code %s: %s", code, msg)
	}
	data, ok := raw["data"].([]interface{})
	if !ok || len(data) == 0 {
		return 0, fmt.Errorf("empty quote data")
	}
	first, ok := data[0].(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("invalid data format")
	}

	// 1) 尝试 routerResult.toTokenAmount
	if rr, ok := first["routerResult"].(map[string]interface{}); ok {
		for _, key := range []string{"toTokenAmount", "toTokenAmountOut"} {
			if v := rr[key]; v != nil {
				var s string
				switch val := v.(type) {
				case string:
					s = val
				case float64:
					s = strconv.FormatFloat(val, 'f', -1, 64)
				case json.Number:
					s = val.String()
				}
				if s != "" {
					toAmount, err := strconv.ParseFloat(s, 64)
					if err != nil {
						continue
					}
					tokenAmount := toAmount / math.Pow(10, float64(tokenDecimals))
					if tokenAmount > 0 {
						return 500 / tokenAmount, nil
					}
				}
			}
		}
	}

	// 2) 尝试 data[0].toToken.tokenUnitPrice（聚合结果，v6 顶层）
	if toToken, ok := first["toToken"].(map[string]interface{}); ok {
		if v := toToken["tokenUnitPrice"]; v != nil {
			var price float64
			switch val := v.(type) {
			case string:
				price, _ = strconv.ParseFloat(val, 64)
			case float64:
				price = val
			}
			if price > 0 {
				return price, nil
			}
		}
	}

	// 3) 回退：dexRouterList 最后一项的 toToken.tokenUnitPrice（多跳时第一项可能是中间代币如 USDC）
	if list, ok := first["dexRouterList"].([]interface{}); ok && len(list) > 0 {
		lastIdx := len(list) - 1
		if item, ok := list[lastIdx].(map[string]interface{}); ok {
			if toToken, ok := item["toToken"].(map[string]interface{}); ok {
				if v := toToken["tokenUnitPrice"]; v != nil {
					var price float64
					switch val := v.(type) {
					case string:
						price, _ = strconv.ParseFloat(val, 64)
					case float64:
						price = val
					}
					if price > 0 {
						return price, nil
					}
				}
			}
		}
	}
	return 0, fmt.Errorf("empty toTokenAmount and tokenUnitPrice")
}

func ensure0x(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	if !strings.HasPrefix(strings.ToLower(addr), "0x") {
		return "0x" + addr
	}
	return addr
}

// ChainPriceFetcher 链上 DEX 价格获取
type ChainPriceFetcher struct {
	store   *tokenregistry.Storage
	client  onchain.OnchainClient
	rd      *tokenregistry.RegistryData
	rdMu    sync.RWMutex
	cache   map[string]cachedPrice // key: "asset:chainID"
	cacheMu sync.RWMutex
	ttl     time.Duration
}

type cachedPrice struct {
	price float64
	at    time.Time
}

// NewChainPriceFetcher 创建链上价格获取器
func NewChainPriceFetcher(registryPath string, client onchain.OnchainClient, cacheTTL time.Duration) (*ChainPriceFetcher, error) {
	store := tokenregistry.NewStorage(registryPath)
	rd, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load token registry: %w", err)
	}
	if cacheTTL == 0 {
		cacheTTL = 30 * time.Second
	}
	return &ChainPriceFetcher{
		store:  store,
		client: client,
		rd:     rd,
		cache:  make(map[string]cachedPrice),
		ttl:    cacheTTL,
	}, nil
}

// ReloadRegistry 重新加载 token 信息（token sync 更新后调用）
func (f *ChainPriceFetcher) ReloadRegistry() error {
	rd, err := f.store.Load()
	if err != nil {
		return err
	}
	f.rdMu.Lock()
	f.rd = rd
	f.rdMu.Unlock()
	return nil
}

// QueryDexPrice 查询某资产在某链上的 DEX 价格（USDT 计价）
// 返回价格，若无法获取则返回 0 和 error
func (f *ChainPriceFetcher) QueryDexPrice(asset, chainID string) (float64, error) {
	key := strings.ToUpper(asset) + ":" + chainID
	f.cacheMu.RLock()
	if cp, ok := f.cache[key]; ok && time.Since(cp.at) < f.ttl {
		f.cacheMu.RUnlock()
		return cp.price, nil
	}
	f.cacheMu.RUnlock()

	price, err := f.queryDexPriceUncached(asset, chainID)
	if err != nil {
		return 0, err
	}

	f.cacheMu.Lock()
	f.cache[key] = cachedPrice{price: price, at: time.Now()}
	f.cacheMu.Unlock()
	return price, nil
}

func (f *ChainPriceFetcher) queryDexPriceUncached(asset, chainID string) (float64, error) {
	if f.client == nil {
		return 0, fmt.Errorf("onchain client not configured")
	}

	f.rdMu.RLock()
	rd := f.rd
	tokenInfo, ok := f.store.GetTokenInfo(rd, asset, chainID)
	usdtInfo, usdtOk := f.store.GetTokenInfo(rd, "USDT", chainID)
	f.rdMu.RUnlock()

	if !ok || tokenInfo.Address == "" {
		return 0, fmt.Errorf("token %s on chain %s not in registry", asset, chainID)
	}
	if !usdtOk || usdtInfo.Address == "" {
		return 0, fmt.Errorf("USDT on chain %s not in registry", chainID)
	}

	chainIndex := constants.GetChainIndex(chainID)
	usdtAddr := ensure0x(usdtInfo.Address)
	tokenAddr := ensure0x(tokenInfo.Address)

	// 按 500 USDT 询价：fromToken=USDT, toToken=目标token，price = 500 / tokenAmount
	usdtAmount := "500"
	usdtDecimalsStr := strconv.Itoa(usdtInfo.Decimals)

	resp, err := f.client.QueryDexQuotePrice(usdtAddr, tokenAddr, chainIndex, usdtAmount, usdtDecimalsStr)
	if err != nil {
		return 0, fmt.Errorf("quote: %w", err)
	}

	price, err := parseQuoteResponse(resp, tokenInfo.Decimals)
	if err != nil {
		return 0, err
	}
	return price, nil
}

// chainDisplayName 链 ID 转展示名（用于日志）
var chainDisplayName = map[string]string{
	"1": "ETH", "56": "BSC", "137": "Polygon", "42161": "Arbitrum",
	"10": "OP", "8453": "Base", "43114": "AVAX", "195": "TRON",
}

// BatchQueryDexPrices 批量查询多个 (asset, chainID) 的价格
// 返回 map["asset:chainID"]price，失败的项不包含在结果中
func (f *ChainPriceFetcher) BatchQueryDexPrices(pairs []AssetChainPair, concurrency int) map[string]float64 {
	if concurrency <= 0 {
		concurrency = 3
	}
	type result struct {
		key   string
		price float64
	}
	type failRecord struct {
		chainID string
		err     error
	}
	results := make(chan result, len(pairs))
	failChan := make(chan failRecord, len(pairs))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, p := range pairs {
		p := p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			price, err := f.QueryDexPrice(p.Asset, p.ChainID)
			if err == nil {
				results <- result{key: p.Asset + ":" + p.ChainID, price: price}
			} else {
				select {
				case failChan <- failRecord{chainID: p.ChainID, err: err}:
				default:
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		close(failChan)
	}()

	out := make(map[string]float64)
	for r := range results {
		out[r.key] = r.price
	}
	// 统计失败按链分布，便于排查为何只显示 ETH
	failByChain := make(map[string]int)
	var sampleErr map[string]error
	for fr := range failChan {
		failByChain[fr.chainID]++
		if sampleErr == nil {
			sampleErr = make(map[string]error)
		}
		if sampleErr[fr.chainID] == nil {
			sampleErr[fr.chainID] = fr.err
		}
	}
	if len(failByChain) > 0 {
		var parts []string
		for cid, n := range failByChain {
			name := chainDisplayName[cid]
			if name == "" {
				name = "链" + cid
			}
			sample := ""
			if sampleErr != nil && sampleErr[cid] != nil {
				sample = " 示例:" + sampleErr[cid].Error()
			}
			parts = append(parts, name+"失败"+strconv.Itoa(n)+"次"+sample)
		}
		log.Printf("[ChainPrice] 失败按链: %s", strings.Join(parts, "; "))
	}
	if len(out) == 0 && len(pairs) > 0 {
		if len(sampleErr) > 0 {
			for _, e := range sampleErr {
				if e != nil {
					log.Printf("[ChainPrice] 全部失败，示例错误: %v", e)
					break
				}
			}
		}
	}
	return out
}

// AssetChainPair 资产+链对
type AssetChainPair struct {
	Asset   string
	ChainID string
}

// GetAllTokenChains 从 registry 获取某资产支持的所有链
func (f *ChainPriceFetcher) GetAllTokenChains(asset string) []string {
	f.rdMu.RLock()
	defer f.rdMu.RUnlock()
	asset = strings.ToUpper(asset)
	if f.rd.Assets[asset] == nil {
		return nil
	}
	var chains []string
	for c := range f.rd.Assets[asset] {
		chains = append(chains, c)
	}
	return chains
}

// ChainsWithUSDT 返回 registry 中 USDT 存在的所有链（用于过滤报价对）
func (f *ChainPriceFetcher) ChainsWithUSDT() map[string]bool {
	f.rdMu.RLock()
	defer f.rdMu.RUnlock()
	out := make(map[string]bool)
	if f.rd.Assets["USDT"] == nil {
		return out
	}
	for c := range f.rd.Assets["USDT"] {
		info := f.rd.Assets["USDT"][c]
		if info.Address != "" {
			out[c] = true
		}
	}
	return out
}

// GetAllAssets 从 registry 获取所有资产（排除 USDT，用于报价需 USDT 计价）
func (f *ChainPriceFetcher) GetAllAssets() []string {
	f.rdMu.RLock()
	defer f.rdMu.RUnlock()
	var assets []string
	for a := range f.rd.Assets {
		if a == "USDT" {
			continue
		}
		if len(f.rd.Assets[a]) > 0 {
			assets = append(assets, a)
		}
	}
	return assets
}
