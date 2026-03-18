package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/qw225967/auto-monitor/internal/api"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/detector"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/onchain"
	"github.com/qw225967/auto-monitor/internal/opportunities"
	"github.com/qw225967/auto-monitor/internal/opportunities/kline"
	"github.com/qw225967/auto-monitor/internal/price"
	"github.com/qw225967/auto-monitor/internal/runner"
	"github.com/qw225967/auto-monitor/internal/source/seeingstone"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
	tg "github.com/qw225967/auto-monitor/internal/utils/notify/telegram"
)

type assetPriorityMetric struct {
	Hits      float64
	MaxSpread float64
	LastSeen  time.Time
}

type assetPriorityTracker struct {
	mu       sync.RWMutex
	metrics  map[string]*assetPriorityMetric
	decay    float64
	staleTTL time.Duration
}

func newAssetPriorityTracker(decay float64, staleTTL time.Duration) *assetPriorityTracker {
	if decay <= 0 || decay >= 1 {
		decay = 0.9
	}
	if staleTTL <= 0 {
		staleTTL = 1 * time.Hour
	}
	return &assetPriorityTracker{
		metrics:  make(map[string]*assetPriorityMetric),
		decay:    decay,
		staleTTL: staleTTL,
	}
}

func (t *assetPriorityTracker) Observe(items []model.SpreadItem, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for asset, m := range t.metrics {
		if now.Sub(m.LastSeen) > t.staleTTL*2 {
			delete(t.metrics, asset)
			continue
		}
		m.Hits *= t.decay
		m.MaxSpread *= t.decay
	}

	for _, it := range items {
		asset, _ := tokenregistry.SymbolToAsset(it.Symbol)
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset == "" {
			continue
		}
		m := t.metrics[asset]
		if m == nil {
			m = &assetPriorityMetric{}
			t.metrics[asset] = m
		}
		m.Hits += 1
		if it.SpreadPercent > m.MaxSpread {
			m.MaxSpread = it.SpreadPercent
		}
		m.LastSeen = now
	}
}

// chainDistribution 返回链分布摘要，如 "链分布: ETH=50 BSC=20"
func chainDistribution(prices map[string]float64) string {
	names := map[string]string{"1": "ETH", "56": "BSC", "137": "Polygon", "42161": "Arbitrum", "10": "OP", "43114": "AVAX", "8453": "Base", "195": "TRON"}
	count := make(map[string]int)
	for k := range prices {
		if idx := strings.Index(k, ":"); idx > 0 {
			c := k[idx+1:]
			if n, ok := names[c]; ok {
				count[n]++
			} else {
				count["链"+c]++
			}
		}
	}
	order := []string{"ETH", "BSC", "Polygon", "Arbitrum", "OP", "AVAX", "Base", "TRON"}
	var parts []string
	for _, n := range order {
		if count[n] > 0 {
			parts = append(parts, n+"="+fmt.Sprint(count[n]))
		}
	}
	for n, v := range count {
		if !strings.Contains(strings.Join(order, " "), n) {
			parts = append(parts, n+"="+fmt.Sprint(v))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "链分布: " + strings.Join(parts, " ")
}

func pairDistribution(byChain map[string]int) string {
	names := map[string]string{"1": "ETH", "56": "BSC", "137": "Polygon", "42161": "Arbitrum", "10": "OP", "43114": "AVAX", "8453": "Base", "195": "TRON"}
	var parts []string
	for cid, v := range byChain {
		if v <= 0 {
			continue
		}
		n := names[cid]
		if n == "" {
			n = "链" + cid
		}
		parts = append(parts, n+"="+fmt.Sprint(v))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func (t *assetPriorityTracker) TopAssets(topN int, now time.Time) []string {
	if topN <= 0 {
		return nil
	}

	type score struct {
		asset string
		val   float64
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	maxHits := 0.0
	maxSpread := 0.0
	for _, m := range t.metrics {
		if now.Sub(m.LastSeen) > t.staleTTL {
			continue
		}
		if m.Hits > maxHits {
			maxHits = m.Hits
		}
		if m.MaxSpread > maxSpread {
			maxSpread = m.MaxSpread
		}
	}
	if maxHits <= 0 {
		maxHits = 1
	}
	if maxSpread <= 0 {
		maxSpread = 1
	}

	var rows []score
	for a, m := range t.metrics {
		age := now.Sub(m.LastSeen)
		if age > t.staleTTL {
			continue
		}
		freshness := 1.0 - math.Min(1.0, float64(age)/float64(t.staleTTL))
		v := 0.55*(m.Hits/maxHits) + 0.35*(m.MaxSpread/maxSpread) + 0.10*freshness
		rows = append(rows, score{asset: a, val: v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].val == rows[j].val {
			return rows[i].asset < rows[j].asset
		}
		return rows[i].val > rows[j].val
	})
	if len(rows) > topN {
		rows = rows[:topN]
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.asset)
	}
	return out
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

func main() {
	_ = godotenv.Load() // 可选：加载 .env
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// 交易所 API 密钥（config/exchange_keys.json，用于充提网络查询等）
	if keys := config.TryLoadExchangeKeys(); keys != nil {
		log.Println("[Config] 已加载交易所密钥: Bitget/Bybit/Gate 等")
	} else {
		log.Println("[Config] 未找到 exchange_keys.json，充提网络将使用公开 API（Binance/OKX 需密钥）")
	}

	// OKEx Key：用于 DEX Quote / 链上价格
	if cfg.Okex.AppKey != "" && cfg.Okex.SecretKey != "" && cfg.Okex.Passphrase != "" {
		config.SetOkexKeyManager(config.NewOkexKeyManagerFromConfig(cfg.Okex.AppKey, cfg.Okex.SecretKey, cfg.Okex.Passphrase))
	}

	// 数据源（MockMode 时使用模拟数据）
	var fetchSpread func(context.Context) ([]model.SpreadItem, error)
	if cfg.MockMode {
		log.Println("[Config] MockMode enabled, using mock spread data")
		fetchSpread = seeingstone.FetchMock
	} else {
		ssAdapter := seeingstone.New(seeingstone.Config{
			BaseURL:        cfg.SeeingStone.APIURL,
			Token:          cfg.SeeingStone.APIToken,
			RequestTimeout: cfg.RequestTimeout(),
		})
		fetchSpread = ssAdapter.FetchSpread
	}

		// 路由探测：初始化跨链桥管理器，仅展示经 GetBridgeQuote 验证的跨链协议
		bridgeMgr := detector.NewBridgeManagerForDetect()
		det := detector.NewArbitrageAdapter(bridgeMgr)

	// Runner
	runner.NewWithOptions(det, cfg.Threshold.Spread, runner.Options{
		DetectConcurrency: cfg.Runner.DetectMaxConcurrency,
		DetectTimeout:     time.Duration(cfg.Runner.DetectRouteTimeout) * time.Second,
	})

	// API
	handler := api.New()

	// 机会发现
	oppFinder := opportunities.NewFinder()
	opportunities.RegisterExchangeAdapters(oppFinder) // 注册 Binance/Bybit/OKX/Gate/Bitget 公共订单簿
	oppHandler := opportunities.NewHandler(oppFinder)

	// Telegram 通知器（如果配置了 Telegram Bot）
	var oppNotifier *opportunities.OpportunityNotifier
	tgConfig := config.GetGlobalConfig()
	if tgConfig != nil && tgConfig.Telegram != nil && tgConfig.Telegram.BotToken != "" && tgConfig.Telegram.ChatID != "" {
		tgClient := tg.NewTelegramClient(tgConfig.Telegram.BotToken, tgConfig.Telegram.ChatID)
		oppNotifier = opportunities.NewOpportunityNotifier(tgClient)
		log.Printf("[Telegram] 机会通知器已启动，ChatID: %s", tgConfig.Telegram.ChatID)
	} else {
		log.Printf("[Telegram] 未配置 Telegram Bot，跳过通知功能")
	}

	// K 线拉取：每 3s 分批查询多交易所，存储用于量能/斜率
	klineStore := kline.NewStore(600)
	klineFetcher := kline.NewFetcher(klineStore, nil, []string{"binance", "bybit", "okx", "gate", "bitget"})
	// 全部 1m K 线，每 3s 一轮，请求在 3s 内均衡摊开
	klineFetcher.SetOnAppend(oppFinder.FeedKline)
	klineCtx := context.Background()
	go klineFetcher.RunLoop(klineCtx)

	// Gin
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), corsMiddleware())
	router.GET("/api/overview", handler.GetOverview)
	router.GET("/api/opportunities", oppHandler.GetOpportunities)
	router.POST("/api/config/exchange-keys", handler.PostExchangeKeys)
	router.POST("/api/config/liquidity-threshold", handler.PostLiquidityThreshold)

	// 缓存：最新价差数据
	var cachedItems []model.SpreadItem
	var cacheMu sync.RWMutex
	priorityTracker := newAssetPriorityTracker(0.9, 2*time.Hour)

	// Ticker A: 10s 拉取价差
	go func() {
		ticker := time.NewTicker(cfg.FetchInterval())
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout())
			items, err := fetchSpread(ctx)
			cancel()
			if err != nil {
				log.Printf("[Fetch] %v", err)
				continue
			}
			cacheMu.Lock()
			cachedItems = items
			cacheMu.Unlock()
			priorityTracker.Observe(items, time.Now())
			log.Printf("[Fetch] got %d items", len(items))

			// 更新机会发现数据
			if oppFinder != nil {
				resp := oppFinder.Find(items)
				oppHandler.UpdateResponse(resp)
				// 通知 Telegram
				if oppNotifier != nil {
					oppNotifier.Notify(resp.Opportunities)
				}
			}
			// 更新 K 线拉取 symbol 列表（负价差去重，最多 500 个）
			if klineFetcher != nil {
				symbols := opportunities.GetSymbolsForKline(items, 500)
				klineFetcher.SetSymbols(symbols)
			}
		}
	}()

	// Ticker D: Token 信息补全（仅非 Mock 且配置了 API Token 时）
	if !cfg.MockMode && cfg.SeeingStone.APIToken != "" {
		go func() {
			// 启动时立即执行一次
			ctx0, cancel0 := context.WithTimeout(context.Background(), 5*time.Minute)
			if updated, err := tokenregistry.RunSync(ctx0, tokenregistry.SyncConfig{
				RegistryPath:     cfg.TokenRegistry.Path,
				APIURL:           cfg.SeeingStone.APIURL,
				APIToken:         cfg.SeeingStone.APIToken,
				RequestTimeout:   cfg.RequestTimeout(),
				UseAllSymbols:    true,
				CoingeckoDelay:   10 * time.Second,
				TokenRefreshTTL:  cfg.TokenRefreshTTL(),
				BudgetPath:       cfg.TokenRegistry.CoinGeckoBudgetPath,
				BudgetEnabled:    cfg.TokenRegistry.CoinGeckoBudgetEnabled,
				BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
				CoinGeckoAPIKey:  cfg.TokenRegistry.CoinGeckoAPIKey,
				CoinGeckoPro:     cfg.TokenRegistry.CoinGeckoPro,
			}); err == nil && updated > 0 {
				log.Printf("[TokenSync] 启动同步: 更新 %d 条", updated)
			}
			cancel0()

			ticker := time.NewTicker(cfg.TokenSyncInterval())
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				updated, err := tokenregistry.RunSync(ctx, tokenregistry.SyncConfig{
					RegistryPath:    cfg.TokenRegistry.Path,
					APIURL:          cfg.SeeingStone.APIURL,
					APIToken:        cfg.SeeingStone.APIToken,
					RequestTimeout:  cfg.RequestTimeout(),
					UseAllSymbols:   true,
					CoingeckoDelay:  10 * time.Second,
					TokenRefreshTTL: cfg.TokenRefreshTTL(),
					BudgetPath:      cfg.TokenRegistry.CoinGeckoBudgetPath,
					BudgetEnabled:   cfg.TokenRegistry.CoinGeckoBudgetEnabled,
					BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
					CoinGeckoAPIKey: cfg.TokenRegistry.CoinGeckoAPIKey,
					CoinGeckoPro:    cfg.TokenRegistry.CoinGeckoPro,
				})
				cancel()
				if err != nil {
					log.Printf("[TokenSync] %v", err)
					continue
				}
				if updated > 0 {
					log.Printf("[TokenSync] 更新 %d 条，保存至 %s", updated, cfg.TokenRegistry.Path)
				}
			}
		}()
	}
	if cfg.MockMode {
		log.Println("[Config] TokenSync 跳过（MockMode）")
	}

	// 流动性：从 registry 加载初始值，启动时立即同步一次，并定时全表同步（需 API Key）
	if cfg.TokenRegistry.CoinGeckoAPIKey != "" {
		store := tokenregistry.NewStorage(cfg.TokenRegistry.Path)
		if rd, err := store.Load(); err == nil {
			handler.UpdateLiquidity(tokenregistry.LiquidityMapFromRegistry(rd))
			log.Println("[Config] 已从 registry 加载流动性缓存")
		}
		go func() {
			// 启动时立即执行一次流动性同步
			log.Println("[LiquiditySync] 启动中...")
			ctx0, cancel0 := context.WithTimeout(context.Background(), 2*time.Hour)
			liquidity, err := tokenregistry.RunLiquiditySync(ctx0, tokenregistry.LiquiditySyncConfig{
				RegistryPath:    cfg.TokenRegistry.Path,
				CoinGeckoAPIKey: cfg.TokenRegistry.CoinGeckoAPIKey,
				CoinGeckoPro:    cfg.TokenRegistry.CoinGeckoPro,
				DelayPerRequest: 1 * time.Second,
				MaxRetries:      cfg.TokenRegistry.LiquidityRetryMax,
				BackoffBase:     time.Duration(cfg.TokenRegistry.LiquidityBackoffBaseMs) * time.Millisecond,
				BackoffMax:      time.Duration(cfg.TokenRegistry.LiquidityBackoffMaxMs) * time.Millisecond,
				BackoffJitter:   cfg.TokenRegistry.LiquidityBackoffJitter,
				NegativeTTL:     time.Duration(cfg.TokenRegistry.LiquidityNegativeTTL) * time.Second,
				BudgetPath:      cfg.TokenRegistry.CoinGeckoBudgetPath,
				BudgetEnabled:   cfg.TokenRegistry.CoinGeckoBudgetEnabled,
				BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
			})
			cancel0()
			if err == nil {
				handler.UpdateLiquidity(liquidity)
				log.Printf("[LiquiditySync] 启动同步完成，%d 条流动性", len(liquidity))
			} else {
				log.Printf("[LiquiditySync] 启动同步失败: %v", err)
			}
			ticker := time.NewTicker(cfg.LiquiditySyncInterval())
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
				liquidity, err := tokenregistry.RunLiquiditySync(ctx, tokenregistry.LiquiditySyncConfig{
					RegistryPath:    cfg.TokenRegistry.Path,
					CoinGeckoAPIKey: cfg.TokenRegistry.CoinGeckoAPIKey,
					CoinGeckoPro:    cfg.TokenRegistry.CoinGeckoPro,
					DelayPerRequest: 1 * time.Second,
					MaxRetries:      cfg.TokenRegistry.LiquidityRetryMax,
					BackoffBase:     time.Duration(cfg.TokenRegistry.LiquidityBackoffBaseMs) * time.Millisecond,
					BackoffMax:      time.Duration(cfg.TokenRegistry.LiquidityBackoffMaxMs) * time.Millisecond,
					BackoffJitter:   cfg.TokenRegistry.LiquidityBackoffJitter,
					NegativeTTL:     time.Duration(cfg.TokenRegistry.LiquidityNegativeTTL) * time.Second,
					BudgetPath:      cfg.TokenRegistry.CoinGeckoBudgetPath,
					BudgetEnabled:   cfg.TokenRegistry.CoinGeckoBudgetEnabled,
					BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
				})
				cancel()
				if err != nil {
					log.Printf("[LiquiditySync] %v", err)
					continue
				}
				handler.UpdateLiquidity(liquidity)
			}
		}()
		if cfg.TokenRegistry.PrioritySyncEnabled {
			go func() {
				ticker := time.NewTicker(cfg.PriorityLiquiditySyncInterval())
				defer ticker.Stop()
				for range ticker.C {
					cacheMu.RLock()
					items := make([]model.SpreadItem, len(cachedItems))
					copy(items, cachedItems)
					cacheMu.RUnlock()

					assets := priorityTracker.TopAssets(cfg.TokenRegistry.PriorityTopAssets, time.Now())
					if len(assets) == 0 {
						continue
					}

					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
					liquidity, err := tokenregistry.RunLiquiditySync(ctx, tokenregistry.LiquiditySyncConfig{
						RegistryPath:      cfg.TokenRegistry.Path,
						CoinGeckoAPIKey:   cfg.TokenRegistry.CoinGeckoAPIKey,
						CoinGeckoPro:      cfg.TokenRegistry.CoinGeckoPro,
						DelayPerRequest:   1 * time.Second,
						MaxRetries:        cfg.TokenRegistry.LiquidityRetryMax,
						BackoffBase:       time.Duration(cfg.TokenRegistry.LiquidityBackoffBaseMs) * time.Millisecond,
						BackoffMax:        time.Duration(cfg.TokenRegistry.LiquidityBackoffMaxMs) * time.Millisecond,
						BackoffJitter:     cfg.TokenRegistry.LiquidityBackoffJitter,
						NegativeTTL:       time.Duration(cfg.TokenRegistry.LiquidityNegativeTTL) * time.Second,
						BudgetPath:        cfg.TokenRegistry.CoinGeckoBudgetPath,
						BudgetEnabled:     cfg.TokenRegistry.CoinGeckoBudgetEnabled,
						BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
						IncludeAssets:     assets,
						MaxRequests:       cfg.TokenRegistry.PriorityMaxRequests,
					})
					cancel()
					if err != nil {
						log.Printf("[PriorityLiquiditySync] %v", err)
						continue
					}
					handler.UpdateLiquidity(liquidity)
					log.Printf("[PriorityLiquiditySync] 更新完成，assets=%d map=%d", len(assets), len(liquidity))
				}
			}()
			log.Printf("[Config] PriorityLiquiditySync 已启动，间隔 %v", cfg.PriorityLiquiditySyncInterval())
		}
		log.Printf("[Config] LiquiditySync 已启动，间隔 %v", cfg.LiquiditySyncInterval())
	} else {
		log.Println("[Config] LiquiditySync 跳过: 未配置 COINGECKO_API_KEY")
	}

	// Ticker C: 链上价格（需 OKEx Key + token registry）
	var chainPriceFetcher *price.ChainPriceFetcher
	if !cfg.MockMode {
		oc := onchain.NewOkdex()
		if err := oc.Init(); err != nil {
			log.Printf("[Config] ChainPrice 跳过: %v", err)
		} else {
			fetcher, err := price.NewChainPriceFetcher(cfg.TokenRegistry.Path, oc,
				time.Duration(cfg.ChainPrice.CacheTTL)*time.Second)
			if err != nil {
				log.Printf("[Config] ChainPrice 创建失败: %v", err)
			} else {
				chainPriceFetcher = fetcher
				// 启动时立即拉取一次链上价格
				go func() {
					fetcher.ReloadRegistry()
					usdtChains := fetcher.ChainsWithUSDT()
					var pairs []price.AssetChainPair
					pairByChain := make(map[string]int)
					for _, asset := range fetcher.GetAllAssets() {
						for _, chainID := range fetcher.GetAllTokenChains(asset) {
							if usdtChains[chainID] && constants.OKXChainSupported(chainID) {
								pairs = append(pairs, price.AssetChainPair{Asset: asset, ChainID: chainID})
								pairByChain[chainID]++
							}
						}
					}
					if len(pairs) > 0 {
						prices := fetcher.BatchQueryDexPrices(pairs, cfg.ChainPrice.Concurrency)
						handler.UpdateChainPrices(prices)
						chainDist := chainDistribution(prices)
						reqDist := pairDistribution(pairByChain)
						log.Printf("[ChainPrice] 启动同步: %d 对中成功 %d 条 %s (请求%s)", len(pairs), len(prices), chainDist, reqDist)
					} else {
						log.Printf("[ChainPrice] 无可用 (asset,chain) 对，请先运行 tokensync 补全 token 信息")
					}

					ticker := time.NewTicker(cfg.ChainPriceInterval())
					defer ticker.Stop()
					for range ticker.C {
						fetcher.ReloadRegistry()
						usdtChains := fetcher.ChainsWithUSDT()
						pairs = pairs[:0]
						pairByChain = make(map[string]int)
						for _, asset := range fetcher.GetAllAssets() {
							for _, chainID := range fetcher.GetAllTokenChains(asset) {
								if usdtChains[chainID] && constants.OKXChainSupported(chainID) {
									pairs = append(pairs, price.AssetChainPair{Asset: asset, ChainID: chainID})
									pairByChain[chainID]++
								}
							}
						}
						if len(pairs) > 0 {
							prices := fetcher.BatchQueryDexPrices(pairs, cfg.ChainPrice.Concurrency)
							handler.UpdateChainPrices(prices)
							chainDist := chainDistribution(prices)
							reqDist := pairDistribution(pairByChain)
							log.Printf("[ChainPrice] %d 对中成功 %d 条 %s (请求%s)", len(pairs), len(prices), chainDist, reqDist)
						}
					}
				}()
				log.Println("[Config] ChainPrice 已启动")
			}
		}
	}
	_ = chainPriceFetcher

	// Ticker B: 30s 全量探测 + 表格组装 (暂时禁用)
	// go func() {
	// 	ticker := time.NewTicker(cfg.DetectInterval())
	// 	defer ticker.Stop()
	// 	for range ticker.C {
	// 		cacheMu.RLock()
	// 		items := make([]model.SpreadItem, len(cachedItems))
	// 		copy(items, cachedItems)
	// 		cacheMu.RUnlock()

	// 		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	// 		chainPrices := handler.GetAllChainPrices()
	// 		liquidity := handler.GetAllLiquidity()
	// 		resp, err := r.RunDetect(ctx, items, chainPrices, liquidity)
	// 		cancel()
	// 		if err != nil {
	// 			log.Printf("[Detect] %v", err)
	// 			handler.SetLastDetectError(err.Error())
	// 			continue
	// 		}
	// 		handler.UpdateOverview(resp)
	// 		log.Printf("[Detect] overview updated, %d rows", len(resp.Overview))
	// 	}
	// }()

	// 启动时立即拉取一次
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout())
	items, _ := fetchSpread(ctx)
	cancel()
	if len(items) > 0 {
		cacheMu.Lock()
		cachedItems = items
		cacheMu.Unlock()
		priorityTracker.Observe(items, time.Now())

		// 更新机会发现数据（启动时）
		if oppFinder != nil {
			resp := oppFinder.Find(items)
			oppHandler.UpdateResponse(resp)
			// 通知 Telegram
			if oppNotifier != nil {
				oppNotifier.Notify(resp.Opportunities)
			}
		}

		// 更新 K 线 symbol 列表
		if klineFetcher != nil {
			symbols := opportunities.GetSymbolsForKline(items, 500)
			klineFetcher.SetSymbols(symbols)
		}

		// 搬砖监控（暂时禁用）
		// ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
		// resp, _ := r.RunDetect(ctx2, items, handler.GetAllChainPrices(), handler.GetAllLiquidity())
		// cancel2()
		// if resp != nil {
		// 	handler.UpdateOverview(resp)
		// }
	}

	// HTTP 服务
	srv := &http.Server{
		Addr:    ":" + fmt.Sprint(cfg.Server.Port),
		Handler: router,
	}
	go func() {
		log.Printf("server listening on :%d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutting down...")
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	log.Println("done")
}
