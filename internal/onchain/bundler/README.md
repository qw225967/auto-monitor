# Bundler 支持

本模块提供了通过 Flashbots 和 48club 等 bundler 发送交易的功能，以降低 gas 费用。

## 支持的 Bundler

### 1. Flashbots
- **支持的链**: Ethereum 主网 (chainID = "1")
- **特点**: 通过 MEV 保护降低 gas 费用
- **需要**: 私钥用于签名请求

### 2. 48club
- **支持的链**: Ethereum 主网 (chainID = "1") 和 BSC (chainID = "56")
- **特点**: 支持多条链，通过 bundler 降低 gas 费用
- **需要**: API Key（可选）
- **48SoulPoint 签名**（可选）: 提供 48SoulPoint 成员私钥可以获得更好的服务
  - 更高的请求限流（根据会员等级：Entry/Gold/Platinum）
  - 更多的每 bundle 交易数量（6-50 个交易，取决于会员等级）
  - 详细的失败信息
  - WebSocket RPC 支持
  - 文档: https://docs.48.club/puissant-builder/48-soulpoint-benefits

## 使用方法

### 1. 初始化 Bundler

```go
import (
    "auto-arbitrage/internal/onchain/bundler"
    "auto-arbitrage/internal/onchain"
)

// 创建 bundler 管理器
bundlerMgr := bundler.NewManager()

// 添加 Flashbots bundler (仅支持 Ethereum)
flashbotsBundler, err := bundler.NewFlashbotsBundler(
    "0x你的私钥", // 用于签名请求的私钥
    "", // relay URL，空字符串使用默认值
)
if err == nil {
    bundlerMgr.AddBundler(flashbotsBundler)
}

// 添加 48club bundler (支持 Ethereum 和 BSC)
// 注意：48SoulPoint 私钥是可选的，但提供后可以获得更好的服务
// 文档: https://docs.48.club/puissant-builder/48-soulpoint-benefits
fortyEightClubBundler, err := bundler.NewFortyEightClubBundler(
    "你的48club API Key",        // 可选，用于认证和限流
    "",                          // API URL，空字符串使用默认值
    "你的48SoulPoint私钥",        // 可选，用于签名以获得更好的服务
)
if err == nil {
    bundlerMgr.AddBundler(fortyEightClubBundler)
}

// 创建 okdex 客户端
client := onchain.NewOkdex()

// 设置 bundler 管理器并启用
if okdexClient, ok := client.(*onchain.Okdex); ok {
    okdexClient.SetBundlerManager(bundlerMgr, true) // true 表示启用 bundler
}
```

### 2. 自动选择 Bundler

当启用 bundler 后，系统会自动：
1. 根据链 ID 选择支持的 bundler
2. 尝试通过 bundler 发送交易
3. 如果 bundler 失败，自动 fallback 到普通广播方式

### 3. 向所有 Bundler 发送

```go
// 向所有支持的 bundler 发送 bundle（提高成功率）
results, errors := bundlerMgr.SendBundleToAll(signedTx, chainID)
for bundlerName, bundleHash := range results {
    fmt.Printf("Bundler %s: %s\n", bundlerName, bundleHash)
}
```

## 配置

### 环境变量（建议）

```bash
# Flashbots 私钥（用于签名请求）
FLASHBOTS_PRIVATE_KEY=0x...

# 48club API Key（可选）
FORTY_EIGHT_CLUB_API_KEY=your_api_key

# 48SoulPoint 成员私钥（可选，用于获得更好的服务）
# 文档: https://docs.48.club/puissant-builder/48-soulpoint-benefits
FORTY_EIGHT_SOUL_POINT_PRIVATE_KEY=0x...

# 是否启用 bundler（true/false）
USE_BUNDLER=true
```

## 注意事项

1. **Bundle Hash vs Transaction Hash**: 
   - Bundler 返回的是 bundle hash，不是交易 hash
   - 需要通过其他方式（如 RPC 查询）获取实际的交易 hash

2. **Gas 费用**:
   - Bundler 可能会降低 gas 费用，但不保证
   - 实际效果取决于网络状况和 bundler 策略

3. **失败处理**:
   - 如果 bundler 发送失败，系统会自动 fallback 到普通广播
   - 确保普通广播方式仍然可用

4. **链支持**:
   - Flashbots 仅支持 Ethereum 主网
   - 48club 支持 Ethereum 和 BSC
   - 其他链需要使用普通广播方式

5. **48SoulPoint 签名**:
   - 提供 48SoulPoint 成员私钥可以获得更好的服务
   - 根据会员等级（Entry/Gold/Platinum）获得不同的限流和功能
   - 签名方法：对所有交易哈希进行拼接，然后 Keccak256 哈希，最后用私钥签名
   - 详细文档: https://docs.48.club/puissant-builder/48-soulpoint-benefits

## API 参考

### Bundler 接口

```go
type Bundler interface {
    SendBundle(signedTx string, chainID string) (string, error)
    GetBundleStatus(bundleHash string, chainID string) (string, error)
    SupportsChain(chainID string) bool
    GetName() string
}
```

### Manager 方法

```go
// 添加 bundler
AddBundler(bundler Bundler)

// 根据链 ID 获取支持的 bundler
GetBundler(chainID string) (Bundler, error)

// 向所有支持的 bundler 发送
SendBundleToAll(signedTx string, chainID string) (map[string]string, []error)
```

