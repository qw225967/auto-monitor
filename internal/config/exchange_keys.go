package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ExchangeKeysConfig 交易所 API 密钥配置（与 exchange_keys.json 格式一致）
type ExchangeKeysConfig struct {
	BitGet   *ExchangeKeyEntry `json:"BitGet,omitempty"`
	Bybit    *ExchangeKeyEntry `json:"Bybit,omitempty"`
	Gate     *ExchangeKeyEntry `json:"Gate,omitempty"`
	Binance  *BinanceKeyEntry  `json:"Binance,omitempty"`
	OKX      *ExchangeKeyEntry `json:"OKX,omitempty"`
	Telegram *TelegramEntry    `json:"Telegram,omitempty"`
	Hyperliquid *HyperliquidEntry `json:"Hyperliquid,omitempty"`
	Lighter  *LighterEntry    `json:"Lighter,omitempty"`
	Aster    *ExchangeKeyEntry `json:"Aster,omitempty"`
}

// ExchangeKeyEntry 通用交易所密钥（APIKey + Secret + Passphrase 可选）
type ExchangeKeyEntry struct {
	APIKey     string `json:"APIKey"`
	Secret     string `json:"Secret"`
	Passphrase string `json:"Passphrase,omitempty"`
}

// BinanceKeyEntry Binance 使用 SecretKey
type BinanceKeyEntry struct {
	APIKey    string `json:"APIKey"`
	SecretKey string `json:"SecretKey"`
}

// TelegramEntry Telegram 配置
type TelegramEntry struct {
	BotToken           string  `json:"BotToken"`
	ChatID             string  `json:"ChatID"`
	BaselineInvestment float64 `json:"BaselineInvestment,omitempty"`
}

// HyperliquidEntry Hyperliquid 配置
type HyperliquidEntry struct {
	UserAddress   string `json:"UserAddress"`
	APIPrivateKey string `json:"APIPrivateKey"`
}

// LighterEntry Lighter 配置
type LighterEntry struct {
	APIKey       string `json:"APIKey"`
	Secret       string `json:"Secret"`
	AccountIndex int    `json:"AccountIndex,omitempty"`
	APIKeyIndex  int    `json:"APIKeyIndex,omitempty"`
}

var (
	exchangeKeys     *ExchangeKeysConfig
	exchangeKeysMu   sync.RWMutex
	exchangeKeysPath = "config/exchange_keys.json"
)

// LoadExchangeKeys 从 JSON 文件加载交易所密钥
// 路径优先：环境变量 EXCHANGE_KEYS_PATH > config/exchange_keys.json
func LoadExchangeKeys(path string) (*ExchangeKeysConfig, error) {
	if path == "" {
		path = exchangeKeysPath
	}
	if p := os.Getenv("EXCHANGE_KEYS_PATH"); p != "" {
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg ExchangeKeysConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	exchangeKeysMu.Lock()
	exchangeKeys = &cfg
	exchangeKeysMu.Unlock()
	return &cfg, nil
}

// GetExchangeKeys 获取已加载的交易所密钥（未加载返回 nil）
func GetExchangeKeys() *ExchangeKeysConfig {
	exchangeKeysMu.RLock()
	defer exchangeKeysMu.RUnlock()
	return exchangeKeys
}

// SetExchangeKeysFromJSON 从 JSON 字符串设置交易所密钥（仅内存，不落盘，避免泄露）
func SetExchangeKeysFromJSON(jsonStr string) error {
	var cfg ExchangeKeysConfig
	if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
		return err
	}
	exchangeKeysMu.Lock()
	exchangeKeys = &cfg
	exchangeKeysMu.Unlock()
	return nil
}

// TryLoadExchangeKeys 尝试加载，失败返回 nil 不报错
func TryLoadExchangeKeys() *ExchangeKeysConfig {
	paths := []string{"config/exchange_keys.json", "exchange_keys.json"}
	if p := os.Getenv("EXCHANGE_KEYS_PATH"); p != "" {
		paths = append([]string{p}, paths...)
	}
	for _, p := range paths {
		if cfg, err := LoadExchangeKeys(p); err == nil {
			return cfg
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		p := filepath.Join(dir, "config", "exchange_keys.json")
		if cfg, err := LoadExchangeKeys(p); err == nil {
			return cfg
		}
	}
	return nil
}

// BitgetKeys 获取 Bitget 密钥
func (c *ExchangeKeysConfig) BitgetKeys() (apiKey, secret, passphrase string) {
	if c == nil || c.BitGet == nil {
		return "", "", ""
	}
	return c.BitGet.APIKey, c.BitGet.Secret, c.BitGet.Passphrase
}

// BybitKeys 获取 Bybit 密钥
func (c *ExchangeKeysConfig) BybitKeys() (apiKey, secret string) {
	if c == nil || c.Bybit == nil {
		return "", ""
	}
	return c.Bybit.APIKey, c.Bybit.Secret
}

// GateKeys 获取 Gate 密钥
func (c *ExchangeKeysConfig) GateKeys() (apiKey, secret string) {
	if c == nil || c.Gate == nil {
		return "", ""
	}
	return c.Gate.APIKey, c.Gate.Secret
}

// BinanceKeys 获取 Binance 密钥
func (c *ExchangeKeysConfig) BinanceKeys() (apiKey, secretKey string) {
	if c == nil || c.Binance == nil {
		return "", ""
	}
	return c.Binance.APIKey, c.Binance.SecretKey
}

// OKXKeys 获取 OKX 密钥
func (c *ExchangeKeysConfig) OKXKeys() (apiKey, secret, passphrase string) {
	if c == nil || c.OKX == nil {
		return "", "", ""
	}
	return c.OKX.APIKey, c.OKX.Secret, c.OKX.Passphrase
}

// HasKeys 检查某交易所是否已配置密钥
func (c *ExchangeKeysConfig) HasKeys(exchange string) bool {
	if c == nil {
		return false
	}
	ex := strings.ToLower(strings.TrimSpace(exchange))
	switch ex {
	case "bitget":
		return c.BitGet != nil && c.BitGet.APIKey != "" && c.BitGet.Secret != ""
	case "bybit":
		return c.Bybit != nil && c.Bybit.APIKey != "" && c.Bybit.Secret != ""
	case "gate":
		return c.Gate != nil && c.Gate.APIKey != "" && c.Gate.Secret != ""
	case "binance":
		return c.Binance != nil && c.Binance.APIKey != "" && c.Binance.SecretKey != ""
	case "okx", "okex":
		return c.OKX != nil && c.OKX.APIKey != "" && c.OKX.Secret != ""
	}
	return false
}
