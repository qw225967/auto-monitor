package pipeline

import "time"

// AmountType 表示边上配置的金额类型
type AmountType string

const (
	// AmountTypeFixed 固定数量
	AmountTypeFixed AmountType = "fixed"
	// AmountTypePercentage 按百分比（0-1）计算
	AmountTypePercentage AmountType = "percentage"
	// AmountTypeAll 表示转出全部可用余额
	AmountTypeAll AmountType = "all"
)

// EdgeConfig 描述两个节点之间一次“转移”的配置
//
// 根据你的偏好：
// - 网络 / 跨链协议等更适合在创建节点时一并配置；
// - EdgeConfig 主要负责“这一步转多少”“等待策略”等。
//
// 为了兼容最初的计划，这里仍然保留 Network / BridgeProtocol 字段，
// 但在具体实现中会优先使用节点上的默认配置，Edge 上的配置只作为覆盖项。
type EdgeConfig struct {
	FromNodeID string // 源节点ID
	ToNodeID   string // 目标节点ID

	// 金额配置
	AmountType  AmountType // 固定/百分比/全部
	Amount      float64    // 固定金额或百分比（0-1）
	PositionSize float64   // 总仓位大小（用于基于仓位的百分比计算，当 AmountType=AmountTypePercentage 且 PositionSize>0 时使用）

	// 资产与链（可选）：本条边使用的资产与链，执行时优先于节点默认值
	Asset   string // 本条边使用的资产（如 USDT）；空则用源节点 GetAsset()
	ChainID string // 本条边使用的链（用于多链选择时与 Network 对应）

	// 网络配置（可选覆盖）
	// 通常由目标节点/跨链节点自身的配置决定，这里只作为覆盖配置存在。
	Network string // 网络（如 BEP20, ERC20, TRC20）
	Memo    string // 备注（某些链需要）

	// 跨链配置（可选覆盖）
	BridgeProtocol string // 跨链协议（"layerzero", "wormhole", "auto"）

	// 等待配置
	MaxWaitTime   time.Duration // 最大等待时间
	CheckInterval time.Duration // 状态检查间隔
	Confirmations int           // 所需确认数（链上）
}

