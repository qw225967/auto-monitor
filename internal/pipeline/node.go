package pipeline

import (
	"errors"

	"auto-arbitrage/internal/model"
)

// NodeType 表示节点类型（仅资产持有位置：交易所 / 链上；跨链为边上的行为）
type NodeType string

const (
	// NodeTypeExchange 交易所节点（Binance / Bybit / Bitget / Gate 等）
	NodeTypeExchange NodeType = "exchange"
	// NodeTypeOnchain 链上节点（BSC / ETH / Arbitrum 等）；跨链由边触发，不作为节点类型
	NodeTypeOnchain NodeType = "onchain"
)

// 通用错误
var (
	// ErrNotSupported 表示该节点不支持某个操作（如部分交易所不支持充提）
	ErrNotSupported = errors.New("operation not supported")
)

// Node 表示自动充提 Pipeline 中的一个节点（仅资产持有位置；跨链由边触发，不由节点实现）
type Node interface {
	// 基本信息

	// GetID 返回节点唯一 ID（用于在 Pipeline 内部引用）
	GetID() string
	// GetName 返回节点的人类可读名称（如 "binance-spot-USDT"）
	GetName() string
	// GetType 返回节点类型
	GetType() NodeType
	// GetAsset 返回该节点关注的资产符号（如 "USDT"、"BTC"）
	GetAsset() string

	// 余额检查

	// CheckBalance 检查当前节点是否持有目标资产，返回持有数量
	CheckBalance() (float64, error)
	// GetAvailableBalance 返回可用于转出的余额（通常为可用余额）
	GetAvailableBalance() (float64, error)

	// 充币相关（从上一个节点流入到本节点）

	// CanDeposit 表示该节点是否支持“接收”资产（充币）
	CanDeposit() bool
	// GetDepositAddress 按指定资产(symbol)向本节点查询充币地址。
	// asset: 本步要充入的资产符号，交易所节点会用它调用交易所 API 查询该币种的充币地址；为空时交易所使用节点配置资产。
	// network: 充币网络（如 ERC20、BEP20、ARBITRUM）；链上节点可忽略。
	// 对于链上节点，返回配置的钱包地址（与 asset 无关）。
	GetDepositAddress(asset, network string) (*model.DepositAddress, error)
	// CheckDepositStatus 检查某次充币是否已到账（基于 txHash 或其他标识）
	// 返回值：
	//   - bool: 是否已确认到账
	//   - error: 查询过程中出现的错误
	CheckDepositStatus(txHash string) (bool, error)

	// 提币相关（从本节点流出到下一个节点）

	// CanWithdraw 表示该节点是否支持“转出”资产（提币）
	CanWithdraw() bool
	// Withdraw 从本节点向给定地址提币指定数量资产
	// 参数：
	//   - amount: 提币数量
	//   - toAddress: 目标地址
	//   - network: 使用的网络（如 ERC20 / TRC20 / BEP20 等）
	//   - memo: 备注（某些链或交易所需要，如 XRP Tag）
	Withdraw(amount float64, toAddress string, network string, memo string) (*model.WithdrawResponse, error)
	// CheckWithdrawStatus 检查某次提币是否已完成
	// 注意：具体实现可通过交易所的提币历史或链上交易状态查询。
	CheckWithdrawStatus(withdrawID string) (bool, error)
}

