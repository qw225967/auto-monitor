package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server       ServerConfig
	SeeingStone  SeeingStoneConfig
	Threshold    ThresholdConfig
	Intervals    IntervalsConfig
	MockMode     bool // 开发模式：使用模拟数据，不请求真实 API
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

	// 默认值
	viper.SetDefault("seeingstone.api_url", "https://seeingstone.cloud")
	viper.SetDefault("threshold.spread", 1.0)
	viper.SetDefault("intervals.fetch", 10)
	viper.SetDefault("intervals.detect", 30)
	viper.SetDefault("seeingstone.request_timeout", 10)
	viper.SetDefault("server.port", 8080)
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
		MockMode: viper.GetBool("mock_mode") || viper.GetBool("MOCK_MODE"),
	}

	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.SeeingStone.RequestTimeout == 0 {
		cfg.SeeingStone.RequestTimeout = 10
	}
	if cfg.Intervals.Fetch == 0 {
		cfg.Intervals.Fetch = 10
	}
	if cfg.Intervals.Detect == 0 {
		cfg.Intervals.Detect = 30
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
