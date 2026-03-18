// tokensync 同步 token 信息：仅搜符合阈值的 token，按 TTL 刷新并链级增量合并
package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/joho/godotenv"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/tokenregistry"
)

const delaySuccess = 10 * time.Second // 成功后的间隔（CoinGecko 免费版约 10-30 次/分钟）

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

	updated, err := tokenregistry.RunSync(ctx, tokenregistry.SyncConfig{
		RegistryPath:    *registryPath,
		APIURL:          cfg.SeeingStone.APIURL,
		APIToken:        cfg.SeeingStone.APIToken,
		RequestTimeout:  cfg.RequestTimeout(),
		UseAllSymbols:   false,
		SpreadThreshold: cfg.Threshold.Spread,
		CoingeckoDelay:  delaySuccess,
		TokenRefreshTTL: cfg.TokenRefreshTTL(),
		BudgetPath:      cfg.TokenRegistry.CoinGeckoBudgetPath,
		BudgetEnabled:   cfg.TokenRegistry.CoinGeckoBudgetEnabled,
		BudgetMonthlyLimit: cfg.TokenRegistry.CoinGeckoMonthlyLimit,
		CoinGeckoAPIKey: cfg.TokenRegistry.CoinGeckoAPIKey,
		CoinGeckoPro:    cfg.TokenRegistry.CoinGeckoPro,
	})
	if err != nil {
		log.Fatalf("tokensync failed: %v", err)
	}
	log.Printf("[tokensync] 完成，本次更新 %d 条，保存至 %s", updated, *registryPath)
}
