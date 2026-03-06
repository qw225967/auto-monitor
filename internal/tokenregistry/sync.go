package tokenregistry

import (
	"context"
	"log"
	"strings"
	"time"
)

// SyncConfig Token 同步配置
type SyncConfig struct {
	RegistryPath     string
	APIURL           string
	APIToken         string
	RequestTimeout   time.Duration
	UseAllSymbols    bool
	SpreadThreshold  float64
	CoingeckoDelay   time.Duration
	CoinGeckoAPIKey  string
	CoinGeckoPro     bool
}

// RunSync 执行一轮 token 信息同步
// 从 SeeingStone 获取 symbol 列表，提取资产，对本地未缓存的资产从 CoinGecko 拉取并增量保存
func RunSync(ctx context.Context, cfg SyncConfig) (updated int, err error) {
	var assets []string
	if cfg.UseAllSymbols {
		assets, err = AssetsFromSeeingStone(ctx, cfg.APIURL, cfg.APIToken, cfg.RequestTimeout)
	} else {
		assets, err = AssetsFromSeeingStoneWithThreshold(ctx, cfg.APIURL, cfg.APIToken, cfg.SpreadThreshold, cfg.RequestTimeout)
	}
	if err != nil {
		return 0, err
	}
	if len(assets) == 0 {
		return 0, nil
	}

	store := NewStorage(cfg.RegistryPath)
	rd, err := store.Load()
	if err != nil {
		return 0, err
	}

	fetcher := NewCoinGeckoFetcher(cfg.CoinGeckoAPIKey, cfg.CoinGeckoPro)
	delay := cfg.CoingeckoDelay
	if delay == 0 {
		delay = 10 * time.Second
	}
	delay429 := 65 * time.Second

	for _, asset := range assets {
		asset = strings.ToUpper(strings.TrimSpace(asset))
		if asset == "" {
			continue
		}
		if HasAsset(rd, asset) {
			continue
		}
		infos, err := fetcher.FetchTokenInfos(ctx, asset)
		if err != nil {
			log.Printf("[TokenSync] 跳过 %s: %v", asset, err)
			if strings.Contains(err.Error(), "429") {
				log.Printf("[TokenSync] 触发限流，等待 %v", delay429)
				select {
				case <-ctx.Done():
					return updated, ctx.Err()
				case <-time.After(delay429):
				}
			}
			continue
		}
		updated += store.MergeIncremental(rd, infos)
		select {
		case <-ctx.Done():
			return updated, ctx.Err()
		case <-time.After(delay):
		}
	}

	if updated > 0 {
		if err := store.Save(rd); err != nil {
			return updated, err
		}
	}
	return updated, nil
}
