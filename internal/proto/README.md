# Proto 包 - 接口协议定义层

## 概述

`proto` 包定义了系统中各个模块之间的接口协议。这些接口用于解耦不同模块之间的依赖关系，使得模块之间通过接口交互而不是直接依赖具体实现。

## 设计原则

1. **接口隔离**：只定义对外暴露的必要方法
2. **避免循环依赖**：proto 包不依赖其他业务包
3. **易于测试**：接口可以轻松 mock 进行单元测试
4. **向后兼容**：接口变更需要谨慎，保持向后兼容

## 接口定义

### TriggerManager 接口

定义了 Trigger 管理器的接口，包括：
- Trigger 的增删改查
- Trigger 的创建和配置
- 上下文管理

### Trigger 接口

定义了单个 Trigger 的接口，包括：
- 基本信息获取（ID、Symbol、Status）
- 配置管理（方向启用、阈值区间、Telegram 通知）
- 数据获取（最优阈值、滑点数据）
- 生命周期管理（启动、停止）
- 数据清理（清空价差数据）

### TokenMappingManager 接口

定义了 Token 映射管理器的接口，包括：
- 映射关系的增删改查
- 文件持久化

## 使用方式

其他模块（如 `web` 包）应该依赖 `proto` 包中的接口，而不是直接依赖 `trigger` 包的具体实现。

```go
import "auto-arbitrage/internal/proto"

type Dashboard struct {
    triggerManager proto.TriggerManager  // 依赖接口，而非具体实现
}
```

## 实现

`trigger` 包中的 `TriggerManager` 和 `Trigger` 类型隐式实现了这些接口（Go 的接口是隐式实现的）。




