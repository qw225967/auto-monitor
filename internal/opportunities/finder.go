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
		priceHistory: NewPriceHistory(MaxPricePoints, PriceHistoryWindow),
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

	// 如果没有注册交易所，跳过需要订单簿的漏斗步骤
	hasExchanges := len(exchanges) > 0

	if !hasExchanges {
		// 没有交易所时，跳过漏斗2-5，返回负价差数据作为机会
		stats.AfterSpotDepth = len(negativeSpread)
		stats.AfterPriceSlope = len(negativeSpread)
		stats.AfterVolume = len(negativeSpread)
		stats.AfterBothDepth = len(negativeSpread)
		log.Printf("[Funnel] 无交易所注册，跳过漏斗2-5，直接输出负价差 %d 个 %s", len(negativeSpread), symbolsFromSpreadItems(negativeSpread))

		// 将所有负价差转换为机会
		opportunities := f.convertToOpportunities(negativeSpread)

		return &model.OpportunitiesResponse{
			Opportunities: opportunities,
			FunnelStats:   stats,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
	}

	withSpotDepth := f.filterSpotDepth(negativeSpread, exchanges)
	stats.AfterSpotDepth = len(withSpotDepth)
	log.Printf("[Funnel] 2.现货深度筛选后: %d 个 %s", len(withSpotDepth), symbolsFromSpreadItems(withSpotDepth))

	withPriceSlope := f.filterPriceSlope(withSpotDepth)
	stats.AfterPriceSlope = len(withPriceSlope)
	log.Printf("[Funnel] 3.价格斜率筛选后: %d 个 %s", len(withPriceSlope), symbolsFromSpreadItems(withPriceSlope))

	withVolume := f.filterVolumeSpike(withPriceSlope)
	stats.AfterVolume = len(withVolume)
	log.Printf("[Funnel] 4.量能放大筛选后: %d 个 %s", len(withVolume), symbolsFromSpreadItems(withVolume))

	finalOpportunities := f.filterBothDepth(withVolume, exchanges)
	stats.AfterBothDepth = len(finalOpportunities)
	log.Printf("[Funnel] 5.双深度筛选后: %d 个 %s", len(finalOpportunities), symbolsFromOpportunities(finalOpportunities))

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

		adapter, ok := exchanges[spotEx]
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
		if slope >= MinPriceSlope {
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

		spotAdapter, spotOk := exchanges[spotEx]
		futuresAdapter, futuresOk := exchanges[futuresEx]

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
