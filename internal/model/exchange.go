package model

import (
	"fmt"
	"time"
)

// Ticker 价格数据
type Ticker struct {
	Symbol    string    // 交易对符号（如 BTCUSDT）
	LastPrice float64   // 最新成交价
	BidPrice  float64   // 买一价
	AskPrice  float64   // 卖一价
	BidQty    float64   // 买一量
	AskQty    float64   // 卖一量
	Volume    float64   // 24小时成交量
	Timestamp time.Time // 时间戳
}

func (t *Ticker) PrintLog() string {
	return fmt.Sprintf("ticker - Symbol: %s, Bid: %.4f, Ask: %.4f, LastPrice: %.4f, Timestamp: %s",
		t.Symbol,
		t.BidPrice,
		t.AskPrice,
		t.LastPrice,
		t.Timestamp.Format(time.RFC3339),
	)
}

// MarketType 市场类型
type MarketType string

const (
	MarketTypeSpot    MarketType = "SPOT"    // 现货
	MarketTypeFutures MarketType = "FUTURES" // 合约
)

// PlaceOrderRequest 下单请求
type PlaceOrderRequest struct {
	Symbol       string       // 交易对符号（如 BTCUSDT）
	Side         OrderSide    // 买卖方向：Buy 或 Sell
	Type         OrderType    // 订单类型：Market 或 Limit
	Quantity     float64      // 数量
	Price        float64      // 价格（限价单必填）
	MarketType   MarketType   // 市场类型：SPOT 或 FUTURES（默认 FUTURES）
	ReduceOnly   bool         // 是否只减仓（合约交易）
	PositionSide PositionSide // 持仓方向：Long 或 Short（合约交易）
}

// OrderSide 订单方向
type OrderSide string

const (
	OrderSideBuy  OrderSide = "BUY"
	OrderSideSell OrderSide = "SELL"
)

// OrderType 订单类型
type OrderType string

const (
	OrderTypeMarket OrderType = "MARKET" // 市价单
	OrderTypeLimit  OrderType = "LIMIT"  // 限价单
)

// PositionSide 持仓方向（合约交易）
type PositionSide string

const (
	PositionSideLong  PositionSide = "LONG"  // 多头（双向持仓模式）
	PositionSideShort PositionSide = "SHORT" // 空头（双向持仓模式）
	PositionSideBoth  PositionSide = "BOTH"  // 单向持仓模式（One-way Mode）使用
)

// Order 订单信息
type Order struct {
	OrderID     string      // 订单ID
	Symbol      string      // 交易对符号
	Side        OrderSide   // 买卖方向
	Type        OrderType   // 订单类型
	Status      OrderStatus // 订单状态
	Quantity    float64     // 下单数量
	Price       float64     // 下单价格
	FilledQty   float64     // 已成交数量
	FilledPrice float64     // 平均成交价
	Fee         float64     // 手续费
	CreateTime  time.Time   // 创建时间
	UpdateTime  time.Time   // 更新时间
}

// OrderStatus 订单状态
type OrderStatus string

const (
	OrderStatusNew             OrderStatus = "NEW"              // 新建订单
	OrderStatusPartiallyFilled OrderStatus = "PARTIALLY_FILLED" // 部分成交
	OrderStatusFilled          OrderStatus = "FILLED"           // 完全成交
	OrderStatusCanceled        OrderStatus = "CANCELED"         // 已取消
	OrderStatusRejected        OrderStatus = "REJECTED"         // 已拒绝
	OrderStatusExpired         OrderStatus = "EXPIRED"          // 已过期
)

// Balance 账户余额
type Balance struct {
	Asset      string    // 资产名称（如 USDT）
	Available  float64   // 可用余额
	Locked     float64   // 冻结余额
	Total      float64   // 总余额
	UpdateTime time.Time // 更新时间
}

// Position 持仓信息（合约交易）
type Position struct {
	Symbol        string       // 交易对符号
	Side          PositionSide // 持仓方向：Long 或 Short
	Size          float64      // 持仓数量
	EntryPrice    float64      // 开仓均价
	MarkPrice     float64      // 标记价格，当前价格
	UnrealizedPnl float64      // 未实现盈亏
	Leverage      int          // 杠杆倍数
	UpdateTime    time.Time    // 更新时间
}

// SymbolInfo 交易对信息
type SymbolInfo struct {
	Symbol     string  // 交易对符号
	BaseAsset  string  // 基础资产（如 BTC）
	QuoteAsset string  // 计价资产（如 USDT）
	MinQty     float64 // 最小下单数量
	MaxQty     float64 // 最大下单数量
	StepSize   float64 // 数量精度步长
	TickSize   float64 // 价格精度步长
	TradingFee float64 // 交易手续费率
}

// OrderBookDepth 订单簿深度
type OrderBookDepth struct {
	Symbol       string           // 交易对符号
	LastUpdateID int64            // 最后更新ID
	Bids         []OrderBookEntry // 买盘（从高到低）
	Asks         []OrderBookEntry // 卖盘（从低到高）
}

// OrderBookEntry 订单簿条目
type OrderBookEntry struct {
	Price    float64 // 价格
	Quantity float64 // 数量
}

// BinanceUnifiedAccountBalance Binance 统一账户余额响应结构
type BinanceUnifiedAccountBalance struct {
	Asset               string `json:"asset"`
	TotalWalletBalance  string `json:"totalWalletBalance"`
	CrossMarginAsset    string `json:"crossMarginAsset"`
	CrossMarginBorrowed string `json:"crossMarginBorrowed"`
	CrossMarginFree     string `json:"crossMarginFree"`
	CrossMarginInterest string `json:"crossMarginInterest"`
	CrossMarginLocked   string `json:"crossMarginLocked"`
	UmWalletBalance     string `json:"umWalletBalance"`
	UmUnrealizedPNL     string `json:"umUnrealizedPNL"`
	CmWalletBalance     string `json:"cmWalletBalance"`
	CmUnrealizedPNL     string `json:"cmUnrealizedPNL"`
	NegativeBalance     string `json:"negativeBalance"`
	UpdateTime          int64  `json:"updateTime"`
}

// BinanceFuturesPositionRisk Binance 合约持仓风险信息响应结构
type BinanceFuturesPositionRisk struct {
	Symbol           string `json:"symbol"`
	PositionAmt      string `json:"positionAmt"`
	EntryPrice       string `json:"entryPrice"`
	MarkPrice        string `json:"markPrice"`
	UnRealizedProfit string `json:"unRealizedProfit"`
	LiquidationPrice string `json:"liquidationPrice"`
	Leverage         string `json:"leverage"`
	MarginType       string `json:"marginType"`
	IsolatedMargin   string `json:"isolatedMargin"`
	PositionSide     string `json:"positionSide"`
	UpdateTime       int64  `json:"updateTime"`
}

// WithdrawRcvrInfo 提币接收方信息（如 OKX 特定主体用户需提供的 rcvrInfo）
// 对应 OKX API: rcvrInfo { walletType, exchId, rcvrFirstName, rcvrLastName }
type WithdrawRcvrInfo struct {
	// WalletType 钱包类型，必填。exchange=提币到交易所钱包，private=提币到私人钱包
	WalletType string `json:"walletType"`
	// ExchId 交易所 ID，如 "did:ethr:0x..."
	ExchId string `json:"exchId"`
	// RcvrFirstName 接收方名；接收方为公司时填公司名称
	RcvrFirstName string `json:"rcvrFirstName"`
	// RcvrLastName 接收方姓；接收方为公司时填 "N/A"。地址可填公司注册地址
	RcvrLastName string `json:"rcvrLastName"`
}

// WithdrawRequest 提币请求
type WithdrawRequest struct {
	Asset      string            // 资产名称（如 USDT）
	Amount     float64           // 提币数量
	Address    string            // 提币地址
	Network    string            // 网络（如 ERC20, TRC20, BEP20）
	Memo       string            // 备注（某些链需要，如 XRP）
	WalletType *int              // 钱包类型（仅部分交易所使用，如 Binance 0=现货 1=资金）。OKX 不使用此顶层字段，walletType 在 RcvrInfo 内
	RcvrInfo   *WithdrawRcvrInfo // 接收方信息（OKX 特定主体用户必填，含 walletType: exchange/private）
}

// WithdrawResponse 提币响应
type WithdrawResponse struct {
	WithdrawID string    // 提币ID
	Status     string    // 状态（PENDING, PROCESSING, COMPLETED, FAILED）
	TxHash     string    // 交易哈希（完成时）
	CreateTime time.Time // 创建时间
}

// DepositAddress 充币地址
type DepositAddress struct {
	Asset   string // 资产名称
	Address string // 充币地址
	Network string // 网络
	Memo    string // 备注（某些链需要）
}

// DepositRecord 充币记录
type DepositRecord struct {
	TxHash     string    // 交易哈希
	Asset      string    // 资产名称
	Amount     float64   // 充币数量
	Network    string    // 网络
	Status     string    // 状态
	CreateTime time.Time // 创建时间
}

// WithdrawRecord 提币记录
type WithdrawRecord struct {
	WithdrawID string    // 提币ID
	TxHash     string    // 交易哈希
	Asset      string    // 资产名称
	Amount     float64   // 提币数量
	Network    string    // 网络
	Address    string    // 提币地址
	Status     string    // 状态
	CreateTime time.Time // 创建时间
}
