# 加密货币搬砖监控系统 (Arbitrage Path Monitor)

价格发现 + 物理通路监控 + 监控面板

## 技术栈

- **后端**: Go 1.21+ (Gin)
- **前端**: React + TypeScript (Vite)

## 快速开始

### 一键启动（推荐）

```bash
./scripts/start.sh          # 启动后端 + 前端
./scripts/start.sh restart  # 重启
./scripts/start.sh stop     # 停止
```

启动后访问 http://localhost:5173

### 手动启动

```bash
# 后端
MOCK_MODE=1 go run ./cmd/server

# 前端（新终端）
cd frontend && npm install && npm run dev
```

### 2. 生产模式（连接 SeeingStone API）

```bash
# 复制配置并填入 Token
cp .env.example .env
# 编辑 .env，设置 SEEINGSTONE_API_TOKEN=你的JWT

# 后端（不设置 MOCK_MODE 即使用真实 API）
go run ./cmd/server

# 前端
cd frontend && npm run build && npm run preview
```

**真实 API 请求示例**：
```bash
curl -H "Authorization: Bearer YOUR_TOKEN" https://seeingstone.cloud/api/spreads
```

## 项目结构

```
├── cmd/server/          # 程序入口
├── internal/
│   ├── config/          # 配置
│   ├── source/          # 数据源（DataSource 接口，可扩展）
│   │   └── seeingstone/  # SeeingStone 价差 API
│   ├── aggregator/      # 聚合筛选
│   ├── detector/        # 路由探测（TODO: 待迁移真实实现）
│   ├── builder/         # 表格组装（TableBuilder 接口，可扩展）
│   ├── runner/          # 主流程编排
│   └── api/             # REST 接口
├── frontend/            # React 监控面板
└── config/              # YAML 配置
```

## 待迁移

- **路由探测模块**: `internal/detector/mock.go` 为模拟实现，需替换为真实的路由探测逻辑（HTTP 或本地包）

## 配置

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| SEEINGSTONE_API_URL | API 地址 | https://seeingstone.cloud |
| SEEINGSTONE_API_TOKEN | Bearer Token | - |
| SPREAD_THRESHOLD | 价差过滤阈值 (%) | 1.0 |
| FETCH_INTERVAL | 价差拉取间隔 (秒) | 10 |
| DETECT_INTERVAL | 通路探测间隔 (秒) | 30 |
| MOCK_MODE | 使用模拟数据 | false |
