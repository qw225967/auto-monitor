package registry

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// networksYAML 配置文件结构
type networksYAML struct {
	Exchanges map[string]map[string][]string `yaml:"exchanges"`
}

// loadNetworksFromFile 从 YAML 加载交易所-资产-链配置
// 返回 exchange(lowercase) -> asset(uppercase) -> chainIDs，失败返回 nil
func loadNetworksFromFile(path string) map[string]map[string][]string {
	if path == "" {
		path = "config/networks.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg networksYAML
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	if cfg.Exchanges == nil {
		return nil
	}
	out := make(map[string]map[string][]string)
	for ex, assets := range cfg.Exchanges {
		ex = strings.ToLower(strings.TrimSpace(ex))
		if ex == "okx" {
			ex = "okex"
		}
		if assets == nil {
			continue
		}
		out[ex] = make(map[string][]string)
		for asset, chains := range assets {
			asset = strings.ToUpper(strings.TrimSpace(asset))
			if asset == "" || len(chains) == 0 {
				continue
			}
			var valid []string
			for _, c := range chains {
				c = strings.TrimSpace(c)
				if c != "" {
					valid = append(valid, c)
				}
			}
			if len(valid) > 0 {
				out[ex][asset] = valid
			}
		}
	}
	return out
}

// findConfigPath 查找配置文件路径（支持工作目录）
func findConfigPath() string {
	for _, p := range []string{"config/networks.yaml", "networks.yaml"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// 尝试从可执行文件所在目录查找
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		p := filepath.Join(dir, "config", "networks.yaml")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
