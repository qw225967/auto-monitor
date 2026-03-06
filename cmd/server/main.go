package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	"github.com/qw225967/auto-monitor/internal/price"
	"github.com/qw225967/auto-monitor/internal/runner"
	"github.com/qw225967/auto-monitor/internal/source/seeingstone"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

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

	// 路由探测：使用 pipeline 迁移的 ArbitrageAdapter（bridgeMgr 为 nil 时跨链段不可用）
	det := detector.NewArbitrageAdapter(nil)

	// Runner
	r := runner.New(det, cfg.Threshold.Spread)

	// API
	handler := api.New()

	// Gin
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery(), corsMiddleware())
	router.GET("/api/overview", handler.GetOverview)
	router.POST("/api/config/exchange-keys", handler.PostExchangeKeys)
	router.POST("/api/config/liquidity-threshold", handler.PostLiquidityThreshold)

	// 缓存：最新价差数据
	var cachedItems []model.SpreadItem
	var cacheMu sync.RWMutex

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
			log.Printf("[Fetch] got %d items", len(items))
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
				})
				cancel()
				if err != nil {
					log.Printf("[LiquiditySync] %v", err)
					continue
				}
				handler.UpdateLiquidity(liquidity)
			}
		}()
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
					for _, asset := range fetcher.GetAllAssets() {
						for _, chainID := range fetcher.GetAllTokenChains(asset) {
							if usdtChains[chainID] && constants.OKXChainSupported(chainID) {
								pairs = append(pairs, price.AssetChainPair{Asset: asset, ChainID: chainID})
							}
						}
					}
					if len(pairs) > 0 {
						prices := fetcher.BatchQueryDexPrices(pairs, cfg.ChainPrice.Concurrency)
						handler.UpdateChainPrices(prices)
						log.Printf("[ChainPrice] 启动同步: %d 对中成功 %d 条", len(pairs), len(prices))
					} else {
						log.Printf("[ChainPrice] 无可用 (asset,chain) 对，请先运行 tokensync 补全 token 信息")
					}

					ticker := time.NewTicker(cfg.ChainPriceInterval())
					defer ticker.Stop()
					for range ticker.C {
						fetcher.ReloadRegistry()
						usdtChains := fetcher.ChainsWithUSDT()
						pairs = pairs[:0]
						for _, asset := range fetcher.GetAllAssets() {
							for _, chainID := range fetcher.GetAllTokenChains(asset) {
								if usdtChains[chainID] && constants.OKXChainSupported(chainID) {
									pairs = append(pairs, price.AssetChainPair{Asset: asset, ChainID: chainID})
								}
							}
						}
						if len(pairs) > 0 {
							prices := fetcher.BatchQueryDexPrices(pairs, cfg.ChainPrice.Concurrency)
							handler.UpdateChainPrices(prices)
							log.Printf("[ChainPrice] %d 对中成功 %d 条", len(pairs), len(prices))
						}
					}
				}()
				log.Println("[Config] ChainPrice 已启动")
			}
		}
	}
	_ = chainPriceFetcher

	// Ticker B: 30s 全量探测 + 表格组装
	go func() {
		ticker := time.NewTicker(cfg.DetectInterval())
		defer ticker.Stop()
		for range ticker.C {
			cacheMu.RLock()
			items := make([]model.SpreadItem, len(cachedItems))
			copy(items, cachedItems)
			cacheMu.RUnlock()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			chainPrices := handler.GetAllChainPrices()
			liquidity := handler.GetAllLiquidity()
			resp, err := r.RunDetect(ctx, items, chainPrices, liquidity)
			cancel()
			if err != nil {
				log.Printf("[Detect] %v", err)
				continue
			}
			handler.UpdateOverview(resp)
			log.Printf("[Detect] overview updated, %d rows", len(resp.Overview))
		}
	}()

	// 启动时立即拉取一次
	ctx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout())
	items, _ := fetchSpread(ctx)
	cancel()
	if len(items) > 0 {
		cacheMu.Lock()
		cachedItems = items
		cacheMu.Unlock()
		ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
		resp, _ := r.RunDetect(ctx2, items, handler.GetAllChainPrices(), handler.GetAllLiquidity())
		cancel2()
		if resp != nil {
			handler.UpdateOverview(resp)
		}
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
