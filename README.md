# 加密货币搬砖监控系统 (Arbitrage Path Monitor)

价格发现 + 物理通路监控 + 监控面板

## 技术栈

- **后端**: Go 1.21+ (Gin)
- **前端**: React + TypeScript (Vite)

## 快速开始

### 1. 开发模式（使用模拟数据）

```bash
# 后端
MOCK_MODE=1 go run ./cmd/server

# 前端（新终端）
cd frontend && npm install && npm run dev
```

访问 http://localhost:5173

### 2. 生产模式（连接 SeeingStone API）

```bash
# 复制配置
cp .env.example .env
# 编辑 .env，填入 SEEINGSTONE_API_TOKEN

# 后端
go run ./cmd/server

# 前端
cd frontend && npm run build && npm run preview
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
