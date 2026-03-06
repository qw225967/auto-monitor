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
	fromAddr := ensure0x(tokenInfo.Address)
	toAddr := ensure0x(usdtInfo.Address)
	// 1 单位 token 换 USDT：fromToken=目标token, toToken=USDT, amount=1
	amount := "1"
	resp, err := f.client.QueryDexQuotePrice(
		fromAddr,
		toAddr,
		chainIndex,
		amount,
		strconv.Itoa(tokenInfo.Decimals),
	)
	if err != nil {
		return 0, fmt.Errorf("quote: %w", err)
	}

	var apiResp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			RouterResult struct {
				ToTokenAmount string `json:"toTokenAmount"`
			} `json:"routerResult"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &apiResp); err != nil {
		return 0, fmt.Errorf("parse response: %w", err)
	}
	if apiResp.Code != "0" {
		return 0, fmt.Errorf("api code %s: %s", apiResp.Code, apiResp.Msg)
	}
	if len(apiResp.Data) == 0 {
		return 0, fmt.Errorf("empty quote data")
	}

	toAmountStr := apiResp.Data[0].RouterResult.ToTokenAmount
	if toAmountStr == "" {
		return 0, fmt.Errorf("empty toTokenAmount")
	}

	toAmount, err := strconv.ParseFloat(toAmountStr, 64)
	if err != nil {
		return 0, fmt.Errorf("parse toTokenAmount: %w", err)
	}
	price := toAmount / math.Pow(10, float64(usdtInfo.Decimals))
	if price <= 0 {
		return 0, fmt.Errorf("invalid price %.6f", price)
	}
	return price, nil
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
	results := make(chan result, len(pairs))
	errChan := make(chan error, len(pairs))
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
				case errChan <- err:
				default:
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
		close(errChan)
	}()

	out := make(map[string]float64)
	for r := range results {
		out[r.key] = r.price
	}
	if len(out) == 0 && len(pairs) > 0 {
		if e := <-errChan; e != nil {
			log.Printf("[ChainPrice] 全部失败，示例错误: %v", e)
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
