# Onchain 能力说明

## 当前保留能力

### 1. OK DEX Quote（询价）
- **QueryDexQuotePrice**：调用 OKX DEX v6 聚合器询价接口，仅询价不交易
- 输入：fromTokenAddress, toTokenAddress, chainIndex, amount, fromTokenDecimals
- 返回：原始 API 响应字符串

### 2. Bridge（跨链桥）
- **BridgeToken**：跨链转账
- **GetBridgeStatus**：查询跨链状态
- **GetBridgeQuote**：获取跨链报价（费用、时间等）
- 协议：LayerZero、Wormhole、CCIP 等

## 已移除
- Swap 兑换（queryDexSwap、StartSwap、StopSwap）
- 广播交易（BroadcastSwapTx、broadcastTransaction）
- Bundler 包（Flashbots、FortyEightClub 等）
- 余额查询（GetBalance、GetAllTokenBalances）
- 交易状态（GetTxResult、queryTxResult）
- 授权、签名、Nonce 等交易相关逻辑

## 注意
- `internal/position`、`internal/pipeline` 依赖原完整 OnchainClient 接口，精简后需单独适配
- 主流程（cmd/server + detector）仅使用 bridge，不受影响
