# Pipeline 自动充提系统

## 概述

Pipeline 模块提供了一个灵活的自动充提系统，支持在多个节点（交易所、链上钱包）之间自动转移资产。**跨链是边上的行为**，不是节点：当相邻两个链上节点属于不同链时，该边表示跨链，由 Pipeline 持有的 BridgeManager 执行。系统支持动态拼接节点、顺序执行、等待确认等功能。

## 核心概念

### 节点（Node）

节点是 Pipeline 中的基本单元，代表**资产所在位置**（仅两类）：

- **ExchangeNode**：交易所节点（Binance、Bybit、Bitget 等）
- **OnchainNode**：链上节点（BSC、ETH、Polygon 等）

跨链不作为节点；链 A → 链 B 的转移由**边**表示，执行时调用 BridgeManager。

### 边（Edge）

边配置描述两个节点之间的转移参数：
- 金额配置（固定/百分比/全部）
- 网络配置（ERC20、TRC20、BEP20 等）
- **跨链边**：当源、目标均为 OnchainNode 且链 ID 不同时，可通过 EdgeConfig.BridgeProtocol 指定协议（layerzero、wormhole、auto），需对 Pipeline 调用 `SetBridgeManager(mgr)`
- 等待策略（超时时间、检查间隔等）

### Pipeline

Pipeline 由一组有序的节点和边配置组成，按顺序执行转账操作。

## 使用示例

### 基本用法

```go
import "auto-arbitrage/internal/pipeline"

// 创建节点
node1, _ := pipeline.CreateNode(pipeline.TypeNodeBinance, "USDT")
node2, _ := pipeline.CreateNode(pipeline.TypeNodeBitget, "USDT")

// 创建 Pipeline
pipelineAB := pipeline.CreateAutoWithdrawPipeline("pipeline-AB", node1, node2)

// 配置边（可选）
edgeConfig := &pipeline.EdgeConfig{
    AmountType:   pipeline.AmountTypeAll,
    Network:      "ERC20",
    MaxWaitTime:  30 * time.Minute,
    CheckInterval: 10 * time.Second,
}
pipelineAB.SetEdgeConfig(node1.GetID(), node2.GetID(), edgeConfig)

// 执行 Pipeline
err := pipelineAB.Run()
```

### 复杂 Pipeline（包含跨链）

跨链为**边上的行为**：路径中只含资产持有节点，BSC → ETH 通过边配置 + BridgeManager 完成。

```go
// binance -> bsc -> eth -> gate（BSC->ETH 为跨链边）
nodeBinance, _ := pipeline.CreateNode(pipeline.TypeNodeBinance, "USDT")
nodeBSC, _ := pipeline.CreateNode(pipeline.TypeNodeOnchainBSC, "USDT", walletAddr, tokenAddr, onchainClient)
nodeETH, _ := pipeline.CreateNode(pipeline.TypeNodeOnchainETH, "USDT", walletAddr, tokenAddr, onchainClient)
nodeGate, _ := pipeline.CreateNode(pipeline.TypeNodeGate, "USDT")

p := pipeline.CreateAutoWithdrawPipeline("complex-pipeline", 
    nodeBinance, nodeBSC, nodeETH, nodeGate)
p.SetBridgeManager(bridgeManager) // 跨链边执行时使用

// BSC -> ETH 边：跨链
p.SetEdgeConfig(nodeBSC.GetID(), nodeETH.GetID(), &pipeline.EdgeConfig{
    AmountType:     pipeline.AmountTypeAll,
    BridgeProtocol: "layerzero",
    MaxWaitTime:    60 * time.Minute,
    CheckInterval:  30 * time.Second,
})

// 其他边配置...
err := p.Run()
```

## API 说明

### 节点管理

- `CreateNode(nodeType, assetSymbol, configs...)` - 创建节点
- `Pipeline.AddNode(node)` - 添加节点
- `Pipeline.InsertNode(index, node)` - 插入节点
- `Pipeline.RemoveNode(nodeID)` - 删除节点

### Pipeline 执行

- `Pipeline.Run()` - 执行 Pipeline
- `Pipeline.Stop()` - 停止执行
- `Pipeline.Status()` - 获取状态
- `Pipeline.CurrentStep()` - 获取当前步骤

### 边配置

- `Pipeline.SetEdgeConfig(fromNodeID, toNodeID, config)` - 设置边配置
- `Pipeline.GetEdgeConfig(fromNodeID, toNodeID)` - 获取边配置

## 节点配置

### ExchangeNode 配置

在创建节点时，网络等信息可以通过 EdgeConfig 配置，或使用节点的默认值。

### OnchainNode 配置

创建链上节点时需要提供：
- 链ID（通过节点类型指定，如 `onchain:56`）
- 资产符号
- 钱包地址
- 代币合约地址（可选）
- OnchainClient 实例

### 跨链边配置

当路径中存在「链 A → 链 B」相邻节点时：
- 对 Pipeline 调用 `SetBridgeManager(bridge.Manager)` 注入跨链管理器
- 对该边调用 `SetEdgeConfig(fromNodeID, toNodeID, &EdgeConfig{BridgeProtocol: "layerzero"|"wormhole"|"auto", ...})`

## 执行流程

1. **预检查**：检查每个节点是否有足够余额
2. **顺序执行**：对于每对相邻节点：
   - 计算转账金额（根据 EdgeConfig）
   - 执行转账（提币或跨链）
   - 等待确认到账
   - 继续下一个节点
3. **状态跟踪**：记录每个步骤的执行状态和日志

## 注意事项

1. **余额检查**：节点通过 WalletManager 获取余额，确保 WalletManager 已初始化
2. **状态轮询**：使用可配置的间隔和超时时间，避免无限等待
3. **错误处理**：Pipeline 执行失败时会停止，不会自动重试
4. **并发安全**：Pipeline 使用 mutex 保护状态，支持并发查询

## Web API

- `POST /api/pipeline/create` - 创建 Pipeline
- `POST /api/pipeline/run?id=xxx` - 执行 Pipeline
- `GET /api/pipeline/status?id=xxx` - 查询状态
- `POST /api/pipeline/nodes?id=xxx` - 添加/插入节点
- `DELETE /api/pipeline/nodes?id=xxx&nodeId=yyy` - 删除节点
