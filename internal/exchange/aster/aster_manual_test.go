package aster

import (
	"testing"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

func init() {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()
}

// TestAsterInit 测试 Aster 初始化
func TestAsterInit(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("🔧 测试 Aster DEX 初始化...")

	globalConfig := config.GetGlobalConfig()
	exch := NewAster(globalConfig.Aster.APIKey, globalConfig.Aster.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	t.Log("✅ Aster DEX 初始化成功")
}

// TestAsterGetBalance 测试余额查询
func TestAsterGetBalance(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("💰 测试 Aster 余额查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewAster(globalConfig.Aster.APIKey, globalConfig.Aster.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	balance, err := exch.GetBalance()
	if err != nil {
		t.Fatalf("❌ 获取余额失败: %v", err)
	}

	t.Logf("✅ 获取余额成功: Total=%.2f, Available=%.2f, Locked=%.2f",
		balance.Total, balance.Available, balance.Locked)
}

// TestAsterGetPosition 测试持仓查询
func TestAsterGetPosition(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📈 测试 Aster 持仓查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewAster(globalConfig.Aster.APIKey, globalConfig.Aster.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	symbol := "BTCUSDT"
	position, err := exch.GetPosition(symbol)
	if err != nil {
		t.Fatalf("❌ 获取持仓失败: %v", err)
	}

	t.Logf("✅ 获取持仓成功: %s", symbol)
	t.Logf("   数量: %.2f, 入场价: %.2f, 未实现盈亏: %.2f",
		position.Size, position.EntryPrice, position.UnrealizedPnl)
}

// TestAsterGetSpotOrderBook 测试现货订单簿查询
func TestAsterGetSpotOrderBook(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📊 测试 Aster 现货订单簿查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewAster(globalConfig.Aster.APIKey, globalConfig.Aster.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	symbol := "BTCUSDT"
	bids, asks, err := exch.GetSpotOrderBook(symbol)
	if err != nil {
		t.Fatalf("❌ 获取现货订单簿失败: %v", err)
	}

	t.Logf("✅ 获取现货订单簿成功: %s", symbol)
	if len(bids) > 0 {
		t.Logf("   买一: 价格=%s, 数量=%s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   卖一: 价格=%s, 数量=%s", asks[0][0], asks[0][1])
	}
}

// TestAsterPlaceOrder 测试下单（谨慎运行）
func TestAsterPlaceOrder(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📝 测试 Aster 下单...")

	globalConfig := config.GetGlobalConfig()
	exch := NewAster(globalConfig.Aster.APIKey, globalConfig.Aster.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.001,
		Price:      40000.0,
		MarketType: model.MarketTypeSpot,
	}

	order, err := exch.PlaceOrder(req)
	if err != nil {
		t.Fatalf("❌ 下单失败: %v", err)
	}

	t.Logf("✅ 下单成功: OrderID=%s, Symbol=%s, Side=%s, Quantity=%.2f",
		order.OrderID, order.Symbol, order.Side, order.Quantity)
}
