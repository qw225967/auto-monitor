# Token 映射管理器使用文档

## 概述

`TokenMappingManager` 是一个用于管理代币合约地址（`ToTokenContractAddress`）和代币符号（`ToTokenSymbol`）之间双向映射关系的工具。它提供了内存映射管理和文件持久化功能，支持在应用启动时自动加载映射关系。

## 核心功能

1. **双向映射查询**：根据合约地址查询代币符号，或根据代币符号查询合约地址
2. **映射管理**：添加、更新、删除映射关系
3. **持久化存储**：将映射关系保存到 JSON 文件，启动时自动加载

## 基本使用

### 1. 获取管理器实例

```go
import "auto-arbitrage/internal/trigger"

mappingMgr := trigger.GetTokenMappingManager()
```

### 2. 启动时加载映射关系

在应用启动时（如 `main.go` 或初始化函数中）调用：

```go
if err := mappingMgr.LoadFromFile(); err != nil {
    logger.Errorf("加载 Token 映射关系失败: %v", err)
    // 如果文件不存在，会自动创建空映射，不会报错
}
```

### 3. 添加映射关系

```go
contractAddress := "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f"
symbol := "STABLE"

if err := mappingMgr.AddMapping(contractAddress, symbol); err != nil {
    logger.Errorf("添加映射关系失败: %v", err)
} else {
    // 保存到文件（可选，也可以批量操作后统一保存）
    if err := mappingMgr.SaveToFile(); err != nil {
        logger.Errorf("保存映射关系失败: %v", err)
    }
}
```

### 4. 查询映射关系

#### 根据合约地址查询代币符号

```go
address := "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f"
symbol, err := mappingMgr.GetSymbolByAddress(address)
if err != nil {
    logger.Errorf("查询失败: %v", err)
} else {
    logger.Infof("合约地址 %s 对应的代币符号: %s", address, symbol)
}
```

#### 根据代币符号查询合约地址

```go
symbol := "STABLE"
address, err := mappingMgr.GetAddressBySymbol(symbol)
if err != nil {
    logger.Errorf("查询失败: %v", err)
} else {
    logger.Infof("代币符号 %s 对应的合约地址: %s", symbol, address)
}
```

### 5. 删除映射关系

可以通过地址或符号删除：

```go
// 通过地址删除
if err := mappingMgr.RemoveMapping("0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f"); err != nil {
    logger.Errorf("删除失败: %v", err)
}

// 或通过符号删除
if err := mappingMgr.RemoveMapping("STABLE"); err != nil {
    logger.Errorf("删除失败: %v", err)
}

// 删除后记得保存
mappingMgr.SaveToFile()
```

## 在 wrapperOnChainSubscribe 中使用示例

以下是在 `trigger.go` 的 `wrapperOnChainSubscribe` 方法中的完整使用示例：

```go
func (t *Trigger) wrapperOnChainSubscribe(symbol string) {
    okOnChainClient := onchain.NewOkdex()
    okOnChainClient.Init()
    okOnChainClient.SetPriceCallback(t.OnChainPriceCallback())

    // ... 其他初始化代码 ...

    // 1. 初始化并加载 Token 映射关系
    mappingMgr := GetTokenMappingManager()
    if err := mappingMgr.LoadFromFile(); err != nil {
        t.logger.Warnf("加载 Token 映射关系失败: %v", err)
    }

    amountStr := fmt.Sprintf("%.0f", t.positionManager.GetDefaultSize())
    
    // 2. 从 SwapInfo 中获取地址和符号
    toTokenAddress := "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f"
    toTokenSymbol := "STABLE"
    
    // 3. 添加映射关系（如果不存在）
    if err := mappingMgr.AddMapping(toTokenAddress, toTokenSymbol); err != nil {
        t.logger.Errorf("添加映射关系失败: %v", err)
    } else {
        // 保存到文件
        if err := mappingMgr.SaveToFile(); err != nil {
            t.logger.Errorf("保存映射关系失败: %v", err)
        }
    }
    
    // 4. 使用映射关系查询（示例）
    // 如果只知道地址，可以通过映射获取符号
    if symbolFromMapping, err := mappingMgr.GetSymbolByAddress(toTokenAddress); err == nil {
        t.logger.Infof("从映射中获取的符号: %s", symbolFromMapping)
    }
    
    // 如果只知道符号，可以通过映射获取地址
    if addressFromMapping, err := mappingMgr.GetAddressBySymbol(toTokenSymbol); err == nil {
        t.logger.Infof("从映射中获取的地址: %s", addressFromMapping)
    }

    swapInfo := &model.SwapInfo{
        FromTokenSymbol:          "USDT",
        ToTokenSymbol:            toTokenSymbol,
        FromTokenContractAddress: "0x55d398326f99059ff775485246999027b3197955",
        ToTokenContractAddress:   toTokenAddress,
        ChainIndex:               "56",
        Amount:                   amountStr,
        DecimalsFrom:             "18",
        DecimalsTo:               "18",
        SwapMode:                 "exactIn",
        Slippage:                 "0.2",
        GasLimit:                 "300000",
        WalletAddress:            "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a",
    }

    okOnChainClient.StartSwap(swapInfo)
    t.onChainClient = okOnChainClient
}
```

## API 文档

### GetTokenMappingManager() *TokenMappingManager

获取 Token 映射管理器单例实例。

**返回值**：`*TokenMappingManager` - 管理器实例

---

### LoadFromFile() error

从文件加载映射关系到内存。如果文件不存在，会自动创建空映射。

**返回值**：
- `error` - 如果加载失败返回错误，文件不存在不会报错

**示例**：
```go
if err := mappingMgr.LoadFromFile(); err != nil {
    logger.Errorf("加载失败: %v", err)
}
```

---

### SaveToFile() error

将内存中的映射关系保存到文件。

**返回值**：
- `error` - 如果保存失败返回错误

**示例**：
```go
if err := mappingMgr.SaveToFile(); err != nil {
    logger.Errorf("保存失败: %v", err)
}
```

---

### AddMapping(contractAddress, symbol string) error

添加或更新映射关系（双向）。如果地址或符号已存在，会更新为新的映射关系。

**参数**：
- `contractAddress string` - 代币合约地址
- `symbol string` - 代币符号

**返回值**：
- `error` - 如果参数为空或操作失败返回错误

**示例**：
```go
err := mappingMgr.AddMapping(
    "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f",
    "STABLE",
)
```

---

### GetSymbolByAddress(contractAddress string) (string, error)

根据合约地址获取代币符号。

**参数**：
- `contractAddress string` - 代币合约地址

**返回值**：
- `string` - 代币符号
- `error` - 如果地址为空或未找到映射返回错误

**示例**：
```go
symbol, err := mappingMgr.GetSymbolByAddress("0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f")
if err != nil {
    // 处理错误
}
```

---

### GetAddressBySymbol(symbol string) (string, error)

根据代币符号获取合约地址。

**参数**：
- `symbol string` - 代币符号

**返回值**：
- `string` - 合约地址
- `error` - 如果符号为空或未找到映射返回错误

**示例**：
```go
address, err := mappingMgr.GetAddressBySymbol("STABLE")
if err != nil {
    // 处理错误
}
```

---

### RemoveMapping(contractAddressOrSymbol string) error

删除映射关系（双向删除）。可以通过地址或符号删除。

**参数**：
- `contractAddressOrSymbol string` - 合约地址或代币符号

**返回值**：
- `error` - 如果参数为空或未找到映射返回错误

**示例**：
```go
// 通过地址删除
err := mappingMgr.RemoveMapping("0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f")

// 或通过符号删除
err := mappingMgr.RemoveMapping("STABLE")
```

---

### GetAllMappings() map[string]string

获取所有映射关系（用于调试或导出）。返回地址到符号的映射副本。

**返回值**：
- `map[string]string` - 地址到符号的映射表（副本）

**示例**：
```go
allMappings := mappingMgr.GetAllMappings()
for address, symbol := range allMappings {
    logger.Infof("%s -> %s", address, symbol)
}
```

---

### SetFilePath(path string)

设置映射文件的存储路径（可选，用于自定义路径）。

**参数**：
- `path string` - 文件路径

**示例**：
```go
mappingMgr.SetFilePath("/custom/path/token_mapping.json")
```

---

## 文件格式

映射关系存储在 `data/token_mapping.json` 文件中，格式如下：

```json
[
  {
    "contractAddress": "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f",
    "symbol": "STABLE"
  },
  {
    "contractAddress": "0x55d398326f99059ff775485246999027b3197955",
    "symbol": "USDT"
  }
]
```

## 地址标准化

管理器会自动标准化地址格式：
- 统一转为小写
- 统一添加 `0x` 前缀（如果没有）
- 去除前后空格

例如：
- `0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f`
- `0X011EBE7D75E2C9D1E0BD0BE0BEF5C36F0A90075F`
- `011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f`

以上三种格式都会被标准化为：`0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f`

## 线程安全

所有方法都是线程安全的，可以在多个 goroutine 中并发调用。

## 注意事项

1. **启动时加载**：建议在应用启动时调用 `LoadFromFile()` 加载映射关系
2. **及时保存**：添加或修改映射后，记得调用 `SaveToFile()` 保存到文件
3. **文件路径**：默认文件路径为 `data/token_mapping.json`，可通过 `SetFilePath()` 自定义
4. **目录创建**：如果文件目录不存在，会自动创建
5. **映射更新**：如果地址或符号已存在，`AddMapping()` 会更新为新的映射关系，并删除旧的冲突映射

## 错误处理

所有方法都会返回 `error`，建议始终检查错误：

```go
if err := mappingMgr.AddMapping(address, symbol); err != nil {
    logger.Errorf("操作失败: %v", err)
    // 处理错误
}
```

## 完整示例

```go
package main

import (
    "auto-arbitrage/internal/trigger"
    "auto-arbitrage/internal/utils/logger"
)

func main() {
    // 获取管理器实例
    mappingMgr := trigger.GetTokenMappingManager()
    
    // 启动时加载映射关系
    if err := mappingMgr.LoadFromFile(); err != nil {
        logger.GetLoggerInstance().Sugar().Errorf("加载失败: %v", err)
    }
    
    // 添加映射
    if err := mappingMgr.AddMapping(
        "0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f",
        "STABLE",
    ); err != nil {
        logger.GetLoggerInstance().Sugar().Errorf("添加失败: %v", err)
    }
    
    // 保存到文件
    if err := mappingMgr.SaveToFile(); err != nil {
        logger.GetLoggerInstance().Sugar().Errorf("保存失败: %v", err)
    }
    
    // 查询使用
    symbol, err := mappingMgr.GetSymbolByAddress("0x011ebe7d75e2c9d1e0bd0be0bef5c36f0a90075f")
    if err != nil {
        logger.GetLoggerInstance().Sugar().Errorf("查询失败: %v", err)
    } else {
        logger.GetLoggerInstance().Sugar().Infof("查询结果: %s", symbol)
    }
}
```



