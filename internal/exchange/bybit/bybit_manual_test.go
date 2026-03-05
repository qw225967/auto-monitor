package bybit

import (
	"os"
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 在 internal/config/config.go 中配置你的 Bybit API Key
// 2. 建议使用测试网或子账户进行测试
// 3. 确保 API Key 只有读取和交易权限，不要给提现权限
// 4. 运行命令：go test -v -run TestBybit

// TestBybitInit 测试 Bybit 初始化
func TestBybitInit(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("🔧 测试 Bybit 初始化...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	if bybitEx.GetType() != "bybit" {
		t.Errorf("❌ GetType() 返回错误: got %s, want bybit", bybitEx.GetType())
	}

	t.Log("✅ Bybit 初始化成功")
}

// TestBybitGetBalance 测试余额查询
func TestBybitGetBalance(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("💰 测试 Bybit 余额查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	// 等待初始化完成
	time.Sleep(2 * time.Second)

	balance, err := bybitEx.GetBalance()
	if err != nil {
		t.Fatalf("❌ 获取余额失败: %v", err)
	}

	t.Logf("✅ USDT 余额:")
	t.Logf("   可用: %.4f USDT", balance.Available)
	t.Logf("   冻结: %.4f USDT", balance.Locked)
	t.Logf("   总计: %.4f USDT", balance.Total)
}

// TestBybitGetAllBalances 测试所有余额查询
func TestBybitGetAllBalances(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("💰 测试 Bybit 所有余额查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	balances, err := bybitEx.GetAllBalances()
	if err != nil {
		t.Fatalf("❌ 获取所有余额失败: %v", err)
	}

	t.Logf("✅ 找到 %d 个币种余额:", len(balances))
	for coin, bal := range balances {
		if bal.Total > 0 {
			t.Logf("   %s: %.4f (可用: %.4f, 冻结: %.4f)",
				coin, bal.Total, bal.Available, bal.Locked)
		}
	}
}

// TestBybitGetSpotOrderBook 测试现货订单簿查询
func TestBybitGetSpotOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("📊 测试 Bybit 现货订单簿查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	bids, asks, err := bybitEx.GetSpotOrderBook("BTCUSDT")
	if err != nil {
		t.Fatalf("❌ 获取订单簿失败: %v", err)
	}

	t.Logf("✅ 订单簿 BTCUSDT:")
	t.Logf("   买单数量: %d", len(bids))
	t.Logf("   卖单数量: %d", len(asks))

	if len(bids) > 0 {
		t.Logf("   最佳买价: %s @ %s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   最佳卖价: %s @ %s", asks[0][0], asks[0][1])
	}
}

// TestBybitGetFuturesOrderBook 测试合约订单簿查询
func TestBybitGetFuturesOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("📊 测试 Bybit 合约订单簿查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	bids, asks, err := bybitEx.GetFuturesOrderBook("ETHUSDT")
	if err != nil {
		t.Fatalf("❌ 获取合约订单簿失败: %v", err)
	}

	t.Logf("✅ 合约订单簿 ETHUSDT:")
	t.Logf("   买单数量: %d", len(bids))
	t.Logf("   卖单数量: %d", len(asks))

	if len(bids) > 0 {
		t.Logf("   最佳买价: %s @ %s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   最佳卖价: %s @ %s", asks[0][0], asks[0][1])
	}
}

// TestBybitGetPosition 测试持仓查询
func TestBybitGetPosition(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("📈 测试 Bybit 持仓查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	position, err := bybitEx.GetPosition("BTCUSDT")
	if err != nil {
		t.Fatalf("❌ 获取持仓失败: %v", err)
	}

	if position.Size > 0 {
		t.Logf("✅ BTCUSDT 持仓:")
		t.Logf("   方向: %s", position.Side)
		t.Logf("   数量: %.4f", position.Size)
		t.Logf("   开仓价: %.2f", position.EntryPrice)
		t.Logf("   标记价: %.2f", position.MarkPrice)
		t.Logf("   未实现盈亏: %.4f", position.UnrealizedPnl)
		t.Logf("   杠杆: %dx", position.Leverage)
	} else {
		t.Log("✅ 无 BTCUSDT 持仓")
	}
}

// TestBybitGetPositions 测试所有持仓查询
func TestBybitGetPositions(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("📈 测试 Bybit 所有持仓查询...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	positions, err := bybitEx.GetPositions()
	if err != nil {
		t.Fatalf("❌ 获取所有持仓失败: %v", err)
	}

	if len(positions) > 0 {
		t.Logf("✅ 找到 %d 个持仓:", len(positions))
		for _, pos := range positions {
			t.Logf("   %s: %s %.4f @ %.2f (PnL: %.4f)",
				pos.Symbol, pos.Side, pos.Size, pos.EntryPrice, pos.UnrealizedPnl)
		}
	} else {
		t.Log("✅ 无持仓")
	}
}

// TestBybitWebSocketTicker 测试 WebSocket Ticker 订阅
func TestBybitWebSocketTicker(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("📡 测试 Bybit WebSocket Ticker 订阅...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	// 设置 ticker 回调
	receivedTickers := 0
	bybitEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		t.Logf("📊 Ticker [%d]: %s | Bid: %.2f, Ask: %.2f, Last: %.2f",
			receivedTickers, symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice)
	})

	// 订阅现货和合约
	err = bybitEx.SubscribeTicker(
		[]string{"BTCUSDT", "ETHUSDT"}, // 现货
		[]string{"SOLUSDT"},            // 合约
	)
	if err != nil {
		t.Fatalf("❌ 订阅失败: %v", err)
	}

	t.Log("⏳ 等待 30 秒接收 ticker 数据...")
	time.Sleep(30 * time.Second)

	if receivedTickers > 0 {
		t.Logf("✅ 成功收到 %d 个 ticker 更新", receivedTickers)
	} else {
		t.Error("❌ 未收到任何 ticker 数据")
	}

	// 取消订阅
	err = bybitEx.UnsubscribeTicker(
		[]string{"BTCUSDT", "ETHUSDT"},
		[]string{"SOLUSDT"},
	)
	if err != nil {
		t.Errorf("❌ 取消订阅失败: %v", err)
	} else {
		t.Log("✅ 取消订阅成功")
	}
}

// TestBybitPlaceOrder 测试下单（谨慎使用！）
func TestBybitPlaceOrder(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("💸 测试 Bybit 下单功能...")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// ⚠️ 使用远低于市场价的限价单，不会立即成交
	order, err := bybitEx.PlaceOrder(&model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.00001, // 极小数量
		Price:      10000.0, // 远低于市场价（不会成交）
		MarketType: model.MarketTypeSpot,
	})

	if err != nil {
		t.Fatalf("❌ 下单失败: %v", err)
	}

	t.Logf("✅ 订单创建成功:")
	t.Logf("   订单 ID: %s", order.OrderID)
	t.Logf("   交易对: %s", order.Symbol)
	t.Logf("   方向: %s", order.Side)
	t.Logf("   类型: %s", order.Type)
	t.Logf("   数量: %.5f", order.Quantity)
	t.Logf("   价格: %.2f", order.Price)
	t.Logf("   状态: %s", order.Status)
	t.Logf("   创建时间: %s", order.CreateTime)

	t.Log("⚠️  记得手动取消这个订单！")
}

// TestBybitReconnection 测试重连机制
func TestBybitReconnection(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("🔄 测试 Bybit WebSocket 重连机制...")
	t.Log("ℹ️  Bybit 使用 SDK 内置重连，SDK 会自动处理断线重连")

	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	bybitEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		if receivedTickers%5 == 0 {
			t.Logf("📊 已收到 %d 个 ticker 更新", receivedTickers)
		}
	})

	err = bybitEx.SubscribeTicker([]string{"BTCUSDT"}, []string{})
	if err != nil {
		t.Fatalf("❌ 订阅失败: %v", err)
	}

	t.Log("⏳ 运行 60 秒，观察连接稳定性...")
	t.Log("💡 如果想测试重连，可以暂时关闭网络再恢复")

	for i := 0; i < 12; i++ {
		time.Sleep(5 * time.Second)
		t.Logf("   [%ds] 已收到 %d 个更新", (i+1)*5, receivedTickers)
	}

	if receivedTickers > 0 {
		t.Logf("✅ 连接稳定，共收到 %d 个更新", receivedTickers)
	} else {
		t.Error("❌ 连接异常，未收到任何更新")
	}
}

// TestBybitFullWorkflow 完整工作流测试
func TestBybitFullWorkflow(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BybitAPIKey")
	}

	t.Log("🚀 Bybit 完整工作流测试")

	// 1. 初始化
	t.Log("\n=== 步骤 1: 初始化 ===")
	bybitEx := NewBybit()
	err := bybitEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}
	t.Log("✅ 初始化成功")
	time.Sleep(2 * time.Second)

	// 2. 查询余额
	t.Log("\n=== 步骤 2: 查询余额 ===")
	balance, err := bybitEx.GetBalance()
	if err != nil {
		t.Errorf("❌ 查询余额失败: %v", err)
	} else {
		t.Logf("✅ USDT 余额: %.4f", balance.Available)
	}

	// 3. 查询订单簿
	t.Log("\n=== 步骤 3: 查询订单簿 ===")
	bids, asks, err := bybitEx.GetSpotOrderBook("BTCUSDT")
	if err != nil {
		t.Errorf("❌ 查询订单簿失败: %v", err)
	} else {
		t.Logf("✅ 订单簿: %d 买单, %d 卖单", len(bids), len(asks))
	}

	// 4. WebSocket 订阅
	t.Log("\n=== 步骤 4: WebSocket 订阅 ===")
	receivedCount := 0
	bybitEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedCount++
		if receivedCount <= 3 {
			t.Logf("📊 Ticker: %s, Bid: %.2f, Ask: %.2f",
				symbol, ticker.BidPrice, ticker.AskPrice)
		}
	})

	err = bybitEx.SubscribeTicker([]string{"BTCUSDT"}, []string{})
	if err != nil {
		t.Errorf("❌ 订阅失败: %v", err)
	} else {
		t.Log("✅ 订阅成功")
		time.Sleep(10 * time.Second)
		t.Logf("✅ 收到 %d 个 ticker 更新", receivedCount)
	}

	t.Log("\n🎉 完整工作流测试完成！")
}

// TestBybitQueryOrder_Real 使用真实订单 ID + symbol 查询订单（集成测试，支持现货/合约）
// 必填环境变量: BYBIT_ORDER_TEST_ORDER_ID
// 可选环境变量: BYBIT_ORDER_TEST_SYMBOL（默认 BTCUSDT）, BYBIT_ORDER_TEST_MARKET（spot|futures，默认 futures）
// 运行: go test -v -run TestBybitQueryOrder_Real ./internal/exchange/bybit/
func TestBybitQueryOrder_Real(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil || globalConfig.Bybit.APIKey == "" {
		t.Skip("⚠️  请先配置 Bybit API Key 后再运行本测试")
	}

	orderID := os.Getenv("BYBIT_ORDER_TEST_ORDER_ID")
	if orderID == "" {
		t.Skip("⚠️  请设置环境变量 BYBIT_ORDER_TEST_ORDER_ID 为要查询的订单 ID")
	}
	symbol := os.Getenv("BYBIT_ORDER_TEST_SYMBOL")
	if symbol == "" {
		symbol = "BTCUSDT"
	}
	market := os.Getenv("BYBIT_ORDER_TEST_MARKET")
	if market == "" {
		market = "futures"
	}

	bybitEx := NewBybit()
	if err := bybitEx.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	ex, ok := bybitEx.(*bybit)
	if !ok {
		t.Fatal("❌ 类型断言 *bybit 失败")
	}

	var order *model.Order
	var err error
	if market == "spot" {
		t.Logf("🔍 查询 Bybit 现货订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QuerySpotOrder(symbol, orderID)
	} else {
		t.Logf("🔍 查询 Bybit 合约订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QueryFuturesOrder(symbol, orderID)
	}
	if err != nil {
		t.Fatalf("❌ QueryOrder 失败: %v", err)
	}
	if order == nil {
		t.Fatal("❌ 返回订单为 nil")
	}

	logOrderFields(t, order)
}

func logOrderFields(t *testing.T, order *model.Order) {
	t.Logf("✅ 订单查询成功:")
	t.Logf("   OrderID:    %s", order.OrderID)
	t.Logf("   Symbol:    %s", order.Symbol)
	t.Logf("   Side:      %s", order.Side)
	t.Logf("   Type:      %s", order.Type)
	t.Logf("   Status:    %s", order.Status)
	t.Logf("   Quantity:  %f", order.Quantity)
	t.Logf("   Price:     %f", order.Price)
	t.Logf("   FilledQty: %f", order.FilledQty)
	t.Logf("   FilledPrice: %f", order.FilledPrice)
	t.Logf("   Fee:       %f", order.Fee)
	t.Logf("   CreateTime: %v", order.CreateTime)
	t.Logf("   UpdateTime: %v", order.UpdateTime)
	if order.Status == model.OrderStatusFilled && (order.FilledQty <= 0 || order.FilledPrice <= 0) {
		t.Logf("⚠️  订单状态为 Filled 但 FilledQty 或 FilledPrice 为空")
	}
}
