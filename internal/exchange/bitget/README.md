# Bitget 交易所模块

## 概述

Bitget 模块实现了与 Bitget 交易所的交互，支持现货和合约交易，包括 WebSocket 价格订阅、订单操作、账户查询等功能。

## 核心功能

- **WebSocket 连接**：实时价格数据订阅
- **现货交易**：现货下单、撤单、查询
- **合约交易**：合约下单、撤单、持仓查询
- **账户查询**：余额查询、持仓查询
- **订单簿查询**：获取买卖盘深度

## 关键文件

| 文件 | 职责 |
|------|------|
| `bitget.go` | 主结构和初始化 |
| `ws.go` | WebSocket 连接管理 |
| `api.go` | REST API 调用 |
| `order.go` | 订单操作 |
| `account.go` | 账户查询 |

## API 端点

- **API**: `https://api.bitget.com`
- **WebSocket**: `wss://ws.bitget.com`

## 使用示例

```go
import "auto-arbitrage/internal/exchange/bitget"

ex := bitget.NewBitget(apiKey, secretKey, passphrase)
ex.Init()

ex.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
    fmt.Printf("%s: Bid=%f, Ask=%f\n", symbol, ticker.BidPrice, ticker.AskPrice)
})
ex.SubscribeTicker(nil, []string{"BTCUSDT"})
```

## 符号格式

- **现货**: `BTCUSDT`
- **合约**: `BTCUSDT`

## 变更历史

参见 [CHANGELOG](../../../docs/CHANGELOG.md)
