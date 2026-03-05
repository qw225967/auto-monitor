# Analytics 模块

## 概述

Analytics 模块负责参数分析和优化，包括价差区间分析、滑点预测、成本计算、最低价差阈值计算等功能。为 Trigger 模块提供决策支持。

## 核心功能

- **价差区间分析**：统计历史价差，找出最优交易区间
- **滑点预测**：估算链上滑点和交易所滑点
- **成本计算**：计算 Gas 费、手续费、价差变化缓冲
- **最低价差阈值**：计算单笔交易的最低盈利价差
- **交易周期分析**：分析触发频率，优化交易时机
- **可视化展示**：提供 HTTP 服务展示价差曲线和阈值线

## 关键文件

| 文件 | 职责 |
|------|------|
| `analytics.go` | 核心分析器实现 |
| `slippage.go` | 滑点预测 |
| `cost.go` | 成本计算 |
| `threshold.go` | 阈值计算 |
| `interval.go` | 价差区间分析 |
| `cycle.go` | 交易周期分析 |
| `visualizer.go` | 可视化展示 |

## API 说明

### Analyzer 接口

```go
type Analyzer interface {
    GetSlippage() (slippageA, slippageB float64)
    GetTotalCost() float64
    GetOptimalThreshold() (thresholdAB, thresholdBA float64)
    UpdatePriceData(priceA, priceB float64)
}
```

## 使用示例

```go
import "auto-arbitrage/internal/analytics"

analyzer := analytics.NewAnalyzer(config)
slippageA, slippageB := analyzer.GetSlippage()
thresholdAB, thresholdBA := analyzer.GetOptimalThreshold()
```

## 依赖关系

### 依赖的模块
- `model` - 数据模型
- `config` - 配置管理

### 被依赖的模块
- `trigger` - 使用分析结果进行决策
- `position` - 使用分析器计算价格

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)