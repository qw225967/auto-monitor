# 路由探测模块

## 当前状态

- **MockDetector**: 模拟实现，返回固定的 2 条物理路径
- **待迁移**: 接入真实的路由探测逻辑

## 接口

```go
type Detector interface {
    DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error)
}
```

## 迁移步骤

1. 实现 `Detector` 接口（HTTP 调用外部服务，或 import 本地包）
2. 在 `cmd/server/main.go` 中替换 `detector.NewMock()` 为真实实现
3. 删除或保留 `mock.go` 用于测试
