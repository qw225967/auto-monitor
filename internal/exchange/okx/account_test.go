package okx

import (
	"testing"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 本测试文件用于测试 account.go（GetBalance、GetAllBalances、GetPosition、GetPositions 等）
// 2. 请在下方常量中填写你的 OKX API Key、Secret、Passphrase（或保持占位符则跳过需要实盘的用例）
// 3. 运行：go test -v -run TestOkxAccount ./internal/exchange/okx/
// 4. 如需代理：本文件内已使用 test.SetupProxyForTest，可修改代理地址

// 硬编码的 OKX API Key 信息（仅用于本文件账户相关测试）
const (
	accountTestOKXAPIKey     = "your-api-key-here" // 请替换为你的 OKX API Key
	accountTestOKXSecretKey  = ""                  // 请替换为你的 OKX Secret Key
	accountTestOKXPassphrase = ""                  // 请替换为你的 OKX Passphrase
)

// setupTestOkxForAccount 创建并初始化用于账户测试的 OKX 实例（使用本文件硬编码的 API Key）
func setupTestOkxForAccount(t *testing.T) *okx {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()

	okxEx := NewOkx().(*okx)
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		t.Fatalf("无法获取全局配置")
	}
	globalConfig.OkEx.KeyList = []config.OkExKeyRecord{
		{
			APIKey:       accountTestOKXAPIKey,
			Secret:       accountTestOKXSecretKey,
			Passphrase:   accountTestOKXPassphrase,
			CanBroadcast: true,
		},
	}
	if err := okxEx.Init(); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	return okxEx
}

func skipIfNoAccountKey(t *testing.T) {
	if accountTestOKXAPIKey == "your-api-key-here" || accountTestOKXSecretKey == "your-secret-key-here" {
		t.Skip("请先在 account_test.go 中配置 accountTestOKXAPIKey, accountTestOKXSecretKey, accountTestOKXPassphrase")
	}
}

// TestOkxAccountGetBalance 测试 GetBalance（USDT 余额）
func TestOkxAccountGetBalance(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)
	bal, err := okxEx.GetBalance()
	if err != nil {
		t.Fatalf("GetBalance 失败: %v", err)
	}
	if bal == nil {
		t.Fatal("GetBalance 返回 nil")
	}
	t.Logf("✅ USDT 余额: Available=%.4f, Locked=%.4f, Total=%.4f", bal.Available, bal.Locked, bal.Total)
}

// TestOkxAccountGetAllBalances 测试 GetAllBalances
func TestOkxAccountGetAllBalances(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)
	m, err := okxEx.GetAllBalances()
	if err != nil {
		t.Fatalf("GetAllBalances 失败: %v", err)
	}
	if m == nil {
		t.Fatal("GetAllBalances 返回 nil")
	}
	t.Logf("✅ 全币种余额数量: %d", len(m))
	for ccy, b := range m {
		if b.Total > 0 || b.Available > 0 || b.Locked > 0 {
			t.Logf("   %s: Available=%.4f, Locked=%.4f, Total=%.4f", ccy, b.Available, b.Locked, b.Total)
		}
	}
}

// TestOkxAccountGetSpotBalances 测试 GetSpotBalances（OKX 统一账户，与 GetAllBalances 同源）
func TestOkxAccountGetSpotBalances(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)
	m, err := okxEx.GetSpotBalances()
	if err != nil {
		t.Fatalf("GetSpotBalances 失败: %v", err)
	}
	if m == nil {
		t.Fatal("GetSpotBalances 返回 nil")
	}
	t.Logf("✅ GetSpotBalances 返回 %d 个币种", len(m))
}

// TestOkxAccountGetFuturesBalances 测试 GetFuturesBalances（OKX 统一账户，与 GetAllBalances 同源）
func TestOkxAccountGetFuturesBalances(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)
	m, err := okxEx.GetFuturesBalances()
	if err != nil {
		t.Fatalf("GetFuturesBalances 失败: %v", err)
	}
	if m == nil {
		t.Fatal("GetFuturesBalances 返回 nil")
	}
	t.Logf("✅ GetFuturesBalances 返回 %d 个币种", len(m))
}

// TestOkxAccountGetPosition 测试 GetPosition（单合约持仓）
func TestOkxAccountGetPosition(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)

	t.Run("empty_symbol_returns_error", func(t *testing.T) {
		_, err := okxEx.GetPosition("")
		if err != exchange.ErrInvalidSymbol {
			t.Errorf("期望 ErrInvalidSymbol，得到: %v", err)
		}
	})

	t.Run("valid_symbol", func(t *testing.T) {
		pos, err := okxEx.GetPosition("BTCUSDT")
		if err != nil {
			t.Fatalf("GetPosition(BTCUSDT) 失败: %v", err)
		}
		if pos == nil {
			t.Fatal("GetPosition 返回 nil")
		}
		t.Logf("✅ BTCUSDT 持仓: Symbol=%s, Side=%s, Size=%.4f, EntryPrice=%.4f, MarkPrice=%.4f, Upl=%.4f",
			pos.Symbol, pos.Side, pos.Size, pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnl)
	})
}

// TestOkxAccountGetPositions 测试 GetPositions（所有 SWAP 持仓）
func TestOkxAccountGetPositions(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoAccountKey(t)

	okxEx := setupTestOkxForAccount(t)
	list, err := okxEx.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions 失败: %v", err)
	}
	if list == nil {
		t.Fatal("GetPositions 返回 nil")
	}
	t.Logf("✅ 持仓数量: %d", len(list))
	for _, pos := range list {
		t.Logf("   %s: Side=%s, Size=%.4f, Entry=%.4f, Mark=%.4f, Upl=%.4f",
			pos.Symbol, pos.Side, pos.Size, pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnl)
	}
}
