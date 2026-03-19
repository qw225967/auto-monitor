package ticker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	TickerReqTimeout = 5 * time.Second
	TickerInterval   = 3 * time.Second
)

// OnPriceFunc 收到 ticker 价格后的回调
type OnPriceFunc func(symbol, exchange string, price float64, ts time.Time)

// Fetcher Ticker 拉取器，每交易所 1 次请求获取全市场最新价，响应快于 K 线
type Fetcher struct {
	client  *http.Client
	symbols []string
	exchs   []string
	onPrice OnPriceFunc
	mu      sync.RWMutex
}

// NewFetcher 创建 Ticker 拉取器
func NewFetcher(exchanges []string) *Fetcher {
	return &Fetcher{
		client:  &http.Client{Timeout: TickerReqTimeout},
		symbols: nil,
		exchs:   exchanges,
	}
}

// SetSymbols 设置待拉取 symbol 列表
func (f *Fetcher) SetSymbols(symbols []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.symbols = symbols
}

// SetOnPrice 设置价格回调
func (f *Fetcher) SetOnPrice(fn OnPriceFunc) {
	f.onPrice = fn
}

// RunBatch 执行一轮拉取，每交易所 1 次请求
func (f *Fetcher) RunBatch(ctx context.Context) {
	f.mu.RLock()
	symbols := f.symbols
	exchs := f.exchs
	f.mu.RUnlock()

	if len(symbols) == 0 {
		return
	}

	symbolSet := make(map[string]bool)
	for _, s := range symbols {
		symbolSet[s] = true
	}

	now := time.Now()
	var wg sync.WaitGroup
	for _, ex := range exchs {
		wg.Add(1)
		go func(exchange string) {
			defer wg.Done()
			prices, err := f.fetchTickers(ctx, exchange)
			if err != nil {
				return
			}
			if f.onPrice == nil {
				return
			}
			for sym, price := range prices {
				if symbolSet[sym] && price > 0 {
					f.onPrice(sym, exchange, price, now)
				}
			}
		}(ex)
	}
	wg.Wait()
}

func (f *Fetcher) fetchTickers(ctx context.Context, exchange string) (map[string]float64, error) {
	ex := strings.ToLower(exchange)
	switch ex {
	case "binance":
		return f.fetchBinance(ctx)
	case "bybit":
		return f.fetchBybit(ctx)
	case "okx":
		return f.fetchOKX(ctx)
	case "gate":
		return f.fetchGate(ctx)
	case "bitget":
		return f.fetchBitget(ctx)
	default:
		return nil, fmt.Errorf("unsupported: %s", exchange)
	}
}

// Binance: GET /api/v3/ticker/price 返回全市场
func (f *Fetcher) fetchBinance(ctx context.Context) (map[string]float64, error) {
	u := "https://api.binance.com/api/v3/ticker/price"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Symbol string `json:"symbol"`
		Price  string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]float64)
	for _, r := range raw {
		if p, err := strconv.ParseFloat(r.Price, 64); err == nil {
			out[r.Symbol] = p
		}
	}
	return out, nil
}

// Bybit: GET /v5/market/tickers?category=spot
func (f *Fetcher) fetchBybit(ctx context.Context) (map[string]float64, error) {
	u := "https://api.bybit.com/v5/market/tickers?category=spot"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			List []struct {
				Symbol    string `json:"symbol"`
				LastPrice string `json:"lastPrice"`
			} `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	m := make(map[string]float64)
	for _, r := range out.Result.List {
		if p, err := strconv.ParseFloat(r.LastPrice, 64); err == nil {
			m[r.Symbol] = p
		}
	}
	return m, nil
}

// OKX: GET /api/v5/market/tickers?instType=SPOT
func (f *Fetcher) fetchOKX(ctx context.Context) (map[string]float64, error) {
	u := "https://www.okx.com/api/v5/market/tickers?instType=SPOT"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			InstID  string `json:"instId"`
			Last    string `json:"last"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	m := make(map[string]float64)
	for _, r := range out.Data {
		sym := strings.ReplaceAll(r.InstID, "-", "")
		if p, err := strconv.ParseFloat(r.Last, 64); err == nil {
			m[sym] = p
		}
	}
	return m, nil
}

// Gate: GET /api/v4/spot/tickers
func (f *Fetcher) fetchGate(ctx context.Context) (map[string]float64, error) {
	u := "https://api.gateio.ws/api/v4/spot/tickers"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		CurrencyPair string `json:"currency_pair"`
		Last         string `json:"last"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	m := make(map[string]float64)
	for _, r := range raw {
		sym := strings.ReplaceAll(r.CurrencyPair, "_", "")
		if p, err := strconv.ParseFloat(r.Last, 64); err == nil {
			m[sym] = p
		}
	}
	return m, nil
}

// Bitget: GET /api/v2/spot/market/tickers
func (f *Fetcher) fetchBitget(ctx context.Context) (map[string]float64, error) {
	u := "https://api.bitget.com/api/v2/spot/market/tickers"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			Symbol string `json:"symbol"`
			LastPr string `json:"lastPr"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	m := make(map[string]float64)
	for _, r := range out.Data {
		if p, err := strconv.ParseFloat(r.LastPr, 64); err == nil {
			m[r.Symbol] = p
		}
	}
	return m, nil
}

// RunLoop 后台循环拉取，每 3s 一轮
func (f *Fetcher) RunLoop(ctx context.Context) {
	ticker := time.NewTicker(TickerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.RunBatch(ctx)
			f.mu.RLock()
			n := len(f.symbols)
			f.mu.RUnlock()
			if n > 0 {
				log.Printf("[Ticker] 本轮回传完成, symbols=%d exchanges=%d", n, len(f.exchs))
			}
		}
	}
}
