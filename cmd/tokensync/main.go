// tokensync 同步 token 信息：仅搜符合阈值的 token，本地已有则使用本地
package main

import (
	"context"
	"flag"
	"log"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

const (
	delaySuccess   = 10 * time.Second // 成功后的间隔（CoinGecko 免费版约 10-30 次/分钟）
	delayRateLimit = 65 * time.Second // 429 后等待（免费版约 1 分钟冷却）
)

func main() {
	_ = godotenv.Load()

	var (
		registryPath = flag.String("registry", "data/token_registry.json", "token 信息存储路径")
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.SeeingStone.APIToken == "" {
		log.Fatal("SEEINGSTONE_API_TOKEN 未配置，无法获取符合阈值的 symbol 列表")
	}

	// 1. 仅获取符合阈值的 symbol，提取资产列表
	assets, err := tokenregistry.AssetsFromSeeingStoneWithThreshold(ctx,
		cfg.SeeingStone.APIURL, cfg.SeeingStone.APIToken, cfg.Threshold.Spread)
	if err != nil {
		log.Fatalf("获取价差数据: %v", err)
	}
	if len(assets) == 0 {
		log.Printf("[tokensync] 无符合阈值(%.2f%%)的 symbol，跳过", cfg.Threshold.Spread)
		return
	}
	log.Printf("[tokensync] 符合阈值的资产: %v", assets)

	// 2. 加载本地已有数据
	store := tokenregistry.NewStorage(*registryPath)
	rd, err := store.Load()
	if err != nil {
		log.Fatalf("load registry: %v", err)
	}

	// 3. 仅拉取本地未保存的资产；已有则使用本地，不请求
	fetcher := tokenregistry.NewCoinGeckoFetcher(cfg.TokenRegistry.CoinGeckoAPIKey, cfg.TokenRegistry.CoinGeckoPro)
	totalUpdated := 0
	fetchIdx := 0
	for _, asset := range assets {
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset == "" {
			continue
		}
		if tokenregistry.HasAsset(rd, asset) {
			log.Printf("[tokensync] %s: 使用本地缓存，跳过", asset)
			continue
		}
		fetchIdx++
		log.Printf("[tokensync] [%d] 拉取 %s ...", fetchIdx, asset)
		infos, err := fetcher.FetchTokenInfos(ctx, asset)
		if err != nil {
			// 429 时等待后重试一次
			if strings.Contains(err.Error(), "429") {
				log.Printf("[tokensync] %s 限流，等待 %v 后重试", asset, delayRateLimit)
				time.Sleep(delayRateLimit)
				infos, err = fetcher.FetchTokenInfos(ctx, asset)
			}
			if err != nil {
				log.Printf("[tokensync] 跳过 %s: %v", asset, err)
				continue
			}
		}
		n := store.MergeIncremental(rd, infos)
		totalUpdated += n
		log.Printf("[tokensync] %s: 新增/更新 %d 条", asset, n)
		time.Sleep(delaySuccess)
	}

	// 4. 保存
	if totalUpdated > 0 {
		if err := store.Save(rd); err != nil {
			log.Fatalf("save registry: %v", err)
		}
	}
	log.Printf("[tokensync] 完成，本次更新 %d 条，保存至 %s", totalUpdated, *registryPath)
}
