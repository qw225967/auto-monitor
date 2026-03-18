# 套利监控完整需求实现计划

> 补全 CEX-CEX / CEX-DEX / DEX-DEX 三类套利机会的统一展示

---

## 一、需求总览

| 类型 | 说明 | 数据来源 | 当前状态 |
|------|------|----------|----------|
| **CEX-CEX** | 交易所间价差 | SeeingStone `/api/spreads` | ✅ 已实现 |
| **CEX-DEX** | 交易所价格 vs 链上 DEX 价格 | SeeingStone + OKEx DEX Quote | ❌ 待实现 |
| **DEX-DEX** | 同币种不同链的 DEX 价差 | OKEx DEX Quote（多链） | ❌ 待实现 |

**目标**：在展示列表前完成 token 信息补全，定时获取链上价格，将三类机会统一展示在一张表中，仅展示超过阈值的项。

---

## 二、数据流与依赖

```
SeeingStone API (10s)
       │
       ├──► 价差数据 (SpreadItem[]) ──► 聚合筛选 ──► CEX-CEX 机会
       │
       └──► 全量 symbol 去重 ──► Token 信息补全 ──► token_registry.json
                                         │
                                         ▼
                              OKEx DEX Quote (定时) ──► 链上价格
                                         │
                                         ├──► CEX 价格 vs DEX 价格 ──► CEX-DEX 机会
                                         └──► 链A DEX vs 链B DEX ──► DEX-DEX 机会
                                         │
                                         ▼
                              统一表格（CEX-CEX + CEX-DEX + DEX-DEX）
```

---

## 三、分阶段实现计划

### 阶段 7：Token 信息集成到主流程

**目标**：在主服务启动/运行中，从 SeeingStone 全量 symbol 去重，按需补全 token 信息，优先使用本地缓存。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 7.1 | 主流程集成 token 补全 | 在 Ticker B 或独立 Ticker 中，拉取 SeeingStone 全量 symbol，提取资产列表 | 资产列表来自 SeeingStone，不依赖 tokensync |
| 7.2 | 增量补全逻辑 | 对每个资产：若 `tokenregistry.HasAsset(rd, asset)` 则跳过；否则调用 CoinGecko 拉取并 `MergeIncremental` | 有缓存则跳过，无缓存则拉取并保存 |
| 7.3 | 补全触发时机 | 方案 A：每次 Ticker B 前检查；方案 B：独立 Ticker（如 5 分钟）后台补全 | 不阻塞主流程，可配置 |
| 7.4 | 存储路径 | 使用 `config` 或环境变量指定 `TOKEN_REGISTRY_PATH`，默认 `data/token_registry.json` | 与 tokensync 共用同一文件 |

**涉及文件**：
- `cmd/server/main.go`：增加 token 补全 goroutine
- `internal/tokenregistry/`：复用 `AssetsFromSeeingStone`、`Storage`、`CoinGeckoFetcher`
- `config/settings.yaml`：新增 `token_registry_path`、`token_sync_interval`

---

### 阶段 8：链上价格获取

**目标**：使用 OKEx DEX v6 Quote 接口，按 (asset, chainID) 获取 token 的 DEX 价格。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 8.1 | 价格查询接口 | 封装 `QueryDexPrice(asset, chainID string) (price float64, err error)` | 输入 asset+chain，输出 USDT 计价价格 |
| 8.2 | Quote 调用方式 | `fromToken=目标 token 地址`，`toToken=USDT 地址`，`amount=1`（1 单位），解析 `toTokenAmount` 换算为价格 | 需从 token registry 取 address、decimals、chainID |
| 8.3 | ChainID 映射 | OKEx 使用 `chainIndex`，需维护 `chainID -> chainIndex` 映射（参考 `coingecko.go` 的 `platformToChainID` 反向） | 调用时使用正确 chainIndex |
| 8.4 | 批量与限流 | 多 (asset, chain) 并发查询，控制 QPS，单次失败不中断 | 可配置并发数、间隔 |
| 8.5 | 缓存 | 链上价格缓存（如 30s–1min），避免每次表格刷新都全量拉取 | 减少 API 调用 |

**涉及文件**：
- `internal/onchain/`：新增 `PriceFetcher` 或扩展 `OnchainClient`
- `internal/price/`（新建）：`chain_price.go` 封装 DEX 价格获取逻辑
- `constants/constants.go`：chainID ↔ chainIndex 映射

**Quote 价格换算**：
- 请求：`fromToken=TOKEN_X`, `toToken=USDT`, `amount=1`（1 个 X）
- 响应：`toTokenAmount` = 得到的 USDT 数量（最小单位）
- 价格：`price = toTokenAmount / 10^usdt_decimals`

---

### 阶段 9：CEX 价格获取（CEX-DEX 对比前置）

**目标**：获取各交易所的 token 价格，用于与 DEX 价格对比。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|------|----------|
| 9.1 | 确认 SeeingStone 数据结构 | 检查 `/api/spreads` 是否返回 `buy_price`、`sell_price` 或等价字段 | 若有则直接使用 |
| 9.2 | 若无则对接行情源 | 可选：交易所公开行情 API（如 Binance ticker）、或 SeeingStone 其他端点 | 能获取 CEX 买卖价 |
| 9.3 | 扩展 SpreadItem | 若需新字段，在 `model.SpreadItem` 增加 `BuyPrice`、`SellPrice`（可选） | 与现有逻辑兼容 |

**注意**：若 SeeingStone 仅提供 `spread_percent` 而无绝对价格，CEX-DEX 需通过「价差百分比」推导：例如 DEX 价 P，则 CEX 买价可近似为 `P * (1 - spread/100)` 或需额外行情源。计划中预留扩展点。

---

### 阶段 10：价差计算与机会生成

**目标**：计算 CEX-DEX、DEX-DEX 价差，生成与 CEX-CEX 同结构的「机会行」。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 10.1 | CEX-DEX 价差 | 对每个 (symbol, exchange)：`spread = |cex_price - dex_price| / dex_price * 100` | 超过阈值则加入列表 |
| 10.2 | DEX-DEX 价差 | 对每个 (asset, chainA, chainB)：`spread = |price_A - price_B| / min(price_A, price_B) * 100` | 超过阈值则加入列表 |
| 10.3 | 统一机会模型 | 扩展 `OverviewRow` 或新建 `ArbitrageOpportunity`，含 `Type: "cex_cex"|"cex_dex"|"dex_dex"` | 三种类型结构统一 |
| 10.4 | 合并到 Runner | Runner 输出 = CEX-CEX 结果 + CEX-DEX 结果 + DEX-DEX 结果 | 按价差降序排序 |

**数据结构建议**：

```go
// ArbitrageOpportunity 统一套利机会（三种类型）
type ArbitrageOpportunity struct {
    Type             string  // "cex_cex" | "cex_dex" | "dex_dex"
    Symbol           string  // 如 POWERUSDT
    SpreadPercent    float64
    BuySource        string  // CEX 名 或 "Chain_56"
    SellSource       string  // CEX 名 或 "Chain_1"
    AvailablePathCount int   // CEX-CEX 有；CEX-DEX/DEX-DEX 可为 0 或 N/A
    DetailPaths      []DetailPathRow
}
```

---

### 阶段 11：表格组装与 API 扩展

**目标**：Builder 消费三类机会，输出统一表格；API 返回扩展结构。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 11.1 | 扩展 AggregatedInput | 增加 `CexDexOpportunities`、`DexDexOpportunities` 字段 | Runner 可传入三类数据 |
| 11.2 | ArbitrageBuilder 扩展 | 合并 CEX-CEX、CEX-DEX、DEX-DEX，按 `SpreadPercent` 降序，去重/合并逻辑 | 一张表展示全部 |
| 11.3 | 前端展示 | 表格增加「类型」列：CEX-CEX / 交易所-链 / 链-链 | 用户可区分来源 |
| 11.4 | 路径展示 | CEX-DEX、DEX-DEX 的 `PathDisplay` 可简化为「交易所 ↔ 链」或「链A ↔ 链B」 | 与 CEX-CEX 风格一致 |

---

### 阶段 12：主流程编排与配置

**目标**：整合所有 Ticker，配置化，保证 10s 价差、30s 探测、链上价格、token 补全协调运行。

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 12.1 | Ticker 设计 | Ticker A (10s): 价差；Ticker B (30s): 探测 + 表格；Ticker C (60s): 链上价格；Ticker D (5min): token 补全 | 互不阻塞，可配置间隔 |
| 12.2 | 配置项 | `CHAIN_PRICE_INTERVAL`、`TOKEN_SYNC_INTERVAL`、`CHAIN_PRICE_CACHE_TTL` | 支持环境变量覆盖 |
| 12.3 | 初始化顺序 | 启动时加载 token registry，必要时先跑一轮 token 补全（或依赖 tokensync 预填充） | 链上价格查询不因缺 token 而大量失败 |

---

## 四、配置项补充

| 配置项 | 说明 | 默认值 | 环境变量 |
|--------|------|--------|----------|
| `token_registry_path` | Token 信息 JSON 路径 | `data/token_registry.json` | `TOKEN_REGISTRY_PATH` |
| `token_sync_interval` | Token 补全间隔 (秒) | `300` (5min) | `TOKEN_SYNC_INTERVAL` |
| `chain_price_interval` | 链上价格拉取间隔 (秒) | `60` | `CHAIN_PRICE_INTERVAL` |
| `chain_price_cache_ttl` | 链上价格缓存时长 (秒) | `30` | `CHAIN_PRICE_CACHE_TTL` |
| `chain_price_concurrency` | 链上价格并发数 | `5` | `CHAIN_PRICE_CONCURRENCY` |

---

## 五、依赖关系

```
阶段 7 (Token 集成) ──► 阶段 8 (链上价格) ──► 阶段 10 (价差计算)
       │                        │
       │                        └── 依赖 token registry (address, decimals, chainID)
       │
       └── 阶段 9 (CEX 价格) ──► 阶段 10
                    │
                    └── 可与 7、8 并行开发，最后合并

阶段 10 ──► 阶段 11 (表格+API) ──► 阶段 12 (编排)
```

---

## 六、风险与应对

| 风险 | 影响 | 应对 |
|------|------|------|
| CoinGecko 限流 | Token 补全变慢 | 增加间隔、退避重试；tokensync 预填充 |
| OKEx Quote 限流 | 链上价格拉取失败 | 缓存延长、降低并发、分批拉取 |
| chainID/chainIndex 不一致 | 报价失败 | 维护映射表，支持配置覆盖 |
| SeeingStone 无价格字段 | CEX-DEX 无法直接计算 | 对接交易所行情 API 或暂不实现 CEX-DEX |

---

## 七、实施顺序建议

1. **阶段 7**：Token 集成（复用 tokensync 逻辑，接入主流程）✅
2. **阶段 8**：链上价格（独立模块，可单测）✅
3. **阶段 9**：CEX 价格（SpreadItem 扩展 BuyPrice/SellPrice）✅
4. **阶段 10**：价差计算（CEX-DEX、DEX-DEX）✅
5. **阶段 11**：表格与 API 扩展（类型列、合并展示）✅
6. **阶段 12**：编排与配置（多 Ticker 协调）✅

---

*文档版本：V1.0 | 更新日期：2026-03-05*
