package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

const (
	doGetMaxRetries      = 3
	doGetRetryBackoff    = 500 * time.Millisecond
	delayBetweenRequests = 250 * time.Millisecond // 交易所间请求间隔，铺满探测周期
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
		client:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (a *APINetworkRegistry) cacheKey(exchange, asset string) string {
	return strings.ToLower(strings.TrimSpace(exchange)) + ":" + strings.ToUpper(strings.TrimSpace(asset))
}

// Refresh 用价差中的 symbols 刷新充提网络（每 30s 探测前调用）
// 5 所交易所全部请求，每个交易所内请求之间加间隔，铺满探测周期
func (a *APINetworkRegistry) Refresh(ctx context.Context, symbols []string) {
	assets := tokenregistry.ExtractAssetsFromSymbols(symbols)
	if len(assets) == 0 {
		log.Printf("[Registry] Refresh: 无 assets，symbols=%v", symbols)
		return
	}
	log.Printf("[Registry] Refresh: symbols=%d assets=%d 示例=%v", len(symbols), len(assets), truncateSlice(assets, 5))

	wd := make(map[string][]model.WithdrawNetworkInfo)
	dep := make(map[string][]model.WithdrawNetworkInfo)
	var mergeMu sync.Mutex

	// 5 所交易所并行，每所内串行 + 请求间隔
	exchanges := []string{"bitget", "bybit", "gate", "binance", "okex"}
	var wg sync.WaitGroup
	for _, ex := range exchanges {
		ex := ex
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.refreshOneExchange(ctx, ex, assets, &mergeMu, wd, dep)
		}()
	}
	wg.Wait()

	// 统计每所缓存数量
	wdByEx := make(map[string]int)
	depByEx := make(map[string]int)
	for k := range wd {
		ex := strings.SplitN(k, ":", 2)[0]
		wdByEx[ex]++
	}
	for k := range dep {
		ex := strings.SplitN(k, ":", 2)[0]
		depByEx[ex]++
	}
	log.Printf("[Registry] Refresh 完成: wd=%v dep=%v", wdByEx, depByEx)

	a.mu.Lock()
	a.withdraw = wd
	a.deposit = dep
	a.mu.Unlock()
}

func truncateSlice(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), "...")
}

// refreshOneExchange 单所串行请求，每次请求后等待，铺满间隔
func (a *APINetworkRegistry) refreshOneExchange(ctx context.Context, exchange string, assets []string, mergeMu *sync.Mutex, wd, dep map[string][]model.WithdrawNetworkInfo) {
	okCount := 0
	for i, asset := range assets {
		select {
		case <-ctx.Done():
			log.Printf("[Registry] %s 被取消，已缓存 %d/%d", exchange, okCount, len(assets))
			return
		default:
		}
		asset = strings.ToUpper(strings.TrimSpace(asset))
		w, d := a.fetchNetworksWithRetry(ctx, exchange, asset)
		if len(w) > 0 || len(d) > 0 {
			okCount++
			mergeMu.Lock()
			k := a.cacheKey(exchange, asset)
			if len(w) > 0 {
				wd[k] = w
			}
			if len(d) > 0 {
				dep[k] = d
			}
			mergeMu.Unlock()
		}
		// 请求间隔，最后一次不等待
		if i < len(assets)-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delayBetweenRequests):
			}
		}
	}
	log.Printf("[Registry] %s 完成: %d/%d 有数据", exchange, okCount, len(assets))
}

func (a *APINetworkRegistry) fetchNetworksWithRetry(ctx context.Context, exchange, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	switch exchange {
	case "bitget":
		return a.fetchBitgetWithRetry(ctx, asset)
	case "bybit":
		return a.fetchBybitWithRetry(ctx, asset)
	case "gate":
		return a.fetchGateWithRetry(ctx, asset)
	case "binance":
		return a.fetchBinance(ctx, asset)
	case "okex", "okx":
		return a.fetchOkex(ctx, asset)
	}
	return nil, nil
}

// doGetWithRetry 带重试的 GET，对 429、超时进行退避重试
func (a *APINetworkRegistry) doGetWithRetry(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for i := 0; i < doGetMaxRetries; i++ {
		body, err := a.doGet(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, lastErr
		}
		// 429 或超时则重试
		if strings.Contains(err.Error(), "429") || strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "deadline") {
			select {
			case <-ctx.Done():
				return nil, lastErr
			case <-time.After(doGetRetryBackoff * time.Duration(i+1)):
				continue
			}
		}
		return nil, lastErr
	}
	return nil, lastErr
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

// fetchBitgetWithRetry GET /api/v2/spot/public/coins?coin=X 公开接口，带重试
func (a *APINetworkRegistry) fetchBitgetWithRetry(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/api/v2/spot/public/coins?coin=%s", constants.BitgetRestBaseUrl, asset)
	body, err := a.doGetWithRetry(ctx, url)
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

// fetchBybitWithRetry GET /v5/asset/coin/query-info?coin=USDT 公开接口，带重试
func (a *APINetworkRegistry) fetchBybitWithRetry(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/v5/asset/coin/query-info?coin=%s", constants.BybitRestBaseUrl, asset)
	body, err := a.doGetWithRetry(ctx, url)
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

// fetchGateWithRetry GET /api/v4/wallet/currency_chains?currency=USDT 公开接口，带重试
func (a *APINetworkRegistry) fetchGateWithRetry(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	url := fmt.Sprintf("%s/wallet/currency_chains?currency=%s", constants.GateRestBaseUrl, asset)
	body, err := a.doGetWithRetry(ctx, url)
	if err != nil {
		return nil, nil
	}
	var chains []struct {
		Chain              string `json:"chain"`
		IsDisabled         int    `json:"is_disabled"`
		IsDepositDisabled  int    `json:"is_deposit_disabled"`
		IsWithdrawDisabled int    `json:"is_withdraw_disabled"`
	}
	if err := json.Unmarshal(body, &chains); err != nil {
		return nil, nil
	}
	for _, ch := range chains {
		// 暂停提币/充币或整链禁用的不加入
		if ch.IsDisabled == 1 {
			continue
		}
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
	v, ok := a.withdraw[k]
	a.mu.RUnlock()
	if ok {
		return v, nil
	}
	// 仅对有公开 API 的交易所记录未命中（binance/okex 需认证，预期为空）
	if ex == "bitget" || ex == "gate" || ex == "bybit" {
		log.Printf("[Registry] GetWithdrawNetworks 缓存未命中: %s", k)
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
	v, ok := a.deposit[k]
	a.mu.RUnlock()
	if ok {
		return v, nil
	}
	if ex == "bitget" || ex == "gate" || ex == "bybit" {
		log.Printf("[Registry] GetDepositNetworks 缓存未命中: %s", k)
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
