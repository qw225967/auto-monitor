# OKX 交易所模块

## 概述

OKX 模块实现了与 OKX 交易所的交互，支持现货与永续合约、WebSocket 行情、订单与订单簿、账户查询及充提币（Deposit/Withdraw）。API 使用 OKX v5 统一接口，API Key 从全局配置 `OkEx.KeyList` 读取。

## 核心功能

- **WebSocket 行情**：公开 tickers 订阅（现货 + 永续共用一条连接）
- **现货交易**：PlaceOrder（tdMode=cash）、订单簿
- **合约交易**：PlaceOrder（tdMode=cross）、订单簿、持仓查询
- **账户**：GetBalance（USDT）、GetAllBalances、GetSpotBalances、GetFuturesBalances（统一账户同源）、GetPosition、GetPositions
- **滑点**：CalculateSlippage（基于订单簿，委托 analytics）
- **充提币**：Deposit、Withdraw、GetDepositHistory、GetWithdrawHistory（见 withdraw.go）

## 关键文件

| 文件 | 职责 |
|------|------|
| `okx.go` | 主结构、Init、订阅/取消订阅、CalculateSlippage |
| `utils.go` | 签名、时间戳、Headers、符号转换（ToOKXSpotInstId/ToOKXSwapInstId/FromOKXInstId）、数量/价格格式化 |
| `websocket.go` | 公开行情 WS 连接、订阅 tickers、解析并回调 |
| `order.go` | PlaceOrder（现货/合约）、parseOrderResponse |
| `orderbook.go` | GetSpotOrderBook、GetFuturesOrderBook |
| `account.go` | GetBalance、GetAllBalances、GetSpotBalances、GetFuturesBalances、GetPosition、GetPositions |
| `withdraw.go` | Deposit、Withdraw、GetDepositHistory、GetWithdrawHistory、可选 WithdrawNetworkLister |

## API 与端点

- **REST 主站**: `https://www.okx.com`
- **公开行情 WS**: `wss://ws.okx.com:8443/ws/v5/public`
- **账户/交易**：需签名，请求头 `OK-ACCESS-KEY`、`OK-ACCESS-SIGN`、`OK-ACCESS-TIMESTAMP`、`OK-ACCESS-PASSPHRASE`

签名规则：`timestamp + method + requestPath + body` 做 HMAC-SHA256 后 Base64；时间戳为 ISO 8601 格式。

## 符号格式

- **统一符号**（本模块对外）：`BTCUSDT`
- **现货 instId**：`BTC-USDT`（`ToOKXSpotInstId`）
- **永续 instId**：`BTC-USDT-SWAP`（`ToOKXSwapInstId`）
- 反向转换：`FromOKXInstId`

## 使用示例

```go
import "auto-arbitrage/internal/exchange/okx"

ex := okx.NewOkx()
ex.Init()

ex.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
    fmt.Printf("%s %s: Bid=%f, Ask=%f\n", symbol, marketType, ticker.BidPrice, ticker.AskPrice)
})
ex.SubscribeTicker([]string{"BTCUSDT"}, []string{"BTCUSDT"})

// 下单、订单簿、余额、持仓等
order, _ := ex.PlaceOrder(&model.PlaceOrderRequest{...})
bids, asks, _ := ex.GetSpotOrderBook("BTCUSDT")
bal, _ := ex.GetBalance()
positions, _ := ex.GetPositions()
```

## 配置

API 密钥从 `config.GetGlobalConfig().OkEx.KeyList` 获取，每项包含 `APIKey`、`Secret`、`Passphrase`。当前实现使用列表中第一个可用密钥。

## 变更历史

参见项目根目录或 docs 下的 CHANGELOG。
