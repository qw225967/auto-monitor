package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// KlineBar 单根 K 线（收盘用于价差）
type KlineBar struct {
	OpenTime time.Time
	Open     float64
	High     float64
	Low      float64
	Close    float64
	Volume   float64
}

const binanceKlineLimit = 1000

// FetchBinanceSpotKlines 拉取 Binance 现货 K 线（1m），按时间分页
func FetchBinanceSpotKlines(ctx context.Context, client *http.Client, symbol string, from, to time.Time) ([]KlineBar, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var out []KlineBar
	start := from.UnixMilli()
	end := to.UnixMilli()
	for start < end {
		u := "https://api.binance.com/api/v3/klines?" +
			"symbol=" + url.QueryEscape(symbol) +
			"&interval=1m" +
			"&startTime=" + strconv.FormatInt(start, 10) +
			"&endTime=" + strconv.FormatInt(end, 10) +
			"&limit=" + strconv.Itoa(binanceKlineLimit)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := readBodyClose(resp)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance spot klines: %s: %s", resp.Status, string(body))
		}
		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, row := range raw {
			if len(row) < 6 {
				continue
			}
			ts, _ := toInt64(row[0])
			o, _ := toFloat(row[1])
			h, _ := toFloat(row[2])
			l, _ := toFloat(row[3])
			c, _ := toFloat(row[4])
			v, _ := toFloat(row[5])
			out = append(out, KlineBar{
				OpenTime: time.UnixMilli(ts),
				Open:     o, High: h, Low: l, Close: c, Volume: v,
			})
		}
		last := raw[len(raw)-1]
		lastOpen, _ := toInt64(last[0])
		start = lastOpen + 60_000 // next minute
		if len(raw) < binanceKlineLimit {
			break
		}
	}
	return out, nil
}

// FetchBinanceFuturesKlines U 本位合约 1m
func FetchBinanceFuturesKlines(ctx context.Context, client *http.Client, symbol string, from, to time.Time) ([]KlineBar, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var out []KlineBar
	start := from.UnixMilli()
	end := to.UnixMilli()
	for start < end {
		u := "https://fapi.binance.com/fapi/v1/klines?" +
			"symbol=" + url.QueryEscape(symbol) +
			"&interval=1m" +
			"&startTime=" + strconv.FormatInt(start, 10) +
			"&endTime=" + strconv.FormatInt(end, 10) +
			"&limit=" + strconv.Itoa(binanceKlineLimit)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := readBodyClose(resp)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("binance futures klines: %s: %s", resp.Status, string(body))
		}
		var raw [][]interface{}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		if len(raw) == 0 {
			break
		}
		for _, row := range raw {
			if len(row) < 6 {
				continue
			}
			ts, _ := toInt64(row[0])
			o, _ := toFloat(row[1])
			h, _ := toFloat(row[2])
			l, _ := toFloat(row[3])
			c, _ := toFloat(row[4])
			v, _ := toFloat(row[5])
			out = append(out, KlineBar{
				OpenTime: time.UnixMilli(ts),
				Open:     o, High: h, Low: l, Close: c, Volume: v,
			})
		}
		last := raw[len(raw)-1]
		lastOpen, _ := toInt64(last[0])
		start = lastOpen + 60_000
		if len(raw) < binanceKlineLimit {
			break
		}
	}
	return out, nil
}

func readBodyClose(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func toInt64(v interface{}) (int64, error) {
	switch x := v.(type) {
	case float64:
		return int64(x), nil
	case string:
		return strconv.ParseInt(x, 10, 64)
	default:
		return 0, fmt.Errorf("toInt64: %T", v)
	}
}

func toFloat(v interface{}) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case string:
		return strconv.ParseFloat(x, 64)
	default:
		return 0, fmt.Errorf("toFloat: %T", v)
	}
}

// AlignSpotFutures 按 OpenTime 对齐现货与合约 K 线（分钟对齐）
func AlignSpotFutures(spot, fut []KlineBar) ([]KlineBar, []KlineBar) {
	sm := make(map[int64]KlineBar)
	for _, b := range spot {
		sm[b.OpenTime.UnixMilli()] = b
	}
	var sOut, fOut []KlineBar
	for _, fb := range fut {
		k := fb.OpenTime.UnixMilli()
		if sb, ok := sm[k]; ok {
			sOut = append(sOut, sb)
			fOut = append(fOut, fb)
		}
	}
	return sOut, fOut
}
