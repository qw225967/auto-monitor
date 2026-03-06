package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server        ServerConfig
	SeeingStone   SeeingStoneConfig
	Threshold     ThresholdConfig
	Intervals     IntervalsConfig
	TokenRegistry TokenRegistryConfig
	ChainPrice    ChainPriceConfig
	Okex          OkexConfig
	MockMode      bool // 开发模式：使用模拟数据，不请求真实 API
}

// OkexConfig OKEx API 配置（用于 DEX Quote / 链上价格）
type OkexConfig struct {
	AppKey     string
	SecretKey  string
	Passphrase string
}

type ServerConfig struct {
	Port int
}

type SeeingStoneConfig struct {
	APIURL         string
	APIToken       string
	RequestTimeout int
}

type ThresholdConfig struct {
	Spread float64
}

type IntervalsConfig struct {
	Fetch  int
	Detect int
}

// TokenRegistryConfig Token 信息补全配置
type TokenRegistryConfig struct {
	Path            string
	SyncInterval    int    // 秒
	CoinGeckoAPIKey string // CoinGecko Demo/Pro API Key，避免 429 限流
	CoinGeckoPro    bool   // true 时使用 Pro API (pro-api.coingecko.com)
}

// ChainPriceConfig 链上价格配置
type ChainPriceConfig struct {
	Interval     int // 拉取间隔 (秒)
	CacheTTL     int // 缓存时长 (秒)
	Concurrency  int // 并发数
}

func Load() (*Config, error) {
	viper.SetConfigName("settings")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("config")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	// 显式绑定环境变量（.env 通过 godotenv 加载后生效）
	_ = viper.BindEnv("seeingstone.api_token", "SEEINGSTONE_API_TOKEN")
	_ = viper.BindEnv("seeingstone.api_url", "SEEINGSTONE_API_URL")
	_ = viper.BindEnv("server.port", "SERVER_PORT")
	_ = viper.BindEnv("token_registry.path", "TOKEN_REGISTRY_PATH")
	_ = viper.BindEnv("token_registry.sync_interval", "TOKEN_SYNC_INTERVAL")
	_ = viper.BindEnv("token_registry.coingecko_api_key", "COINGECKO_API_KEY")
	_ = viper.BindEnv("token_registry.coingecko_pro", "COINGECKO_PRO")
	_ = viper.BindEnv("chain_price.interval", "CHAIN_PRICE_INTERVAL")
	_ = viper.BindEnv("chain_price.cache_ttl", "CHAIN_PRICE_CACHE_TTL")
	_ = viper.BindEnv("chain_price.concurrency", "CHAIN_PRICE_CONCURRENCY")
	_ = viper.BindEnv("okex.app_key", "OKEX_APP_KEY")
	_ = viper.BindEnv("okex.secret_key", "OKEX_SECRET_KEY")
	_ = viper.BindEnv("okex.passphrase", "OKEX_PASSPHRASE")

	// 默认值
	viper.SetDefault("seeingstone.api_url", "https://seeingstone.cloud")
	viper.SetDefault("threshold.spread", 0.5)
	viper.SetDefault("intervals.fetch", 3)
	viper.SetDefault("intervals.detect", 30)
	viper.SetDefault("seeingstone.request_timeout", 10)
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("mock_mode", false)
	viper.SetDefault("token_registry.path", "data/token_registry.json")
	viper.SetDefault("token_registry.sync_interval", 300)
	viper.SetDefault("chain_price.interval", 3)
	viper.SetDefault("chain_price.cache_ttl", 30)
	viper.SetDefault("chain_price.concurrency", 3)

	cfg := &Config{
		Server: ServerConfig{
			Port: viper.GetInt("server.port"),
		},
		SeeingStone: SeeingStoneConfig{
			APIURL:         viper.GetString("seeingstone.api_url"),
			APIToken:       viper.GetString("seeingstone.api_token"),
			RequestTimeout: viper.GetInt("seeingstone.request_timeout"),
		},
		Threshold: ThresholdConfig{
			Spread: viper.GetFloat64("threshold.spread"),
		},
		Intervals: IntervalsConfig{
			Fetch:  viper.GetInt("intervals.fetch"),
			Detect: viper.GetInt("intervals.detect"),
		},
		TokenRegistry: TokenRegistryConfig{
			Path:            viper.GetString("token_registry.path"),
			SyncInterval:    viper.GetInt("token_registry.sync_interval"),
			CoinGeckoAPIKey: viper.GetString("token_registry.coingecko_api_key"),
			CoinGeckoPro:    viper.GetBool("token_registry.coingecko_pro"),
		},
		ChainPrice: ChainPriceConfig{
			Interval:    viper.GetInt("chain_price.interval"),
			CacheTTL:    viper.GetInt("chain_price.cache_ttl"),
			Concurrency: viper.GetInt("chain_price.concurrency"),
		},
		Okex: OkexConfig{
			AppKey:     viper.GetString("okex.app_key"),
			SecretKey:  viper.GetString("okex.secret_key"),
			Passphrase: viper.GetString("okex.passphrase"),
		},
		MockMode: viper.GetBool("mock_mode") || viper.GetBool("MOCK_MODE"),
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.SeeingStone.RequestTimeout == 0 {
		cfg.SeeingStone.RequestTimeout = 10
	}
	if cfg.Intervals.Fetch == 0 {
		cfg.Intervals.Fetch = 3
	}
	if cfg.Intervals.Detect == 0 {
		cfg.Intervals.Detect = 30
	}
	if cfg.TokenRegistry.Path == "" {
		cfg.TokenRegistry.Path = "data/token_registry.json"
	}
	if cfg.TokenRegistry.SyncInterval == 0 {
		cfg.TokenRegistry.SyncInterval = 300
	}
	if cfg.ChainPrice.Interval == 0 {
		cfg.ChainPrice.Interval = 3
	}
	if cfg.ChainPrice.CacheTTL == 0 {
		cfg.ChainPrice.CacheTTL = 30
	}
	if cfg.ChainPrice.Concurrency == 0 {
		cfg.ChainPrice.Concurrency = 3
	}

	return cfg, nil
}

func (c *Config) ChainPriceInterval() time.Duration {
	return time.Duration(c.ChainPrice.Interval) * time.Second
}

func (c *Config) TokenSyncInterval() time.Duration {
	return time.Duration(c.TokenRegistry.SyncInterval) * time.Second
}

func (c *Config) FetchInterval() time.Duration {
	return time.Duration(c.Intervals.Fetch) * time.Second
}

func (c *Config) DetectInterval() time.Duration {
	return time.Duration(c.Intervals.Detect) * time.Second
}

func (c *Config) RequestTimeout() time.Duration {
	return time.Duration(c.SeeingStone.RequestTimeout) * time.Second
}
