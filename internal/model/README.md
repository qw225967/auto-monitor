# Model 模块

## 概述

Model 模块定义了系统中使用的各种数据结构，包括交易所相关结构、链上相关结构、套利相关结构、持仓相关结构等。

## 关键文件

| 文件 | 职责 |
|------|------|
| `exchange.go` | 交易所相关结构（订单、余额、持仓等） |
| `onchain.go` | 链上相关结构（Swap、Token、交易结果等） |
| `arbitrage.go` | 套利相关结构 |
| `positon.go` | 持仓相关结构 |

## 主要数据结构

### 交易所相关

```go
// 订单请求
type PlaceOrderRequest struct {
    Symbol    string
    Side      OrderSide
    Type      OrderType
    Quantity  float64
    Price     float64
    IsFutures bool
}

// 订单结果
type Order struct {
    OrderID   string
    Symbol    string
    Side      OrderSide
    Status    OrderStatus
    Price     float64
    Quantity  float64
    FilledQty float64
}

// 余额
type Balance struct {
    Asset     string
    Available float64
    Frozen    float64
    Total     float64
}

// 持仓
type Position struct {
    Symbol        string
    Side          string
    Size          float64
    EntryPrice    float64
    MarkPrice     float64
    UnrealizedPnl float64
    Leverage      int
}

// Ticker
type Ticker struct {
    Symbol    string
    BidPrice  float64
    AskPrice  float64
    LastPrice float64
    Time      time.Time
}
```

### 链上相关

```go
// Swap 信息
type SwapInfo struct {
    FromTokenSymbol string
    ToTokenSymbol   string
    ChainIndex      string
    Amount          string
    DecimalsFrom    string
    DecimalsTo      string
    Slippage        string
    WalletAddress   string
}

// 链上价格信息
type ChainPriceInfo struct {
    BuyPrice  string
    SellPrice string
    GasPrice  string
}

// 交易结果
type TradeResult struct {
    TxHash    string
    Status    string
    AmountIn  string
    AmountOut string
    GasFee    string
}
```

### 枚举类型

```go
// 订单方向
type OrderSide string
const (
    OrderSideBuy  OrderSide = "BUY"
    OrderSideSell OrderSide = "SELL"
)

// 订单类型
type OrderType string
const (
    OrderTypeMarket OrderType = "MARKET"
    OrderTypeLimit  OrderType = "LIMIT"
)

// 订单状态
type OrderStatus string
const (
    OrderStatusNew           OrderStatus = "NEW"
    OrderStatusPartialFilled OrderStatus = "PARTIALLY_FILLED"
    OrderStatusFilled        OrderStatus = "FILLED"
    OrderStatusCanceled      OrderStatus = "CANCELED"
)
```

## 依赖关系

### 被依赖的模块
- 几乎所有模块都依赖 model 模块

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)
