# 加密货币搬砖监控系统 - 可执行实现计划 (V2.0)

> 基于需求与数据接口定义 V1.0 及实现方案修正 V2.0 整理

---

## 一、项目概览

| 项目 | 说明 |
|------|------|
| 系统名称 | 加密货币搬砖监控系统 (Arbitrage Path Monitor) |
| 核心功能 | 价格发现 + 物理通路监控 + 监控面板 |
| 数据源 | SeeingStone API (`/api/spreads`) |
| 数据获取频率 | 10 秒 |
| 全 symbol 通路探测频率 | 30 秒 |
| 当前状态 | 空白项目，需从零搭建 |

---

## 二、技术栈（已确认）

| 层级 | 方案 | 说明 |
|------|------|------|
| 全栈（后端） | **Go 1.21+** (标准库 + Gin/Echo) | 已确认采用 Go，goroutine 并发、10s 拉价 + 30s 全量探测 |
| Web 前端 | React + TypeScript | 监控面板：表格、折叠详情、自动刷新 |
| 路由探测 | 独立模块/服务 | 输入 `(symbol, buy_exchange, sell_exchange)`，输出物理通路数组 |
| 配置 | `.env` + YAML | API 地址、Token、阈值等 |

> 注：技术栈已确认，后端全栈 Go，无 Python/Node 等备选。

---

## 三、系统架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           前端 (React)                                    │
│  ┌─────────────────────┐  ┌─────────────────────────────────────────┐   │
│  │ 聚合概览表 (10s 价差 / 30s 通路) │  │ 下钻详情表 (折叠)                         │   │
│  └─────────────────────┘  └─────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
                                    │ WebSocket / REST
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                           后端 (Go)                                       │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
│  │ 数据获取层   │→ │ 聚合筛选层   │→ │ 路由探测层   │→ │ 表格组装层   │  │
│  │ (10s 拉价)   │  │ (按symbol)   │  │ (30s 全量探测)│  │ (主表+详情)  │  │
│  └──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────┐
│ 外部依赖                                                                  │
│  • SeeingStone API (GET /api/spreads, Bearer Token)                      │
│  • 路由探测模块 (symbol, buy_exchange, sell_exchange) → Hops[]            │
│  • 各交易所充提状态 API (可选，由探测模块内部封装)                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 四、分阶段实现计划

### 阶段 0：项目初始化 (预计 0.5 天)

| 序号 | 任务 | 产出 | 验收标准 |
|------|------|------|----------|
| 0.1 | 创建项目结构 | `cmd/`, `internal/`, `frontend/`, `config/` | 目录清晰 |
| 0.2 | 后端依赖 | `go.mod`, `go.sum` | Gin/Echo、resty、viper 等 |
| 0.3 | 前端依赖 | `package.json` | React, TypeScript, 表格组件 |
| 0.4 | 环境配置 | `.env.example`, `config/settings.yaml` | API_URL, TOKEN, SPREAD_THRESHOLD |

**目录结构建议：**

```
auto-monitor/
├── cmd/
│   └── server/
│       └── main.go          # 程序入口
├── internal/
│   ├── config/
│   │   └── config.go        # 配置加载
│   ├── fetcher/
│   │   └── fetcher.go       # 阶段1: 数据获取
│   ├── aggregator/
│   │   └── aggregator.go    # 阶段2: 聚合筛选
│   ├── detector/
│   │   └── detector.go      # 阶段3: 路由探测
│   ├── builder/
│   │   └── builder.go       # 阶段4: 表格组装
│   └── api/
│       └── handler.go       # REST/WebSocket 接口
├── frontend/
│   ├── src/
│   │   ├── components/
│   │   │   ├── OverviewTable.tsx
│   │   │   └── DetailTable.tsx
│   │   └── App.tsx
│   └── package.json
├── config/
│   └── settings.yaml
├── go.mod
├── .env.example
└── IMPLEMENTATION_PLAN.md
```

---

### 阶段 1：数据获取层 (预计 1 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 1.1 | HTTP 客户端 | `net/http` 或 `resty`，Bearer Token 鉴权 | 能成功请求 `/api/spreads` |
| 1.2 | 10 秒轮询循环 | `time.Ticker(10*time.Second)` + goroutine | 每 10s 拉取一次价差数据 |
| 1.3 | 容错处理 | 超时 `context.WithTimeout` 10s，失败时 `log` 并继续下一轮 | 单次失败不中断循环 |
| 1.4 | 数据解析 | `encoding/json` 解析，提取 `data` 数组 | 得到 `[]SpreadItem` |

**接口定义：**

```go
// 输入 JSON 单条结构
type SpreadItem struct {
    Symbol        string  `json:"symbol"`
    BuyExchange   string  `json:"buy_exchange"`
    SellExchange  string  `json:"sell_exchange"`
    SpreadPercent float64 `json:"spread_percent"`
    UpdatedAt     string  `json:"updated_at"`
}
```

---

### 阶段 2：聚合与筛选层 (预计 1 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 2.1 | 按 symbol 分组 | `map[string][]PathItem` 遍历聚合 | `{symbol: [path1, path2, ...]}` |
| 2.2 | 路径结构 | 每个 path: `PathItem{symbol, buy_exchange, sell_exchange, spread_percent}` | 包含买卖方信息 |
| 2.3 | 阈值过滤 | `spread_percent >= SPREAD_THRESHOLD` | 可配置，默认建议 1.0 |
| 2.4 | 输出格式 | `map[string][]PathItem` | 供阶段 3 消费 |

**数据结构：**

```go
type PathItem struct {
    Symbol        string
    BuyExchange   string
    SellExchange  string
    SpreadPercent float64
}

// 输出: map[string][]PathItem，如 map["POWERUSDT"]{...}
```

---

### 阶段 3：路由探测层 (预计 2–3 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 3.1 | 探测模块接口 | 定义 `DetectRoutes(symbol, buyEx, sellEx) ([]PhysicalPath, error)` | 接口清晰 |
| 3.2 | 集成/迁移 | 若为外部服务：HTTP 调用；若为本地包：直接 import | 能返回物理通路列表 |
| 3.3 | 30s 全量探测 | 独立 `time.Ticker(30*time.Second)`，对当前聚合结果做全 symbol 通路探测 | 每 30s 触发一轮 |
| 3.4 | Hop 状态校验 | 对每条路径的每个 Hop 调用充提 API | 返回 ✅/⚠️/❌ |
| 3.5 | 链路结构化 | 标准化输出：`PathID`, `Hops`, `Status` | 供阶段 4 使用 |

**物理路径结构：**

```go
type Hop struct {
    FromNode  string // 如 "BITGET"
    EdgeDesc  string // 如 "提现BSC"
    ToNode    string // 如 "BSC链"
    Status    string // "ok" | "maintenance" | "unavailable"
}

type PhysicalPath struct {
    PathID        string // 如 "Path_01"
    Hops          []Hop
    OverallStatus string // "ok" | "maintenance" | "unavailable"
}
```

**Mock 方案（若探测模块未就绪）：**

- 返回 1–2 条模拟路径，状态随机或固定，便于前后端联调。

---

### 阶段 4：表格组装层 (预计 1 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 4.1 | 主表汇总 | 每个 symbol 取 `max(spread_percent)` 的 path | 展示最高价差 |
| 4.2 | 可用通路数 | 统计 `overall_status == "ok"` 的 PhysicalPath 数量 | 数字准确 |
| 4.3 | 下钻详情 | 该 symbol 下所有 PhysicalPath，按 path_id 排序 | 可展开查看 |
| 4.4 | 数据出口 | 组装为前端可直接消费的 JSON | 结构稳定 |

**主表输出结构：**

```json
{
  "overview": [
    {
      "symbol": "POWERUSDT",
      "path_display": "BITGET → GATE",
      "buy_exchange": "BITGET",
      "sell_exchange": "GATE",
      "spread_percent": 20.38,
      "available_path_count": 2,
      "detail_paths": [
        {
          "path_id": "Path_01",
          "physical_flow": "BITGET → (提现BSC) → BSC链 → ... → GATE",
          "status": "ok"
        }
      ]
    }
  ]
}
```

---

### 阶段 5：前端监控界面 (预计 2 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 5.1 | 聚合概览表 | 表格列：币种、路径、原始价差、可用通路数、操作 | 符合 UI 需求 |
| 5.2 | 自动刷新 | 价差 10s 更新，通路状态 30s 更新（或 WebSocket 推送） | 数据更新 |
| 5.3 | 下钻详情 | 点击 [查看详情] 展开/折叠该行详情 | 折叠交互正常 |
| 5.4 | 状态显示 | ✅ 畅通、⚠️ 维护中、❌ 不可用 | 颜色/图标区分 |
| 5.5 | 精度保留 | 原始价差保留 API 返回精度 | 不四舍五入 |

---

### 阶段 6：联调与优化 (预计 1 天)

| 序号 | 任务 | 实现要点 | 验收标准 |
|------|------|----------|----------|
| 6.1 | 端到端联调 | 后端 → 前端完整链路 | 数据正确展示 |
| 6.2 | 异常场景 | API 超时、空数据、探测失败 | 有友好提示 |
| 6.3 | 性能 | 10s 拉价不阻塞；30s 探测轮内完成全量通路校验 | 满足双周期刷新 |

---

## 五、关键配置项

| 配置项 | 说明 | 默认值 | 环境变量 |
|--------|------|--------|----------|
| `API_BASE_URL` | SeeingStone API 地址 | `https://seeingstone.cloud` | `SEEINGSTONE_API_URL` |
| `API_TOKEN` | Bearer Token | - | `SEEINGSTONE_API_TOKEN` |
| `SPREAD_THRESHOLD` | 价差过滤阈值 (%) | `1.0` | `SPREAD_THRESHOLD` |
| `FETCH_INTERVAL` | 价差拉取间隔 (秒) | `10` | `FETCH_INTERVAL` |
| `DETECT_INTERVAL` | 全 symbol 通路探测间隔 (秒) | `30` | `DETECT_INTERVAL` |
| `REQUEST_TIMEOUT` | HTTP 超时 (秒) | `10` | `REQUEST_TIMEOUT` |

---

## 六、依赖关系与里程碑

```
阶段0 ──► 阶段1 ──► 阶段2 ──► 阶段3 ──► 阶段4 ──► 阶段5 ──► 阶段6
  │         │         │         │         │         │
  │         │         │         │         │         └── 前端依赖 4 的输出
  │         │         │         │         └── 表格组装依赖 3 的输出
  │         │         │         └── 探测依赖 2 的 path 列表
  │         │         └── 聚合依赖 1 的 data
  │         └── 数据获取独立
  └── 所有阶段依赖

双周期设计：
  • Ticker A (10s)：拉取价差 API → 聚合筛选 → 更新缓存
  • Ticker B (30s)：读取最新缓存 → 全 symbol 通路探测 → 表格组装 → 推前端
```

| 里程碑 | 完成标志 | 预计耗时 |
|--------|----------|----------|
| M1 | 能拉取并解析 spreads API | 1.5 天 |
| M2 | 能按 symbol 聚合并过滤 | 2.5 天 |
| M3 | 能调用探测模块并得到物理路径 | 4–5 天 |
| M4 | 后端完整输出主表+详情 | 5–6 天 |
| M5 | 前端完整展示，10s 价差 + 30s 通路刷新 | 7–8 天 |
| M6 | 联调通过，可交付 | 8–9 天 |

---

## 七、风险与应对

| 风险 | 影响 | 应对 |
|------|------|------|
| 路由探测模块未就绪 | 阶段 3 阻塞 | 使用 Mock 数据先行开发，接口约定清晰 |
| API 限流 | 10s 拉价可能被限 | 增加重试退避，或与 API 方协商 |
| 充提 API 不可用 | Hop 状态无法获取 | 探测模块内部降级，返回「未知」状态 |
| 数据量过大 | 前端渲染慢 | 分页或虚拟滚动，后端做聚合上限 |

---

## 八、下一步行动

1. **确认路由探测模块**：是否已有现成实现？接口形式（HTTP/本地调用）？
2. **执行阶段 0**：初始化 Go 项目结构（`go mod init`），创建配置文件与依赖声明。
3. **按阶段顺序推进**：每完成一阶段进行自测，再进入下一阶段。

---

*文档版本：V2.1 | 更新日期：2026-03-05 | 技术栈：全栈 Go，Web 前端 React + TypeScript*
