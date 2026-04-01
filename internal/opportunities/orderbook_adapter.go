package opportunities

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// httpOrderbookAdapter 基于 HTTP 公共 API 的订单簿适配器（无需鉴权）
type httpOrderbookAdapter struct {
	client       *http.Client
	baseURL      string
	exchange     string
	symbolFmt    func(string) string // 将 BTCUSDT 转为交易所格式
	fetchSpot    func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error)
	fetchFutures func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error)
	fetchTrades  func(ctx context.Context, a *httpOrderbookAdapter, symbol string, limit int) (float64, error)
}

func (a *httpOrderbookAdapter) GetSpotOrderBook(symbol string) ([][]string, [][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.fetchSpot(ctx, a, symbol)
}

func (a *httpOrderbookAdapter) GetFuturesOrderBook(symbol string) ([][]string, [][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.fetchFutures(ctx, a, symbol)
}

func (a *httpOrderbookAdapter) GetRecentTrades(symbol string, limit int) (float64, error) {
	if a.fetchTrades == nil {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.fetchTrades(ctx, a, symbol, limit)
}

// GetBestBidQty 获取现货第一档 bid 挂单量（一手量）及最优买价
func (a *httpOrderbookAdapter) GetBestBidQty(symbol string) (qty float64, price float64, err error) {
	bids, _, err := a.GetSpotOrderBook(symbol)
	if err != nil || len(bids) == 0 {
		return 0, 0, err
	}
	fmt.Sscanf(bids[0][0], "%f", &price)
	fmt.Sscanf(bids[0][1], "%f", &qty)
	return qty, price, nil
}

// newBinanceOrderbookAdapter 创建 Binance 公共订单簿适配器
func newBinanceOrderbookAdapter() *httpOrderbookAdapter {
	return &httpOrderbookAdapter{
		client:    &http.Client{Timeout: 10 * time.Second},
		baseURL:   "https://api.binance.com",
		exchange:  "binance",
		symbolFmt: func(s string) string { return s },
		fetchSpot: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := a.baseURL + "/api/v3/depth?symbol=" + url.QueryEscape(symbol) + "&limit=50"
			return fetchBinanceOrderbook(ctx, a.client, u)
		},
		fetchFutures: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := "https://fapi.binance.com/fapi/v1/depth?symbol=" + url.QueryEscape(symbol) + "&limit=100"
			return fetchBinanceOrderbook(ctx, a.client, u)
		},
		fetchTrades: binanceFetchRecentTrades,
	}
}

// binanceFetchRecentTrades 获取 Binance 近期成交，累加 quoteQty 返回总成交额（USDT）
func binanceFetchRecentTrades(ctx context.Context, a *httpOrderbookAdapter, symbol string, limit int) (float64, error) {
	u := fmt.Sprintf("%s/api/v3/trades?symbol=%s&limit=%d", a.baseURL, url.QueryEscape(symbol), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var trades []struct {
		QuoteQty string `json:"quoteQty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&trades); err != nil {
		return 0, err
	}

	var totalQuoteQty float64
	for _, trade := range trades {
		var qty float64
		fmt.Sscanf(trade.QuoteQty, "%f", &qty)
		totalQuoteQty += qty
	}
	return totalQuoteQty, nil
}

func fetchBinanceOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Bids [][]interface{} `json:"bids"`
		Asks [][]interface{} `json:"asks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	bids := parseOrderbookSide(out.Bids)
	asks := parseOrderbookSide(out.Asks)
	return bids, asks, nil
}

// newBybitOrderbookAdapter 创建 Bybit 公共订单簿适配器
func newBybitOrderbookAdapter() *httpOrderbookAdapter {
	return &httpOrderbookAdapter{
		client:    &http.Client{Timeout: 10 * time.Second},
		baseURL:   "https://api.bybit.com",
		exchange:  "bybit",
		symbolFmt: func(s string) string { return s },
		fetchSpot: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := a.baseURL + "/v5/market/orderbook?category=spot&symbol=" + url.QueryEscape(symbol) + "&limit=100"
			return fetchBybitOrderbook(ctx, a.client, u)
		},
		fetchFutures: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := a.baseURL + "/v5/market/orderbook?category=linear&symbol=" + url.QueryEscape(symbol) + "&limit=100"
			return fetchBybitOrderbook(ctx, a.client, u)
		},
	}
}

func fetchBybitOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Bids [][]interface{} `json:"b"`
			Asks [][]interface{} `json:"a"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	bids := parseOrderbookSide(out.Result.Bids)
	asks := parseOrderbookSide(out.Result.Asks)
	return bids, asks, nil
}

// newOKXOrderbookAdapter 创建 OKX 公共订单簿适配器
func newOKXOrderbookAdapter() *httpOrderbookAdapter {
	return &httpOrderbookAdapter{
		client:   &http.Client{Timeout: 10 * time.Second},
		baseURL:  "https://www.okx.com",
		exchange: "okx",
		symbolFmt: func(s string) string {
			return strings.ReplaceAll(s, "USDT", "-USDT")
		},
		fetchSpot: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			instId := strings.ReplaceAll(symbol, "USDT", "-USDT")
			u := a.baseURL + "/api/v5/market/books?instId=" + url.QueryEscape(instId) + "&sz=100"
			return fetchOKXOrderbook(ctx, a.client, u)
		},
		fetchFutures: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			instId := strings.ReplaceAll(symbol, "USDT", "-USDT") + "-SWAP"
			u := a.baseURL + "/api/v5/market/books?instId=" + url.QueryEscape(instId) + "&sz=100"
			return fetchOKXOrderbook(ctx, a.client, u)
		},
	}
}

func fetchOKXOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data []struct {
			Bids [][]interface{} `json:"bids"`
			Asks [][]interface{} `json:"asks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil, fmt.Errorf("okx empty orderbook")
	}
	bids := parseOrderbookSide(out.Data[0].Bids)
	asks := parseOrderbookSide(out.Data[0].Asks)
	return bids, asks, nil
}

// newGateOrderbookAdapter 创建 Gate.io 公共订单簿适配器
func newGateOrderbookAdapter() *httpOrderbookAdapter {
	return &httpOrderbookAdapter{
		client:   &http.Client{Timeout: 10 * time.Second},
		baseURL:  "https://api.gateio.ws/api/v4",
		exchange: "gate",
		symbolFmt: func(s string) string {
			return strings.ReplaceAll(s, "USDT", "_USDT")
		},
		fetchSpot: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			cp := strings.ReplaceAll(symbol, "USDT", "_USDT")
			u := a.baseURL + "/spot/order_book?currency_pair=" + url.QueryEscape(cp) + "&limit=100"
			return fetchGateOrderbook(ctx, a.client, u)
		},
		fetchFutures: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			// Gate 合约: 合约名如 BTC_USDT
			cp := strings.ReplaceAll(symbol, "USDT", "_USDT")
			u := a.baseURL + "/futures/usdt/order_book?contract=" + url.QueryEscape(cp) + "&limit=100"
			return fetchGateFuturesOrderbook(ctx, a.client, u)
		},
	}
}

func fetchGateOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Bids [][]string `json:"bids"`
		Asks [][]string `json:"asks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	return out.Bids, out.Asks, nil
}

func fetchGateFuturesOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Bids []struct {
			P string `json:"p"`
			S int    `json:"s"`
		} `json:"bids"`
		Asks []struct {
			P string `json:"p"`
			S int    `json:"s"`
		} `json:"asks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	bids := make([][]string, len(out.Bids))
	for i, b := range out.Bids {
		bids[i] = []string{b.P, fmt.Sprintf("%d", b.S)}
	}
	asks := make([][]string, len(out.Asks))
	for i, a := range out.Asks {
		asks[i] = []string{a.P, fmt.Sprintf("%d", a.S)}
	}
	return bids, asks, nil
}

// newBitgetOrderbookAdapter 创建 Bitget 公共订单簿适配器（REST 公开）
func newBitgetOrderbookAdapter() *httpOrderbookAdapter {
	return &httpOrderbookAdapter{
		client:    &http.Client{Timeout: 10 * time.Second},
		baseURL:   "https://api.bitget.com",
		exchange:  "bitget",
		symbolFmt: func(s string) string { return s },
		fetchSpot: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := a.baseURL + "/api/v2/spot/market/orderbook?symbol=" + url.QueryEscape(symbol) + "&type=step0&limit=100"
			return fetchBitgetOrderbook(ctx, a.client, u)
		},
		fetchFutures: func(ctx context.Context, a *httpOrderbookAdapter, symbol string) ([][]string, [][]string, error) {
			u := a.baseURL + "/api/v2/mix/market/orderbook?productType=USDT-FUTURES&symbol=" + url.QueryEscape(symbol) + "&limit=100"
			return fetchBitgetOrderbook(ctx, a.client, u)
		},
	}
}

func fetchBitgetOrderbook(ctx context.Context, client *http.Client, u string) ([][]string, [][]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Bids [][]string `json:"bids"`
			Asks [][]string `json:"asks"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, err
	}
	return out.Data.Bids, out.Data.Asks, nil
}

func parseOrderbookSide(side [][]interface{}) [][]string {
	out := make([][]string, 0, len(side))
	for _, row := range side {
		if len(row) >= 2 {
			p := fmt.Sprintf("%v", row[0])
			q := fmt.Sprintf("%v", row[1])
			out = append(out, []string{p, q})
		}
	}
	return out
}

// RegisterExchangeAdapters 注册常用交易所的公共订单簿适配器（无需 API Key）
func RegisterExchangeAdapters(f *Finder) {
	f.RegisterExchange("binance", newBinanceOrderbookAdapter())
	f.RegisterExchange("bybit", newBybitOrderbookAdapter())
	f.RegisterExchange("bitget", newBitgetOrderbookAdapter())
	f.RegisterExchange("gate", newGateOrderbookAdapter())
	f.RegisterExchange("okx", newOKXOrderbookAdapter())
}
