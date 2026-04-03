package backtest

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/opportunities"
)

const (
	spotExBacktest = "binance"
	futExBacktest  = "binance"
	// SellExchange 使用与 determineSpotAndFutures 兼容的后缀
	sellExLabel = "binance_futures"
)

// MaxBacktestRange 单次回测最大时间跨度（避免超时与限频）
const MaxBacktestRange = 7 * 24 * time.Hour

// Run 使用 Binance 现货+U 本位 1m K 线对齐，无交易所适配器注册（漏斗降级为层1），逐步 FindAt。
func Run(ctx context.Context, fc config.FunnelConfig, symbol string, from, to time.Time, httpClient *http.Client) (*model.BacktestResponse, error) {
	if to.Before(from) {
		return nil, fmt.Errorf("结束时间早于开始时间")
	}
	if to.Sub(from) > MaxBacktestRange {
		return nil, fmt.Errorf("时间跨度超过上限 %v", MaxBacktestRange)
	}

	spot, err := FetchBinanceSpotKlines(ctx, httpClient, symbol, from, to)
	if err != nil {
		return nil, fmt.Errorf("现货 K 线: %w", err)
	}
	fut, err := FetchBinanceFuturesKlines(ctx, httpClient, symbol, from, to)
	if err != nil {
		return nil, fmt.Errorf("合约 K 线: %w", err)
	}
	spot, fut = AlignSpotFutures(spot, fut)
	if len(spot) == 0 {
		return nil, fmt.Errorf("现货与合约无对齐 K 线，请检查 symbol 或时间范围")
	}

	finder := opportunities.NewFinder(fc)
	// 不 RegisterExchange：降级模式，层2–4 跳过，层1 异常进入机会列表

	var spreadSeries []model.BacktestSeriesPoint
	var priceSeries []model.BacktestSeriesPoint
	var signals []model.BacktestSignal

	prevHadOpp := false
	for i := range spot {
		t := spot[i].OpenTime
		sp := spot[i].Close
		fp := fut[i].Close
		if sp <= 0 {
			continue
		}
		spreadPct := (fp - sp) / sp * 100

		spreadSeries = append(spreadSeries, model.BacktestSeriesPoint{T: t.UTC().Format(time.RFC3339), V: spreadPct})
		priceSeries = append(priceSeries, model.BacktestSeriesPoint{T: t.UTC().Format(time.RFC3339), V: sp})

		item := model.SpreadItem{
			Symbol:        symbol,
			BuyExchange:   spotExBacktest,
			SellExchange:  sellExLabel,
			SpreadPercent: spreadPct,
			BuyPrice:      sp,
			SellPrice:     fp,
			UpdatedAt:     t.UTC().Format(time.RFC3339),
		}

		finder.FeedTicker(symbol, spotExBacktest, sp, t)
		resp := finder.FindAt([]model.SpreadItem{item}, t)

		hasOpp := len(resp.Opportunities) > 0
		if hasOpp && !prevHadOpp {
			for _, o := range resp.Opportunities {
				signals = append(signals, model.BacktestSignal{
					T:             t.UTC().Format(time.RFC3339),
					Layer:         "L1",
					Message:       "漏斗触发（降级：无 REST 订单簿，对应层1 异常/机会）",
					SpreadPercent: o.SpreadPercent,
					Confidence:    o.Confidence,
				})
				break // 单 symbol 一条即可
			}
		}
		prevHadOpp = hasOpp
	}

	warn := WarningsForPair("binance", "binance")
	return &model.BacktestResponse{
		Symbol:       symbol,
		Granularity:  string(Granularity1m),
		Warnings:     warn,
		SpreadSeries: spreadSeries,
		PriceSeries:  priceSeries,
		Signals:      signals,
		SpotExchange: spotExBacktest,
		FutExchange:  "binance (USDT-M)",
	}, nil
}
