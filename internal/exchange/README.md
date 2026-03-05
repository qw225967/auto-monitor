# Exchange 模块

## 概述

Exchange 模块提供与中心化交易所（CEX）交互的功能，支持多个主流交易所。每个交易所实现统一的 Exchange 接口，支持 WebSocket 价格订阅、订单操作、账户查询等功能。

## 核心功能

- **WebSocket 连接**：实时价格数据订阅
- **订单操作**：下单、撤单、查询订单
- **账户查询**：余额查询、持仓查询
- **订单簿查询**：获取买卖盘深度
- **滑点计算**：基于订单簿计算滑点

## 支持的交易所

| 交易所 | 状态 | 目录 |
|--------|------|------|
| Binance | ✅ 完成 | `binance/` |
| Bybit | ✅ 完成 | `bybit/` |
| Bitget | ✅ 完成 | `bitget/` |
| Gate | 🔄 进行中 | `gate/` |
| Hyperliquid | 🔄 进行中 | `hyperliquid/` |
| Lighter | 📋 计划中 | `lighter/` |
| Aster | 📋 计划中 | `aster/` |

## 关键文件

| 文件 | 职责 |
|------|------|
| `interface.go` | Exchange 接口定义 |
| `README_NEW_EXCHANGES.md` | 新交易所接入指南 |

## API 说明

### Exchange 接口

```go
type Exchange interface {
    Init() error
    GetType() string
    SubscribeTicker(spotSymbols, futureSymbols []string) error
    UnsubscribeTicker(spotSymbols, futureSymbols []string) error
    SetTickerCallback(callback TickerCallback)
    PlaceOrder(req *model.PlaceOrderRequest) (*model.Order, error)
    GetSpotOrderBook(symbol string) (bids, asks [][]string, err error)
    GetFuturesOrderBook(symbol string) (bids, asks [][]string, err error)
    CalculateSlippage(symbol string, amount float64, isFutures bool, 
                      side model.OrderSide, slippageLimit float64) (float64, float64)
    GetBalance() (*model.Balance, error)
    GetAllBalances() (map[string]*model.Balance, error)
    GetPosition(symbol string) (*model.Position, error)
    GetPositions() ([]*model.Position, error)
}
```

## 使用示例

```go
import "auto-arbitrage/internal/exchange/binance"

// 创建实例
ex := binance.NewBinance(apiKey, secretKey)
ex.Init()

// 订阅价格
ex.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
    fmt.Printf("%s: Bid=%f, Ask=%f\n", symbol, ticker.BidPrice, ticker.AskPrice)
})
ex.SubscribeTicker(nil, []string{"BTCUSDT"})
```

## 依赖关系

### 依赖的模块
- `model` - 数据模型
- `config` - 配置管理

### 被依赖的模块
- `trader` - CEXTrader 封装 Exchange

## 扩展指南

添加新交易所请参考 [README_NEW_EXCHANGES.md](README_NEW_EXCHANGES.md)

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)