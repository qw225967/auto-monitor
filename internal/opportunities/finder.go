package opportunities

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

const (
	MinNegativeSpread   = -1.0
	MinSpotDepthUSDT   = 10000
	MinPriceSlope      = 0.0001
	VolumeSpikeThreshold = 2.0
	MinBothDepthUSDT   = 10000

	MaxPricePoints     = 1000
	PriceHistoryWindow = 10 * time.Minute
	// 每个币现货价保留最近 10 个数据点用于斜率计算
	SlopePricePoints = 10

	// 漏斗减负：价差阈值（只保留更负的，如 -0.2 表示 <-0.2%）
	SpreadThresholdStrict = -0.2
	// 漏斗减负：进入深度筛前最多保留条数（避免 3 万次 API 调用）
	MaxTokensBeforeDepth = 2000
)

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
	for _, it := range negativeSpread {
		if it.BuyPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.BuyExchange, it.BuyPrice, 0)
		}
		if it.SellPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.SellExchange, it.SellPrice, 0)
		}
	}

	// 漏斗减负 1：价差阈值（只保留 spread < -0.5% 等更有价值的）
	afterSpreadThreshold := f.filterSpreadThreshold(negativeSpread)
	log.Printf("[Funnel] 1b.价差阈值(<-%.1f%%)后: %d 个", -SpreadThresholdStrict, len(afterSpreadThreshold))

	// 漏斗减负 2：按 symbol 去重，每 symbol 保留价差最负的 1 条
	deduped := f.dedupeBySymbol(afterSpreadThreshold)
	log.Printf("[Funnel] 1c.按symbol去重后: %d 个 %s", len(deduped), symbolsFromSpreadItems(deduped))

	// 漏斗减负 3：价格斜率提前（有历史时要求上涨，无数据放行）
	withPriceSlope := f.filterPriceSlope(deduped)
	stats.AfterPriceSlope = len(withPriceSlope)
	log.Printf("[Funnel] 1d.价格斜率(上涨/无数据)后: %d 个 %s", len(withPriceSlope), symbolsFromSpreadItems(withPriceSlope))

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
	log.Printf("[Funnel] 3.量能放大筛选后: %d 个 %s", len(withVolume), symbolsFromSpreadItems(withVolume))

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
	if len(items) == 0 {
		return "[]"
	}
	seen := make(map[string]bool)
	var symbols []string
	for _, it := range items {
		key := it.Symbol + ":" + it.BuyExchange + "->" + it.SellExchange
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
	var result []model.SpreadItem
	for _, item := range items {
		slope := f.priceHistory.GetSlope(item.Symbol, item.BuyExchange)
		// 无足够价格历史时 slope=0，放行；有历史时要求价格持续上涨（slope>0）
		if slope == 0 || slope > 0 {
			result = append(result, item)
		}
	}
	return result
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
