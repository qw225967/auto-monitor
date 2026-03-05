# 路由探测模块 - 集成完成

## 来源

路由探测逻辑已从 auto-arbitrage 迁移至 auto-monitor（main 分支 internal 目录）。

## 实现

- **ArbitrageAdapter**：`arbitrage_adapter.go` - 使用 NetworkRegistry + PipelineBuilder 生成路径，调用 routeProbe 校验
- **NetworkRegistry**：`registry/interface.go` - 交易所充提网络查询接口
- **StaticRegistry**：`registry/static.go` - 静态配置的交易所→资产→链支持（无 API 调用）
- **PipelineBuilder**：`pipeline_builder.go` - 根据 registry 构建邻接图，枚举所有可达路径（FindAllRoutes）
- **routeProbe**：`routeprobe.go` - 精简版 RouteProbe，校验每段可达性（提现/充值/跨链）
- **Bridge Manager**：可传 `nil`，跨链段将不可用；传入 `bridge.NewManager(true)` 并注册协议后可获取跨链报价

## 路径生成流程

1. 从 StaticRegistry 获取 buyExchange、sellExchange 在 asset 上的提现/充币网络
2. PipelineBuilder 构建邻接图：交易所→链、链→交易所、链→链（跨链）
3. FindAllRoutes 枚举所有路径（最多 4 跳）
4. 每条路径经 routeProbe 校验各段可达性，转为 PhysicalPath

## 使用

```go
det := detector.NewArbitrageAdapter(nil)  // 无跨链桥，跨链段标记为不可用
```

## 支持的交易所

binance, bybit, bitget, gate, okex/okx, hyperliquid, lighter, aster
