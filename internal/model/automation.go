package model

import "time"

// ArbitrageOpportunity 套利机会
type ArbitrageOpportunity struct {
	Symbol      string  // 交易对符号（如 BTCUSDT）
	TraderAType string  // A类型（如 "onchain:56"）
	TraderBType string  // B类型（如 "binance:futures"）
	Profit      float64 // 预期利润
	Status      string  // 状态（如 "active", "closed"）
	UpdatedAt   int64   // 更新时间戳
}

// APIEndpointConfig 单个API端点配置
type APIEndpointConfig struct {
	Type    string // 类型标识（如 "chain-chain", "chain-cex:spot", "chain-cex:futures"）
	URL     string // API URL
	APIKey  string // API密钥（如果需要）
	Enabled bool   // 是否启用此端点
}

// AutomationConfig 自动化配置
type AutomationConfig struct {
	Enabled            bool                // 是否启用
	PollInterval       time.Duration       // 拉取频率（默认30秒）
	ProfitThreshold    float64             // 利润阈值（默认0.3）
	APIEndpoints       []APIEndpointConfig // 多个API端点配置
	AllowedTraderTypes []string            // 允许创建的交易对类型白名单（如 ["binance", "gate", "onchain"]）
}
