# 下一阶段开发计划

## 当前状态

- ✅ Mock 数据源 + Mock 路由探测 → Web 可正常展示
- ⏳ 待完成：真实数据接入、真实路由探测

---

## 方向 A：接入真实 SeeingStone API

**目标**：用真实价差数据替代 Mock

| 步骤 | 任务 | 说明 |
|------|------|------|
| A1 | 配置 Token | 在 `.env` 中设置 `SEEINGSTONE_API_TOKEN` |
| A2 | 关闭 Mock | 启动时**不**设置 `MOCK_MODE=1` |
| A3 | 验证 | 确认能拉取到真实价差，表格有真实数据 |

**命令**：
```bash
# 复制 .env.example 为 .env，填入真实 Token
cp .env.example .env
# 编辑 .env: SEEINGSTONE_API_TOKEN=你的token

# 直接启动（无 MOCK_MODE）
go run ./cmd/server
```

---

## 方向 B：接入真实路由探测模块

**目标**：用真实物理通路探测替代 MockDetector

| 步骤 | 任务 | 说明 |
|------|------|------|
| B1 | 确认探测模块形态 | HTTP 服务 / 本地 Go 包 / 其他 |
| B2 | 实现 Detector 接口 | 新建 `internal/detector/real.go` 或类似 |
| B3 | 替换 main.go | 将 `detector.NewMock()` 改为真实实现 |
| B4 | 联调 | 验证物理路径、状态正确展示 |

**接口约定**：
```go
type Detector interface {
    DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error)
}
```

---

## 方向 C：其他增强（可选）

| 任务 | 说明 |
|------|------|
| WebSocket 推送 | 替代 30s 轮询，实时推送数据 |
| 多数据源 | 接入 symbol 列表等新数据源 |
| 新监控表格 | 资金费率表、深度表等 |
| 部署配置 | Docker、systemd 等 |

---

## 建议顺序

1. **先做方向 A**：有 Token 即可，快速看到真实价差
2. **再做方向 B**：路由探测模块就绪后接入
3. **按需做方向 C**：根据业务需求逐步扩展
