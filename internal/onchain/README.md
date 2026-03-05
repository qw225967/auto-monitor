# Onchain 模块

## 概述

Onchain 模块提供与区块链网络和去中心化交易所（DEX）交互的功能。主要通过 OKEx DEX 聚合器进行链上 Swap 操作，支持多链交易、Bundler 加速等功能。

## 核心功能

- **RPC 连接**：连接区块链网络
- **Swap 询价**：通过 OKEx 聚合器获取最优交易路径
- **交易广播**：发起链上交易并广播
- **成交检测**：监控链上交易确认状态
- **资产监控**：监控链上钱包余额
- **Bundler 支持**：支持 Flashbots、48Club 等 Bundler

## 关键文件

| 文件 | 职责 |
|------|------|
| `interface.go` | OnchainClient 接口定义 |
| `okdex.go` | OKEx DEX 主实现 |
| `okdex_api.go` | OKEx API 调用 |
| `okdex_tx.go` | 交易构建和广播 |
| `okdex_internal.go` | 内部逻辑 |
| `okdex_utils.go` | 工具函数 |
| `bundler/` | Bundler 实现目录 |

## API 说明

### OnchainClient 接口

```go
type OnchainClient interface {
    Init() error
    SetPriceCallback(callback PriceCallback)
    StartSwap(swapInfo *model.SwapInfo)
    BroadcastSwapTx(direction SwapDirection) (string, error)
    GetTxResult(txHash, chainIndex string) (model.TradeResult, error)
    GetBalance() (*model.TokenBalance, error)
    GetAllTokenBalances(address, chains string, excludeRiskToken bool) ([]model.OkexTokenAsset, error)
    GetLatestSwapTx() interface{}
    GetSwapInfo() *model.SwapInfo
    UpdateSwapInfoAmount(amount string)
    UpdateSwapInfoSlippage(slippage string)
    ResetNonce(walletAddress, chainIndex string)
}
```

### SwapDirection 类型

```go
const (
    SwapDirectionBuy  SwapDirection = "buy"   // USDT -> Coin
    SwapDirectionSell SwapDirection = "sell"  // Coin -> USDT
)
```

## 使用示例

```go
import "auto-arbitrage/internal/onchain"

// 创建实例
okdex := onchain.NewOKDex(config)
okdex.Init()

// 设置价格回调
okdex.SetPriceCallback(func(price *model.ChainPriceInfo) {
    fmt.Printf("链上价格: Buy=%s, Sell=%s\n", price.BuyPrice, price.SellPrice)
})

// 启动 Swap
swapInfo := &model.SwapInfo{
    FromTokenSymbol: "USDT",
    ToTokenSymbol:   "BTC",
    ChainIndex:      "56",
    Amount:          "1000",
}
okdex.StartSwap(swapInfo)
```

## 依赖关系

### 依赖的模块
- `model` - 数据模型
- `config` - 配置管理
- `go-ethereum` - 以太坊客户端库

### 被依赖的模块
- `trader` - OnchainTrader 封装 OnchainClient
- `position` - 查询链上余额

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)