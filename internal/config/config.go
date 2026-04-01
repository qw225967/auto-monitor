package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
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
	if wd, err := os.Getwd(); err != nil {
		log.Printf("[Config] 获取工作目录失败: %v", err)
	} else {
		log.Printf("[Config] 工作目录: %s", wd)
	}

	// 将 .env 注入进程环境；先 config/ 再根目录（后者覆盖前者，与常见 monorepo 习惯一致）
	envPaths := []string{"config/.env", ".env"}
	loadedAny := false
	for _, path := range envPaths {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := godotenv.Load(path); err != nil {
			log.Printf("[Config] 读取 %s 失败: %v", path, err)
			continue
		}
		log.Printf("[Config] 已从 %s 注入环境变量", path)
		loadedAny = true
	}
	if !loadedAny {
		log.Printf("[Config] 未找到 %v（仅使用已有环境变量与 yaml）", envPaths)
	}

	viper.SetConfigName("settings")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("config")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
		log.Printf("[Config] 未找到 settings.yaml（config/ 或当前目录），仅使用默认值与 .env/环境变量")
	} else {
		log.Printf("[Config] 已读取配置文件: %s", viper.ConfigFileUsed())
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

	tokenInfo := "未设置"
	if t := cfg.SeeingStone.APIToken; t != "" {
		tokenInfo = fmt.Sprintf("已设置(len=%d)", len(t))
	}
	log.Printf("[Config] SeeingStone: api_url=%s request_timeout=%ds api_token=%s",
		cfg.SeeingStone.APIURL, cfg.SeeingStone.RequestTimeout, tokenInfo)
	log.Printf("[Config] server.port=%d threshold.spread=%.6f intervals.fetch=%ds intervals.detect=%ds mock_mode=%v",
		cfg.Server.Port, cfg.Threshold.Spread, cfg.Intervals.Fetch, cfg.Intervals.Detect, cfg.MockMode)
	log.Printf("[Config] runner: detect_max_concurrency=%d detect_route_timeout=%ds",
		cfg.Runner.DetectMaxConcurrency, cfg.Runner.DetectRouteTimeout)
	okexOK := cfg.Okex.AppKey != "" && cfg.Okex.SecretKey != "" && cfg.Okex.Passphrase != ""
	log.Printf("[Config] OKEx DEX: 密钥完整=%v (app_key len=%d)", okexOK, len(cfg.Okex.AppKey))

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
