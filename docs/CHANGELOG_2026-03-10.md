# 变更总结（2026-03-10）

## 1. 本轮变更范围与逻辑

### 1.1 CoinGecko 配额治理与请求价值化

涉及文件：
- `internal/tokenregistry/liquidity.go`
- `internal/tokenregistry/sync.go`
- `internal/tokenregistry/storage.go`
- `internal/tokenregistry/model.go`
- `internal/tokenregistry/budget.go`
- `internal/config/config.go`
- `config/settings.yaml`
- `.env.example`
- `cmd/server/main.go`
- `cmd/tokensync/main.go`

核心变更：
- 修复 CoinGecko Demo/Pro 鉴权 header 逻辑。
- TokenSync 从“资产存在即跳过”改为“TTL 刷新 + 链级增量”。
- LiquiditySync 增加 retry/backoff/jitter。
- 引入负缓存（404/无池）+ TTL，减少无价值请求。
- 引入预算管理器（按月上限 + 按天节奏），并接入 TokenSync 和 LiquiditySync。
- 增加候选优先流动性同步（资产子集 + 每轮请求上限）。

影响范围：
- 直接影响 CoinGecko 请求量、429 风险、流动性数据新鲜度和候选稳定性。

### 1.2 探测稳定性与并发治理

涉及文件：
- `internal/detector/registry/api_registry.go`
- `internal/runner/runner.go`
- `internal/config/config.go`
- `config/settings.yaml`
- `.env.example`
- `cmd/server/main.go`

核心变更：
- Registry refresh 从整表替换改为增量更新 + TTL 清理，刷新失败时保留旧缓存。
- RunDetect 增加并发上限和单路探测超时控制（可配置）。

影响范围：
- 降低“单点失败导致全量通路抖空”与探测资源失控风险。

### 1.3 机会质量升级（净收益 + 置信度）

涉及文件：
- `internal/model/model.go`
- `internal/opportunities/economics.go`
- `internal/opportunities/opportunities.go`
- `internal/runner/runner.go`

核心变更：
- 新增 `gross_spread_percent`、`estimated_cost_percent`、`net_spread_percent`、`confidence_score`。
- 引入静态成本模型 v1，过滤净收益 <= 0 机会。
- 排序从“仅看 spread”升级为“净收益优先、置信度次级”。

影响范围：
- 机会排序与候选质量显著变化，更偏可执行收益。

### 1.4 API freshness 与前端接线

涉及文件：
- `internal/api/handler.go`
- `frontend/src/types.ts`
- `frontend/src/App.tsx`
- `frontend/src/components/OverviewTable.tsx`
- `frontend/src/App.css`

核心变更：
- `/api/overview` 新增 freshness 元数据（updated_at + age_sec）。
- 前端新增 freshness 面板、净价差/置信度展示。
- 前端新增排序偏好与新鲜度策略切换，并支持 localStorage 持久化。

影响范围：
- 可观测性增强，前端可直接感知数据时效和候选质量。

### 1.5 文档与发布治理

新增文档：
- `docs/ROADMAP.md`
- `docs/DECISIONS.md`
- `docs/IMPLEMENTATION_TRACKER.md`
- `docs/RELEASE_CHECKLIST.md`

作用：
- 打通“规划-决策-执行-发布”闭环，便于持续推进与回滚。

## 2. 回归清单（建议）

### 2.1 后端测试与编译
- `go test ./internal/tokenregistry ./internal/detector/registry ./internal/runner ./internal/opportunities ./internal/api ./cmd/server ./cmd/tokensync`

### 2.2 前端构建
- `cd frontend && npm run build`

### 2.3 API 合同回归
- `GET /api/overview` 验证新增字段：
  - `overview_updated_at`
  - `chain_prices_updated_at`
  - `liquidity_updated_at`
  - `overview_age_sec`
  - `chain_prices_age_sec`
  - `liquidity_age_sec`
  - `net_spread_percent`
  - `confidence_score`

### 2.4 关键行为回归
- 预算开启后系统可降级但不崩溃。
- 负缓存命中后同类无效请求下降。
- 注册表局部刷新失败时不出现全量抖空。
- 探测高负载下并发受控、超时可观测。
- 净收益 <= 0 机会被过滤。

### 2.5 前端交互回归
- freshness 面板显示与告警颜色正确。
- 排序模式切换生效。
- 新鲜度策略切换生效。
- 页面刷新后偏好仍保留（localStorage）。

## 3. 当前工作区注意事项

本轮主线改动较大，建议提交前确认以下非主线文件是否纳入：
- `frontend/package-lock.json`
- `frontend/vite.config.ts`
- `scripts/start.sh`
- `.cursor/` 目录

