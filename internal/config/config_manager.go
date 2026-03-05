package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"auto-arbitrage/internal/model"
)

var (
	// globalConfig 全局配置实例
	globalConfig *SelfConfig
	// setConfigMutex 保护 SetSelfConfigWeb 方法的互斥锁
	setConfigMutex sync.Mutex
)

// SelfConfig 统一管理所有配置常量
type SelfConfig struct {
	// MyProjectId 项目ID
	MyProjectId string

	// OkEx OKEx API配置
	OkEx OkExConfig

	// BitGet BitGet API配置
	BitGet BitGetConfig

	// Bybit Bybit API配置
	Bybit BybitConfig

	// Gate Gate.io API配置
	Gate GateConfig

	// Binance Binance API配置
	Binance BinanceConfig

	// OKX OKX 交易所配置
	OKX OKXConfig

	// Hyperliquid Hyperliquid DEX 配置
	Hyperliquid HyperliquidConfig

	// Lighter Lighter DEX 配置
	Lighter LighterConfig

	// Aster Aster DEX 配置
	Aster AsterConfig

	// Telegram Telegram Bot 配置
	Telegram TelegramConfig

	// Bundler Bundler 配置
	Bundler BundlerConfig

	// Arbitrage 套利系统配置
	Arbitrage ArbitrageConfig

	// Wallet 钱包配置
	Wallet WalletConfig

	// Onchain 链上交易配置
	Onchain OnchainConfig

	// Bridge 跨链配置
	Bridge BridgeConfig

	// Automation 自动化配置
	Automation model.AutomationConfig
}

type SelfConfigWeb struct {
	// BitGet BitGet API配置
	BitGet BitGetConfig

	// Bybit Bybit API配置
	Bybit BybitConfig

	// Gate Gate.io API配置
	Gate GateConfig

	// Binance Binance API配置
	Binance BinanceConfig

	// OKX OKX 交易所配置
	OKX OKXConfig

	// Telegram Telegram Bot 配置
	Telegram TelegramConfig

	// Wallet 钱包配置
	Wallet WalletConfig

	// Hyperliquid Hyperliquid DEX 配置
	Hyperliquid HyperliquidConfig

	// Lighter Lighter DEX 配置
	Lighter LighterConfig

	// Aster Aster DEX 配置
	Aster AsterConfig

	// Onchain 链上交易配置
	Onchain OnchainConfig

	// Bridge 跨链配置
	Bridge BridgeConfig

	// Arbitrage 套利系统配置
	Arbitrage ArbitrageConfig

	// Automation 自动化配置
	Automation model.AutomationConfig

	// MyProjectId 项目ID
	MyProjectId string
}

// OkExConfig OKEx 交易所配置（支持多个密钥以提高并发量）
type OkExConfig struct {
	// KeyList 多个 OKEx API Key 列表（用于 swap 操作，提高并发量）
	// 每个密钥包含 APIKey, Secret, Passphrase 和 CanBroadcast 标志
	KeyList []OkExKeyRecord
}

// OkExKeyRecord OKEx 单个密钥配置
type OkExKeyRecord struct {
	APIKey       string // OKEx API Key
	Secret       string // OKEx Secret Key
	Passphrase   string // OKEx Passphrase
	CanBroadcast bool   // 是否可以用于广播交易
}

// BitGetConfig BitGet 交易所配置
type BitGetConfig struct {
	APIKey     string // BitGet API Key
	Secret     string // BitGet Secret Key
	Passphrase string // BitGet Passphrase
}

// BybitConfig Bybit 交易所配置
type BybitConfig struct {
	APIKey string // Bybit API Key
	Secret string // Bybit Secret Key
}

// GateConfig Gate.io 交易所配置
type GateConfig struct {
	APIKey string // Gate.io API Key
	Secret string // Gate.io Secret Key
}

// BinanceConfig Binance 交易所配置
type BinanceConfig struct {
	APIKey    string // Binance API Key
	SecretKey string // Binance Secret Key
}

// OKXConfig OKX 交易所配置（独立于旧的 OkEx swap 配置）
type OKXConfig struct {
	APIKey     string // OKX API Key
	Secret     string // OKX Secret Key
	Passphrase string // OKX Passphrase
}

// HyperliquidConfig Hyperliquid DEX 配置
type HyperliquidConfig struct {
	UserAddress   string // 用户的主钱包地址
	APIPrivateKey string // API 钱包私钥（地址会从私钥派生）
}

// LighterConfig Lighter DEX 配置
type LighterConfig struct {
	APIKey       string // Lighter API Key
	Secret       string // Lighter API Secret
	AccountIndex int64  // 账户索引（默认 1，主账户）
	APIKeyIndex  uint8  // API Key 索引（默认 255，使用默认）
}

// AsterConfig Aster DEX 配置
type AsterConfig struct {
	APIKey string // Aster API Key
	Secret string // Aster API Secret
}

// TelegramConfig Telegram Bot 配置
type TelegramConfig struct {
	BotToken          string  // Telegram Bot Token（从 @BotFather 获取，格式类似: "123456789:ABCdefGHIjklMNOpqrsTUVwxyz"）
	ChatID            string  // Telegram Chat ID（接收消息的 Chat ID，可以是用户ID或群组ID）
	BaselineInvestment float64 // 基准总投入（USDT），用于计算收益/亏损；为 0 时使用 7 日历史首值
}

// BundlerConfig Bundler 配置
type BundlerConfig struct {
	FlashbotsPrivateKey           string // Flashbots 私钥（用于签名请求，不是交易签名私钥）
	FortyEightClubAPIKey          string // 48club API Key
	FortyEightSoulPointPrivateKey string // 48SoulPoint 成员私钥（用于 48club bundler 签名，获得更好的服务）
	UseBundler                    bool   // 是否启用 bundler
}

// ArbitrageConfig 套利系统配置
type ArbitrageConfig struct {
	DefaultTargetThresholdInterval float64 // 默认目标价差阈值区间（表示 AB 阈值和 BA 阈值之和的目标值，用于最优阈值计算）
}

// WalletConfig 钱包配置
type WalletConfig struct {
	WalletAddress string // 钱包地址
	PrivateSecret string // 钱包私钥（用于签名交易），注意：实际使用时应该从环境变量或安全存储中获取，不要硬编码
}

// OnchainConfig 链上交易配置
type OnchainConfig struct {
	GasMultiplier   float64 // Gas 乘数，用于调整 API 返回的 gas（如 1.5 表示增加 50%），默认 1.0
	DefaultGasLimit string  // 默认 Gas 限制，传给 OKEx API 的请求参数，默认 "500000"
}

// BridgeConfig 跨链配置
type BridgeConfig struct {
	LayerZero  LayerZeroConfig // LayerZero 配置
	Wormhole   WormholeConfig  // Wormhole 配置
	CCIP       CCIPConfig      // CCIP 配置（可选）
	AutoSelect bool            // 是否自动选择最优协议（默认 true）
}

// CCIPConfig CCIP 跨链协议配置
type CCIPConfig struct {
	TokenPools map[string]string // 手动配置 Token Pool：key 为 "chainID:symbol"（如 "56:ZAMA"），value 为该链上 CCIP Token Pool 合约地址
	RPCURLs    map[string]string // 各链的 RPC URL，key 为链ID（如 "1" 表示 Ethereum）。优先于 LayerZero.RPCURLs，用于 CCIP 发送交易（建议配置 Alchemy/Infura 等可靠 RPC）
}

// LayerZeroConfig LayerZero 跨链协议配置
type LayerZeroConfig struct {
	Enabled      bool              // 是否启用 LayerZero（默认 true）
	RPCURLs      map[string]string // 各链的 RPC URL，key 为链ID（如 "1" 表示 Ethereum, "56" 表示 BSC）
	OFTContracts map[string]string // 手动配置 OFT 合约：key 为 "chainID:symbol"（如 "56:ZAMA"），value 为该链上 OFT 合约地址（LayerZero API 未收录时可在此配置）
}

// WormholeConfig Wormhole 跨链协议配置
type WormholeConfig struct {
	Enabled        bool              // 是否启用 Wormhole（默认 true）
	RPCURLs        map[string]string // 各链的 RPC URL，key 为链ID（如 "1" 表示 Ethereum, "56" 表示 BSC）
	TokenContracts map[string]string // 手动配置代币合约：key 为 "chainID:symbol"（如 "1:USDC"），value 为该链上代币合约地址
}

const SkEncrypted = "******"

// getConfigPath 返回配置文件路径：优先环境变量 CONFIG_PATH，否则 configs/config.json
func getConfigPath() string {
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}
	return "configs/config.json"
}

// buildDefaultFromConstants 构建默认配置，作为配置文件缺失时的兜底
// 所有密钥字段默认值为 "请添加" 或空字符串，不再依赖硬编码常量
func buildDefaultFromConstants() *SelfConfig {
	return &SelfConfig{
		MyProjectId: "请添加",
		OkEx: OkExConfig{
			KeyList: []OkExKeyRecord{}, // 默认空列表，需要从配置文件添加
		},
		BitGet: BitGetConfig{
			APIKey:     "请添加",
			Secret:     "请添加",
			Passphrase: "请添加",
		},
		Bybit: BybitConfig{
			APIKey: "请添加",
			Secret: "请添加",
		},
		Gate: GateConfig{
			APIKey: "请添加",
			Secret: "请添加",
		},
		Binance: BinanceConfig{
			APIKey:    "请添加",
			SecretKey: "请添加",
		},
		OKX: OKXConfig{
			APIKey:     "请添加",
			Secret:     "请添加",
			Passphrase: "请添加",
		},
		Hyperliquid: HyperliquidConfig{
			UserAddress:   "请添加",
			APIPrivateKey: "请添加",
		},
		Lighter: LighterConfig{
			APIKey:       "请添加",
			Secret:       "请添加",
			AccountIndex: 1,
			APIKeyIndex:  255,
		},
		Aster: AsterConfig{
			APIKey: "请添加",
			Secret: "请添加",
		},
		Telegram: TelegramConfig{
			BotToken:         "请添加",
			ChatID:           "请添加",
			BaselineInvestment: 10000,
		},
		Bundler: BundlerConfig{
			FlashbotsPrivateKey:           "",
			FortyEightClubAPIKey:          "",
			FortyEightSoulPointPrivateKey: "",
			UseBundler:                    false,
		},
		Arbitrage: ArbitrageConfig{
			DefaultTargetThresholdInterval: 0.5,
		},
		Wallet: WalletConfig{
			PrivateSecret: "请添加",
			WalletAddress: "请添加",
		},
		Onchain: OnchainConfig{
			GasMultiplier:   1.0,
			DefaultGasLimit: "500000",
		},
		Bridge: BridgeConfig{
			LayerZero:  LayerZeroConfig{Enabled: true, RPCURLs: nil, OFTContracts: nil},
			Wormhole:   WormholeConfig{Enabled: true, RPCURLs: nil, TokenContracts: nil},
			CCIP:       CCIPConfig{TokenPools: nil},
			AutoSelect: true,
		},
		Automation: model.AutomationConfig{
			Enabled:            false,
			PollInterval:       30 * time.Second,
			ProfitThreshold:    0.3,
			APIEndpoints:       []model.APIEndpointConfig{},
			AllowedTraderTypes: []string{},
		},
	}
}

// loadFromFile 从 path 加载配置：若文件存在则用其覆盖 default 的对应字段后返回；若不存在则直接返回 default。错误仅在最存在但解析失败时返回。
func loadFromFile(path string) (*SelfConfig, error) {
	cfg := buildDefaultFromConstants()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	defer f.Close()

	var m map[string]json.RawMessage
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode config file %s: %w", path, err)
	}
	for k, raw := range m {
		switch k {
		case "MyProjectId":
			_ = json.Unmarshal(raw, &cfg.MyProjectId)
		case "OkEx":
			_ = json.Unmarshal(raw, &cfg.OkEx)
		case "BitGet":
			_ = json.Unmarshal(raw, &cfg.BitGet)
		case "Bybit":
			_ = json.Unmarshal(raw, &cfg.Bybit)
		case "Gate":
			_ = json.Unmarshal(raw, &cfg.Gate)
		case "Binance":
			_ = json.Unmarshal(raw, &cfg.Binance)
		case "OKX":
			_ = json.Unmarshal(raw, &cfg.OKX)
		case "Hyperliquid":
			_ = json.Unmarshal(raw, &cfg.Hyperliquid)
		case "Lighter":
			_ = json.Unmarshal(raw, &cfg.Lighter)
		case "Aster":
			_ = json.Unmarshal(raw, &cfg.Aster)
		case "Telegram":
			_ = json.Unmarshal(raw, &cfg.Telegram)
			if cfg.Telegram.BaselineInvestment == 0 {
				cfg.Telegram.BaselineInvestment = 10000
			}
		case "Bundler":
			_ = json.Unmarshal(raw, &cfg.Bundler)
		case "Arbitrage":
			_ = json.Unmarshal(raw, &cfg.Arbitrage)
		case "Wallet":
			_ = json.Unmarshal(raw, &cfg.Wallet)
		case "Onchain":
			_ = json.Unmarshal(raw, &cfg.Onchain)
		case "Bridge":
			_ = json.Unmarshal(raw, &cfg.Bridge)
		case "Automation":
			_ = json.Unmarshal(raw, &cfg.Automation)
		}
	}
	return cfg, nil
}

// InitSelfConfigFromDefault 初始化配置：优先从 configs/config.json（或 CONFIG_PATH）加载，缺失或解析失败时退化为 config 包内常量（config.go）
func InitSelfConfigFromDefault() error {
	path := getConfigPath()
	cfg, err := loadFromFile(path)
	if err != nil {
		return err
	}
	globalConfig = cfg
	return nil
}

// GetGlobalConfig 获取全局配置实例
func GetGlobalConfig() *SelfConfig {
	return globalConfig
}

// GetSelfConfigWeb 获取全局配置实例（包含所有配置项，不包含 Bundler 和 OkEx）
func GetSelfConfigWeb() *SelfConfigWeb {
	return &SelfConfigWeb{
		BitGet:      globalConfig.BitGet,
		Bybit:       globalConfig.Bybit,
		Gate:        globalConfig.Gate,
		Binance:     globalConfig.Binance,
		OKX:         globalConfig.OKX,
		Telegram:    globalConfig.Telegram,
		Wallet:      globalConfig.Wallet,
		Hyperliquid: globalConfig.Hyperliquid,
		Lighter:     globalConfig.Lighter,
		Aster:       globalConfig.Aster,
		Onchain:     globalConfig.Onchain,
		Arbitrage:   globalConfig.Arbitrage,
		Automation:  globalConfig.Automation,
		MyProjectId: globalConfig.MyProjectId,
	}
}

// GetSelfConfigWebEncrypted 获取全局配置实例-sk加密（包含所有配置项，不包含 Bundler 和 OkEx）
func GetSelfConfigWebEncrypted() *SelfConfigWeb {
	cfg := &SelfConfigWeb{
		BitGet:      globalConfig.BitGet,
		Bybit:       globalConfig.Bybit,
		Gate:        globalConfig.Gate,
		Binance:     globalConfig.Binance,
		OKX:         globalConfig.OKX,
		Telegram:    globalConfig.Telegram,
		Wallet:      globalConfig.Wallet,
		Hyperliquid: globalConfig.Hyperliquid,
		Lighter:     globalConfig.Lighter,
		Aster:       globalConfig.Aster,
		Onchain:     globalConfig.Onchain,
		Arbitrage:   globalConfig.Arbitrage,
		Automation:  globalConfig.Automation,
		MyProjectId: globalConfig.MyProjectId,
	}
	sc := SkEncrypted

	// 加密所有敏感字段
	if cfg.BitGet.Secret != "" {
		cfg.BitGet.Secret = sc
	}
	if cfg.BitGet.Passphrase != "" {
		cfg.BitGet.Passphrase = sc
	}
	if cfg.Bybit.Secret != "" {
		cfg.Bybit.Secret = sc
	}
	if cfg.Gate.Secret != "" {
		cfg.Gate.Secret = sc
	}
	if cfg.Binance.SecretKey != "" {
		cfg.Binance.SecretKey = sc
	}
	// OKX 敏感字段
	if cfg.OKX.Secret != "" {
		cfg.OKX.Secret = sc
	}
	if cfg.OKX.Passphrase != "" {
		cfg.OKX.Passphrase = sc
	}
	if cfg.Wallet.PrivateSecret != "" {
		cfg.Wallet.PrivateSecret = sc
	}
	if cfg.Telegram.BotToken != "" {
		cfg.Telegram.BotToken = sc
	}
	// Hyperliquid 敏感字段
	if cfg.Hyperliquid.APIPrivateKey != "" {
		cfg.Hyperliquid.APIPrivateKey = sc
	}
	// Lighter 敏感字段
	if cfg.Lighter.Secret != "" {
		cfg.Lighter.Secret = sc
	}
	// Aster 敏感字段
	if cfg.Aster.Secret != "" {
		cfg.Aster.Secret = sc
	}

	return cfg
}

// SetSelfConfigWeb 从web传入 JSON 字符串设置全局配置（支持所有配置项，不包含 Bundler）
func SetSelfConfigWeb(jsonStr string) error {
	setConfigMutex.Lock()
	defer setConfigMutex.Unlock()

	cfg := &SelfConfigWeb{}
	if err := json.Unmarshal([]byte(jsonStr), cfg); err != nil {
		return fmt.Errorf("failed to unmarshal config JSON: %w", err)
	}

	// 更新 BitGet 配置
	if cfg.BitGet.Secret != SkEncrypted && cfg.BitGet.Passphrase != SkEncrypted {
		globalConfig.BitGet = cfg.BitGet
	}

	// 更新 Bybit 配置
	if cfg.Bybit.Secret != SkEncrypted {
		globalConfig.Bybit = cfg.Bybit
	}

	// 更新 Gate 配置
	if cfg.Gate.Secret != SkEncrypted {
		globalConfig.Gate = cfg.Gate
	}

	// 更新 Binance 配置
	if cfg.Binance.SecretKey != SkEncrypted {
		globalConfig.Binance = cfg.Binance
	}

	// 更新 OKX 配置
	if cfg.OKX.Secret != SkEncrypted && cfg.OKX.Passphrase != SkEncrypted {
		globalConfig.OKX = cfg.OKX
	}

	// 更新 Telegram 配置（如果 BotToken 不是加密标记）
	if cfg.Telegram.BotToken != SkEncrypted && cfg.Telegram.BotToken != "" {
		globalConfig.Telegram = cfg.Telegram
		if globalConfig.Telegram.BaselineInvestment == 0 {
			globalConfig.Telegram.BaselineInvestment = 10000
		}
	}

	// 更新 Wallet 配置
	if cfg.Wallet.PrivateSecret != SkEncrypted {
		globalConfig.Wallet = cfg.Wallet
	}

	// OkEx 配置不在 Web API 中更新，只能通过配置文件修改

	// 更新 Hyperliquid 配置
	if cfg.Hyperliquid.APIPrivateKey != SkEncrypted {
		globalConfig.Hyperliquid = cfg.Hyperliquid
	}

	// 更新 Lighter 配置
	if cfg.Lighter.Secret != SkEncrypted {
		globalConfig.Lighter = cfg.Lighter
	}

	// 更新 Aster 配置
	if cfg.Aster.Secret != SkEncrypted {
		globalConfig.Aster = cfg.Aster
	}

	// 更新 Onchain 配置
	globalConfig.Onchain = cfg.Onchain

	// 更新 Arbitrage 配置
	globalConfig.Arbitrage = cfg.Arbitrage

	// 更新 Automation 配置
	globalConfig.Automation = cfg.Automation

	// 更新 MyProjectId
	if cfg.MyProjectId != "" {
		globalConfig.MyProjectId = cfg.MyProjectId
	}

	return nil
}
