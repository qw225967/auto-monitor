# Release Checklist

用于本轮“配额治理 + 机会质量 + 前端可视化”改造的上线核对与回滚指引。

## 1. 发布前准备

- [ ] 后端环境变量已核对：
  - [ ] `COINGECKO_API_KEY`
  - [ ] `COINGECKO_BUDGET_ENABLED`
  - [ ] `COINGECKO_MONTHLY_LIMIT`
  - [ ] `LIQUIDITY_NEGATIVE_TTL`
  - [ ] `RUN_DETECT_MAX_CONCURRENCY`
  - [ ] `RUN_DETECT_ROUTE_TIMEOUT`
- [ ] `config/settings.yaml` 已确认 priority 同步参数：
  - [ ] `priority_sync_enabled`
  - [ ] `priority_sync_interval`
  - [ ] `priority_top_assets`
  - [ ] `priority_max_requests`
- [ ] `data/` 目录权限正确（可写）：
  - [ ] `data/token_registry.json`
  - [ ] `data/coingecko_budget.json`
- [ ] 前端构建产物可用：`npm run build`
- [ ] 关键单测通过：
  - [ ] `go test ./internal/tokenregistry ./internal/detector/registry ./internal/runner ./internal/opportunities ./internal/api`

## 2. 灰度发布步骤

1. 先发布后端，前端保持旧版本 10-15 分钟观察。
2. 检查后端日志关键字是否异常暴增：
   - `budget deny`
   - `LiquiditySync`
   - `detect timeout`
3. 观察 1 个探测周期后再发布前端。
4. 前端发布后验证：
   - freshness 面板正常显示
   - `net_spread_percent`、`confidence_score` 有数据
   - 排序/策略切换与本地持久化生效

## 3. 发布后观察指标（前 60 分钟）

- [ ] CoinGecko 429 比例是否下降（对比发布前）
- [ ] `budget deny` 是否在预期范围（不应长期 100% 拒绝）
- [ ] overview 数据更新是否稳定（`overview_age_sec` 不持续飙升）
- [ ] detect 周期是否超时（`detect timeout` 是否异常增长）
- [ ] 前端错误率/空白页是否异常

## 4. 回滚触发条件

满足任一条件建议回滚：

- 连续 10 分钟 `overview_age_sec` 显著高于基线且无恢复趋势
- `budget deny` 导致候选几乎清空（明显业务不可用）
- `detect timeout` 大量出现并导致页面长期无可用通路
- 前端关键视图无法渲染（表格空白或报错）

## 5. 回滚策略

### 快速降级（不回滚代码）

- 将 `COINGECKO_BUDGET_ENABLED=0`
- 提高 `PRIORITY_LIQUIDITY_SYNC_INTERVAL`，降低 `PRIORITY_LIQUIDITY_MAX_REQUESTS`
- 提高 `RUN_DETECT_ROUTE_TIMEOUT`，降低 `RUN_DETECT_MAX_CONCURRENCY`

### 完整回滚

- 回滚后端到上一个稳定版本
- 回滚前端到上一个稳定版本
- 保留 `data/token_registry.json` 与 `data/coingecko_budget.json`（用于后续排障）

## 6. 发布结论记录

- 发布时间：
- 发布人：
- 发布版本（后端/前端）：
- 观察结论：
- 是否回滚：
- 后续行动：

