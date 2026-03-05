# Utils 模块

## 概述

Utils 模块提供通用的工具函数和辅助功能，可被项目中的任何模块使用。

## 子模块

| 子模块 | 路径 | 职责 |
|--------|------|------|
| **logger** | `logger/` | 日志工具（基于 zap） |
| **rest** | `rest/` | REST 客户端 |
| **time** | `time/` | 时间工具 |
| **mysql** | `mysql/` | MySQL 数据库工具 |
| **common** | `common/` | 通用工具函数 |
| **errors** | `errors/` | 错误处理 |
| **notify** | `notify/` | 通知服务（Telegram 等） |
| **parallel** | `parallel/` | 并发工具 |
| **test** | `test/` | 测试辅助 |

## 主要功能

### Logger

```go
import "auto-arbitrage/internal/utils/logger"

log := logger.GetLoggerInstance().Sugar()
log.Info("消息", zap.String("key", "value"))
```

### Parallel

```go
import "auto-arbitrage/internal/utils/parallel"

rg := parallel.NewRoutineGroup()
rg.GoSafe(func() {
    // 协程逻辑
})
rg.Wait()
```

### REST Client

```go
import "auto-arbitrage/internal/utils/rest"

client := rest.NewRestClient()
client.InitRestClient()
resp, err := client.Get(url, headers)
```

## 依赖关系

### 依赖的外部库
- `go.uber.org/zap` - 日志
- `github.com/go-sql-driver/mysql` - MySQL

### 被依赖的模块
- 几乎所有模块都依赖 utils 模块

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)

