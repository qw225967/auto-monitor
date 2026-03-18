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
	"sync/atomic"
	"time"
)

const (
	// BatchTimeout 单轮拉取总超时（含均衡摊开延迟）
	BatchTimeout = 10 * time.Second
	// ReqTimeout 单次请求超时
	ReqTimeout = 2 * time.Second
	// MaxConcurrent 最大并发请求数
	MaxConcurrent = 64
	// KlineLimit 每次拉取 K 线根数
	KlineLimit = 100
	// StaggerWindow 请求摊开的时间窗口（每个币约 3s 一次）
	StaggerWindow = 3 * time.Second
)

// OnAppendFunc 追加 K 线后的回调，用于同步到 PriceHistory 等
type OnAppendFunc func(symbol, exchange string, bars []KlinePoint)

// Fetcher K 线拉取器，支持多交易所、分批并发，请求在 3s 内均衡摊开
type Fetcher struct {
	client   *http.Client
	store    *Store
	symbols  []string
	exchs    []string
	onAppend OnAppendFunc
	fedPairs int64 // 本轮成功喂入的 (symbol,exchange) 对数，用于诊断
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

// SetSymbols 更新待拉取 symbol 列表
func (f *Fetcher) SetSymbols(symbols []string) {
	f.symbols = symbols
}

// SetOnAppend 设置追加后的回调（如同步到 PriceHistory）
func (f *Fetcher) SetOnAppend(fn OnAppendFunc) {
	f.onAppend = fn
}

// RunBatch 执行一轮拉取，请求在 StaggerWindow(3s) 内均衡摊开
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

	total := len(tasks)
	sem := make(chan struct{}, MaxConcurrent)
	var wg sync.WaitGroup
	loopStart := time.Now()
	for i, t := range tasks {
		select {
		case <-ctx.Done():
			break
		default:
		}
		// 均衡摊开：第 i 个任务在 i*(StaggerWindow/total) 时启动，避免瞬时 burst
		if total > 0 && i > 0 {
			targetStart := loopStart.Add(time.Duration(i) * StaggerWindow / time.Duration(total))
			if now := time.Now(); now.Before(targetStart) {
				select {
				case <-ctx.Done():
					break
				case <-time.After(time.Until(targetStart)):
				}
			}
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
					atomic.AddInt64(&f.fedPairs, 1)
				}
			}
		}(t.symbol, t.exchange)
	}
	wg.Wait()
	fed := atomic.SwapInt64(&f.fedPairs, 0)
	if len(tasks) > 0 {
		log.Printf("[Kline] 本轮回传: %d/%d 对 (symbol,exchange) 有 bar 且已喂入 PriceHistory", fed, len(tasks))
	}
}

func (f *Fetcher) fetchOne(ctx context.Context, symbol, exchange string) ([]KlinePoint, error) {
	ex := strings.ToLower(exchange)
	switch ex {
	case "binance":
		return f.fetchBinance(ctx, symbol)
	case "bybit":
		return f.fetchBybit(ctx, symbol)
	case "okx":
		return f.fetchOKX(ctx, symbol)
	case "gate":
		return f.fetchGate(ctx, symbol)
	case "bitget":
		return f.fetchBitget(ctx, symbol)
	default:
		return nil, fmt.Errorf("unsupported exchange: %s", exchange)
	}
}

// Binance: GET /api/v3/klines?symbol=X&interval=1m&limit=100
func (f *Fetcher) fetchBinance(ctx context.Context, symbol string) ([]KlinePoint, error) {
	u := "https://api.binance.com/api/v3/klines?symbol=" + url.QueryEscape(symbol) + "&interval=1m&limit=" + strconv.Itoa(KlineLimit)
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

// Bybit: GET /v5/market/kline?category=spot&symbol=X&interval=1&limit=100 (interval=1 表示 1 分钟)
func (f *Fetcher) fetchBybit(ctx context.Context, symbol string) ([]KlinePoint, error) {
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

// OKX: GET /api/v5/market/candles?instId=X-USDT&bar=1m&limit=100
func (f *Fetcher) fetchOKX(ctx context.Context, symbol string) ([]KlinePoint, error) {
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

// Gate: GET /api/v4/spot/candlesticks?currency_pair=X_USDT&interval=1m&limit=100
// 返回 [[timestamp, volume, close, high, low, open], ...]，timestamp 为秒
func (f *Fetcher) fetchGate(ctx context.Context, symbol string) ([]KlinePoint, error) {
	cp := strings.ReplaceAll(symbol, "USDT", "_USDT")
	u := "https://api.gateio.ws/api/v4/spot/candlesticks?currency_pair=" + url.QueryEscape(cp) + "&interval=1m&limit=" + strconv.Itoa(KlineLimit)
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

// Bitget: GET /api/v2/spot/market/candles?symbol=X&period=1m&limit=100
func (f *Fetcher) fetchBitget(ctx context.Context, symbol string) ([]KlinePoint, error) {
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

// RunLoop 后台循环拉取，每 3s 一轮（每轮完成后等待至下一 3s 周期）
func (f *Fetcher) RunLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		start := time.Now()
		f.RunBatch(ctx)
		elapsed := time.Since(start)
		log.Printf("[Kline] batch done, symbols=%d exchanges=%d, elapsed=%v", len(f.symbols), len(f.exchs), elapsed.Round(time.Millisecond))
		// 等待至下一 3s 周期
		if remaining := StaggerWindow - elapsed; remaining > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(remaining):
			}
		}
	}
}
