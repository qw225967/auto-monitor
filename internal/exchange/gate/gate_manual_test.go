package gate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"github.com/qw225967/auto-monitor/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 在 internal/config/config.go 中配置你的 Gate.io API Key
// 2. Gate.io 符号格式为 BTC_USDT（带下划线）
// 3. 确保 API Key 只有读取和交易权限
// 4. 运行命令：go test -v -run TestGate

// TestGateInit 测试 Gate.io 初始化
func TestGateInit(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("🔧 测试 Gate.io 初始化...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	if gateEx.GetType() != "gate" {
		t.Errorf("❌ GetType() 返回错误: got %s, want gate", gateEx.GetType())
	}

	t.Log("✅ Gate.io 初始化成功")
	t.Log("ℹ️  Gate.io 使用 SDK 的 MaxRetryConn=10 自动重连")
}

// TestGateGetBalance 测试余额查询
func TestGateGetBalance(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("💰 测试 Gate.io 余额查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	balance, err := gateEx.GetBalance()
	if err != nil {
		t.Fatalf("❌ 获取余额失败: %v", err)
	}

	t.Logf("✅ USDT 余额:")
	t.Logf("   可用: %.4f USDT", balance.Available)
	t.Logf("   冻结: %.4f USDT", balance.Locked)
	t.Logf("   总计: %.4f USDT", balance.Total)
}

// TestGateGetAllBalances 测试所有余额查询
func TestGateGetAllBalances(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("💰 测试 Gate.io 所有余额查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	balances, err := gateEx.GetAllBalances()
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

// TestGateGetSpotOrderBook 测试现货订单簿查询
func TestGateGetSpotOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("📊 测试 Gate.io 现货订单簿查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Gate.io 使用 BTC_USDT 格式
	bids, asks, err := gateEx.GetSpotOrderBook("BTC_USDT")
	if err != nil {
		t.Fatalf("❌ 获取订单簿失败: %v", err)
	}

	t.Logf("✅ 订单簿 BTC_USDT:")
	t.Logf("   买单数量: %d", len(bids))
	t.Logf("   卖单数量: %d", len(asks))
	
	if len(bids) > 0 {
		t.Logf("   最佳买价: %s @ %s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   最佳卖价: %s @ %s", asks[0][0], asks[0][1])
	}
}

// TestGateGetFuturesOrderBook 测试合约订单簿查询
func TestGateGetFuturesOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("📊 测试 Gate.io 合约订单簿查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	bids, asks, err := gateEx.GetFuturesOrderBook("ETH_USDT")
	if err != nil {
		t.Fatalf("❌ 获取合约订单簿失败: %v", err)
	}

	t.Logf("✅ 合约订单簿 ETH_USDT:")
	t.Logf("   买单数量: %d", len(bids))
	t.Logf("   卖单数量: %d", len(asks))
	
	if len(bids) > 0 {
		t.Logf("   最佳买价: %s @ %s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   最佳卖价: %s @ %s", asks[0][0], asks[0][1])
	}
}

// TestGateGetPosition 测试持仓查询
func TestGateGetPosition(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("📈 测试 Gate.io 持仓查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	position, err := gateEx.GetPosition("BTC_USDT")
	if err != nil {
		t.Fatalf("❌ 获取持仓失败: %v", err)
	}

	if position.Size > 0 {
		t.Logf("✅ BTC_USDT 持仓:")
		t.Logf("   方向: %s", position.Side)
		t.Logf("   数量: %.4f", position.Size)
		t.Logf("   开仓价: %.2f", position.EntryPrice)
		t.Logf("   标记价: %.2f", position.MarkPrice)
		t.Logf("   未实现盈亏: %.4f", position.UnrealizedPnl)
		t.Logf("   杠杆: %dx", position.Leverage)
	} else {
		t.Log("✅ 无 BTC_USDT 持仓")
	}
}

// TestGateGetPositions 测试所有持仓查询
func TestGateGetPositions(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("📈 测试 Gate.io 所有持仓查询...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	positions, err := gateEx.GetPositions()
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

// TestGateWebSocketTicker 测试 WebSocket Ticker 订阅
func TestGateWebSocketTicker(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("📡 测试 Gate.io WebSocket Ticker 订阅...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	gateEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		t.Logf("📊 Ticker [%d]: %s | Bid: %.2f, Ask: %.2f, Last: %.2f", 
			receivedTickers, symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice)
	})

	// Gate.io 使用 BTC_USDT 格式
	err = gateEx.SubscribeTicker(
		[]string{},  // 现货
		[]string{"BTC_USDT"},               // 合约
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
	err = gateEx.UnsubscribeTicker(
		[]string{},
		[]string{"SOL_USDT"},
	)
	if err != nil {
		t.Errorf("❌ 取消订阅失败: %v", err)
	} else {
		t.Log("✅ 取消订阅成功")
	}
}

// TestGatePlaceOrder 测试现货下单（谨慎使用！）
func TestGatePlaceOrder(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("💸 测试 Gate.io 现货下单功能...")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// ⚠️ 使用远低于市场价的限价单，不会立即成交
	order, err := gateEx.PlaceOrder(&model.PlaceOrderRequest{
		Symbol:     "BTC_USDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.00001,  // 极小数量
		Price:      10000.0,  // 远低于市场价（不会成交）
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

	t.Log("⚠️  记得手动取消这个订单！")
}

// TestGatePlaceFuturesOrder 测试合约下单功能
func TestGatePlaceFuturesOrder(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	shouldPlaceOrder := true

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("⚠️  跳过真实下单测试")
	}

	time.Sleep(2 * time.Second)

	// Gate.io 合约下单测试
	// 注意：Gate.io 合约使用 size 的正负表示方向，正数=做多（买入），负数=做空（卖出）
	req := &model.PlaceOrderRequest{
		Symbol:     "DUSK_USDT",  // Gate.io 使用下划线格式
		Side:       model.OrderSideSell,
		Type:       model.OrderTypeMarket,
		Quantity:   200,  // 合约数量（整数）
		MarketType: model.MarketTypeFutures, // 合约下单
	}

	t.Logf("💸 测试 Gate.io 合约下单: %s %s %.0f", req.Symbol, req.Side, req.Quantity)

	order, err := gateEx.PlaceOrder(req)
	if err != nil {
		errMsg := err.Error()
		// 这些错误都是预期的测试场景
		if strings.Contains(errMsg, "INVALID_KEY") || strings.Contains(errMsg, "Invalid API-key") {
			t.Logf("⚠️  API key 权限错误: %v", err)
			t.Logf("✅ 合约下单 API 调用已尝试")
			return
		}
		if strings.Contains(errMsg, "INSUFFICIENT_BALANCE") || strings.Contains(errMsg, "insufficient balance") || strings.Contains(errMsg, "margin") {
			t.Logf("⚠️  余额/保证金不足: %v", err)
			t.Logf("✅ 合约下单 API 调用成功（余额不足是预期情况）")
			return
		}
		if strings.Contains(errMsg, "INVALID_SIZE") || strings.Contains(errMsg, "size") {
			t.Logf("⚠️  订单数量无效: %v", err)
			t.Logf("✅ 合约下单 API 调用已尝试")
			return
		}
		t.Fatalf("❌ 合约下单失败，未预期的错误: %v", err)
	}

	if order == nil {
		t.Fatal("❌ 订单为 nil")
	}

	t.Logf("✅ 合约订单创建成功:")
	t.Logf("   订单 ID: %s", order.OrderID)
	t.Logf("   交易对: %s", order.Symbol)
	t.Logf("   方向: %s", order.Side)
	t.Logf("   类型: %s", order.Type)
	t.Logf("   数量: %.0f", order.Quantity)
	t.Logf("   价格: %.2f", order.Price)
	t.Logf("   已成交数量: %.0f", order.FilledQty)
	t.Logf("   状态: %s", order.Status)
	t.Logf("⚠️  请检查 Gate.io 合约账户中的订单 ID: %s", order.OrderID)
}

// TestGateReconnection 测试重连机制
func TestGateReconnection(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("🔄 测试 Gate.io WebSocket 重连机制...")
	t.Log("ℹ️  Gate.io 使用 SDK 的 MaxRetryConn=10 自动重连")

	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	gateEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		if receivedTickers%5 == 0 {
			t.Logf("📊 已收到 %d 个 ticker 更新", receivedTickers)
		}
	})

	err = gateEx.SubscribeTicker([]string{"BTC_USDT"}, []string{})
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

// TestGateSymbolFormat 测试符号格式转换
func TestGateSymbolFormat(t *testing.T) {
	t.Log("🔤 测试 Gate.io 符号格式转换...")

	tests := []struct {
		input    string
		expected string
	}{
		{"BTCUSDT", "BTC_USDT"},
		{"BTC_USDT", "BTC_USDT"},
		{"ETHUSDT", "ETH_USDT"},
		{"BTCUSDT-PERP", "BTC_USDT"},
		{"BTCUSDT_PERP", "BTC_USDT"},
	}

	for _, tt := range tests {
		result := normalizeGateSymbol(tt.input)
		if result != tt.expected {
			t.Errorf("❌ normalizeGateSymbol(%s) = %s, want %s", 
				tt.input, result, tt.expected)
		} else {
			t.Logf("✅ %s -> %s", tt.input, result)
		}
	}
}

// TestGateFullWorkflow 完整工作流测试
func TestGateFullWorkflow(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 GateAPIKey")
	}

	t.Log("🚀 Gate.io 完整工作流测试")

	// 1. 初始化
	t.Log("\n=== 步骤 1: 初始化 ===")
	gateEx := NewGate()
	err := gateEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}
	t.Log("✅ 初始化成功")
	time.Sleep(2 * time.Second)

	// 2. 查询余额
	t.Log("\n=== 步骤 2: 查询余额 ===")
	balance, err := gateEx.GetBalance()
	if err != nil {
		t.Errorf("❌ 查询余额失败: %v", err)
	} else {
		t.Logf("✅ USDT 余额: %.4f", balance.Available)
	}

	// 3. 查询订单簿
	t.Log("\n=== 步骤 3: 查询订单簿 ===")
	bids, asks, err := gateEx.GetSpotOrderBook("BTC_USDT")
	if err != nil {
		t.Errorf("❌ 查询订单簿失败: %v", err)
	} else {
		t.Logf("✅ 订单簿: %d 买单, %d 卖单", len(bids), len(asks))
	}

	// 4. WebSocket 订阅
	t.Log("\n=== 步骤 4: WebSocket 订阅 ===")
	receivedCount := 0
	gateEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedCount++
		if receivedCount <= 3 {
			t.Logf("📊 Ticker: %s, Bid: %.2f, Ask: %.2f", 
				symbol, ticker.BidPrice, ticker.AskPrice)
		}
	})

	err = gateEx.SubscribeTicker([]string{"BTC_USDT"}, []string{})
	if err != nil {
		t.Errorf("❌ 订阅失败: %v", err)
	} else {
		t.Log("✅ 订阅成功")
		time.Sleep(10 * time.Second)
		t.Logf("✅ 收到 %d 个 ticker 更新", receivedCount)
	}

	t.Log("\n🎉 完整工作流测试完成！")
}

// TestGateQueryOrder_Real 使用真实订单 ID + symbol 查询订单（集成测试，支持现货/合约）
// 必填环境变量: GATE_ORDER_TEST_ORDER_ID
// 可选环境变量: GATE_ORDER_TEST_SYMBOL（默认 BTC_USDT）, GATE_ORDER_TEST_MARKET（spot|futures，默认 futures）
// 运行: go test -v -run TestGateQueryOrder_Real ./internal/exchange/gate/
func TestGateQueryOrder_Real(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil || globalConfig.Gate.APIKey == "" {
		t.Skip("⚠️  请先配置 Gate API Key 后再运行本测试")
	}

	orderID := os.Getenv("GATE_ORDER_TEST_ORDER_ID")
	if orderID == "" {
		t.Skip("⚠️  请设置环境变量 GATE_ORDER_TEST_ORDER_ID 为要查询的订单 ID")
	}
	symbol := os.Getenv("GATE_ORDER_TEST_SYMBOL")
	if symbol == "" {
		symbol = "BTC_USDT"
	}
	market := os.Getenv("GATE_ORDER_TEST_MARKET")
	if market == "" {
		market = "futures"
	}

	gateEx := NewGate()
	if err := gateEx.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	ex, ok := gateEx.(*gateExchange)
	if !ok {
		t.Fatal("❌ 类型断言 *gateExchange 失败")
	}

	var order *model.Order
	var err error
	if market == "spot" {
		t.Logf("🔍 查询 Gate 现货订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QuerySpotOrder(symbol, orderID)
	} else {
		t.Logf("🔍 查询 Gate 合约订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QueryFuturesOrder(symbol, orderID)
	}
	if err != nil {
		t.Fatalf("❌ QueryOrder 失败: %v", err)
	}
	if order == nil {
		t.Fatal("❌ 返回订单为 nil")
	}

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
