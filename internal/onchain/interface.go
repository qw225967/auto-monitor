package onchain

import (
	"errors"

	"github.com/qw225967/auto-monitor/internal/model"
)

// SwapDirection 交易方向
type SwapDirection string

const (
	SwapDirectionBuy  SwapDirection = "buy"  // 买入：USDT -> Coin
	SwapDirectionSell SwapDirection = "sell" // 卖出：Coin -> USDT
)

// OnchainClient 定义了链上操作的核心接口
// 所有链上实现（OKEx DEX、RPC等）都需要实现此接口
type OnchainClient interface {
	// 连接管理
	Init() error // 初始化连接（RPC、API等）

	// 价格数据回调
	SetPriceCallback(callback PriceCallback) // 设置价格数据回调函数

	// 根据币对开始循环交易
	// 注意：内部创建单独请求线程，循环swap并定期上抛请求结果和swap的hx块
	// swapInfo: 代币信息，包括 FromTokenSymbol, ToTokenSymbol, ChainIndex, Amount, DecimalsFrom, DecimalsTo, SwapMode, Slippage, GasLimit, WalletAddress
	StartSwap(swapInfo *model.SwapInfo)

	// StopSwap 停止链上询价循环，trigger 删除时调用以便完全回收
	StopSwap()

	// 广播交易（使用最新缓存的 Tx 数据）
	// direction: 交易方向，buy 表示买入（USDT -> Coin），sell 表示卖出（Coin -> USDT）
	BroadcastSwapTx(direction SwapDirection) (string, error)

	// 查询交易结果（是否完成）
	GetTxResult(txHash, chainIndex string) (model.TradeResult, error)

	// 账户信息
	GetBalance() (*model.TokenBalance, error)                                                          // 查询链上代币余额（单个代币）
	GetAllTokenBalances(address, chains string, excludeRiskToken bool) ([]model.OkexTokenAsset, error) // 查询地址持有的多个链或指定链的代币余额列表

	// 滑点查询
	GetLatestSwapTx() interface{} // 获取最新的 Swap 交易数据（用于滑点计算）
	GetSwapInfo() *model.SwapInfo // 获取当前的 Swap 信息（用于滑点计算）

	// 更新 Swap 信息
	UpdateSwapInfoAmount(amount string)                     // 更新 Swap 信息中的 Amount 字段（用于动态调整询价金额）
	UpdateSwapInfoDecimals(decimalsFrom, decimalsTo string) // 更新 Swap 信息中的 Decimals 字段（用于自动纠正精度）
	UpdateSwapInfoSlippage(slippage string)                 // 更新 Swap 信息中的 Slippage 字段（用于动态调整滑点）

	// Nonce 管理
	ResetNonce(walletAddress, chainIndex string) // 重置 nonce 缓存（交易失败或超时时调用）

	// Gas 配置
	SetGasMultiplier(multiplier float64) // 设置 gas 乘数（如 1.5 表示增加 50%）
	GetGasMultiplier() float64           // 获取 gas 乘数

	// SwapInfo 管理
	SetSwapInfo(swapInfo *model.SwapInfo)    // 设置 Swap 信息
	UpdateSwapInfoGasLimit(gasLimit string)  // 更新 Swap 信息中的 GasLimit 字段

	// 跨链兑换
	// BridgeToken 跨链转账
	// req: 跨链转账请求，包含源链、目标链、代币、数量等信息
	BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error)
	
	// GetBridgeStatus 查询跨链状态
	// txHash: 源链交易哈希
	// fromChain: 源链ID
	// toChain: 目标链ID
	GetBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error)
	
	// GetBridgeQuote 获取跨链报价（费用、时间等）
	// req: 跨链报价请求
	GetBridgeQuote(req *model.BridgeQuoteRequest) (*model.BridgeQuote, error)
}

// PriceCallback 价格数据回调函数类型
// price: 价格信息（包含买价、卖价等）
type PriceCallback func(price *model.ChainPriceInfo)

// 错误定义
var (
	ErrNotInitialized      = errors.New("onchain client not initialized")
	ErrInvalidToken        = errors.New("invalid token address or symbol")
	ErrInvalidAmount       = errors.New("invalid amount")
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrTransactionFailed   = errors.New("transaction failed")
	ErrTimeout             = errors.New("operation timeout")
	ErrBridgeNotSupported  = errors.New("bridge not supported for this chain pair")
)
