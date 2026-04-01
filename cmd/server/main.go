package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/qw225967/auto-monitor/internal/api"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/opportunities"
	"github.com/qw225967/auto-monitor/internal/opportunities/ticker"
	"github.com/qw225967/auto-monitor/internal/source/seeingstone"
	tg "github.com/qw225967/auto-monitor/internal/utils/notify/telegram"
)

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

	// API
	handler := api.New()

	// 机会发现
	oppFinder := opportunities.NewFinder()
	opportunities.RegisterExchangeAdapters(oppFinder) // 注册 Binance/Bybit/OKX/Gate/Bitget 公共订单簿
	oppHandler := opportunities.NewHandler(oppFinder)

	// Telegram 通知器：优先从 exchange_keys.json 读取，其次 GlobalConfig
	var oppNotifier *opportunities.OpportunityNotifier
	var tgBotToken, tgChatID string
	if keys := config.GetExchangeKeys(); keys != nil && keys.Telegram != nil && keys.Telegram.BotToken != "" && keys.Telegram.ChatID != "" {
		tgBotToken = keys.Telegram.BotToken
		tgChatID = keys.Telegram.ChatID
	} else if g := config.GetGlobalConfig(); g != nil && g.Telegram != nil && g.Telegram.BotToken != "" && g.Telegram.ChatID != "" {
		tgBotToken = g.Telegram.BotToken
		tgChatID = g.Telegram.ChatID
	}
	if tgBotToken != "" && tgChatID != "" {
		tgClient := tg.NewTelegramClient(tgBotToken, tgChatID)
		oppNotifier = opportunities.NewOpportunityNotifier(tgClient)
		log.Printf("[Telegram] 机会通知器已启动，ChatID: %s", tgChatID)
	} else {
		log.Printf("[Telegram] 未配置 Telegram Bot（exchange_keys.json 或 GlobalConfig），跳过通知功能")
	}

	// Ticker 拉取：实时价格（每交易所 1 次请求，每 3s 一轮）
	tickerFetcher := ticker.NewFetcher([]string{"binance", "bybit", "okx", "gate", "bitget"})
	tickerFetcher.SetOnPrice(oppFinder.FeedTicker)
	tickerCtx := context.Background()
	go tickerFetcher.RunLoop(tickerCtx)

	// Gin
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), corsMiddleware())
	router.GET("/api/overview", handler.GetOverview)
	router.GET("/api/opportunities", oppHandler.GetOpportunities)
	router.POST("/api/config/exchange-keys", handler.PostExchangeKeys)
	router.POST("/api/config/liquidity-threshold", handler.PostLiquidityThreshold)

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
			// 更新 Ticker 拉取 symbol 列表（负价差去重，最多 500 个）
			symbols := opportunities.GetSymbolsForKline(items, 500)
			if tickerFetcher != nil {
				tickerFetcher.SetSymbols(symbols)
			}
		}
	}()

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
		// 更新机会发现数据（启动时）
		if oppFinder != nil {
			resp := oppFinder.Find(items)
			oppHandler.UpdateResponse(resp)
			// 通知 Telegram
			if oppNotifier != nil {
				oppNotifier.Notify(resp.Opportunities)
			}
		}

		// 更新 Ticker symbol 列表
		if tickerFetcher != nil {
			symbols := opportunities.GetSymbolsForKline(items, 500)
			tickerFetcher.SetSymbols(symbols)
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
