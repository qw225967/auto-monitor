# Implementation Tracker

## 使用说明

- 状态取值：`todo` / `in_progress` / `blocked` / `done`。
- 每次迭代更新：状态、验收结果、风险、下一步。
- 指标请尽量量化，便于评估是否达到目标。

## 任务看板

| ID | 模块 | 任务 | 优先级 | 状态 | 验收标准 | 指标/结果 | 风险/阻塞 | 下一步 |
|---|---|---|---|---|---|---|---|---|
| CG-001 | tokenregistry | 修复 CoinGecko Demo/Pro header 逻辑统一 | P0 | done | Demo/Pro 均可正常请求，鉴权行为与配置一致 | 已完成：新增 Demo/Pro header 单测并通过 | 无 | 开始 CG-002 |
| CG-002 | tokenregistry | TokenSync 改为链级增量 + TTL 刷新 | P0 | done | 已有资产可增量补新链，且不重复高频请求 | 已完成：新增 TTL 刷新判断、链级触达更新时间刷新，并补测试 | 历史数据中的旧时间戳可能触发首轮补刷 | 开始 CG-003 |
| CG-003 | tokenregistry | LiquiditySync 增加 retry/backoff/jitter | P1 | done | 429/5xx/timeout 可恢复，失败率下降 | 已完成：加入可配置重试/指数退避/抖动，并新增重试逻辑测试 | 外部 API 波动 | 开始 CG-004 |
| CG-004 | tokenregistry | 负缓存（404/无池/不支持链） | P1 | done | 同一无效键在 TTL 内不重复请求 | 已完成：引入 404/无池负缓存字段与 TTL，命中时跳过请求 | 低流动性资产可能因 TTL 延迟恢复 | 开始 CG-005 |
| CG-005 | tokenregistry | 预算管理器（日/月限额） | P1 | done | 超预算自动降级请求，系统不中断 | 已完成：RunSync/RunLiquiditySync 共用预算管理器并持久化状态 | 多实例并发写同一路径需后续加文件锁 | 开始 DET-001 |
| DET-001 | detector/registry | 刷新策略改为局部更新 + TTL，失败保留旧值 | P1 | done | 单交易所失败不导致全局缓存抖空 | 已完成：改为增量合并并引入 per-key TTL 清理，附单测 | TTL 过短可能增加 API 压力 | 开始 RUN-001 |
| RUN-001 | runner | DetectRoutes 并发上限 + timeout 分层 | P1 | done | 周期稳定，资源可控，不出现 goroutine 激增 | 已完成：增加并发信号量与单路超时配置，附并发上限测试 | 阈值配置不当 | 进入 M1 验收 |
| OPP-001 | opportunities | 净收益模型 v1（静态费率） | P1 | done | 输出 net_spread，排序支持按净值 | 已完成：新增 gross/cost/net 字段并按净价差排序，净值<=0 机会过滤 | 费率来源准确性 | 开始 OPP-002 |
| OPP-002 | opportunities | 路径置信度评分（成功率/时延） | P2 | done | 候选按收益与置信度联合排序 | 已完成：新增 confidence_score 并接入排序二级权重（规则版） | 当前未接入真实时延历史，后续可迭代 | 开始 API-001 |
| API-001 | api | 增加 freshness 元数据输出 | P2 | done | 前端可识别数据是否过期 | 已完成：`/api/overview` 输出更新时间与 age 秒级字段 | 前端尚未消费新字段 | 进入前端接线 |
| M2-001 | tokenregistry/server | 候选优先 + 背景补全流动性同步骨架 | P1 | done | 高频候选走子集同步，低频全表补全 | 已完成：新增 priority_sync 配置与 IncludeAssets/MaxRequests 子集同步 | 候选选择仍是 spread 规则，后续可升级价值评分 | 开始 M2-002 |
| M2-002 | server | 候选资产价值评分（频次+强度+新鲜度） | P1 | done | 候选选择不只看 spread，优先更稳定高价值资产 | 已完成：接入内存评分器并替换优先同步资产选择 | 评分参数目前固定，后续可配置化 | 开始 OPP-001 |
| FE-001 | frontend | 接线 freshness + net/confidence 展示 | P1 | done | 前端可见数据新鲜度、净价差、置信度 | 已完成：概览页新增 freshness 面板与净价差/置信度列 | 视觉阈值可继续调优 | 进入 FE-002 |
| FE-002 | frontend | 可调排序偏好与新鲜度策略 | P2 | done | 运营可切换排序方式与告警敏感度 | 已完成：新增排序模式和新鲜度策略下拉控制 | 仍为本地状态，后续可持久化到 URL/localStorage | 进入稳定性回归 |
| FE-003 | frontend | 视图偏好持久化 | P2 | done | 刷新后保留排序与新鲜度策略设置 | 已完成：`localStorage` 持久化 sortMode/freshnessPreset | 多端不同步属预期 | 进入稳定性回归 |
| OPS-001 | docs | 发布核对清单与回滚指引 | P1 | done | 上线前后有统一操作手册 | 已完成：新增 `docs/RELEASE_CHECKLIST.md` | 需按实际运维流程持续修订 | 进入发布演练 |
| ST6-001 | api/frontend | 阶段6 异常场景友好提示 | P1 | done | API 超时、空数据、探测失败有友好提示 | 已完成：last_detect_error 字段、前端超时/重试/错误分类、探测失败警告条 | 无 | 阶段6 验收 |

## 里程碑映射

- M1: `CG-001, CG-002, CG-003, DET-001, RUN-001`
- M2: `CG-004, CG-005`
- M3: `OPP-001, OPP-002`
- M4: 执行约束与双榜单（后续拆任务）

## 变更记录

| 日期 | 变更 | 说明 |
|---|---|---|
| 2026-03-10 | 初始化 Tracker | 建立首版任务、优先级与验收标准 |
| 2026-03-10 | 完成 CG-001 | 统一 LiquidityFetcher 鉴权 header，并增加单测覆盖 |
| 2026-03-10 | 完成 CG-002 | TokenSync 支持 TTL 刷新与链级增量，CLI 与服务端行为统一 |
| 2026-03-10 | 完成 CG-003 | LiquiditySync 支持可配置重试、指数退避与抖动策略 |
| 2026-03-10 | 完成 CG-004 | 增加负缓存 TTL，减少 404/无池重复请求 |
| 2026-03-10 | 完成 CG-005 | 增加 CoinGecko 预算管理（按月上限 + 按天节奏）并接入两条调用链路 |
| 2026-03-10 | 完成 DET-001 | 注册表刷新改为局部更新 + TTL，避免刷新失败导致缓存抖空 |
| 2026-03-10 | 完成 RUN-001 | Runner 加入并发上限与单路探测超时控制 |
| 2026-03-10 | 完成 M2-001 | 新增候选优先流动性同步，形成“高频子集 + 低频全量”双路径 |
| 2026-03-10 | 完成 M2-002 | 优先候选切换为价值评分（频次+强度+新鲜度） |
| 2026-03-10 | 完成 OPP-001 | 接入净收益模型 v1，输出净价差并按净值排序 |
| 2026-03-10 | 完成 OPP-002 | 增加置信度评分并接入净值并列时的二级排序 |
| 2026-03-10 | 完成 API-001 | `/api/overview` 增加 freshness 字段（updated_at + age_sec） |
| 2026-03-10 | 完成 FE-001 | 前端展示 freshness 与净价差/置信度关键指标 |
| 2026-03-10 | 完成 FE-002 | 前端增加排序偏好与新鲜度策略可调开关 |
| 2026-03-10 | 完成 FE-003 | 前端视图偏好持久化到 localStorage |
| 2026-03-10 | 完成 OPS-001 | 新增发布检查清单与回滚策略文档 |
| 2026-03-18 | 完成 ST6-001 | 阶段6 联调与优化：API 返回 last_detect_error；前端超时、重试、错误分类、探测失败警告 |

