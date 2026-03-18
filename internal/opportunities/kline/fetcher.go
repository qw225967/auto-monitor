package kline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// BatchTimeout 单轮拉取总超时
	BatchTimeout = 3 * time.Second
	// ReqTimeout 单次请求超时
	ReqTimeout = 2 * time.Second
	// MaxConcurrent 最大并发请求数
	MaxConcurrent = 8
	// KlineLimit 每次拉取 K 线根数
	KlineLimit = 100

	// 秒级间隔（各交易所支持情况：Binance 1s, Gate 10s, Bybit/OKX/Bitget 最低 1m）
	Interval1s  = "1s"
	Interval10s = "10s"
	Interval1m  = "1m"
)

// OnAppendFunc 追加 K 线后的回调，用于同步到 PriceHistory 等
type OnAppendFunc func(symbol, exchange string, bars []KlinePoint)

// Fetcher K 线拉取器，支持多交易所、分批并发
type Fetcher struct {
	client   *http.Client
	store    *Store
	symbols  []string
	exchs    []string
	onAppend OnAppendFunc
	// useSecondLevel 为 true 时：Binance 用 1s，Gate 用 10s，其它用 1m
	useSecondLevel bool
}

// NewFetcher 创建拉取器
func NewFetcher(store *Store, symbols []string, exchanges []string) *Fetcher {
	return &Fetcher{
		client:  &http.Client{Timeout: ReqTimeout},
		store:   store,
		symbols: symbols,
		exchs:   exchanges,
	}
}

// SetUseSecondLevel 启用秒级 K 线（Binance 1s, Gate 10s, 其它 1m）
func (f *Fetcher) SetUseSecondLevel(use bool) {
	f.useSecondLevel = use
}

func (f *Fetcher) intervalFor(exchange string) string {
	if !f.useSecondLevel {
		return Interval1m
	}
	switch strings.ToLower(exchange) {
	case "binance":
		return Interval1s
	case "gate":
		return Interval10s
	default:
		return Interval1m
	}
}

// SetSymbols 更新待拉取 symbol 列表
func (f *Fetcher) SetSymbols(symbols []string) {
	f.symbols = symbols
}

// SetOnAppend 设置追加后的回调（如同步到 PriceHistory）
func (f *Fetcher) SetOnAppend(fn OnAppendFunc) {
	f.onAppend = fn
}

// RunBatch 执行一轮拉取，3s 内分批完成
func (f *Fetcher) RunBatch(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, BatchTimeout)
	defer cancel()

	var tasks []struct {
		symbol   string
		exchange string
	}
	for _, sym := range f.symbols {
		for _, ex := range f.exchs {
			tasks = append(tasks, struct {
				symbol   string
				exchange string
			}{sym, ex})
		}
	}
	if len(tasks) == 0 {
		return
	}

	sem := make(chan struct{}, MaxConcurrent)
	var wg sync.WaitGroup
	for _, t := range tasks {
		select {
		case <-ctx.Done():
			break
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(symbol, exchange string) {
			defer wg.Done()
			defer func() { <-sem }()
			bars, err := f.fetchOne(ctx, symbol, exchange)
			if err != nil {
				return
			}
			if len(bars) > 0 {
				f.store.Append(symbol, exchange, bars)
				if f.onAppend != nil {
					f.onAppend(symbol, exchange, bars)
				}
			}
		}(t.symbol, t.exchange)
	}
	wg.Wait()
}

func (f *Fetcher) fetchOne(ctx context.Context, symbol, exchange string) ([]KlinePoint, error) {
	ex := strings.ToLower(exchange)
	switch ex {
	case "binance":
		return f.fetchBinance(ctx, symbol, f.intervalFor(ex))
	case "bybit":
		return f.fetchBybit(ctx, symbol, f.intervalFor(ex))
	case "okx":
		return f.fetchOKX(ctx, symbol, f.intervalFor(ex))
	case "gate":
		return f.fetchGate(ctx, symbol, f.intervalFor(ex))
	case "bitget":
		return f.fetchBitget(ctx, symbol, f.intervalFor(ex))
	default:
		return nil, fmt.Errorf("unsupported exchange: %s", exchange)
	}
}

// Binance: GET /api/v3/klines?symbol=X&interval=1m|1s&limit=100
func (f *Fetcher) fetchBinance(ctx context.Context, symbol, interval string) ([]KlinePoint, error) {
	u := "https://api.binance.com/api/v3/klines?symbol=" + url.QueryEscape(symbol) + "&interval=" + interval + "&limit=" + strconv.Itoa(KlineLimit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var bars []KlinePoint
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}
		ts, _ := toInt64(row[0])
		open, _ := toFloat(row[1])
		high, _ := toFloat(row[2])
		low, _ := toFloat(row[3])
		close, _ := toFloat(row[4])
		vol, _ := toFloat(row[5])
		bars = append(bars, KlinePoint{
			Timestamp: time.UnixMilli(ts),
			Open:      open, High: high, Low: low, Close: close,
			Volume: vol,
		})
	}
	return bars, nil
}

// Bybit: GET /v5/market/kline?category=spot&symbol=X&interval=1&limit=100 (interval=1 表示 1 分钟，无秒级)
func (f *Fetcher) fetchBybit(ctx context.Context, symbol, _ string) ([]KlinePoint, error) {
	iv := "1" // Bybit 最小 1 分钟
	u := "https://api.bybit.com/v5/market/kline?category=spot&symbol=" + url.QueryEscape(symbol) + "&interval=" + iv + "&limit=" + strconv.Itoa(KlineLimit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			List [][]string `json:"list"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var bars []KlinePoint
	for i := len(out.Result.List) - 1; i >= 0; i-- {
		row := out.Result.List[i]
		if len(row) < 6 {
			continue
		}
		ts, _ := strconv.ParseInt(row[0], 10, 64)
		open, _ := strconv.ParseFloat(row[1], 64)
		high, _ := strconv.ParseFloat(row[2], 64)
		low, _ := strconv.ParseFloat(row[3], 64)
		close, _ := strconv.ParseFloat(row[4], 64)
		vol, _ := strconv.ParseFloat(row[5], 64)
		bars = append(bars, KlinePoint{
			Timestamp: time.UnixMilli(ts),
			Open:      open, High: high, Low: low, Close: close,
			Volume: vol,
		})
	}
	return bars, nil
}

// OKX: GET /api/v5/market/candles?instId=X-USDT&bar=1m&limit=100 (现货最小 1m)
func (f *Fetcher) fetchOKX(ctx context.Context, symbol, _ string) ([]KlinePoint, error) {
	instId := strings.ReplaceAll(symbol, "USDT", "-USDT")
	bar := "1m"
	u := "https://www.okx.com/api/v5/market/candles?instId=" + url.QueryEscape(instId) + "&bar=" + bar + "&limit=" + strconv.Itoa(KlineLimit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data [][]string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var bars []KlinePoint
	for _, row := range out.Data {
		if len(row) < 5 {
			continue
		}
		ts, _ := strconv.ParseInt(row[0], 10, 64)
		open, _ := strconv.ParseFloat(row[1], 64)
		high, _ := strconv.ParseFloat(row[2], 64)
		low, _ := strconv.ParseFloat(row[3], 64)
		close, _ := strconv.ParseFloat(row[4], 64)
		vol := 0.0
		if len(row) >= 6 {
			vol, _ = strconv.ParseFloat(row[5], 64)
		}
		bars = append(bars, KlinePoint{
			Timestamp: time.UnixMilli(ts),
			Open:      open, High: high, Low: low, Close: close,
			Volume: vol,
		})
	}
	return bars, nil
}

// Gate: GET /api/v4/spot/candlesticks?currency_pair=X_USDT&interval=1m|10s&limit=100
// 返回 [[timestamp, volume, close, high, low, open], ...]，timestamp 为秒
func (f *Fetcher) fetchGate(ctx context.Context, symbol, interval string) ([]KlinePoint, error) {
	cp := strings.ReplaceAll(symbol, "USDT", "_USDT")
	iv := interval
	if iv == Interval1s {
		iv = Interval10s // Gate 最小 10s
	}
	u := "https://api.gateio.ws/api/v4/spot/candlesticks?currency_pair=" + url.QueryEscape(cp) + "&interval=" + iv + "&limit=" + strconv.Itoa(KlineLimit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw [][]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var bars []KlinePoint
	for _, row := range raw {
		if len(row) < 6 {
			continue
		}
		ts, _ := toInt64(row[0])
		vol, _ := toFloat(row[1])
		close, _ := toFloat(row[2])
		high, _ := toFloat(row[3])
		low, _ := toFloat(row[4])
		open, _ := toFloat(row[5])
		// Gate 时间戳为秒
		bars = append(bars, KlinePoint{
			Timestamp: time.Unix(ts, 0),
			Open:      open, High: high, Low: low, Close: close,
			Volume: vol,
		})
	}
	return bars, nil
}

// Bitget: GET /api/v2/spot/market/candles?symbol=X&period=1m&limit=100 (无秒级)
func (f *Fetcher) fetchBitget(ctx context.Context, symbol, _ string) ([]KlinePoint, error) {
	period := "1m"
	u := "https://api.bitget.com/api/v2/spot/market/candles?symbol=" + url.QueryEscape(symbol) + "&period=" + period + "&limit=" + strconv.Itoa(KlineLimit)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data [][]string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var bars []KlinePoint
	for _, row := range out.Data {
		if len(row) < 6 {
			continue
		}
		ts, _ := strconv.ParseInt(row[0], 10, 64)
		open, _ := strconv.ParseFloat(row[1], 64)
		high, _ := strconv.ParseFloat(row[2], 64)
		low, _ := strconv.ParseFloat(row[3], 64)
		close, _ := strconv.ParseFloat(row[4], 64)
		vol, _ := strconv.ParseFloat(row[5], 64)
		bars = append(bars, KlinePoint{
			Timestamp: time.UnixMilli(ts),
			Open:      open, High: high, Low: low, Close: close,
			Volume: vol,
		})
	}
	return bars, nil
}

func toFloat(v interface{}) (float64, error) {
	switch x := v.(type) {
	case string:
		return strconv.ParseFloat(x, 64)
	case float64:
		return x, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func toInt64(v interface{}) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// RunLoop 后台循环拉取，每 3s 一轮
func (f *Fetcher) RunLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.RunBatch(ctx)
			log.Printf("[Kline] batch done, symbols=%d exchanges=%d", len(f.symbols), len(f.exchs))
		}
	}
}
