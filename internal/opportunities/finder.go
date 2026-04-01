package opportunities

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
)

const (
	// 价格历史窗口：保留 45 分钟数据，覆盖 30min 长窗口
	MaxPricePoints     = 5000
	PriceHistoryWindow = 45 * time.Minute
	SlopePricePoints   = 5000

	// 兼容旧代码引用（GetSymbolsForKline 等）
	MinNegativeSpread = -1.0
)

type ExchangeAdapter interface {
	GetSpotOrderBook(symbol string) (bids, asks [][]string, err error)
	GetFuturesOrderBook(symbol string) (bids, asks [][]string, err error)
	GetRecentTrades(symbol string, limit int) (quoteQtySum float64, err error)
	// GetBestBidQty 获取现货第一档 bid 挂单量（一手量，非 USDT）及最优买价
	GetBestBidQty(symbol string) (qty float64, price float64, err error)
}

type Finder struct {
	priceHistory         *PriceHistory
	watchPool            *WatchPool
	exchanges            map[string]ExchangeAdapter
	mu                   sync.RWMutex
	priceAccelThreshold  float64
	depthAccelThreshold  float64
	volumeAccelThreshold float64
}

// NewFinder 创建 Finder；funnel 参数来自 config.Load().Funnel
func NewFinder(fc config.FunnelConfig) *Finder {
	return &Finder{
		priceHistory:         NewPriceHistory(SlopePricePoints, PriceHistoryWindow),
		watchPool:            NewWatchPool(fc),
		exchanges:            make(map[string]ExchangeAdapter),
		priceAccelThreshold:  fc.PriceAccelThreshold,
		depthAccelThreshold:  fc.DepthAccelThreshold,
		volumeAccelThreshold: fc.VolumeAccelThreshold,
	}
}

func (f *Finder) RegisterExchange(name string, adapter ExchangeAdapter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges[name] = adapter
}

// FeedTicker 将 Ticker 实时价喂入 PriceHistory（每 3s 一轮，响应快）
// 价格用 ticker lastPrice，volume 填 0（量能仍由 K 线提供）
func (f *Finder) FeedTicker(symbol, exchange string, price float64, ts time.Time) {
	if price <= 0 {
		return
	}
	f.priceHistory.RecordAt(symbol, exchange, price, 0, ts)
}

// orderbookExchange 在买卖两侧中选出「已注册 REST 订单簿适配器」的一方。
// 避免把 LIGHTER/ASTER/HYPERLIQUID 等无适配器的一侧当成现货侧，导致永远拉不到盘口。
func orderbookExchange(buyEx, sellEx string, exchanges map[string]ExchangeAdapter) (ex string, ok bool) {
	spot, fut := determineSpotAndFutures(buyEx, sellEx)
	for _, cand := range []string{spot, fut, buyEx, sellEx} {
		c := strings.TrimSpace(cand)
		if c == "" {
			continue
		}
		if _, has := exchanges[strings.ToLower(c)]; has {
			return c, true
		}
	}
	return spot, false
}

// prefetchOrderbookForAnomalies 在层2/3 之前为异常候选拉取现货第一档，写入 depth: 时间序列。
// 此前深度仅在层4 写入，导致层3 永远「数据不足」。
func (f *Finder) prefetchOrderbookForAnomalies(items []model.SpreadItem, exchanges map[string]ExchangeAdapter) {
	for _, item := range items {
		ex, ok := orderbookExchange(item.BuyExchange, item.SellExchange, exchanges)
		if !ok {
			continue
		}
		adapter := exchanges[strings.ToLower(ex)]
		bestQty, bestPrice, err := adapter.GetBestBidQty(item.Symbol)
		if err != nil || bestQty <= 0 {
			continue
		}
		f.priceHistory.RecordOrderbookDepth(item.Symbol, bestQty)
		if bestPrice > 0 {
			f.priceHistory.Record(item.Symbol, ex, bestPrice, 0)
		}
	}
}

// Find 四层漏斗主入口：
//
//	层0: 监控池更新（维护 [-1%,1%] 区间的 symbol，Welford 积累历史）
//	层1: 价差突变检测（|spread - mean| / stdDev >= anomaly_stddev_k）
//	层2: 价格斜率加速比（hasData=false 必须过滤）
//	层3: 挂单量斜率加速比（hasData=false 必须过滤）
//	层4: 挂单量猛增检测（拉取第一档 bid，再比 volume_accel_threshold）
func (f *Finder) Find(spreadItems []model.SpreadItem) *model.OpportunitiesResponse {
	f.mu.RLock()
	exchanges := f.exchanges
	f.mu.RUnlock()

	stats := model.FunnelStats{
		TotalSymbols: len(spreadItems),
	}

	// 喂入价格历史（用于加速比计算）
	for _, it := range spreadItems {
		if it.BuyPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.BuyExchange, it.BuyPrice, 0)
		}
		if it.SellPrice > 0 {
			f.priceHistory.Record(it.Symbol, it.SellExchange, it.SellPrice, 0)
		}
	}

	// 层0：监控池更新，返回在池内的 items
	poolItems := f.watchPool.Update(spreadItems)
	stats.AfterSpreadInRange = len(poolItems)
	stats.WatchPoolSize = f.watchPool.GetWatchPoolSize()
	log.Printf("[Funnel] 层0 监控池: 入口%d → 池内%d (池大小=%d)", len(spreadItems), len(poolItems), stats.WatchPoolSize)

	// 层1：价差突变检测（2σ）
	anomalyItems := f.watchPool.DetectAnomalies(poolItems)
	stats.AfterSpreadAnomaly = len(anomalyItems)
	log.Printf("[Funnel] 层1 价差突变(σ≥%.2f): %d → %d %s",
		f.watchPool.anomalyK(), len(poolItems), len(anomalyItems), symbolsFromSpreadItems(anomalyItems))

	coolingList := f.watchPool.GetCooling()
	stats.CoolingPoolSize = len(coolingList)

	// 降级模式：无交易所注册时，跳过层2-4，直接输出层1结果
	if len(exchanges) == 0 {
		log.Printf("[Funnel] 无交易所注册，降级模式：直接输出层1结果 %d 个", len(anomalyItems))
		opportunities := f.buildOpportunitiesFromSpread(anomalyItems)
		return &model.OpportunitiesResponse{
			Opportunities:  opportunities,
			FunnelStats:    stats,
			CoolingSymbols: coolingList,
			UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
		}
	}

	// 层2/3 之前：对异常候选预拉现货盘口，写入深度时间序列（否则层3 从未有过 depth 样本）
	f.prefetchOrderbookForAnomalies(anomalyItems, exchanges)

	// 层2+3：价格斜率加速比 + 挂单量斜率加速比（hasData=false 必须过滤，不放行）
	var afterPriceDepth []model.SpreadItem
	for _, item := range anomalyItems {
		ex, hasOB := orderbookExchange(item.BuyExchange, item.SellExchange, exchanges)
		if !hasOB {
			log.Printf("[Funnel] 层2 %s 无 REST 订单簿适配（buy=%s sell=%s），跳过",
				item.Symbol, item.BuyExchange, item.SellExchange)
			continue
		}

		// 层2：价格加速比，hasData=false 过滤（价格 key 与 Ticker/预拉盘口一致）
		priceAccel, hasPriceData := f.priceHistory.GetPriceSlopeAccel(item.Symbol, ex)
		if !hasPriceData {
			t5, p5 := f.priceHistory.CountPoints(item.Symbol, ex)
			log.Printf("[Funnel] 层2 %s 价格数据不足（近5min ticker 点=%d 有效价=%d，需≥2；计价侧=%s）",
				item.Symbol, t5, p5, ex)
			continue
		}
		if priceAccel < f.priceAccelThreshold {
			log.Printf("[Funnel] 层2 %s 价格加速比不足: %.2f < %.2f", item.Symbol, priceAccel, f.priceAccelThreshold)
			continue
		}

		// 层3：挂单量加速比，hasData=false 过滤
		depthAccel, hasDepthData := f.priceHistory.GetDepthSlopeAccel(item.Symbol)
		if !hasDepthData {
			t5, d5 := f.priceHistory.CountDepthPoints(item.Symbol)
			log.Printf("[Funnel] 层3 %s 挂单量数据不足（近5min 深度样本=%d 有效=%d，需≥2；本轮已预拉盘口）",
				item.Symbol, t5, d5)
			continue
		}
		if depthAccel < f.depthAccelThreshold {
			log.Printf("[Funnel] 层3 %s 挂单量加速比不足: %.2f < %.2f", item.Symbol, depthAccel, f.depthAccelThreshold)
			continue
		}

		afterPriceDepth = append(afterPriceDepth, item)
	}
	stats.AfterPriceAccel = len(afterPriceDepth)
	log.Printf("[Funnel] 层2+3 价格/挂单量加速比: %d → %d %s",
		len(anomalyItems), len(afterPriceDepth), symbolsFromSpreadItems(afterPriceDepth))

	// 层4：对层2+3 通过项再拉一次盘口，用 GetDepthSlopeAccel（与层3 同一指标）与 volume_accel_threshold 比较；
	// 阈值通常高于 depth_accel_threshold，相当于「二次收紧」。无通过项时本层不会执行。
	if len(afterPriceDepth) == 0 {
		log.Printf("[Funnel] 层4 挂单量猛增: 跳过（层2+3 为 0 条，本层不拉盘、不判 accel）")
	}

	// 层4：挂单量猛增检测（拉取第一档 bid，记录历史，计算加速比）
	var finalOpportunities []model.OpportunityItem
	for _, item := range afterPriceDepth {
		spotEx, futuresEx := determineSpotAndFutures(item.BuyExchange, item.SellExchange)
		ex, ok := orderbookExchange(item.BuyExchange, item.SellExchange, exchanges)
		if !ok {
			continue
		}
		adapter := exchanges[strings.ToLower(ex)]

		// 拉取第一档 bid 挂单量及最优买价
		bestQty, bestPrice, obErr := adapter.GetBestBidQty(item.Symbol)
		if obErr == nil && bestQty > 0 {
			// 记录挂单量历史（用于斜率计算）
			f.priceHistory.RecordOrderbookDepth(item.Symbol, bestQty)
			// 将最优买价补充进价格历史（提升实时性）
			if bestPrice > 0 {
				f.priceHistory.Record(item.Symbol, ex, bestPrice, 0)
			}
		}

		// 挂单量猛增检测：GetDepthSlopeAccel = 短窗5m 与 长窗30m 深度斜率之比（与层3 同一套计算）
		volumeAccel, hasVolumeData := f.priceHistory.GetDepthSlopeAccel(item.Symbol)
		if !hasVolumeData || volumeAccel < f.volumeAccelThreshold {
			log.Printf("[Funnel] 层4 %s 挂单量加速比 depth_accel=%.4f 阈值≥%.2f hasData=%v 计价侧=%s 本轮bestBidQty=%.8f obErr=%v",
				item.Symbol, volumeAccel, f.volumeAccelThreshold, hasVolumeData, ex, bestQty, obErr)
			continue
		}

		priceAccel, _ := f.priceHistory.GetPriceSlopeAccel(item.Symbol, ex)
		depthAccel, _ := f.priceHistory.GetDepthSlopeAccel(item.Symbol)
		confidence := f.calculateConfidence(item.SpreadAnomaly, priceAccel, depthAccel, volumeAccel)

		log.Printf("[Funnel] 层4 %s 挂单量猛增通过: depth_accel=%.4f (≥%.2f) 计价侧=%s bestBidQty=%.8f 价差σ=%.2f",
			item.Symbol, volumeAccel, f.volumeAccelThreshold, ex, bestQty, item.SpreadAnomaly)

		finalOpportunities = append(finalOpportunities, model.OpportunityItem{
			Symbol:             item.Symbol,
			SpotExchange:       spotEx,
			FuturesExchange:    futuresEx,
			SpreadPercent:      item.SpreadPercent,
			SpotOrderbookDepth: bestQty, // 一手挂单量（非 USDT 总深度）
			SpreadAnomaly:      item.SpreadAnomaly,
			PriceAccelRatio:    priceAccel,
			VolumeAccelScore:   volumeAccel,
			Confidence:         confidence,
			UpdatedAt:          time.Now().UTC().Format(time.RFC3339),
		})
	}

	stats.AfterDepthVolume = len(finalOpportunities)
	if len(afterPriceDepth) > 0 {
		log.Printf("[Funnel] 层4 挂单量猛增(深度加速比>=%.2f，与层3同指标、阈值更高): %d → %d %s",
			f.volumeAccelThreshold, len(afterPriceDepth), len(finalOpportunities), symbolsFromOpportunities(finalOpportunities))
	}

	sort.Slice(finalOpportunities, func(i, j int) bool {
		return finalOpportunities[i].Confidence > finalOpportunities[j].Confidence
	})

	return &model.OpportunitiesResponse{
		Opportunities:  finalOpportunities,
		FunnelStats:    stats,
		CoolingSymbols: coolingList,
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
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

// buildOpportunitiesFromSpread 降级模式：将 SpreadItem 直接转为 OpportunityItem（无交易所数据时使用）
func (f *Finder) buildOpportunitiesFromSpread(items []model.SpreadItem) []model.OpportunityItem {
	var result []model.OpportunityItem
	for _, item := range items {
		spotEx, futuresEx := determineSpotAndFutures(item.BuyExchange, item.SellExchange)
		priceAccel, _ := f.priceHistory.GetPriceSlopeAccel(item.Symbol, spotEx)
		result = append(result, model.OpportunityItem{
			Symbol:           item.Symbol,
			SpotExchange:     spotEx,
			FuturesExchange:  futuresEx,
			SpreadPercent:    item.SpreadPercent,
			SpreadAnomaly:    item.SpreadAnomaly,
			PriceAccelRatio:  priceAccel,
			VolumeAccelScore: 0,
			Confidence:       50,
			UpdatedAt:        time.Now().UTC().Format(time.RFC3339),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].SpreadPercent < result[j].SpreadPercent
	})
	return result
}

// GetSymbolsForKline 从价差数据中提取需拉取 K 线的 symbol 列表（负价差+已知交易所，去重，最多 maxSymbols 个）
func GetSymbolsForKline(items []model.SpreadItem, maxSymbols int) []string {
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

func isSpotFuturesPair(buyEx, sellEx string) bool {
	spotExchanges := map[string]bool{
		"binance": true, "bybit": true, "bitget": true, "gate": true, "okx": true,
	}
	return spotExchanges[strings.ToLower(buyEx)] || spotExchanges[strings.ToLower(sellEx)]
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

// calculateConfidence 四维置信度评分（满分100）：
//   - 价差偏离（σ倍数）：最高30分
//   - 价格加速比：最高30分
//   - 挂单量加速比（层3）：最高25分
//   - 挂单量猛增倍数（层4）：最高15分
func (f *Finder) calculateConfidence(spreadAnomaly, priceAccel, depthAccel, volumeAccel float64) int {
	score := 0

	// 价差偏离（σ倍数）
	switch {
	case spreadAnomaly >= 4.0:
		score += 30
	case spreadAnomaly >= 3.0:
		score += 22
	case spreadAnomaly >= 2.0:
		score += 15
	}

	pa := f.priceAccelThreshold
	da := f.depthAccelThreshold
	va := f.volumeAccelThreshold

	// 价格加速比（档位随配置的入场阈值缩放）
	switch {
	case priceAccel >= 3*pa:
		score += 30
	case priceAccel >= 2*pa:
		score += 22
	case priceAccel >= pa:
		score += 15
	}

	// 挂单量加速比（层3）
	switch {
	case depthAccel >= 3*da:
		score += 25
	case depthAccel >= 2*da:
		score += 18
	case depthAccel >= da:
		score += 12
	}

	// 挂单量猛增倍数（层4）
	switch {
	case volumeAccel >= 3*va:
		score += 15
	case volumeAccel >= 2*va:
		score += 10
	case volumeAccel >= va:
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score
}
