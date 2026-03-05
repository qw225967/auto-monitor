# Hyperliquid 交易所模块

## 概述

Hyperliquid 模块实现了与 Hyperliquid DEX 的交互。Hyperliquid 是一个去中心化的永续合约交易所，运行在自己的 L1 链上。

## 核心功能

- **WebSocket 连接**：实时价格数据订阅
- **合约交易**：永续合约下单、撤单
- **账户查询**：余额查询、持仓查询
- **订单簿查询**：获取买卖盘深度

## 关键文件

| 文件 | 职责 |
|------|------|
| `hyperliquid.go` | 主结构和初始化 |
| `ws.go` | WebSocket 连接管理 |
| `api.go` | REST API 调用 |
| `order.go` | 订单操作 |
| `account.go` | 账户查询 |

## API 端点

- **API**: `https://api.hyperliquid.xyz`
- **WebSocket**: `wss://api.hyperliquid.xyz/ws`

## 使用示例

```go
import "auto-arbitrage/internal/exchange/hyperliquid"

ex := hyperliquid.NewHyperliquid(privateKey)
ex.Init()

ex.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
    fmt.Printf("%s: Bid=%f, Ask=%f\n", symbol, ticker.BidPrice, ticker.AskPrice)
})
ex.SubscribeTicker(nil, []string{"BTC"})
```

## 符号格式

- **合约**: `BTC`（不带 USDT 后缀）

## 特殊说明

- Hyperliquid 使用钱包私钥进行签名，不使用 API Key/Secret
- 所有交易都是链上交易

## 变更历史

参见 [CHANGELOG](../../../docs/CHANGELOG.md)
