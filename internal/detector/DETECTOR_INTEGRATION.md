# 路由探测模块 - 集成完成

## 来源

路由探测逻辑已从 auto-arbitrage 迁移至 auto-monitor（main 分支 internal 目录）。

## 实现

- **ArbitrageAdapter**：`arbitrage_adapter.go` - 调用 routeProbe，将结果转为 PhysicalPath
- **routeProbe**：`routeprobe.go` - 精简版 RouteProbe，避免依赖完整 pipeline
- **Bridge Manager**：可传 `nil`，跨链段将不可用；传入 `bridge.NewManager(true)` 并注册协议后可获取跨链报价

## 使用

```go
det := detector.NewArbitrageAdapter(nil)  // 无跨链桥
```

## 支持的交易所

binance, bybit, bitget, gate, okex/okx, hyperliquid, lighter, aster
