package okx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 本测试文件用于测试 websocket.go（SubscribeTicker 现货+合约行情推送）
// 2. WebSocket 为公开行情，无需签名；为统一风格在下方硬编码 API Key，仅用于 Init()（可填占位符）
// 3. 运行：go test -v -run TestOkxWebsocket ./internal/exchange/okx/
// 4. 测试会订阅 BTCUSDT 现货与合约，等待数秒内收到回调后判定通过

// 硬编码的 OKX API Key 信息（仅用于本文件测试；WS 公开行情不签名）
const (
	websocketTestOKXAPIKey     = "your-api-key-here" // 请替换为你的 OKX API Key
	websocketTestOKXSecretKey  = ""                  // 请替换为你的 OKX Secret Key
	websocketTestOKXPassphrase = ""                  // 请替换为你的 OKX Passphrase
)

// setupTestOkxForWebsocket 创建并初始化用于 WebSocket 测试的 OKX 实例（使用本文件硬编码的 API Key）
func setupTestOkxForWebsocket(t *testing.T) *okx {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()

	okxEx := NewOkx().(*okx)
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		t.Fatalf("无法获取全局配置")
	}
	globalConfig.OkEx.KeyList = []config.OkExKeyRecord{
		{
			APIKey:       websocketTestOKXAPIKey,
			Secret:       websocketTestOKXSecretKey,
			Passphrase:   websocketTestOKXPassphrase,
			CanBroadcast: true,
		},
	}
	if err := okxEx.Init(); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	return okxEx
}

func skipIfNoWebsocketKey(t *testing.T) {
	if websocketTestOKXAPIKey == "your-api-key-here" || websocketTestOKXSecretKey == "your-secret-key-here" {
		t.Skip("请先在 websocket_test.go 中配置 websocketTestOKXAPIKey, websocketTestOKXSecretKey, websocketTestOKXPassphrase")
	}
}

// TestOkxWebsocketSubscribeSpotAndFutures 测试现货与合约 ticker 订阅（共用一条 WS 连接）
func TestOkxWebsocketSubscribeSpotAndFutures(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoWebsocketKey(t)

	okxEx := setupTestOkxForWebsocket(t)

	var spotCount, futuresCount atomic.Int32
	var lastSpot, lastFutures model.Ticker
	var lastMu sync.Mutex

	okxEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		if ticker == nil {
			return
		}
		if marketType == "spot" {
			spotCount.Add(1)
			lastMu.Lock()
			lastSpot = *ticker
			lastMu.Unlock()
		} else if marketType == "futures" {
			futuresCount.Add(1)
			lastMu.Lock()
			lastFutures = *ticker
			lastMu.Unlock()
		}
	})

	// 订阅现货与合约
	err := okxEx.SubscribeTicker(
		[]string{"BTCUSDT"}, // 现货
		[]string{"BTCUSDT"}, // 合约
	)
	if err != nil {
		t.Fatalf("SubscribeTicker 失败: %v", err)
	}

	// 等待推送（OKX 通常数秒内会有 ticker 更新）
	time.Sleep(5 * time.Second)

	// 取消订阅，释放连接
	_ = okxEx.UnsubscribeTicker([]string{"BTCUSDT"}, []string{"BTCUSDT"})

	s, f := spotCount.Load(), futuresCount.Load()
	t.Logf("✅ 收到现货 ticker 次数: %d, 合约 ticker 次数: %d", s, f)

	lastMu.Lock()
	defer lastMu.Unlock()
	if s > 0 {
		t.Logf("   现货示例: Symbol=%s, Bid=%.2f, Ask=%.2f, Last=%.2f",
			lastSpot.Symbol, lastSpot.BidPrice, lastSpot.AskPrice, lastSpot.LastPrice)
	}
	if f > 0 {
		t.Logf("   合约示例: Symbol=%s, Bid=%.2f, Ask=%.2f, Last=%.2f",
			lastFutures.Symbol, lastFutures.BidPrice, lastFutures.AskPrice, lastFutures.LastPrice)
	}

	if s == 0 && f == 0 {
		t.Fatal("未收到任何 ticker 回调，请检查网络或代理")
	}
	if s == 0 {
		t.Log("⚠️ 未收到现货 ticker（可能 OKX 推送延迟，可重试）")
	}
	if f == 0 {
		t.Log("⚠️ 未收到合约 ticker（可能 OKX 推送延迟，可重试）")
	}
}
