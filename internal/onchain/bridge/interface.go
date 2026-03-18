package bridge

import (
	"github.com/qw225967/auto-monitor/internal/model"
)

// BridgeProtocol 跨链协议接口
// 所有跨链协议实现（LayerZero、Wormhole等）都需要实现此接口
type BridgeProtocol interface {
	// GetName 获取协议名称（如 "layerzero", "wormhole"）
	GetName() string

	// BridgeToken 执行跨链转账
	// req: 跨链转账请求
	// 返回: 跨链转账响应，包含交易哈希、跨链ID等信息
	BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error)

	// GetBridgeStatus 查询跨链状态
	// bridgeID: 跨链ID（用于标识一次跨链转账）
	// fromChain: 源链ID
	// toChain: 目标链ID
	// 返回: 跨链状态，包含当前状态、交易哈希等信息
	GetBridgeStatus(bridgeID string, fromChain, toChain string) (*model.BridgeStatus, error)

	// GetQuote 获取跨链报价（费用、时间等）
	// req: 跨链报价请求
	// 返回: 协议报价，包含费用、预估时间、支持情况等
	GetQuote(req *model.BridgeQuoteRequest) (*model.ProtocolQuote, error)

	// IsChainSupported 检查是否支持该链
	// chainID: 链ID（如 "1" 表示 Ethereum, "56" 表示 BSC）
	// 返回: 是否支持
	IsChainSupported(chainID string) bool

	// IsChainPairSupported 检查是否支持该链对
	// fromChain: 源链ID
	// toChain: 目标链ID
	// 返回: 是否支持该链对之间的跨链转账
	IsChainPairSupported(fromChain, toChain string) bool

	// CheckBridgeReady 预检查跨链条件是否满足（如 OFT 合约是否已配置、RPC 是否可用等）
	// 在 Pipeline 执行前调用，提前发现问题避免执行到中途才失败。
	// fromChain: 源链ID
	// toChain: 目标链ID
	// tokenSymbol: 代币符号（如 "ZAMA"）
	// 返回: nil 表示就绪，非 nil 表示缺少条件（错误信息应包含修复指引）
	CheckBridgeReady(fromChain, toChain, tokenSymbol string) error
}

// TokenDiscoverer 可选接口：桥协议自动发现 token 合约地址。
// knownAddresses: 已知的 chainID→ERC20 合约地址（来自 WalletInfo/token_mapping 等）。
// targetChainIDs: 需要在哪些链上查找。
// 返回: 新发现并已注册的 chainID→合约地址（仅包含本次新增的）。
type TokenDiscoverer interface {
	DiscoverToken(symbol string, knownAddresses map[string]string, targetChainIDs []string) (map[string]string, error)
}
