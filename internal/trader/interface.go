package trader

import (
	"time"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
)

// Trader 统一的交易者接口
// 所有交易所（CEX/DEX）和链上客户端都通过此接口统一抽象
type Trader interface {
	// 核心功能
	// Subscribe 订阅价格数据
	// 对于交易所：统一订阅到交易所实例
	// 对于链上：每个实例独立调用 StartSwap
	// marketType: 市场类型（"spot" 或 "futures"），为空时默认 "futures"
	Subscribe(symbol string, marketType string) error

	// Unsubscribe 取消订阅价格数据
	// marketType: 市场类型（"spot" 或 "futures"），为空时默认 "futures"
	Unsubscribe(symbol string, marketType string) error

	// ExecuteOrder 执行交易订单
	ExecuteOrder(req *model.PlaceOrderRequest) (*model.Order, error)

	// SetPriceCallback 设置价格数据回调函数
	SetPriceCallback(callback PriceCallback)

	// GetBalance 获取账户余额（单个币种）
	GetBalance() (*model.Balance, error)

	// 扩展功能
	// GetType 获取 trader 类型（如 "binance:futures", "onchain:56"）
	GetType() string

	// Init 初始化连接
	Init() error

	// CalculateSlippage 计算滑点（交易所）
	// slippageLimit: 滑点限制（百分比，如 0.5 表示 0.5%）
	// 返回: (滑点百分比, 符合滑点限制的最大size)
	CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64)

	// GetOrderBook 获取订单簿（交易所）
	// isFutures: true 表示合约订单簿，false 表示现货订单簿
	GetOrderBook(symbol string, isFutures bool) (bids [][]string, asks [][]string, err error)

	// GetPosition 获取持仓（交易所）
	GetPosition(symbol string) (*model.Position, error)

	// GetPositions 获取所有持仓（交易所）
	GetPositions() ([]*model.Position, error)

	// GetAllBalances 获取所有币种的余额
	GetAllBalances() (map[string]*model.Balance, error)

	// 分别获取现货和合约余额（如果交易所不支持，可以返回 GetAllBalances 的结果）
	GetSpotBalances() (map[string]*model.Balance, error)    // 获取现货账户余额
	GetFuturesBalances() (map[string]*model.Balance, error) // 获取合约账户余额
}

// PriceCallback 统一的价格数据回调函数类型
// symbol: 交易对符号（如 BTCUSDT）
// priceData: 价格数据（可能是交易所或链上）
type PriceCallback func(symbol string, priceData PriceData)

// PriceData 统一的价格数据结构
type PriceData struct {
	Ticker     *model.Ticker         // 交易所价格数据（如果来自交易所）
	ChainPrice *model.ChainPriceInfo // 链上价格数据（如果来自链上）
}

// OnchainTradeResult 链上交易结果（解析后的浮点数）
type OnchainTradeResult struct {
	AmountInFloat  float64 // 输入数量（浮点）
	AmountOutFloat float64 // 输出数量（浮点）
	GasFee         float64 // Gas 费用（USDT）
	CoinAmount     float64 // 成交的币数量
}

// OnchainTrader 链上 Trader 接口（扩展接口，用于访问链上特有方法）
type OnchainTrader interface {
	Trader

	// StartSwap 链上启动 swap（链上特有）
	StartSwap(swapInfo *model.SwapInfo)

	// StopSwap 停止链上询价循环，trigger 删除时调用
	StopSwap()

	// BroadcastSwapTx 链上广播交易（链上特有）
	BroadcastSwapTx(direction onchain.SwapDirection) (string, error)

	// GetTxResult 链上查询交易结果（链上特有）
	GetTxResult(txHash, chainIndex string) (model.TradeResult, error)

	// WaitForFullTxResult 轮询链上交易结果直到拿到完整兑换记录（AmountIn/AmountOut 非空）或超时，用于异步落库
	WaitForFullTxResult(txHash, chainIndex string, direction onchain.SwapDirection, timeout time.Duration) (*OnchainTradeResult, error)

	// GetLatestSwapTx 获取最新的 Swap 交易数据（用于滑点计算）
	GetLatestSwapTx() interface{}

	// GetSwapInfo 获取当前的 Swap 信息（用于滑点计算）
	GetSwapInfo() *model.SwapInfo

	// UpdateSwapInfoAmount 更新 Swap 信息中的 Amount 字段
	UpdateSwapInfoAmount(amount string)

	// UpdateSwapInfoDecimals 更新 Swap 信息中的 Decimals 字段
	UpdateSwapInfoDecimals(decimalsFrom, decimalsTo string)

	// UpdateSwapInfoSlippage 更新 Swap 信息中的 Slippage 字段
	UpdateSwapInfoSlippage(slippage string)

	// ResetNonce 重置 nonce 缓存（交易失败或超时时调用）
	ResetNonce(walletAddress, chainIndex string)

	// ExecuteOnChain 执行链上交易（包含广播、等待确认、解析结果）
	// chainIndex: 链ID（如 "56" 表示 BSC）
	// direction: 交易方向，buy=买入（USDT->Coin），sell=卖出（Coin->USDT）
	// 返回：交易哈希、链上交易结果、错误
	ExecuteOnChain(chainIndex string, direction onchain.SwapDirection) (string, *OnchainTradeResult, error)

	// ExecuteSwapWithAmount 使用指定的 amount 独立执行 swap（不影响全局 SwapInfo）
	// amount: 交易数量（字符串格式，如 "100"）
	// chainIndex: 链ID（如 "56" 表示 BSC）
	// direction: 交易方向，buy=买入（USDT->Coin），sell=卖出（Coin->USDT）
	// 返回：交易哈希、链上交易结果、错误
	// 注意：此方法会临时更新 SwapInfo.Amount，执行 swap，然后恢复原始值
	ExecuteSwapWithAmount(amount string, chainIndex string, direction onchain.SwapDirection) (string, *OnchainTradeResult, error)
}
