package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server      ServerConfig
	SeeingStone SeeingStoneConfig
	Threshold   ThresholdConfig
	Intervals   IntervalsConfig
	Runner      RunnerConfig
	Okex        OkexConfig
	MockMode    bool // 开发模式：使用模拟数据，不请求真实 API
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

	return cfg, nil
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
