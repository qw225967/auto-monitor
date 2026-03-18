package exchange

import (
	"errors"

	"github.com/qw225967/auto-monitor/internal/model"
)

// Exchange 定义了交易所的核心接口
// 所有交易所实现（币安、Bybit、Bitget、Gate等）都需要实现此接口
type Exchange interface {
	// 连接管理
	Init() error // 建立WebSocket连接，并初始化

	// 交易所类型
	GetType() string // 获取交易所类型（如 "binance", "bybit", "bitget", "gate" 等）

	// 价格数据
	SubscribeTicker(spotSymbols, futureSymbols []string) error   // 订阅ticker价格数据
	UnsubscribeTicker(spotSymbols, futureSymbols []string) error // 取消订阅

	// 价格数据回调
	SetTickerCallback(callback TickerCallback) // 设置价格数据回调函数

	// 订单操作
	PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error) // 下单

	// 计算滑点
	// slippageLimit: 滑点限制（百分比，如 0.5 表示 0.5%）
	// 返回: (滑点百分比, 符合滑点限制的最大size)
	CalculateSlippage(symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64)

	// 订单簿查询
	// 获取现货订单簿，返回 bids 和 asks，每个元素为 [price, qty] 格式的字符串数组
	GetSpotOrderBook(symbol string) (bids [][]string, asks [][]string, err error)
	// 获取合约订单簿，返回 bids 和 asks，每个元素为 [price, qty] 格式的字符串数组
	GetFuturesOrderBook(symbol string) (bids [][]string, asks [][]string, err error)

	// 账户信息
	GetBalance() (*model.Balance, error)                // 查询账户余额
	GetPosition(symbol string) (*model.Position, error) // 查询持仓
	GetPositions() ([]*model.Position, error)           // 查询所有持仓
	GetAllBalances() (map[string]*model.Balance, error) // 获取所有币种的余额（统一余额）
	
	// 分别获取现货和合约余额（如果交易所不支持，可以返回 GetAllBalances 的结果）
	GetSpotBalances() (map[string]*model.Balance, error)   // 获取现货账户余额
	GetFuturesBalances() (map[string]*model.Balance, error) // 获取合约账户余额
}

// TickerCallback 价格数据回调函数类型
// symbol: 交易对符号（如 BTCUSDT）
// ticker: 价格数据
// marketType: 市场类型（"spot" 或 "futures"）
type TickerCallback func(symbol string, ticker *model.Ticker, marketType string)

// QuantoMultiplierProvider 可选接口：提供合约的 quanto_multiplier
// 用于币数量与合约张数换算，以及 GetSize 取整步长（取整步长取双方最大值，且 size 须 >= 该值）
type QuantoMultiplierProvider interface {
	GetQuantoMultiplier(symbol string) (float64, bool)
}

// DepositWithdrawProvider 可选接口：提供充提币功能
// 不是所有交易所都支持充提币，因此作为可选接口
type DepositWithdrawProvider interface {
	Exchange
	// Deposit 获取充币地址
	// asset: 资产名称（如 USDT）
	// network: 网络（如 ERC20, TRC20, BEP20）
	Deposit(asset string, network string) (*model.DepositAddress, error)
	
	// Withdraw 提币
	// req: 提币请求，包含资产、数量、地址、网络等信息
	Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error)
	
	// GetDepositHistory 查询充币记录
	// asset: 资产名称（空字符串表示查询所有资产）
	// limit: 返回记录数量限制
	GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error)
	
	// GetWithdrawHistory 查询提币记录
	// asset: 资产名称（空字符串表示查询所有资产）
	// limit: 返回记录数量限制
	GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error)
}

// WithdrawNetworkLister 可选接口：查询某资产支持的提现网络列表（用于路由探测自动组路径）
// 交易所实现此接口后，路由探测可根据「支持的链」自动尝试直连或经跨链到达目标链
type WithdrawNetworkLister interface {
	Exchange
	// GetWithdrawNetworks 返回该资产在交易所支持的提现网络（含链 ID），仅包含 withdrawEnable 为 true 的
	GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error)
}

// DepositNetworkLister 可选接口：查询某资产支持的充币网络列表（用于链→交易所方向的路由探测）
// 链→交易所时需验证 CanDep，与 WithdrawNetworkLister（CanWd）不同，避免误判不可充币的路径
type DepositNetworkLister interface {
	Exchange
	// GetDepositNetworks 返回该资产在交易所支持的充币网络（含链 ID），仅包含 depositEnable 为 true 的
	GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error)
}

// 错误定义
var (
	ErrNotInitialized = errors.New("exchange not initialized")
	ErrInvalidRequest = errors.New("invalid request")
	ErrInvalidSymbol  = errors.New("invalid symbol")
	ErrNotSupported   = errors.New("operation not supported")
)
