# Web 模块

## 概述

Web 模块提供 Web Dashboard 服务，用于管理和监控套利系统。包括 HTTP API、WebSocket 实时推送、前端页面等功能。通过 `proto` 包定义的接口与 `trigger` 包交互，实现了良好的解耦。

## 核心功能

- **Dashboard 页面**：系统监控和管理界面
- **HTTP API**：RESTful API 接口
- **WebSocket 推送**：实时数据推送
- **Token 映射管理**：Token 地址到符号的映射
- **Contract 映射管理**：合约配置管理

## 关键文件

| 文件 | 职责 |
|------|------|
| `web.go` | Web 服务入口 |
| `dashboard.go` | Dashboard 结构和启动逻辑 |
| `web_api.go` | HTTP API 处理器 |
| `middleware.go` | HTTP 中间件 |
| `trigger_helper.go` | Trigger 相关辅助函数 |
| `handlers_monitor.go` | 监控相关处理器 |
| `token_mapping_sync.go` | Token 映射同步 |
| `ws/` | WebSocket 相关 |
| `templates/` | HTML 模板 |

## HTTP API 列表

### Trigger 管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/triggers` | 获取所有 Trigger 列表 |
| GET | `/api/trigger?symbol=XXX` | 获取指定 Trigger |
| POST | `/api/trigger` | 创建 Trigger |
| DELETE | `/api/trigger?symbol=XXX` | 删除 Trigger |

### Token 映射

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/token-mappings` | 获取所有 Token 映射 |
| POST | `/api/token-mapping` | 添加/更新 Token 映射 |
| DELETE | `/api/token-mapping?address=XXX` | 删除 Token 映射 |

### 监控和统计

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/positions` | 获取持仓信息 |
| GET | `/api/balances` | 获取余额信息 |
| GET | `/api/statistics` | 获取统计数据 |

## 使用示例

```go
import (
    "auto-arbitrage/internal/web"
    "auto-arbitrage/internal/trigger"
    "auto-arbitrage/internal/trigger/token_mapping"
)

// 获取管理器
tm := trigger.GetTriggerManager()
tmm := token_mapping.GetTokenMappingManager()
cmm := trigger.GetContractMappingManager()

// 创建 Dashboard
dashboard := web.NewDashboard(tm, tmm, cmm)

// 启动服务
dashboard.Start(":9090")
```

## 依赖关系

### 依赖的模块
- `proto` - 接口定义
- `trigger` - 触发器管理（通过接口）
- `position` - 仓位管理
- `statistics` - 统计数据
- `config` - 配置管理

### 被依赖的模块
- `cmd/arbitrage` - 主程序启动 Dashboard

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)




