package lighter

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

// TestLighterInit 测试 Lighter 初始化
func TestLighterInit(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("🔧 测试 Lighter DEX 初始化...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	t.Log("✅ Lighter DEX 初始化成功")
}

// TestLighterGetBalance 测试余额查询
func TestLighterGetBalance(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("💰 测试 Lighter 余额查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

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

// TestLighterGetPosition 测试持仓查询
func TestLighterGetPosition(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📈 测试 Lighter 持仓查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

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

// TestLighterGetSpotOrderBook 测试现货订单簿查询
func TestLighterGetSpotOrderBook(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📊 测试 Lighter 现货订单簿查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

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

// TestLighterPlaceOrder 测试下单（谨慎运行）
func TestLighterPlaceOrder(t *testing.T) {
	t.Skip("⚠️ 跳过下单测试以避免意外交易")

	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📝 测试 Lighter 下单...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

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

// TestLighterWebSocketTicker 测试 WebSocket Ticker 订阅
func TestLighterWebSocketTicker(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📡 测试 Lighter WebSocket Ticker 订阅...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret).(*lighter)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	tickerReceived := false
	exch.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		t.Logf("📊 收到 Ticker 数据: %s - BidPrice=%.2f, AskPrice=%.2f, LastPrice=%.2f",
			symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice)
		tickerReceived = true
	})

	// 订阅测试交易对
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

// TestLighterGetFuturesOrderBook 测试合约订单簿查询
func TestLighterGetFuturesOrderBook(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📊 测试 Lighter 合约订单簿查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

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

// TestLighterGetAllBalances 测试所有余额查询
func TestLighterGetAllBalances(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("💰 测试 Lighter 所有余额查询...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	balances, err := exch.GetAllBalances()
	if err != nil {
		t.Fatalf("❌ 获取所有余额失败: %v", err)
	}

	t.Logf("✅ 获取所有余额成功，共 %d 个币种", len(balances))
	for asset, balance := range balances {
		if balance.Total > 0 {
			t.Logf("   %s: Total=%.8f, Available=%.8f, Locked=%.8f",
				asset, balance.Total, balance.Available, balance.Locked)
		}
	}
}

// TestLighterCalculateSlippage 测试滑点计算
func TestLighterCalculateSlippage(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📈 测试 Lighter 滑点计算...")

	globalConfig := config.GetGlobalConfig()
	exch := NewLighter(globalConfig.Lighter.APIKey, globalConfig.Lighter.Secret)

	if err := exch.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	symbol := "BTCUSDT"
	amount := 0.1
	slippageLimit := 0.5 // 0.5%

	// 测试现货滑点计算
	slippage, maxSize := exch.CalculateSlippage(symbol, amount, false, model.OrderSideBuy, slippageLimit)
	t.Logf("✅ 现货滑点计算: Slippage=%.4f%%, MaxSize=%.8f", slippage, maxSize)

	// 测试合约滑点计算
	slippage, maxSize = exch.CalculateSlippage(symbol, amount, true, model.OrderSideBuy, slippageLimit)
	t.Logf("✅ 合约滑点计算: Slippage=%.4f%%, MaxSize=%.8f", slippage, maxSize)
}

// TestLighter_PlaceFuturesOrder 测试合约下单功能
func TestLighter_PlaceFuturesOrder(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()

	shouldPlaceOrder := false // 默认不执行，避免意外交易

	exch := NewLighter(config.LighterAPIKey, config.LighterAPISecret)
	if err := exch.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("⚠️ 跳过实际下单测试以避免意外交易（设置 shouldPlaceOrder=true 以启用）")
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "ETHUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeMarket,
		Quantity:   0.01,
		MarketType: model.MarketTypeFutures, // 合约下单
	}

	t.Logf("Placing futures order via REST API: %s %s %.6f", req.Symbol, req.Side, req.Quantity)

	order, err := exch.PlaceOrder(req)
	if err != nil {
		t.Logf("⚠️  下单失败（可能是余额不足或权限问题）: %v", err)
		t.Logf("✅ REST API order placement attempted")
		return
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Order ID: %s, Status: %s", order.OrderID, order.Status)
	t.Logf("⚠️  Check Lighter Futures account for order ID: %s", order.OrderID)
}

// TestLighter_PlaceSpotOrder 测试现货下单功能
func TestLighter_PlaceSpotOrder(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	shouldPlaceOrder := false // 默认不执行，避免意外交易

	exch := NewLighter(config.LighterAPIKey, config.LighterAPISecret)
	if err := exch.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("⚠️ 跳过实际下单测试以避免意外交易（设置 shouldPlaceOrder=true 以启用）")
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "ETHUSDT",
		Side:       model.OrderSideSell,
		Type:       model.OrderTypeMarket,
		Quantity:   0.009,
		MarketType: model.MarketTypeSpot, // 现货下单
	}

	t.Logf("Placing spot order via REST API: %s %s %.6f", req.Symbol, req.Side, req.Quantity)

	order, err := exch.PlaceOrder(req)
	if err != nil {
		t.Logf("⚠️  下单失败（可能是余额不足或权限问题）: %v", err)
		t.Logf("✅ REST API spot order placement attempted")
		return
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Order ID: %s, Status: %s", order.OrderID, order.Status)
	t.Logf("⚠️  Check Lighter Spot account for order ID: %s", order.OrderID)
}

// TestLighter_PlaceSpotOrder_Limit 测试现货限价单
func TestLighter_PlaceSpotOrder_Limit(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	shouldPlaceOrder := false // 限价单测试默认不执行，避免误下单

	exch := NewLighter(config.LighterAPIKey, config.LighterAPISecret)
	if err := exch.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("⚠️ 跳过实际限价单测试以避免意外交易（设置 shouldPlaceOrder=true 以启用）")
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "ETHUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.001,
		Price:      2000.0,
		MarketType: model.MarketTypeSpot, // 现货下单
	}

	t.Logf("Placing spot limit order via REST API: %s %s %.6f @ %.2f", req.Symbol, req.Side, req.Quantity, req.Price)

	order, err := exch.PlaceOrder(req)
	if err != nil {
		t.Logf("⚠️  下单失败（可能是余额不足或权限问题）: %v", err)
		t.Logf("✅ REST API spot limit order placement attempted")
		return
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Limit Order ID: %s, Status: %s, Price: %.2f", order.OrderID, order.Status, order.Price)
	t.Logf("⚠️  Check Lighter Spot account for order ID: %s", order.OrderID)
}
