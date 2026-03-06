package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

// APINetworkRegistry 从交易所公开 API 实时获取充提网络，按价差 symbol 刷新（跟随 30s 探测周期）
type APINetworkRegistry struct {
	mu       sync.RWMutex
	withdraw map[string][]model.WithdrawNetworkInfo // key: "exchange:asset"
	deposit  map[string][]model.WithdrawNetworkInfo
	client   *http.Client
}

// NewAPINetworkRegistry 创建 API 注册表
func NewAPINetworkRegistry() *APINetworkRegistry {
	return &APINetworkRegistry{
		withdraw: make(map[string][]model.WithdrawNetworkInfo),
		deposit:  make(map[string][]model.WithdrawNetworkInfo),
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *APINetworkRegistry) cacheKey(exchange, asset string) string {
	return strings.ToLower(strings.TrimSpace(exchange)) + ":" + strings.ToUpper(strings.TrimSpace(asset))
}

// Refresh 用价差中的 symbols 刷新充提网络（每 30s 探测前调用）
func (a *APINetworkRegistry) Refresh(ctx context.Context, symbols []string) {
	assets := tokenregistry.ExtractAssetsFromSymbols(symbols)
	if len(assets) == 0 {
		return
	}
	exchanges := []string{"binance", "bybit", "bitget", "gate", "okex"}
	type pair struct{ ex, asset string }
	var pairs []pair
	for _, ex := range exchanges {
		for _, asset := range assets {
			pairs = append(pairs, pair{ex, asset})
		}
	}

	wd := make(map[string][]model.WithdrawNetworkInfo)
	dep := make(map[string][]model.WithdrawNetworkInfo)
	var buildMu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, p := range pairs {
		select {
		case <-ctx.Done():
			goto done
		default:
		}
		p := p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w, d := a.fetchNetworks(ctx, p.ex, p.asset)
			if len(w) > 0 || len(d) > 0 {
				k := a.cacheKey(p.ex, p.asset)
				buildMu.Lock()
				if len(w) > 0 {
					wd[k] = w
				}
				if len(d) > 0 {
					dep[k] = d
				}
				buildMu.Unlock()
			}
		}()
	}
	wg.Wait()
done:
	a.mu.Lock()
	a.withdraw = wd
	a.deposit = dep
	a.mu.Unlock()
}

func (a *APINetworkRegistry) fetchNetworks(ctx context.Context, exchange, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	switch exchange {
	case "bitget":
		return a.fetchBitget(ctx, asset)
	case "bybit":
		return a.fetchBybit(ctx, asset)
	case "gate":
		return a.fetchGate(ctx, asset)
	case "binance":
		return a.fetchBinance(ctx, asset)
	case "okex", "okx":
		return a.fetchOkex(ctx, asset)
	}
	return nil, nil
}

func (a *APINetworkRegistry) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// fetchBitget GET /api/v2/spot/public/coins?coin=USDT 公开接口
func (a *APINetworkRegistry) fetchBitget(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/api/v2/spot/public/coins?coin=%s", constants.BitgetRestBaseUrl, asset)
	body, err := a.doGet(ctx, url)
	if err != nil {
		return nil, nil
	}
	var resp struct {
		Code string `json:"code"`
		Data []struct {
			Coin   string `json:"coin"`
			Chains []struct {
				Chain        string `json:"chain"`
				Withdrawable string `json:"withdrawable"`
				Rechargeable string `json:"rechargeable"`
			} `json:"chains"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Code != "00000" {
		return nil, nil
	}
	assetUpper := strings.ToUpper(asset)
	for _, coin := range resp.Data {
		if strings.ToUpper(coin.Coin) != assetUpper {
			continue
		}
		for _, ch := range coin.Chains {
			chainID := bitgetNetworkToChainID[strings.ToUpper(ch.Chain)]
			if chainID == "" {
				chainID = ch.Chain
			}
			info := model.WithdrawNetworkInfo{
				Network:        ch.Chain,
				ChainID:        chainID,
				WithdrawEnable: ch.Withdrawable == "true",
			}
			if ch.Withdrawable == "true" {
				withdraw = append(withdraw, info)
			}
			if ch.Rechargeable == "true" {
				deposit = append(deposit, info)
			}
		}
		break
	}
	return withdraw, deposit
}

// fetchBybit GET /v5/asset/coin/query-info?coin=USDT 公开接口
func (a *APINetworkRegistry) fetchBybit(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/v5/asset/coin/query-info?coin=%s", constants.BybitRestBaseUrl, asset)
	body, err := a.doGet(ctx, url)
	if err != nil {
		return nil, nil
	}
	var resp struct {
		RetCode int `json:"retCode"`
		Result  struct {
			Rows []struct {
				Coin   string `json:"coin"`
				Chains []struct {
					Chain          string `json:"chain"`
					ChainDeposit   string `json:"chainDeposit"`
					ChainWithdraw  string `json:"chainWithdraw"`
					WithdrawChainId string `json:"withdrawChainId"`
					ChainId        string `json:"chainId"`
				} `json:"chains"`
			} `json:"rows"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.RetCode != 0 {
		return nil, nil
	}
	assetUpper := strings.ToUpper(asset)
	for _, row := range resp.Result.Rows {
		if strings.ToUpper(row.Coin) != assetUpper {
			continue
		}
		for _, ch := range row.Chains {
			chainID := ch.WithdrawChainId
			if chainID == "" {
				chainID = ch.ChainId
			}
			if chainID == "" {
				chainID = bybitNetworkToChainID[strings.ToUpper(ch.Chain)]
			}
			if chainID == "" {
				chainID = ch.Chain
			}
			if ch.ChainWithdraw == "1" {
				withdraw = append(withdraw, model.WithdrawNetworkInfo{
					Network:        ch.Chain,
					ChainID:        chainID,
					WithdrawEnable: true,
				})
			}
			if ch.ChainDeposit == "1" {
				deposit = append(deposit, model.WithdrawNetworkInfo{
					Network:        ch.Chain,
					ChainID:        chainID,
					WithdrawEnable: true,
				})
			}
		}
		break
	}
	return withdraw, deposit
}

// fetchGate GET /api/v4/wallet/currency_chains?currency=USDT 公开接口
func (a *APINetworkRegistry) fetchGate(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/wallet/currency_chains?currency=%s", constants.GateRestBaseUrl, asset)
	body, err := a.doGet(ctx, url)
	if err != nil {
		return nil, nil
	}
	var chains []struct {
		Chain              string `json:"chain"`
		IsDepositDisabled  int    `json:"is_deposit_disabled"`
		IsWithdrawDisabled int    `json:"is_withdraw_disabled"`
	}
	if err := json.Unmarshal(body, &chains); err != nil {
		return nil, nil
	}
	for _, ch := range chains {
		chainID := gateNetworkToChainID[strings.ToUpper(ch.Chain)]
		if chainID == "" {
			chainID = ch.Chain
		}
		if ch.IsWithdrawDisabled == 0 {
			withdraw = append(withdraw, model.WithdrawNetworkInfo{
				Network:        ch.Chain,
				ChainID:        chainID,
				WithdrawEnable: true,
			})
		}
		if ch.IsDepositDisabled == 0 {
			deposit = append(deposit, model.WithdrawNetworkInfo{
				Network:        ch.Chain,
				ChainID:        chainID,
				WithdrawEnable: true,
			})
		}
	}
	return withdraw, deposit
}

// fetchBinance GET /sapi/v1/capital/config/getall 需认证，暂用空
func (a *APINetworkRegistry) fetchBinance(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	// Binance capital config 需 API Key，暂不实现
	return nil, nil
}

// fetchOkex GET /api/v5/asset/currencies 需认证，暂用空
func (a *APINetworkRegistry) fetchOkex(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	// OKX 需认证，暂不实现
	return nil, nil
}

// GetWithdrawNetworks 从缓存返回（Refresh 后才有数据）
func (a *APINetworkRegistry) GetWithdrawNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	ex := strings.ToLower(strings.TrimSpace(exchangeType))
	if ex == "okx" {
		ex = "okex"
	}
	k := a.cacheKey(ex, asset)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if v, ok := a.withdraw[k]; ok {
		return v, nil
	}
	return nil, nil
}

// GetDepositNetworks 从缓存返回
func (a *APINetworkRegistry) GetDepositNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	ex := strings.ToLower(strings.TrimSpace(exchangeType))
	if ex == "okx" {
		ex = "okex"
	}
	k := a.cacheKey(ex, asset)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if v, ok := a.deposit[k]; ok {
		return v, nil
	}
	return nil, nil
}

var bitgetNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARB": "42161",
	"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "BASE": "8453",
	"TRON": "195", "TRC20": "195", "TRX": "195",
}

var bybitNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARBITRUMONE": "42161",
	"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "AVAXC": "43114",
	"BASE": "8453", "TRON": "195", "TRC20": "195", "TRX": "195",
}

var gateNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161",
	"OPTIMISM": "10", "AVAX": "43114", "BASE": "8453",
	"TRON": "195", "TRC20": "195",
}
