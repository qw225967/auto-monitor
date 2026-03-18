package tokenregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const coingeckoBase = "https://api.coingecko.com/api/v3"

// CoinGecko 平台 ID -> ChainID 映射
var platformToChainID = map[string]string{
	"ethereum":            "1",
	"binance-smart-chain": "56",
	"bsc":                 "56",
	"polygon-pos":         "137",
	"polygon":             "137",
	"arbitrum-one":        "42161",
	"arbitrum":            "42161",
	"optimistic-ethereum": "10",
	"optimism":            "10",
	"avalanche":           "43114",
	"fantom":              "250",
	"base":                "8453",
	"tron":                "195",
	"linea":               "59144",
	"scroll":              "534352",
	"mantle":              "5000",
	"zksync":              "324",
	"zksync-era":          "324",
}

// CoinGeckoFetcher 从 CoinGecko 获取 token 信息
type CoinGeckoFetcher struct {
	client  *http.Client
	baseURL string
	apiKey  string
	header  string // x-cg-demo-api-key 或 x-cg-pro-api-key
}

// NewCoinGeckoFetcher 创建，apiKey 为空时使用免费版（易 429）
// usePro: true 时使用 Pro API (pro-api.coingecko.com)
func NewCoinGeckoFetcher(apiKey string, usePro bool) *CoinGeckoFetcher {
	baseURL := coingeckoBase
	header := "x-cg-demo-api-key"
	if usePro {
		baseURL = "https://pro-api.coingecko.com/api/v3"
		header = "x-cg-pro-api-key"
	}
	return &CoinGeckoFetcher{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: baseURL,
		apiKey:  apiKey,
		header:  header,
	}
}

// FetchTokenInfos 根据 asset 获取其在各链上的 token 信息
// asset 如 USDT, POWER, ETH
func (f *CoinGeckoFetcher) FetchTokenInfos(ctx context.Context, asset string) ([]TokenInfo, error) {
	// 1. 搜索
	coinID, err := f.searchCoin(ctx, asset)
	if err != nil || coinID == "" {
		return nil, fmt.Errorf("search %s: %w", asset, err)
	}

	// 2. 获取详情
	infos, err := f.getCoinPlatforms(ctx, coinID, asset)
	if err != nil {
		return nil, fmt.Errorf("get platforms for %s: %w", asset, err)
	}
	return infos, nil
}

type searchResponse struct {
	Coins []struct {
		ID     string `json:"id"`
		Symbol string `json:"symbol"`
		Name   string `json:"name"`
	} `json:"coins"`
}

func (f *CoinGeckoFetcher) setAuth(req *http.Request) {
	if f.apiKey != "" && f.header != "" {
		req.Header.Set(f.header, f.apiKey)
	}
}

func (f *CoinGeckoFetcher) searchCoin(ctx context.Context, query string) (string, error) {
	u := f.baseURL + "/search?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	f.setAuth(req)
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search status %d", resp.StatusCode)
	}
	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	if len(sr.Coins) == 0 {
		return "", fmt.Errorf("no coins found for %s", query)
	}
	// 优先匹配 symbol 完全一致
	queryUpper := strings.ToUpper(strings.TrimSpace(query))
	for _, c := range sr.Coins {
		if strings.ToUpper(c.Symbol) == queryUpper {
			return c.ID, nil
		}
	}
	// 否则取第一个（按市值排序）
	return sr.Coins[0].ID, nil
}

type coinDetailResponse struct {
	ID               string `json:"id"`
	Symbol           string `json:"symbol"`
	Name             string `json:"name"`
	Platforms        map[string]interface{} `json:"platforms"`
	DetailPlatforms  map[string]struct {
		DecimalPlace    int    `json:"decimal_place"`
		ContractAddress string `json:"contract_address"`
	} `json:"detail_platforms"`
}

func (f *CoinGeckoFetcher) getCoinPlatforms(ctx context.Context, coinID, asset string) ([]TokenInfo, error) {
	u := f.baseURL + "/coins/" + url.PathEscape(coinID) + "?localization=false&tickers=false&community_data=false&developer_data=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	f.setAuth(req)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coin detail status %d", resp.StatusCode)
	}
	var cd coinDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&cd); err != nil {
		return nil, err
	}

	var infos []TokenInfo
	for platformID, addrRaw := range cd.Platforms {
		if addrRaw == nil {
			continue
		}
		addr, ok := addrRaw.(string)
		if !ok || addr == "" {
			continue
		}
		chainID := platformToChainID[platformID]
		if chainID == "" {
			chainID = platformID
		}
		decimals := 18
		if dp, ok := cd.DetailPlatforms[platformID]; ok && dp.DecimalPlace > 0 {
			decimals = dp.DecimalPlace
		}
		infos = append(infos, TokenInfo{
			Asset:    asset,
			ChainID:  chainID,
			Address:  addr,
			Decimals: decimals,
			Symbol:   cd.Symbol,
		})
	}
	return infos, nil
}
