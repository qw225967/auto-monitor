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
	Runner        RunnerConfig
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

// RunnerConfig 探测并发与超时配置
type RunnerConfig struct {
	DetectMaxConcurrency int // 探测并发上限
	DetectRouteTimeout   int // 秒，单路探测超时
}

// TokenRegistryConfig Token 信息补全配置
type TokenRegistryConfig struct {
	Path                  string
	SyncInterval          int    // 秒，token 地址增量同步间隔
	TokenRefreshTTL       int    // 秒，token 信息刷新 TTL（过期后重新请求 CoinGecko）
	LiquiditySyncInterval int    // 秒，全表流动性同步间隔，默认 4h，控制 10 万/月 请求量
	PrioritySyncEnabled   bool   // 是否启用优先流动性同步（候选优先）
	PrioritySyncInterval  int    // 秒，优先流动性同步间隔
	PriorityTopAssets     int    // 每轮优先资产数（按候选 spread）
	PriorityMaxRequests   int    // 每轮优先同步最大请求数
	LiquidityRetryMax     int    // 流动性请求最大重试次数（不含首次）
	LiquidityBackoffBaseMs int   // 流动性重试退避基础毫秒
	LiquidityBackoffMaxMs int    // 流动性重试退避最大毫秒
	LiquidityBackoffJitter float64 // 流动性重试抖动百分比（如 20 表示 ±20%）
	LiquidityNegativeTTL  int    // 秒，负缓存 TTL（404/无池）
	CoinGeckoBudgetEnabled bool  // 是否启用 CoinGecko 预算管理
	CoinGeckoBudgetPath   string // CoinGecko 预算文件路径
	CoinGeckoMonthlyLimit int    // CoinGecko 月总请求上限
	CoinGeckoAPIKey       string // CoinGecko Demo/Pro API Key，避免 429 限流
	CoinGeckoPro          bool   // true 时使用 Pro API (pro-api.coingecko.com)
}

// ChainPriceConfig 链上价格配置
type ChainPriceConfig struct {
	Interval    int // 拉取间隔 (秒)
	CacheTTL    int // 缓存时长 (秒)
	Concurrency int // 并发数
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
	_ = viper.BindEnv("seeingstone.request_timeout", "REQUEST_TIMEOUT")
	_ = viper.BindEnv("server.port", "SERVER_PORT")
	_ = viper.BindEnv("runner.detect_max_concurrency", "RUN_DETECT_MAX_CONCURRENCY")
	_ = viper.BindEnv("runner.detect_route_timeout", "RUN_DETECT_ROUTE_TIMEOUT")
	_ = viper.BindEnv("token_registry.path", "TOKEN_REGISTRY_PATH")
	_ = viper.BindEnv("token_registry.sync_interval", "TOKEN_SYNC_INTERVAL")
	_ = viper.BindEnv("token_registry.token_refresh_ttl", "TOKEN_REFRESH_TTL")
	_ = viper.BindEnv("token_registry.liquidity_sync_interval", "LIQUIDITY_SYNC_INTERVAL")
	_ = viper.BindEnv("token_registry.priority_sync_enabled", "PRIORITY_LIQUIDITY_SYNC_ENABLED")
	_ = viper.BindEnv("token_registry.priority_sync_interval", "PRIORITY_LIQUIDITY_SYNC_INTERVAL")
	_ = viper.BindEnv("token_registry.priority_top_assets", "PRIORITY_LIQUIDITY_TOP_ASSETS")
	_ = viper.BindEnv("token_registry.priority_max_requests", "PRIORITY_LIQUIDITY_MAX_REQUESTS")
	_ = viper.BindEnv("token_registry.liquidity_retry_max", "LIQUIDITY_RETRY_MAX")
	_ = viper.BindEnv("token_registry.liquidity_backoff_base_ms", "LIQUIDITY_BACKOFF_BASE_MS")
	_ = viper.BindEnv("token_registry.liquidity_backoff_max_ms", "LIQUIDITY_BACKOFF_MAX_MS")
	_ = viper.BindEnv("token_registry.liquidity_backoff_jitter", "LIQUIDITY_BACKOFF_JITTER")
	_ = viper.BindEnv("token_registry.liquidity_negative_ttl", "LIQUIDITY_NEGATIVE_TTL")
	_ = viper.BindEnv("token_registry.coingecko_budget_enabled", "COINGECKO_BUDGET_ENABLED")
	_ = viper.BindEnv("token_registry.coingecko_budget_path", "COINGECKO_BUDGET_PATH")
	_ = viper.BindEnv("token_registry.coingecko_monthly_limit", "COINGECKO_MONTHLY_LIMIT")
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
	viper.SetDefault("runner.detect_max_concurrency", 64)
	viper.SetDefault("runner.detect_route_timeout", 10)
	viper.SetDefault("seeingstone.request_timeout", 60)
	viper.SetDefault("server.port", 8088)
	viper.SetDefault("mock_mode", false)
	viper.SetDefault("token_registry.path", "data/token_registry.json")
	viper.SetDefault("token_registry.sync_interval", 300)
	viper.SetDefault("token_registry.token_refresh_ttl", 604800) // 7d
	viper.SetDefault("token_registry.liquidity_sync_interval", 14400) // 4h，约 500 对 × 6 次/天 ≈ 9 万/月
	viper.SetDefault("token_registry.priority_sync_enabled", true)
	viper.SetDefault("token_registry.priority_sync_interval", 300)
	viper.SetDefault("token_registry.priority_top_assets", 30)
	viper.SetDefault("token_registry.priority_max_requests", 80)
	viper.SetDefault("token_registry.liquidity_retry_max", 3)
	viper.SetDefault("token_registry.liquidity_backoff_base_ms", 500)
	viper.SetDefault("token_registry.liquidity_backoff_max_ms", 5000)
	viper.SetDefault("token_registry.liquidity_backoff_jitter", 20.0)
	viper.SetDefault("token_registry.liquidity_negative_ttl", 86400) // 24h
	viper.SetDefault("token_registry.coingecko_budget_enabled", true)
	viper.SetDefault("token_registry.coingecko_budget_path", "data/coingecko_budget.json")
	viper.SetDefault("token_registry.coingecko_monthly_limit", 100000)
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
		Runner: RunnerConfig{
			DetectMaxConcurrency: viper.GetInt("runner.detect_max_concurrency"),
			DetectRouteTimeout:   viper.GetInt("runner.detect_route_timeout"),
		},
		TokenRegistry: TokenRegistryConfig{
			Path:                  viper.GetString("token_registry.path"),
			SyncInterval:          viper.GetInt("token_registry.sync_interval"),
			TokenRefreshTTL:       viper.GetInt("token_registry.token_refresh_ttl"),
			LiquiditySyncInterval: viper.GetInt("token_registry.liquidity_sync_interval"),
			PrioritySyncEnabled:   viper.GetBool("token_registry.priority_sync_enabled"),
			PrioritySyncInterval:  viper.GetInt("token_registry.priority_sync_interval"),
			PriorityTopAssets:     viper.GetInt("token_registry.priority_top_assets"),
			PriorityMaxRequests:   viper.GetInt("token_registry.priority_max_requests"),
			LiquidityRetryMax:      viper.GetInt("token_registry.liquidity_retry_max"),
			LiquidityBackoffBaseMs: viper.GetInt("token_registry.liquidity_backoff_base_ms"),
			LiquidityBackoffMaxMs:  viper.GetInt("token_registry.liquidity_backoff_max_ms"),
			LiquidityBackoffJitter: viper.GetFloat64("token_registry.liquidity_backoff_jitter"),
			LiquidityNegativeTTL:   viper.GetInt("token_registry.liquidity_negative_ttl"),
			CoinGeckoBudgetEnabled: viper.GetBool("token_registry.coingecko_budget_enabled"),
			CoinGeckoBudgetPath:    viper.GetString("token_registry.coingecko_budget_path"),
			CoinGeckoMonthlyLimit:  viper.GetInt("token_registry.coingecko_monthly_limit"),
			CoinGeckoAPIKey:       viper.GetString("token_registry.coingecko_api_key"),
			CoinGeckoPro:          viper.GetBool("token_registry.coingecko_pro"),
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
		cfg.Server.Port = 8088
	}
	if cfg.SeeingStone.RequestTimeout == 0 {
		cfg.SeeingStone.RequestTimeout = 60
	}
	if cfg.Intervals.Fetch == 0 {
		cfg.Intervals.Fetch = 3
	}
	if cfg.Intervals.Detect == 0 {
		cfg.Intervals.Detect = 30
	}
	if cfg.Runner.DetectMaxConcurrency <= 0 {
		cfg.Runner.DetectMaxConcurrency = 64
	}
	if cfg.Runner.DetectRouteTimeout <= 0 {
		cfg.Runner.DetectRouteTimeout = 10
	}
	if cfg.TokenRegistry.Path == "" {
		cfg.TokenRegistry.Path = "data/token_registry.json"
	}
	if cfg.TokenRegistry.SyncInterval == 0 {
		cfg.TokenRegistry.SyncInterval = 300
	}
	if cfg.TokenRegistry.TokenRefreshTTL == 0 {
		cfg.TokenRegistry.TokenRefreshTTL = 604800
	}
	if cfg.TokenRegistry.LiquiditySyncInterval == 0 {
		cfg.TokenRegistry.LiquiditySyncInterval = 14400
	}
	if cfg.TokenRegistry.PrioritySyncInterval <= 0 {
		cfg.TokenRegistry.PrioritySyncInterval = 300
	}
	if cfg.TokenRegistry.PriorityTopAssets <= 0 {
		cfg.TokenRegistry.PriorityTopAssets = 30
	}
	if cfg.TokenRegistry.PriorityMaxRequests <= 0 {
		cfg.TokenRegistry.PriorityMaxRequests = 80
	}
	if cfg.TokenRegistry.LiquidityRetryMax < 0 {
		cfg.TokenRegistry.LiquidityRetryMax = 0
	}
	if cfg.TokenRegistry.LiquidityBackoffBaseMs <= 0 {
		cfg.TokenRegistry.LiquidityBackoffBaseMs = 500
	}
	if cfg.TokenRegistry.LiquidityBackoffMaxMs <= 0 {
		cfg.TokenRegistry.LiquidityBackoffMaxMs = 5000
	}
	if cfg.TokenRegistry.LiquidityBackoffJitter < 0 {
		cfg.TokenRegistry.LiquidityBackoffJitter = 0
	}
	if cfg.TokenRegistry.LiquidityNegativeTTL <= 0 {
		cfg.TokenRegistry.LiquidityNegativeTTL = 86400
	}
	if cfg.TokenRegistry.CoinGeckoBudgetPath == "" {
		cfg.TokenRegistry.CoinGeckoBudgetPath = "data/coingecko_budget.json"
	}
	if cfg.TokenRegistry.CoinGeckoMonthlyLimit < 0 {
		cfg.TokenRegistry.CoinGeckoMonthlyLimit = 0
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

func (c *Config) LiquiditySyncInterval() time.Duration {
	return time.Duration(c.TokenRegistry.LiquiditySyncInterval) * time.Second
}

func (c *Config) PriorityLiquiditySyncInterval() time.Duration {
	return time.Duration(c.TokenRegistry.PrioritySyncInterval) * time.Second
}

func (c *Config) TokenRefreshTTL() time.Duration {
	return time.Duration(c.TokenRegistry.TokenRefreshTTL) * time.Second
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
