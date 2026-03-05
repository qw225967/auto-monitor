package proto

import (
	"context"
	"time"
)

// TriggerManager 定义 Trigger 管理器的接口
// 这个接口定义了 TriggerManager 对外提供的所有方法
type TriggerManager interface {
	// GetTrigger 根据 symbol 获取 trigger
	GetTrigger(symbol string) (Trigger, error)

	// GetAllTriggers 获取所有 trigger
	GetAllTriggers() []Trigger

	// AddTrigger 添加新的 trigger
	AddTrigger(symbol string, trigger Trigger) error

	// RemoveTrigger 移除指定的 trigger
	RemoveTrigger(symbol string) error

	// NewTrigger 创建新的 trigger 实例
	// sourceA: 通常为 nil
	// sourceB: 交易所实例
	// mode: 触发模式，0 表示 ModeInstant（即时触发），1 表示 ModeScheduled（定时触发）
	NewTrigger(symbol string, sourceA, sourceB interface{}, mode int) Trigger

	// GetExchangeSource 获取交易所实例
	GetExchangeSource(exchangeType interface{}) interface{}

	// GetTriggerContext 获取 trigger 上下文（用于启动 trigger）
	GetTriggerContext() context.Context
}

// Trigger 定义单个 Trigger 的接口
// 这个接口定义了 Trigger 对外提供的所有方法
type Trigger interface {
	// GetID 获取 Trigger ID
	GetID() uint64

	// GetSymbol 获取交易对符号
	GetSymbol() string

	// GetStatus 获取 trigger 的运行状态（"running" 或 "stopped"）
	GetStatus() string

	// GetDirectionEnabled 获取方向的订单执行启用状态
	// direction: 0 表示 DirectionAB (+A-B), 1 表示 DirectionBA (-A+B)
	GetDirectionEnabled(direction int) bool

	// SetDirectionEnabled 设置方向的订单执行启用状态
	SetDirectionEnabled(direction int, enabled bool)

	// GetTargetThresholdInterval 获取目标价差阈值区间（向后兼容，返回 minThreshold）
	// Deprecated: 请使用 GetThresholdRange
	GetTargetThresholdInterval() float64

	// SetTargetThresholdInterval 设置目标价差阈值区间（向后兼容，设置 minThreshold）
	// Deprecated: 请使用 SetThresholdRange
	SetTargetThresholdInterval(interval float64) error

	// GetMinThreshold 获取最小阈值
	GetMinThreshold() float64

	// SetMinThreshold 设置最小阈值
	SetMinThreshold(minThreshold float64) error

	// GetMaxThreshold 获取最大阈值
	GetMaxThreshold() float64

	// SetMaxThreshold 设置最大阈值
	SetMaxThreshold(maxThreshold float64) error

	// GetOptimalThresholds 获取最优阈值区间
	// 返回包含 thresholdAB, thresholdBA, abTradeCount, baTradeCount, totalTrades 的 map
	GetOptimalThresholds() map[string]interface{}

	// GetSlippageData 获取滑点计算结果
	// 返回包含 exchangeBuy, exchangeSell, onchainBuy, onchainSell 的 map
	GetSlippageData() map[string]interface{}

	// GetTelegramNotificationEnabled 获取 Telegram 通知启用状态
	GetTelegramNotificationEnabled() bool

	// SetTelegramNotificationEnabled 设置 Telegram 通知启用状态
	SetTelegramNotificationEnabled(enabled bool)

	// Start 启动 trigger
	Start(ctx context.Context) error

	// Stop 停止 trigger
	Stop() error

	// ClearPriceDiffs 清空历史价差数据
	ClearPriceDiffs() error

	// IsBundlerEnabled 检查是否启用了 Bundler
	IsBundlerEnabled() bool

	// EnableBundler 启用 Bundler
	EnableBundler() error

	// DisableBundler 禁用 Bundler
	DisableBundler() error

	// GetOnChainSlippage 获取链上滑点配置
	GetOnChainSlippage() string

	// SetOnChainSlippage 设置链上滑点配置
	SetOnChainSlippage(slippage string) error

	// GetGasMultiplier 获取链上 gas 乘数配置
	GetGasMultiplier() float64

	// SetGasMultiplier 设置链上 gas 乘数配置
	SetGasMultiplier(multiplier float64) error

	// GetOnChainGasLimit 获取链上 GasLimit 配置
	GetOnChainGasLimit() string

	// SetOnChainGasLimit 设置链上 GasLimit 配置
	SetOnChainGasLimit(gasLimit string) error

	// GetChainId 获取链ID
	GetChainId() string

	// GetExchangeType 获取交易所类型
	GetExchangeType() string

	// GetTraderAType 获取 A 的类型（如 "onchain:56"）
	GetTraderAType() string

	// GetTraderBType 获取 B 的类型（如 "binance:futures"）
	GetTraderBType() string

	// ==================== 快速触发优化器参数 ====================

	// GetFastTriggerConfig 获取快速触发优化器配置
	GetFastTriggerConfig() map[string]interface{}

	// GetFastTriggerSpeedWeight 获取快速触发速度权重
	GetFastTriggerSpeedWeight() float64

	// SetFastTriggerSpeedWeight 设置快速触发速度权重
	SetFastTriggerSpeedWeight(weight float64) error

	// GetFastTriggerQuantileLevel 获取快速触发分位数水平
	GetFastTriggerQuantileLevel() float64

	// SetFastTriggerQuantileLevel 设置快速触发分位数水平
	SetFastTriggerQuantileLevel(level float64) error

	// GetFastTriggerMaxAcceptableDelay 获取快速触发最大可接受延迟（毫秒）
	GetFastTriggerMaxAcceptableDelay() int64

	// SetFastTriggerMaxAcceptableDelay 设置快速触发最大可接受延迟（毫秒）
	SetFastTriggerMaxAcceptableDelay(delayMs int64) error

	// GetFastTriggerMinValidTriggers 获取快速触发最小有效触发次数
	GetFastTriggerMinValidTriggers() int

	// SetFastTriggerMinValidTriggers 设置快速触发最小有效触发次数
	SetFastTriggerMinValidTriggers(count int) error

	// SetCleanupPriceDiffsInterval 设置清理价差数据间隔
	SetCleanupPriceDiffsInterval(interval time.Duration) error

	// GetCleanupPriceDiffsInterval 获取清理价差数据间隔
	GetCleanupPriceDiffsInterval() time.Duration
}


