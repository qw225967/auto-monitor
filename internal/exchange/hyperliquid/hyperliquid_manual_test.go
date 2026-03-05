package hyperliquid

import (
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

func init() {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()
}

// TestHyperliquidInit 测试 Hyperliquid 初始化
func TestHyperliquidInit(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("🔧 测试 Hyperliquid DEX 初始化...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	t.Log("✅ Hyperliquid DEX 初始化成功")
}

// TestHyperliquidGetBalance 测试余额查询
func TestHyperliquidGetBalance(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("💰 测试 Hyperliquid 余额查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

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

// TestHyperliquidGetAllBalances 测试所有余额查询
func TestHyperliquidGetAllBalances(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("💰 测试 Hyperliquid 所有余额查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	balances, err := exch.GetAllBalances()
	if err != nil {
		t.Fatalf("❌ 获取所有余额失败: %v", err)
	}

	t.Logf("✅ 获取所有余额成功，共 %d 个币种", len(balances))
	for symbol, balance := range balances {
		if balance.Total > 0 {
			t.Logf("   %s: Total=%.2f, Available=%.2f, Locked=%.2f",
				symbol, balance.Total, balance.Available, balance.Locked)
		}
	}
}

// TestHyperliquidGetSpotOrderBook 测试现货订单簿查询
func TestHyperliquidGetSpotOrderBook(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📊 测试 Hyperliquid 现货订单簿查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	symbol := "BTCUSDT" // 或 "BTC-USD" 根据 Hyperliquid 的格式
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

// TestHyperliquidGetFuturesOrderBook 测试合约订单簿查询
func TestHyperliquidGetFuturesOrderBook(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📊 测试 Hyperliquid 合约订单簿查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	symbol := "BTCUSDT"
	bids, asks, err := exch.GetFuturesOrderBook(symbol)
	if err != nil {
		t.Fatalf("❌ 获取合约订单簿失败: %v", err)
	}

	t.Logf("✅ 获取合约订单簿成功: %s", symbol)
	if len(bids) > 0 {
		t.Logf("   买一: 价格=%s, 数量=%s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   卖一: 价格=%s, 数量=%s", asks[0][0], asks[0][1])
	}
}

// TestHyperliquidGetPosition 测试持仓查询
func TestHyperliquidGetPosition(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📈 测试 Hyperliquid 持仓查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

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

// TestHyperliquidGetPositions 测试所有持仓查询
func TestHyperliquidGetPositions(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📈 测试 Hyperliquid 所有持仓查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	positions, err := exch.GetPositions()
	if err != nil {
		t.Fatalf("❌ 获取所有持仓失败: %v", err)
	}

	t.Logf("✅ 获取所有持仓成功，共 %d 个持仓", len(positions))
	for _, position := range positions {
		if position.Size != 0 {
			t.Logf("   %s: 数量=%.2f, 入场价=%.2f, 未实现盈亏=%.2f",
				position.Symbol, position.Size, position.EntryPrice, position.UnrealizedPnl)
		}
	}
}

// TestHyperliquidPlaceSpotOrder 测试现货下单（谨慎运行）
func TestHyperliquidPlaceSpotOrder(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📝 测试 Hyperliquid 现货下单...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.001, // 小量测试
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

// TestHyperliquidPlaceFuturesOrder 测试合约下单（谨慎运行）
func TestHyperliquidPlaceFuturesOrder(t *testing.T) {

	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📝 测试 Hyperliquid 合约下单...")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.001,
		Price:      40000.0,
		MarketType: model.MarketTypeFutures,
	}

	order, err := exch.PlaceOrder(req)
	if err != nil {
		t.Fatalf("❌ 下单失败: %v", err)
	}

	t.Logf("✅ 下单成功: OrderID=%s, Symbol=%s, Side=%s, Quantity=%.2f",
		order.OrderID, order.Symbol, order.Side, order.Quantity)
}

// TestHyperliquidWebSocketTicker 测试 WebSocket Ticker 订阅
func TestHyperliquidWebSocketTicker(t *testing.T) {
	test.SetupProxyForTest("http://127.0.0.1:7897")

	t.Log("📡 测试 Hyperliquid WebSocket Ticker 订阅...")
	t.Log("ℹ️ Hyperliquid 使用 Agent Wallet 签名进行 WebSocket 连接")

	globalConfig := config.GetGlobalConfig()
	exch := NewHyperliquid(globalConfig.Hyperliquid.UserAddress, globalConfig.Hyperliquid.APIPrivateKey)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	tickerReceived := false
	exch.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		t.Logf("📊 收到 Ticker 数据: %s - BidPrice=%.2f, AskPrice=%.2f, LastPrice=%.2f",
			symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice)
		tickerReceived = true
	})

	// 订阅测试交易对（现货和合约）
	if err := exch.SubscribeTicker([]string{"BTCUSDT", "ETHUSDT"}, []string{"BTCUSDT"}); err != nil {
		t.Fatalf("❌ 订阅失败: %v", err)
	}

	t.Log("⏳ 等待 30 秒接收 ticker 数据...")
	time.Sleep(30 * time.Second)

	if !tickerReceived {
		t.Log("❌ 未收到任何 ticker 数据")
	}

	// 取消订阅
	if err := exch.UnsubscribeTicker([]string{"BTCUSDT", "ETHUSDT"}, []string{"BTCUSDT"}); err != nil {
		t.Fatalf("❌ 取消订阅失败: %v", err)
	}

	t.Log("✅ 取消订阅成功")
}
