# Aster 交易所模块

## 概述

Aster 模块实现了与 Aster DEX 的交互。Aster 是一个去中心化交易所。

## 核心功能

- **WebSocket 连接**：实时价格数据订阅
- **交易操作**：下单、撤单
- **账户查询**：余额查询、持仓查询
- **订单簿查询**：获取买卖盘深度

## 关键文件

| 文件 | 职责 |
|------|------|
| `aster.go` | 主结构和初始化 |
| `ws.go` | WebSocket 连接管理 |
| `api.go` | REST API 调用 |
| `order.go` | 订单操作 |

## 使用示例

```go
import "auto-arbitrage/internal/exchange/aster"

ex := aster.NewAster(config)
ex.Init()

ex.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
    fmt.Printf("%s: Bid=%f, Ask=%f\n", symbol, ticker.BidPrice, ticker.AskPrice)
})
ex.SubscribeTicker(nil, []string{"BTCUSDT"})
```

## 变更历史

参见 [CHANGELOG](../../../docs/CHANGELOG.md)
