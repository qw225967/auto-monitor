package opportunities

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/opportunities/kline"
)

const (
	MinNegativeSpread   = -1.0
	MinSpotDepthUSDT   = 10000
	MinPriceSlope      = 0.002 // 价格斜率阈值，需 > 0.002 才通过（约 1% 涨幅/5 分钟）
	VolumeSpikeThreshold = 2.0
	MinBothDepthUSDT   = 10000

	MaxPricePoints     = 1000
	PriceHistoryWindow = 10 * time.Minute
	// 每个币现货价保留最近 600 个数据点用于斜率计算
	SlopePricePoints = 600

	// 漏斗减负：价差阈值（只保留更负的，如 -0.5 表示 <-0.5%）
	SpreadThresholdStrict = -0.5
	// 漏斗减负：进入深度筛前最多保留条数（避免 3 万次 API 调用）
	MaxTokensBeforeDepth = 2000
)

// 有 K 线数据的交易所（用于斜率回退：BuyExchange 无数据时用这些所的价格趋势）
var klineExchanges = []string{"binance", "bybit", "okx", "gate", "bitget"}

type ExchangeAdapter interface {
	GetSpotOrderBook(symbol string) (bids, asks [][]string, err error)
	GetFuturesOrderBook(symbol string) (bids, asks [][]string, err error)
}

type Finder struct {
	priceHistory *PriceHistory
	exchanges    map[string]ExchangeAdapter
	mu           sync.RWMutex
}

func NewFinder() *Finder {
	return &Finder{
		priceHistory: NewPriceHistory(SlopePricePoints, PriceHistoryWindow),
		exchanges:    make(map[string]ExchangeAdapter),
	}
}

func (f *Finder) RegisterExchange(name string, adapter ExchangeAdapter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges[name] = adapter
}

// FeedKline 将 K 线数据喂入 PriceHistory：量用 K 线，价格优先用原始价差（无则用 K 线 close 供斜率计算）
func (f *Finder) FeedKline(symbol, exchange string, bars []kline.KlinePoint) {
	for _, b := range bars {
		f.priceHistory.RecordAt(symbol, exchange, b.Close, b.Volume, b.Timestamp)
	}
}

func (f *Finder) Find(spreadItems []model.SpreadItem) *model.OpportunitiesResponse {
	f.mu.RLock()
	exchanges := f.exchanges
	f.mu.RUnlock()

	stats := model.FunnelStats{
		TotalSymbols: len(spreadItems),
	}

	log.Printf("[Funnel] 入口: %d 个币种 %s", len(spreadItems), symbolsFromSpreadItems(spreadItems))

	negativeSpread := f.filterNegativeSpread(spreadItems)
	stats.AfterNegativeSpread = len(negativeSpread)
	log.Printf("[Funnel] 1.负价差筛选后: %d 个 %s", len(negativeSpread), symbolsFromSpreadItems(negativeSpread))

	// 用价差数据喂入价格历史（用于价格斜率与量能）
	var withPrice int
	for _, it := range negativeSpread {
		if it.BuyPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.BuyExchange, it.BuyPrice, 0)
			withPrice++
		}
		if it.SellPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.SellExchange, it.SellPrice, 0)
			withPrice++
		}
	}
	if len(negativeSpread) > 0 {
		log.Printf("[Funnel] 价差含价格: %d/%d 条有 BuyPrice/SellPrice>0", withPrice, len(negativeSpread)*2)
	}

	// 漏斗减负 1：价差阈值（只保留 spread < -0.5% 等更有价值的）
	afterSpreadThreshold := f.filterSpreadThreshold(negativeSpread)
	log.Printf("[Funnel] 1b.价差阈值(<-%.1f%%)后: %d 个", -SpreadThresholdStrict, len(afterSpreadThreshold))

	// 漏斗减负 2：按 symbol 去重，每 symbol 保留价差最负的 1 条
	deduped := f.dedupeBySymbol(afterSpreadThreshold)
	log.Printf("[Funnel] 1c.按symbol去重后: %d 个 %s", len(deduped), symbolsFromSpreadItems(deduped))

	// 漏斗减负 3：价格斜率提前（有历史时要求上涨，无数据放行）
	withPriceSlope, slopeDebug := f.filterPriceSlopeWithDebug(deduped)
	stats.AfterPriceSlope = len(withPriceSlope)
	log.Printf("[Funnel] 1d.价格斜率(上涨/无数据)后: %d 个 %s", len(withPriceSlope), symbolsFromSpreadItems(withPriceSlope))
	if slopeDebug != "" {
		log.Printf("[Funnel] 1d.斜率诊断: %s", slopeDebug)
	}

	// 漏斗减负 4：进入深度筛前限量，避免数万次 API 调用
	capped := f.limitTopBySpread(withPriceSlope, MaxTokensBeforeDepth)
	log.Printf("[Funnel] 1e.限量(top%d)后: %d 个", MaxTokensBeforeDepth, len(capped))

	// 如果没有注册交易所，跳过需要订单簿的漏斗步骤
	hasExchanges := len(exchanges) > 0

	if !hasExchanges {
		stats.AfterSpotDepth = len(capped)
		stats.AfterPriceSlope = len(capped)
		stats.AfterVolume = len(capped)
		stats.AfterBothDepth = len(capped)
		log.Printf("[Funnel] 无交易所注册，跳过漏斗2-5，直接输出 %d 个 %s", len(capped), symbolsFromSpreadItems(capped))
		opportunities := f.convertToOpportunities(capped)
		return &model.OpportunitiesResponse{
			Opportunities: opportunities,
			FunnelStats:   stats,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
	}

	withSpotDepth := f.filterSpotDepth(capped, exchanges)
	stats.AfterSpotDepth = len(withSpotDepth)
	log.Printf("[Funnel] 2.现货深度筛选后: %d 个 %s", len(withSpotDepth), symbolsFromSpreadItems(withSpotDepth))

	withVolume := f.filterVolumeSpike(withSpotDepth)
	stats.AfterVolume = len(withVolume)
	log.Printf("[Funnel] 3.量能放大筛选后: %d 个 %s", len(withVolume), symbolsFromSpreadItemsWithSlope(withVolume, f.priceHistory.GetSlope))

	finalOpportunities := f.filterBothDepth(withVolume, exchanges)
	stats.AfterBothDepth = len(finalOpportunities)
	log.Printf("[Funnel] 4.双深度筛选后: %d 个 %s", len(finalOpportunities), symbolsFromOpportunities(finalOpportunities))

	return &model.OpportunitiesResponse{
		Opportunities: finalOpportunities,
		FunnelStats:   stats,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

func symbolsFromSpreadItems(items []model.SpreadItem) string {
	return symbolsFromSpreadItemsWithSlope(items, nil)
}

func symbolsFromSpreadItemsWithSlope(items []model.SpreadItem, getSlope func(string, string) float64) string {
	if len(items) == 0 {
		return "[]"
	}
	seen := make(map[string]bool)
	var symbols []string
	for _, it := range items {
		key := it.Symbol + ":" + it.BuyExchange + "->" + it.SellExchange
		if !seen[key] {
			seen[key] = true
			if getSlope != nil {
				slope := getSlope(it.Symbol, it.BuyExchange)
				symbols = append(symbols, fmt.Sprintf("%s(slope=%.4f)", key, slope))
			} else {
				symbols = append(symbols, key)
			}
		}
	}
	if len(symbols) > 20 {
		return fmt.Sprintf("[%s ... 共%d个]", strings.Join(symbols[:20], ","), len(symbols))
	}
	return "[" + strings.Join(symbols, ",") + "]"
}

func symbolsFromOpportunities(items []model.OpportunityItem) string {
	if len(items) == 0 {
		return "[]"
	}
	seen := make(map[string]bool)
	var symbols []string
	for _, it := range items {
		key := it.Symbol + ":" + it.SpotExchange + "->" + it.FuturesExchange
		if !seen[key] {
			seen[key] = true
			symbols = append(symbols, key)
		}
	}
	if len(symbols) > 20 {
		return fmt.Sprintf("[%s ... 共%d个]", strings.Join(symbols[:20], ","), len(symbols))
	}
	return "[" + strings.Join(symbols, ",") + "]"
}

// convertToOpportunities 将 SpreadItem 转换为机会（无交易所数据时使用）
func (f *Finder) convertToOpportunities(items []model.SpreadItem) []model.OpportunityItem {
	var result []model.OpportunityItem
	for _, item := range items {
		spotEx, futuresEx := determineSpotAndFutures(item.BuyExchange, item.SellExchange)
		result = append(result, model.OpportunityItem{
			Symbol:                item.Symbol,
			SpotExchange:          spotEx,
			FuturesExchange:       futuresEx,
			SpreadPercent:         item.SpreadPercent,
			SpotOrderbookDepth:    0,
			FuturesOrderbookDepth: 0,
			PriceSlope5m:          0,
			VolumeSpike:           false,
			Confidence:            50,
			UpdatedAt:             time.Now().UTC().Format(time.RFC3339),
		})
	}

	// 按价差排序（最负的在前）
	sort.Slice(result, func(i, j int) bool {
		return result[i].SpreadPercent < result[j].SpreadPercent
	})

	return result
}

func (f *Finder) filterNegativeSpread(items []model.SpreadItem) []model.SpreadItem {
	var result []model.SpreadItem
	for _, item := range items {
		if isSpotFuturesPair(item.BuyExchange, item.SellExchange) && item.SpreadPercent < 0 {
			result = append(result, item)
		}
	}
	return result
}

// filterSpreadThreshold 只保留价差更负的（如 <-0.5%）
func (f *Finder) filterSpreadThreshold(items []model.SpreadItem) []model.SpreadItem {
	var result []model.SpreadItem
	for _, item := range items {
		if item.SpreadPercent < SpreadThresholdStrict {
			result = append(result, item)
		}
	}
	return result
}

// dedupeBySymbol 按 symbol 去重，每 symbol 保留价差最负的 1 条
func (f *Finder) dedupeBySymbol(items []model.SpreadItem) []model.SpreadItem {
	best := make(map[string]model.SpreadItem)
	for _, item := range items {
		key := item.Symbol
		if existing, ok := best[key]; !ok || item.SpreadPercent < existing.SpreadPercent {
			best[key] = item
		}
	}
	out := make([]model.SpreadItem, 0, len(best))
	for _, item := range best {
		out = append(out, item)
	}
	return out
}

// limitTopBySpread 按价差排序，只保留前 n 个（最负的优先）
// SymbolsForKline 从价差数据中提取需拉取 K 线的 symbol 列表（负价差+已知交易所，去重，最多 maxSymbols 个）
func SymbolsForKline(items []model.SpreadItem, maxSymbols int) []string {
	best := make(map[string]float64)
	for _, it := range items {
		if isSpotFuturesPair(it.BuyExchange, it.SellExchange) && it.SpreadPercent < 0 {
			if cur, ok := best[it.Symbol]; !ok || it.SpreadPercent < cur {
				best[it.Symbol] = it.SpreadPercent
			}
		}
	}
	type pair struct {
		symbol string
		spread float64
	}
	var list []pair
	for s, sp := range best {
		list = append(list, pair{s, sp})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].spread < list[j].spread })
	if maxSymbols > 0 && len(list) > maxSymbols {
		list = list[:maxSymbols]
	}
	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].symbol
	}
	return out
}

func (f *Finder) limitTopBySpread(items []model.SpreadItem, n int) []model.SpreadItem {
	if n <= 0 || len(items) <= n {
		return items
	}
	sorted := make([]model.SpreadItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].SpreadPercent < sorted[j].SpreadPercent
	})
	return sorted[:n]
}

func isSpotFuturesPair(buyEx, sellEx string) bool {
	spotExchanges := map[string]bool{
		"binance": true, "bybit": true, "bitget": true, "gate": true, "okx": true,
	}
	return spotExchanges[strings.ToLower(buyEx)] || spotExchanges[strings.ToLower(sellEx)]
}

func (f *Finder) filterSpotDepth(items []model.SpreadItem, exchanges map[string]ExchangeAdapter) []model.SpreadItem {
	var result []model.SpreadItem
	for _, item := range items {
		spotEx, _ := determineSpotAndFutures(item.BuyExchange, item.SellExchange)
		adapter, ok := exchanges[strings.ToLower(spotEx)]
		if !ok {
			continue
		}

		depth := f.getSpotDepth(adapter, item.Symbol)
		if depth >= MinSpotDepthUSDT {
			result = append(result, item)
		}
	}
	return result
}

func (f *Finder) getSpotDepth(adapter ExchangeAdapter, symbol string) float64 {
	bids, _, err := adapter.GetSpotOrderBook(symbol)
	if err != nil {
		return 0
	}
	return calculateDepth(bids, 10)
}

func calculateDepth(orders [][]string, topN int) float64 {
	if len(orders) == 0 {
		return 0
	}

	var depth float64
	for i := 0; i < len(orders) && i < topN; i++ {
		if len(orders[i]) >= 2 {
			var price, qty float64
			fmt.Sscanf(orders[i][0], "%f", &price)
			fmt.Sscanf(orders[i][1], "%f", &qty)
			depth += price * qty
		}
	}
	return depth
}

func determineSpotAndFutures(buyEx, sellEx string) (spotEx, futuresEx string) {
	futuresSuffixes := []string{"_futures", "_future", "_perpetual"}
	for _, suffix := range futuresSuffixes {
		if strings.HasSuffix(buyEx, suffix) {
			return sellEx, buyEx
		}
		if strings.HasSuffix(sellEx, suffix) {
			return buyEx, sellEx
		}
	}
	return buyEx, sellEx
}

func (f *Finder) filterPriceSlope(items []model.SpreadItem) []model.SpreadItem {
	r, _ := f.filterPriceSlopeWithDebug(items)
	return r
}

func (f *Finder) filterPriceSlopeWithDebug(items []model.SpreadItem) ([]model.SpreadItem, string) {
	var result []model.SpreadItem
	var debugSample []string
	for i, item := range items {
		slope := f.getSlopeForItem(item.Symbol, item.BuyExchange)
		if slope > MinPriceSlope {
			result = append(result, item)
		}
		if i < 5 && len(debugSample) < 3 {
			pts, withPrice := f.priceHistory.CountPoints(item.Symbol, item.BuyExchange)
			debugSample = append(debugSample, fmt.Sprintf("%s:%s slope=%.4f pts=%d priceOk=%d",
				item.Symbol, item.BuyExchange, slope, pts, withPrice))
		}
	}
	return result, strings.Join(debugSample, "; ")
}

// getSlopeForItem 获取价格斜率，BuyExchange 无 K 线时用参考交易所（binance/bybit 等）回退
func (f *Finder) getSlopeForItem(symbol, buyExchange string) float64 {
	slope := f.priceHistory.GetSlope(symbol, buyExchange)
	if slope > MinPriceSlope {
		return slope
	}
	for _, ex := range klineExchanges {
		if strings.EqualFold(ex, buyExchange) {
			continue
		}
		s := f.priceHistory.GetSlope(symbol, ex)
		if s > MinPriceSlope {
			return s
		}
		if s > slope {
			slope = s
		}
	}
	return slope
}

func (f *Finder) filterVolumeSpike(items []model.SpreadItem) []model.SpreadItem {
	var result []model.SpreadItem
	for _, item := range items {
		spike := f.priceHistory.GetVolumeSpike(item.Symbol, item.BuyExchange, VolumeSpikeThreshold)
		if spike || true {
			result = append(result, item)
		}
	}
	return result
}

func (f *Finder) filterBothDepth(items []model.SpreadItem, exchanges map[string]ExchangeAdapter) []model.OpportunityItem {
	var result []model.OpportunityItem
	for _, item := range items {
		spotEx, futuresEx := determineSpotAndFutures(item.BuyExchange, item.SellExchange)
		spotAdapter, spotOk := exchanges[strings.ToLower(spotEx)]
		futuresAdapter, futuresOk := exchanges[strings.ToLower(futuresEx)]

		if !spotOk || !futuresOk {
			continue
		}

		spotDepth := f.getSpotDepth(spotAdapter, item.Symbol)
		futuresDepth := f.getFuturesDepth(futuresAdapter, item.Symbol)

		if spotDepth >= MinBothDepthUSDT && futuresDepth >= MinBothDepthUSDT {
			confidence := f.calculateConfidence(item.SpreadPercent, spotDepth, futuresDepth)

			result = append(result, model.OpportunityItem{
				Symbol:                item.Symbol,
				SpotExchange:          spotEx,
				FuturesExchange:       futuresEx,
				SpreadPercent:         item.SpreadPercent,
				SpotOrderbookDepth:    spotDepth,
				FuturesOrderbookDepth: futuresDepth,
				PriceSlope5m:          f.priceHistory.GetSlope(item.Symbol, spotEx),
				VolumeSpike:           f.priceHistory.GetVolumeSpike(item.Symbol, spotEx, VolumeSpikeThreshold),
				Confidence:            confidence,
				UpdatedAt:             time.Now().UTC().Format(time.RFC3339),
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	return result
}

func (f *Finder) getFuturesDepth(adapter ExchangeAdapter, symbol string) float64 {
	bids, _, err := adapter.GetFuturesOrderBook(symbol)
	if err != nil {
		return 0
	}
	return calculateDepth(bids, 10)
}

func (f *Finder) calculateConfidence(spread float64, spotDepth, futuresDepth float64) int {
	score := 50

	if spread < -0.5 {
		score += 20
	} else if spread < -0.2 {
		score += 10
	}

	if spotDepth > 50000 {
		score += 15
	} else if spotDepth > 20000 {
		score += 10
	}

	if futuresDepth > 50000 {
		score += 15
	} else if futuresDepth > 20000 {
		score += 10
	}

	if score > 100 {
		score = 100
	}
	return score
}
