package tokenregistry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TokenListConfig config/tokens.yaml 结构
type TokenListConfig struct {
	Assets []string `yaml:"assets"`
}

// LoadTokenListFromYAML 从 YAML 加载资产列表
func LoadTokenListFromYAML(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"USDT", "USDC", "ETH"}, nil
		}
		return nil, fmt.Errorf("read token list: %w", err)
	}
	var cfg TokenListConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse token list: %w", err)
	}
	if len(cfg.Assets) == 0 {
		return []string{"USDT", "USDC", "ETH"}, nil
	}
	return cfg.Assets, nil
}
