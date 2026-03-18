package registry

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

const (
	doGetMaxRetries      = 3
	doGetRetryBackoff    = 500 * time.Millisecond
	delayBetweenRequests = 250 * time.Millisecond // 交易所间请求间隔，铺满探测周期
	defaultCacheTTL      = 10 * time.Minute
)

// APINetworkRegistry 从交易所公开 API 实时获取充提网络，按价差 symbol 刷新（跟随 30s 探测周期）
type APINetworkRegistry struct {
	mu                 sync.RWMutex
	withdraw           map[string][]model.WithdrawNetworkInfo // key: "exchange:asset"
	deposit            map[string][]model.WithdrawNetworkInfo
	withdrawUpdatedAt  map[string]time.Time
	depositUpdatedAt   map[string]time.Time
	cacheTTL           time.Duration
	client             *http.Client
}

// NewAPINetworkRegistry 创建 API 注册表
func NewAPINetworkRegistry() *APINetworkRegistry {
	return NewAPINetworkRegistryWithTTL(defaultCacheTTL)
}

// NewAPINetworkRegistryWithTTL 创建带缓存 TTL 的 API 注册表
func NewAPINetworkRegistryWithTTL(ttl time.Duration) *APINetworkRegistry {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &APINetworkRegistry{
		withdraw:          make(map[string][]model.WithdrawNetworkInfo),
		deposit:           make(map[string][]model.WithdrawNetworkInfo),
		withdrawUpdatedAt: make(map[string]time.Time),
		depositUpdatedAt:  make(map[string]time.Time),
		cacheTTL:          ttl,
		client:            &http.Client{Timeout: 20 * time.Second},
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
		return
	}
	wd := make(map[string][]model.WithdrawNetworkInfo)
	dep := make(map[string][]model.WithdrawNetworkInfo)
	wdTouched := make(map[string]bool)
	depTouched := make(map[string]bool)
	var mergeMu sync.Mutex

	// 5 所交易所并行，每所内串行 + 请求间隔
	exchanges := []string{"bitget", "bybit", "gate", "binance", "okex"}
	var wg sync.WaitGroup
	for _, ex := range exchanges {
		ex := ex
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.refreshOneExchange(ctx, ex, assets, &mergeMu, wd, dep, wdTouched, depTouched)
		}()
	}
	wg.Wait()

	now := time.Now()
	a.mu.Lock()
	// 增量合并：仅更新本轮成功获取到的 key，失败时保留旧缓存，避免全表抖空
	for k, v := range wd {
		if wdTouched[k] {
			a.withdraw[k] = v
			a.withdrawUpdatedAt[k] = now
		}
	}
	for k, v := range dep {
		if depTouched[k] {
			a.deposit[k] = v
			a.depositUpdatedAt[k] = now
		}
	}
	// 清理过期缓存，防止长期残留
	a.cleanupStaleLocked(now)
	a.mu.Unlock()
}

// refreshOneExchange 单所串行请求，每次请求后等待，铺满间隔
// Binance/OKX 需认证，一次请求获取全部，按 asset 过滤
func (a *APINetworkRegistry) refreshOneExchange(ctx context.Context, exchange string, assets []string, mergeMu *sync.Mutex, wd, dep map[string][]model.WithdrawNetworkInfo, wdTouched, depTouched map[string]bool) {
	assetSet := make(map[string]bool)
	for _, s := range assets {
		assetSet[strings.ToUpper(strings.TrimSpace(s))] = true
	}

	// Binance/OKX：一次请求获取全部，需 key
	if exchange == "binance" {
		a.refreshBinanceAll(ctx, assetSet, mergeMu, wd, dep, wdTouched, depTouched)
		return
	}
	if exchange == "okex" || exchange == "okx" {
		a.refreshOkexAll(ctx, assetSet, mergeMu, wd, dep, wdTouched, depTouched)
		return
	}

	okCount := 0
	for i, asset := range assets {
		select {
		case <-ctx.Done():
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
				wdTouched[k] = true
			}
			if len(d) > 0 {
				dep[k] = d
				depTouched[k] = true
			}
			mergeMu.Unlock()
		}
		if i < len(assets)-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delayBetweenRequests):
			}
		}
	}
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

// refreshBinanceAll 一次请求获取 Binance 全部充提网络，需 config.GetExchangeKeys()
func (a *APINetworkRegistry) refreshBinanceAll(ctx context.Context, assetSet map[string]bool, mergeMu *sync.Mutex, wd, dep map[string][]model.WithdrawNetworkInfo, wdTouched, depTouched map[string]bool) {
	keys := config.GetExchangeKeys()
	if keys == nil || !keys.HasKeys("binance") {
		return
	}
	apiKey, secretKey := keys.BinanceKeys()
	if apiKey == "" || secretKey == "" {
		return
	}
	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": strconv.Itoa(constants.BinanceRecvWindow),
	}
	params = binanceSignParams(params, secretKey)
	queryStr := binanceBuildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceCapitalConfigGetAll, queryStr)
	body, err := a.doGetWithHeaders(ctx, apiURL, map[string]string{
		"X-MBX-APIKEY": apiKey,
		"Content-Type": "application/json",
	})
	if err != nil {
		log.Printf("[Registry] Binance getall: %v", err)
		return
	}
	// Binance 错误时返回 {"code":-1022,"msg":"..."}
	if len(body) > 0 && body[0] == '{' {
		var errResp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Code != 0 {
			log.Printf("[Registry] Binance getall: code=%d msg=%s", errResp.Code, errResp.Msg)
			return
		}
	}
	var coins []struct {
		Coin        string `json:"coin"`
		NetworkList []struct {
			Network        string `json:"network"`
			WithdrawEnable bool   `json:"withdrawEnable"`
			DepositEnable  bool   `json:"depositEnable"`
		} `json:"networkList"`
	}
	if err := json.Unmarshal(body, &coins); err != nil {
		return
	}
	for _, c := range coins {
		asset := strings.ToUpper(strings.TrimSpace(c.Coin))
		if !assetSet[asset] {
			continue
		}
		var wdNets, depNets []model.WithdrawNetworkInfo
		for _, n := range c.NetworkList {
			chainID := binanceNetworkToChainID[strings.ToUpper(n.Network)]
			if chainID == "" && n.Network != "" {
				chainID = n.Network
			}
			if n.WithdrawEnable {
				wdNets = append(wdNets, model.WithdrawNetworkInfo{
					Network:        n.Network,
					ChainID:        chainID,
					WithdrawEnable: true,
				})
			}
			if n.DepositEnable {
				depNets = append(depNets, model.WithdrawNetworkInfo{
					Network:        n.Network,
					ChainID:        chainID,
					WithdrawEnable: true,
				})
			}
		}
		if len(wdNets) > 0 || len(depNets) > 0 {
			mergeMu.Lock()
			k := a.cacheKey("binance", asset)
			if len(wdNets) > 0 {
				wd[k] = wdNets
				wdTouched[k] = true
			}
			if len(depNets) > 0 {
				dep[k] = depNets
				depTouched[k] = true
			}
			mergeMu.Unlock()
		}
	}
}

// refreshOkexAll 一次请求获取 OKX 全部充提网络，需 config.GetExchangeKeys()
func (a *APINetworkRegistry) refreshOkexAll(ctx context.Context, assetSet map[string]bool, mergeMu *sync.Mutex, wd, dep map[string][]model.WithdrawNetworkInfo, wdTouched, depTouched map[string]bool) {
	keys := config.GetExchangeKeys()
	if keys == nil || !keys.HasKeys("okx") {
		return
	}
	apiKey, secretKey, passphrase := keys.OKXKeys()
	if apiKey == "" || secretKey == "" {
		return
	}
	requestPath := "/api/v5/asset/currencies"
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	sign := okxBuildSign(timestamp, "GET", requestPath, "", secretKey)
	headers := map[string]string{
		constants.OkexAccessKey:       apiKey,
		constants.OkexAccessSign:      sign,
		constants.OkexAccessTimestamp: timestamp,
		constants.OkexAccessPassphrase: passphrase,
		"Content-Type":               "application/json",
	}
	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)
	body, err := a.doGetWithHeaders(ctx, apiURL, headers)
	if err != nil {
		log.Printf("[Registry] OKX currencies: %v", err)
		return
	}
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Ccy    string `json:"ccy"`
			Chain  string `json:"chain"`
			CanDep bool   `json:"canDep"`
			CanWd  bool   `json:"canWd"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return
	}
	if resp.Code != "0" {
		log.Printf("[Registry] OKX currencies: code=%s msg=%s", resp.Code, resp.Msg)
		return
	}
	// 按 asset 聚合
	wdByAsset := make(map[string][]model.WithdrawNetworkInfo)
	depByAsset := make(map[string][]model.WithdrawNetworkInfo)
	for _, c := range resp.Data {
		asset := strings.ToUpper(strings.TrimSpace(c.Ccy))
		if !assetSet[asset] {
			continue
		}
		chainID := okxChainToChainID(c.Chain)
		if chainID == "" && c.Chain != "" {
			chainID = c.Chain
		}
		info := model.WithdrawNetworkInfo{
			Network:        c.Chain,
			ChainID:        chainID,
			WithdrawEnable: c.CanWd,
		}
		if c.CanWd {
			wdByAsset[asset] = append(wdByAsset[asset], info)
		}
		if c.CanDep {
			depByAsset[asset] = append(depByAsset[asset], info)
		}
	}
	for asset := range assetSet {
		wdNets := wdByAsset[asset]
		depNets := depByAsset[asset]
		if len(wdNets) > 0 || len(depNets) > 0 {
			mergeMu.Lock()
			k := a.cacheKey("okex", asset)
			if len(wdNets) > 0 {
				wd[k] = wdNets
				wdTouched[k] = true
			}
			if len(depNets) > 0 {
				dep[k] = depNets
				depTouched[k] = true
			}
			mergeMu.Unlock()
		}
	}
}

func (a *APINetworkRegistry) isFreshLocked(ts time.Time, now time.Time) bool {
	if ts.IsZero() {
		return false
	}
	return now.Sub(ts) <= a.cacheTTL
}

func (a *APINetworkRegistry) cleanupStaleLocked(now time.Time) {
	for k, ts := range a.withdrawUpdatedAt {
		if !a.isFreshLocked(ts, now) {
			delete(a.withdrawUpdatedAt, k)
			delete(a.withdraw, k)
		}
	}
	for k, ts := range a.depositUpdatedAt {
		if !a.isFreshLocked(ts, now) {
			delete(a.depositUpdatedAt, k)
			delete(a.deposit, k)
		}
	}
}

func (a *APINetworkRegistry) doGetWithHeaders(ctx context.Context, urlStr string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return io.ReadAll(resp.Body)
}

func binanceSignParams(params map[string]string, secretKey string) map[string]string {
	queryString := binanceBuildQueryString(params)
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(queryString))
	params["signature"] = hex.EncodeToString(mac.Sum(nil))
	return params
}

func binanceBuildQueryString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, url.QueryEscape(params[k])))
	}
	return strings.Join(parts, "&")
}

func okxBuildSign(timestamp, method, requestPath, body, secretKey string) string {
	message := timestamp + method + requestPath + body
	mac := hmac.New(sha256.New, []byte(secretKey))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func okxChainToChainID(chain string) string {
	if chain == "" {
		return ""
	}
	okxMap := map[string]string{
		"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
		"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARBITRUM ONE": "42161",
		"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "AVALANCHE C-CHAIN": "43114",
		"BASE": "8453", "TRX": "195", "TRC20": "195",
	}
	if id := okxMap[strings.ToUpper(strings.TrimSpace(chain))]; id != "" {
		return id
	}
	if idx := strings.Index(chain, "-"); idx >= 0 && idx < len(chain)-1 {
		suffix := strings.TrimSpace(chain[idx+1:])
		if id := okxMap[strings.ToUpper(suffix)]; id != "" {
			return id
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "ARBITRUM") {
			return "42161"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "OPTIMISM") || strings.HasPrefix(strings.ToUpper(suffix), "OP ") {
			return "10"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "AVALANCHE") || strings.HasPrefix(strings.ToUpper(suffix), "AVAX") {
			return "43114"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "POLYGON") || strings.HasPrefix(strings.ToUpper(suffix), "MATIC") {
			return "137"
		}
	}
	return ""
}

var binanceNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARB": "42161",
	"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "AVAXC": "43114",
	"BASE": "8453", "TRON": "195", "TRC20": "195", "TRX": "195",
}

// fetchBinance 单资产调用（由 refreshBinanceAll 替代，此处保留供 fetchNetworksWithRetry 兜底）
func (a *APINetworkRegistry) fetchBinance(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
	return nil, nil
}

// fetchOkex 单资产调用（由 refreshOkexAll 替代，此处保留供 fetchNetworksWithRetry 兜底）
func (a *APINetworkRegistry) fetchOkex(ctx context.Context, asset string) (withdraw, deposit []model.WithdrawNetworkInfo) {
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
	ts := a.withdrawUpdatedAt[k]
	a.mu.RUnlock()
	if ok && time.Since(ts) <= a.cacheTTL {
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
	v, ok := a.deposit[k]
	ts := a.depositUpdatedAt[k]
	a.mu.RUnlock()
	if ok && time.Since(ts) <= a.cacheTTL {
		return v, nil
	}
	return nil, nil
}

var bitgetNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARB": "42161",
	"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "BASE": "8453",
	"TRON": "195", "TRC20": "195", "TRX": "195",
	"ECLIPSE": "1111", "SUI": "784", "TON": "607", "ALLORA": "allora",
	"MANTRA": "1442", "SOL": "solana", "VANRY": "vanry",
}

var bybitNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161", "ARBITRUMONE": "42161",
	"OPTIMISM": "10", "OP": "10", "AVAX": "43114", "AVAXC": "43114",
	"BASE": "8453", "TRON": "195", "TRC20": "195", "TRX": "195",
	"ECLIPSE": "1111", "SUI": "784", "TON": "607", "ALLORA": "allora",
	"MANTRA": "1442", "SOL": "solana", "VANRY": "vanry",
}

var gateNetworkToChainID = map[string]string{
	"ETH": "1", "ERC20": "1", "BSC": "56", "BEP20": "56", "BNB": "56",
	"MATIC": "137", "POLYGON": "137", "ARBITRUM": "42161",
	"OPTIMISM": "10", "AVAX": "43114", "BASE": "8453",
	"TRON": "195", "TRC20": "195", "TRX": "195",
	"ECLIPSE": "1111", "SUI": "784", "TON": "607", "ALLORA": "allora",
	"MANTRA": "1442", "SOL": "solana", "VANRY": "vanry",
}
