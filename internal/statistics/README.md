# Statistics 模块

## 概述

Statistics 模块负责统计监控，收集和展示交易数据、价差数据、价格数据等时间序列数据，为系统监控和分析提供数据支持。

## 核心功能

- **滑点统计**：平均值、最大值、最小值、90 分位值
- **成本统计**：成本消耗币数量、成本消耗占比
- **Size 统计**：交易量大小（USDT 价值）
- **时间序列数据**：成交数据、价差数据、价格数据
- **Trigger 统计**：收益/亏损、成交次数、交易量
- **钱包总展示**：钱包余额汇总

## 关键文件

| 文件 | 职责 |
|------|------|
| `statistics.go` | 统计管理器主实现 |
| `design.md` | 设计文档 |
| `monitor/` | 监控相关 |

## 架构设计

```
StatisticsManager (单例)
├── symbolStats map[string]*SymbolStatistics
├── recordInterval (500ms)
├── 定时记录协程
└── 实时记录接口

SymbolStatistics
├── TimeSeriesData []*TimeSeriesPoint
├── SlippageStats *SlippageStatistics
├── CostStats *CostStatistics
├── SizeStats *SizeStatistics
├── TradeRecords []*TradeRecord
├── PriceDiffRecords []*PriceDiffRecord
├── PriceRecords []*PriceRecord
├── TriggerStats *TriggerStatistics
└── WalletStats *WalletStatistics
```

## API 说明

### 记录接口

| 方法 | 说明 |
|------|------|
| `RecordSlippage(symbol, side, value)` | 记录滑点 |
| `RecordCost(symbol, costInCoin, costPercent)` | 记录成本 |
| `RecordSize(symbol, size, valueUSDT)` | 记录 Size |
| `RecordPriceDiff(symbol, diffAB, diffBA)` | 记录价差 |
| `RecordPrice(symbol, exchangePrice, onchainPrice)` | 记录价格 |
| `RecordTrade(symbol, trade)` | 记录成交 |
| `RecordWallet(walletInfo)` | 记录钱包信息 |

### 查询接口

| 方法 | 说明 |
|------|------|
| `GetSymbolStatistics(symbol)` | 获取 symbol 的统计数据 |
| `GetTimeSeriesData(symbol, startTime, endTime)` | 获取时间序列数据 |
| `GetAllStatistics()` | 获取所有统计数据 |

## 数据记录策略

### 定时记录（每 500ms）
- 价差数据
- 价格数据
- 滑点数据
- 成本数据
- Size 数据

### 实时记录
- 成交数据（订单执行时立即记录）
- Trigger 统计（订单执行时立即更新）

## 依赖关系

### 依赖的模块
- `model` - 数据模型

### 被依赖的模块
- `trigger` - 记录交易数据
- `web` - 展示统计数据
- `position` - 使用统计数据

## 变更历史

参见 [CHANGELOG](../../docs/CHANGELOG.md)
