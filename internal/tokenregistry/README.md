# Token Registry - 全链 Token 信息同步

## 功能

1. **仅搜符合阈值的 token**：从 SeeingStone 拉取价差，过滤 `spread_percent >= threshold` 的 symbol，提取资产
2. **本地已有则使用本地**：若 `data/token_registry.json` 已含该资产，跳过 CoinGecko 请求
3. **增量保存**：仅对本地未有的资产拉取，合并后写入

## 存储格式

`data/token_registry.json`:

```json
{
  "assets": {
    "USDT": {
      "1": { "address": "0xdac17f...", "decimals": 6, "symbol": "usdt", "updated_at": "..." },
      "56": { "address": "0x55d398...", "decimals": 18, "symbol": "usdt", "updated_at": "..." }
    }
  }
}
```

## 使用

```bash
# 需配置 SEEINGSTONE_API_TOKEN 和 config/settings.yaml 中的 threshold
make tokensync

# 自定义存储路径
go run ./cmd/tokensync --registry=data/token_registry.json
```

## 流程

1. 拉取 SeeingStone 价差，过滤 `spread >= threshold`
2. 从 symbol 解析资产（如 POWERUSDT → POWER, USDT）
3. 加载本地 registry
4. 对每个资产：若本地已有，跳过；否则请求 CoinGecko 并合并
5. 有更新时保存
