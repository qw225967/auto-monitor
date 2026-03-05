package position

import (
	"os"
	"strings"
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange/binance"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/trader"
)

const (
	TestWalletAddress           = "0x1f95f578aff83682ae64c12f65d89aa756961abf"
	TestWalletAddressChainIndex = "56"
)

// TestPositionManager 测试 PositionManager 的持仓汇总功能
func TestPositionManager(t *testing.T) {
	// 设置代理（如果需要）
	if os.Getenv("HTTP_PROXY") == "" {
		os.Setenv("HTTP_PROXY", "http://127.0.0.1:7890")
	}
	if os.Getenv("HTTPS_PROXY") == "" {
		os.Setenv("HTTPS_PROXY", "http://127.0.0.1:7890")
	}

	// 创建币安实例
	binanceEx := binance.NewBinance(config.BinanceAPIKey, config.BinanceSecretKey)

	// 初始化
	err := binanceEx.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
	}

	// 创建并初始化链上客户端
	t.Log("=== 初始化链上客户端 ===")
	onchainClient := onchain.NewOkdex()
	err = onchainClient.Init()
	if err != nil {
		t.Logf("⚠️  链上客户端初始化失败: %v，将只获取交易所余额", err)
		onchainClient = nil
	} else {
		t.Log("✓ 链上客户端初始化成功")
	}

	// 准备 Trader 列表
	traders := []trader.Trader{trader.NewCexTrader(binanceEx)}

	// 准备链上客户端配置
	onchainClients := make(map[string]*OnchainClientConfig)
	if onchainClient != nil {
		onchainClients["default"] = &OnchainClientConfig{
			Client:              onchainClient,
			WalletAddresses:     []string{TestWalletAddress},
			WalletAddressChains: TestWalletAddressChainIndex,
		}
		t.Log("✓ 已配置链上客户端和钱包地址")
	}

	// 初始化 PositionManager（会自动初始化 WalletManager）
	refreshInterval := 10 * time.Second
	t.Log("=== 初始化 PositionManager ===")
	t.Logf("刷新间隔: %v", refreshInterval)
	positionManager := InitPositionManager(traders, onchainClients, refreshInterval)
	if positionManager == nil {
		t.Fatalf("Failed to init position manager")
	}
	t.Log("✓ PositionManager 已初始化")
	defer StopWalletManager()

	// 等待首次刷新完成
	t.Log("等待首次数据刷新...")
	time.Sleep(3 * time.Second)

	// 测试1: 获取所有 symbol 的持仓汇总
	t.Log("\n=== 测试1: 获取所有 symbol 的持仓汇总 ===")
	allSummaries := positionManager.GetAllSymbolPositionSummaries()
	if allSummaries == nil || len(allSummaries) == 0 {
		t.Log("⚠️  暂无持仓汇总信息（可能账户中没有持仓）")
	} else {
		t.Logf("✓ 找到 %d 个币对的持仓汇总", len(allSummaries))
		for symbol, summary := range allSummaries {
			if summary == nil {
				continue
			}
			printSymbolPositionSummary(t, symbol, summary)
		}
	}

	// 测试2: 获取指定 symbol 的持仓汇总（如果有持仓的话）
	if allSummaries != nil && len(allSummaries) > 0 {
		t.Log("\n=== 测试2: 获取指定 symbol 的持仓汇总 ===")
		// 取第一个 symbol 进行测试
		var testSymbol string
		for symbol := range allSummaries {
			testSymbol = symbol
			break
		}
		if testSymbol != "" {
			t.Logf("测试 Symbol: %s", testSymbol)
			summary := positionManager.GetSymbolPositionSummary(testSymbol)
			if summary != nil {
				printSymbolPositionSummary(t, testSymbol, summary)
			} else {
				t.Logf("⚠️  获取 %s 的持仓汇总失败", testSymbol)
			}
		}
	} else {
		t.Log("\n=== 测试2: 跳过（没有持仓） ===")
	}

	// 测试3: 测试不存在的 symbol
	t.Log("\n=== 测试3: 测试不存在的 symbol ===")
	summary := positionManager.GetSymbolPositionSummary("NONEXISTENTUSDT")
	if summary == nil {
		t.Log("✓ 正确返回 nil（不存在的 symbol）")
	} else {
		t.Logf("⚠️  不存在的 symbol 返回了非 nil 结果")
	}

	t.Log("\n=== PositionManager 测试完成 ===")
}

// printSymbolPositionSummary 打印持仓汇总信息
func printSymbolPositionSummary(t *testing.T, symbol string, summary *SymbolPositionSummary) {
	if summary == nil {
		return
	}

	separator := strings.Repeat("-", 80)
	t.Logf("\n%s", separator)
	t.Logf("【%s 持仓汇总】", symbol)
	t.Logf("%s", separator)

	// 交易所持仓汇总
	t.Logf("\n📊 交易所持仓汇总:")
	t.Logf("  多头持仓: %.8f (价值: %.2f USDT)", summary.TotalExchangeLongSize, summary.TotalExchangeLongValue)
	t.Logf("  空头持仓: %.8f (价值: %.2f USDT)", summary.TotalExchangeShortSize, summary.TotalExchangeShortValue)
	t.Logf("  未实现盈亏: %.2f USDT", summary.TotalExchangeUnrealizedPnl)

	// 链上余额汇总
	t.Logf("\n🔗 链上余额汇总:")
	t.Logf("  总余额: %.8f (价值: %.2f USDT)", summary.TotalOnchainBalance, summary.TotalOnchainValue)

	// 总汇总
	t.Logf("\n💰 总汇总:")
	t.Logf("  总数量: %.8f (Long为正，Short为负)", summary.TotalQuantity)
	t.Logf("  总价值: %.2f USDT", summary.TotalValue)

	// 交易所持仓分布详情
	if len(summary.ExchangePositions) > 0 {
		t.Logf("\n📍 交易所持仓分布 (%d 个):", len(summary.ExchangePositions))
		for i, pos := range summary.ExchangePositions {
			t.Logf("  [%d] %s:", i+1, pos.ExchangeType)
			t.Logf("      方向: %s", pos.Side)
			t.Logf("      数量: %.8f", pos.Size)
			t.Logf("      开仓价: %.8f", pos.EntryPrice)
			t.Logf("      标记价: %.8f", pos.MarkPrice)
			t.Logf("      持仓价值: %.2f USDT", pos.PositionValue)
			t.Logf("      未实现盈亏: %.2f USDT", pos.UnrealizedPnl)
			t.Logf("      杠杆: %dx", pos.Leverage)
		}
	} else {
		t.Logf("\n📍 交易所持仓分布: 无")
	}

	// 链上余额分布详情
	if len(summary.OnchainBalances) > 0 {
		t.Logf("\n🔗 链上余额分布 (%d 个):", len(summary.OnchainBalances))
		for i, balance := range summary.OnchainBalances {
			t.Logf("  [%d] 客户端: %s, 链: %s:", i+1, balance.ClientID, balance.ChainIndex)
			t.Logf("      代币: %s", balance.Symbol)
			t.Logf("      余额: %.8f", balance.Balance)
			t.Logf("      单价: %.8f USD", balance.TokenPrice)
			t.Logf("      价值: %.2f USDT", balance.Value)
		}
	} else {
		t.Logf("\n🔗 链上余额分布: 无")
	}
}

