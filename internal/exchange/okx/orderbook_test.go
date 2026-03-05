package okx

import (
	"testing"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 本测试文件用于测试 orderbook.go（GetSpotOrderBook、GetFuturesOrderBook）
// 2. 订单簿接口为公开接口，无需签名；为统一风格在下方硬编码 API Key，仅用于 Init() 通过（可填占位符）
// 3. 运行：go test -v -run TestOkxOrderbook ./internal/exchange/okx/

// 硬编码的 OKX API Key 信息（仅用于本文件测试；订单簿为公开接口，实际请求不签名）
const (
	orderbookTestOKXAPIKey     = "your-api-key-here" // 请替换为你的 OKX API Key
	orderbookTestOKXSecretKey  = ""                  // 请替换为你的 OKX Secret Key
	orderbookTestOKXPassphrase = ""                  // 请替换为你的 OKX Passphrase
)

// setupTestOkxForOrderbook 创建并初始化用于订单簿测试的 OKX 实例（使用本文件硬编码的 API Key）
func setupTestOkxForOrderbook(t *testing.T) *okx {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()

	okxEx := NewOkx().(*okx)
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		t.Fatalf("无法获取全局配置")
	}
	globalConfig.OkEx.KeyList = []config.OkExKeyRecord{
		{
			APIKey:       orderbookTestOKXAPIKey,
			Secret:       orderbookTestOKXSecretKey,
			Passphrase:   orderbookTestOKXPassphrase,
			CanBroadcast: true,
		},
	}
	if err := okxEx.Init(); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	return okxEx
}

func skipIfNoOrderbookKey(t *testing.T) {
	if orderbookTestOKXAPIKey == "your-api-key-here" || orderbookTestOKXSecretKey == "your-secret-key-here" {
		t.Skip("请先在 orderbook_test.go 中配置 orderbookTestOKXAPIKey, orderbookTestOKXSecretKey, orderbookTestOKXPassphrase")
	}
}

// TestOkxOrderbookGetSpot 测试现货订单簿 GetSpotOrderBook
func TestOkxOrderbookGetSpot(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoOrderbookKey(t)

	okxEx := setupTestOkxForOrderbook(t)

	bids, asks, err := okxEx.GetSpotOrderBook("BTCUSDT")
	if err != nil {
		t.Fatalf("GetSpotOrderBook 失败: %v", err)
	}
	if bids == nil {
		t.Fatal("GetSpotOrderBook 返回 bids=nil")
	}
	if asks == nil {
		t.Fatal("GetSpotOrderBook 返回 asks=nil")
	}
	if len(bids) == 0 {
		t.Log("bids 为空（可能暂时无挂单）")
	} else {
		t.Logf("✅ 现货订单簿 bids 档位: %d, 首档 [price, qty]: %v", len(bids), bids[0])
	}
	if len(asks) == 0 {
		t.Log("asks 为空（可能暂时无挂单）")
	} else {
		t.Logf("✅ 现货订单簿 asks 档位: %d, 首档 [price, qty]: %v", len(asks), asks[0])
	}
}

// TestOkxOrderbookGetFutures 测试合约订单簿 GetFuturesOrderBook
func TestOkxOrderbookGetFutures(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoOrderbookKey(t)

	okxEx := setupTestOkxForOrderbook(t)

	bids, asks, err := okxEx.GetFuturesOrderBook("BTCUSDT")
	if err != nil {
		t.Fatalf("GetFuturesOrderBook 失败: %v", err)
	}
	if bids == nil {
		t.Fatal("GetFuturesOrderBook 返回 bids=nil")
	}
	if asks == nil {
		t.Fatal("GetFuturesOrderBook 返回 asks=nil")
	}
	if len(bids) == 0 {
		t.Log("bids 为空（可能暂时无挂单）")
	} else {
		t.Logf("✅ 合约订单簿 bids 档位: %d, 首档 [price, qty]: %v", len(bids), bids[0])
	}
	if len(asks) == 0 {
		t.Log("asks 为空（可能暂时无挂单）")
	} else {
		t.Logf("✅ 合约订单簿 asks 档位: %d, 首档 [price, qty]: %v", len(asks), asks[0])
	}
}
