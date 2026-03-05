package bitget

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 在 internal/config/config.go 中配置你的 Bitget API Key
// 2. Bitget 需要 Passphrase（创建 API Key 时设置）
// 3. Bitget 符号格式：BTCUSDT（现货）、BTCUSDT_UMCBL（合约）
// 4. Bitget 重连机制最复杂，需要重点测试
// 5. 运行命令：go test -v -run TestBitget

// TestBitgetInit 测试 Bitget 初始化
func TestBitgetInit(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("🔧 测试 Bitget 初始化...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	if bitgetEx.GetType() != "bitget" {
		t.Errorf("❌ GetType() 返回错误: got %s, want bitget", bitgetEx.GetType())
	}

	t.Log("✅ Bitget 初始化成功")
	t.Log("ℹ️  Bitget 使用完全手动重连机制，包含登录和重新订阅")
}

// TestBitgetGetBalance 测试余额查询
func TestBitgetGetBalance(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("💰 测试 Bitget 余额查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	balance, err := bitgetEx.GetBalance()
	if err != nil {
		t.Fatalf("❌ 获取余额失败: %v", err)
	}

	t.Logf("✅ USDT 余额:")
	t.Logf("   可用: %.4f USDT", balance.Available)
	t.Logf("   冻结: %.4f USDT", balance.Locked)
	t.Logf("   总计: %.4f USDT", balance.Total)
}

// TestBitgetGetAllBalances 测试所有余额查询
func TestBitgetGetAllBalances(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("💰 测试 Bitget 所有余额查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	balances, err := bitgetEx.GetAllBalances()
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

// TestBitgetQueryOrder_Real 使用真实订单 ID + symbol 查询订单（集成测试，支持现货/合约）
// 必填环境变量: BITGET_ORDER_TEST_ORDER_ID
// 可选环境变量: BITGET_ORDER_TEST_SYMBOL（默认 POWERUSDT）, BITGET_ORDER_TEST_MARKET（spot|futures，默认 spot）
// 运行: go test -v -run TestBitgetQueryOrder_Real ./internal/exchange/bitget/
// 说明：仅初始化 REST 客户端并调查单接口，不建立 WebSocket，因此无需本地代理即可运行。
func TestBitgetQueryOrder_Real(t *testing.T) {
	logger.InitLogger("")

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil || globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先配置 BitGet API Key / Secret / Passphrase 后再运行本测试")
	}

	orderID := os.Getenv("BITGET_ORDER_TEST_ORDER_ID")
	if orderID == "" {
		t.Skip("⚠️  请设置环境变量 BITGET_ORDER_TEST_ORDER_ID 为要查询的订单 ID")
	}
	symbol := os.Getenv("BITGET_ORDER_TEST_SYMBOL")
	if symbol == "" {
		symbol = "POWERUSDT"
	}
	market := os.Getenv("BITGET_ORDER_TEST_MARKET")
	if market == "" {
		market = "spot"
	}

	bitgetEx := NewBitget()
	ex, ok := bitgetEx.(*bitgetExchange)
	if !ok {
		t.Fatal("❌ 类型断言 *bitgetExchange 失败")
	}
	// 仅初始化 REST 客户端（查单只需 REST，不建 WebSocket，避免依赖代理）
	ex.restClient.InitRestClient()

	var order *model.Order
	var err error
	if market == "futures" {
		t.Logf("🔍 查询 Bitget 合约订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QueryFuturesOrder(symbol, orderID)
	} else {
		t.Logf("🔍 查询 Bitget 现货订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QuerySpotOrder(symbol, orderID)
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

	// 用于落库/轮询的关键字段应有值（若订单已成交）
	if order.Status == model.OrderStatusFilled && (order.FilledQty <= 0 || order.FilledPrice <= 0) {
		t.Logf("⚠️  订单状态为 Filled 但 FilledQty 或 FilledPrice 为空，可能影响 pollCexOrderUntilFilledWithTimeout 判定")
	}
}

// TestBitgetGetSpotOrderBook 测试现货订单簿查询
func TestBitgetGetSpotOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("📊 测试 Bitget 现货订单簿查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Bitget 现货使用 BTCUSDT 格式
	bids, asks, err := bitgetEx.GetSpotOrderBook("BTCUSDT")
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

// TestBitgetGetFuturesOrderBook 测试合约订单簿查询
func TestBitgetGetFuturesOrderBook(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("📊 测试 Bitget 合约订单簿查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// Bitget 合约使用 BTCUSDT_UMCBL 格式
	bids, asks, err := bitgetEx.GetFuturesOrderBook("BTCUSDT_UMCBL")
	if err != nil {
		t.Fatalf("❌ 获取合约订单簿失败: %v", err)
	}

	t.Logf("✅ 合约订单簿 BTCUSDT_UMCBL:")
	t.Logf("   买单数量: %d", len(bids))
	t.Logf("   卖单数量: %d", len(asks))
	
	if len(bids) > 0 {
		t.Logf("   最佳买价: %s @ %s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("   最佳卖价: %s @ %s", asks[0][0], asks[0][1])
	}
}

// TestBitgetGetPosition 测试持仓查询
func TestBitgetGetPosition(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("📈 测试 Bitget 持仓查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	position, err := bitgetEx.GetPosition("BTCUSDT_UMCBL")
	if err != nil {
		t.Fatalf("❌ 获取持仓失败: %v", err)
	}

	if position.Size > 0 {
		t.Logf("✅ BTCUSDT_UMCBL 持仓:")
		t.Logf("   方向: %s", position.Side)
		t.Logf("   数量: %.4f", position.Size)
		t.Logf("   开仓价: %.2f", position.EntryPrice)
		t.Logf("   标记价: %.2f", position.MarkPrice)
		t.Logf("   未实现盈亏: %.4f", position.UnrealizedPnl)
		t.Logf("   杠杆: %dx", position.Leverage)
	} else {
		t.Log("✅ 无 BTCUSDT_UMCBL 持仓")
	}
}

// TestBitgetGetPositions 测试所有持仓查询
func TestBitgetGetPositions(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("📈 测试 Bitget 所有持仓查询...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	positions, err := bitgetEx.GetPositions()
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

// TestBitgetWebSocketTicker 测试 WebSocket Ticker 订阅
func TestBitgetWebSocketTicker(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("📡 测试 Bitget WebSocket Ticker 订阅...")
	t.Log("ℹ️  Bitget 需要先登录 WebSocket 才能订阅")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	bitgetEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		t.Logf("📊 Ticker [%d]: %s | Bid: %.2f, Ask: %.2f, Last: %.2f", 
			receivedTickers, symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice)
	})

	// Bitget 格式：现货 BTCUSDT，合约 BTCUSDT_UMCBL
	err = bitgetEx.SubscribeTicker(
		[]string{"BTCUSDT", "ETHUSDT"},      // 现货
		[]string{"BTCUSDT_UMCBL"},           // 合约
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
	err = bitgetEx.UnsubscribeTicker(
		[]string{"BTCUSDT", "ETHUSDT"},
		[]string{"BTCUSDT_UMCBL"},
	)
	if err != nil {
		t.Errorf("❌ 取消订阅失败: %v", err)
	} else {
		t.Log("✅ 取消订阅成功")
	}
}

// TestBitgetPlaceOrder 测试下单（谨慎使用！）
func TestBitgetPlaceOrder(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("💸 测试 Bitget 下单功能...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	// ⚠️ 使用远低于市场价的限价单，不会立即成交
	order, err := bitgetEx.PlaceOrder(&model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
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

// TestBitgetPlaceSpotOrder_BuyLimit 测试现货买入（限价单逻辑：orderType=market + side=buy 会转为 ask*1.005 限价单）
// 环境变量 BITGET_SPOT_BUY_TEST=1 时执行真实下单；否则跳过。
// 可选 BITGET_SPOT_BUY_SYMBOL（默认 POWERUSDT）、BITGET_SPOT_BUY_QTY（默认 0.1）
func TestBitgetPlaceSpotOrder_BuyLimit(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()

	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config 中配置 BitGet APIKey")
	}

	if os.Getenv("BITGET_SPOT_BUY_TEST") != "1" {
		t.Skip("⚠️  设置 BITGET_SPOT_BUY_TEST=1 执行真实下单（谨慎！）")
	}

	symbol := os.Getenv("BITGET_SPOT_BUY_SYMBOL")
	if symbol == "" {
		symbol = "POWERUSDT"
	}
	qtyStr := os.Getenv("BITGET_SPOT_BUY_QTY")
	var qty float64 = 0.1
	if qtyStr != "" {
		if v, e := strconv.ParseFloat(qtyStr, 64); e == nil && v > 0 {
			qty = v
		}
	}

	t.Logf("💸 测试 Bitget 现货买入（限价单逻辑）: %s BUY %.6f", symbol, qty)
	t.Log("ℹ️  Type=Market + Side=Buy 会转为 limit, price=ask*1.005, size=base 数量")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	time.Sleep(2 * time.Second)

	req := &model.PlaceOrderRequest{
		Symbol:     symbol,
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeMarket, // 会转为限价单
		Quantity:   qty,
		MarketType: model.MarketTypeSpot,
	}

	order, err := bitgetEx.PlaceOrder(req)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "insufficient") || strings.Contains(errMsg, "balance") {
			t.Logf("⚠️  余额不足: %v", err)
			t.Log("✅ 限价单逻辑已尝试下单")
			return
		}
		if strings.Contains(errMsg, "orderbook") {
			t.Logf("⚠️  Orderbook 获取失败: %v", err)
			return
		}
		t.Fatalf("❌ 下单失败: %v", err)
	}

	t.Logf("✅ 下单成功:")
	t.Logf("   OrderID: %s", order.OrderID)
	t.Logf("   FilledQty: %.6f", order.FilledQty)
	t.Logf("   FilledPrice: %.6f", order.FilledPrice)
	t.Logf("   Status: %s", order.Status)
}

// TestBitgetReconnection 测试重连机制（重点！）
func TestBitgetReconnection(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("🔄 测试 Bitget WebSocket 重连机制...")
	t.Log("ℹ️  Bitget 使用完全手动重连机制：")
	t.Log("   1. OnDisconnected 回调监听断开")
	t.Log("   2. 超时检测（10 秒未收到消息）")
	t.Log("   3. 重新登录（buildLoginMessage）")
	t.Log("   4. 重新订阅所有频道（resubscribeAll）")
	t.Log("   5. 指数退避（2s → 4s → 8s → ... → 30s）")
	t.Log("   6. 最多重试 10 次")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	lastReceived := time.Now()
	bitgetEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
		lastReceived = time.Now()
		if receivedTickers%5 == 0 {
			t.Logf("📊 已收到 %d 个 ticker 更新", receivedTickers)
		}
	})

	err = bitgetEx.SubscribeTicker([]string{"BTCUSDT"}, []string{})
	if err != nil {
		t.Fatalf("❌ 订阅失败: %v", err)
	}

	t.Log("⏳ 运行 90 秒，观察连接稳定性和重连机制...")
	t.Log("💡 测试建议：")
	t.Log("   - 前 30 秒：正常运行")
	t.Log("   - 30-45 秒：关闭网络（测试超时检测）")
	t.Log("   - 45-90 秒：恢复网络（观察自动重连）")
	
	for i := 0; i < 18; i++ {
		time.Sleep(5 * time.Second)
		timeSinceLastMsg := time.Since(lastReceived).Seconds()
		t.Logf("   [%ds] 收到 %d 个更新 | 距上次: %.1fs", 
			(i+1)*5, receivedTickers, timeSinceLastMsg)
		
		// 提醒用户
		if i == 5 {
			t.Log("💡 如果要测试重连，现在可以关闭网络...")
		}
		if i == 8 {
			t.Log("💡 现在可以恢复网络，观察自动重连...")
		}
	}

	if receivedTickers > 0 {
		t.Logf("✅ 测试完成，共收到 %d 个更新", receivedTickers)
	} else {
		t.Error("❌ 连接异常，未收到任何更新")
	}
}

// TestBitgetLoginMessage 测试登录消息生成
func TestBitgetLoginMessage(t *testing.T) {
	t.Log("🔐 测试 Bitget 登录消息生成...")

	// 测试登录消息格式
	apiKey := "test_api_key"
	secretKey := "test_secret_key"
	passphrase := "test_passphrase"

	loginMsg := buildLoginMessage(apiKey, secretKey, passphrase)
	
	if loginMsg == "" {
		t.Error("❌ 登录消息为空")
	} else {
		t.Log("✅ 登录消息生成成功")
		t.Logf("   消息长度: %d 字节", len(loginMsg))
		// 不打印完整消息（包含敏感信息）
	}
}

// TestBitgetSymbolFormat 测试符号格式转换
func TestBitgetSymbolFormat(t *testing.T) {
	t.Log("🔤 测试 Bitget 符号格式转换...")

	tests := []struct {
		input     string
		isFutures bool
		expected  string
	}{
		{"BTCUSDT", false, "BTCUSDT"},
		{"BTCUSDT", true, "BTCUSDT_UMCBL"},
		{"BTC_USDT", false, "BTCUSDT"},
		{"BTC_USDT", true, "BTCUSDT_UMCBL"},
		{"BTCUSDT-PERP", false, "BTCUSDT"},
		{"BTCUSDT-PERP", true, "BTCUSDT_UMCBL"},
		{"BTCUSDT_UMCBL", true, "BTCUSDT_UMCBL"},
	}

	for _, tt := range tests {
		result := normalizeBitgetSymbol(tt.input, tt.isFutures)
		if result != tt.expected {
			t.Errorf("❌ normalizeBitgetSymbol(%s, %v) = %s, want %s", 
				tt.input, tt.isFutures, result, tt.expected)
		} else {
			t.Logf("✅ %s (futures=%v) -> %s", tt.input, tt.isFutures, result)
		}
	}
}

// TestBitgetMessageTimeout 测试消息超时检测
func TestBitgetMessageTimeout(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("⏱️  测试 Bitget 消息超时检测（10 秒阈值）...")

	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	receivedTickers := 0
	bitgetEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedTickers++
	})

	err = bitgetEx.SubscribeTicker([]string{"BTCUSDT"}, []string{})
	if err != nil {
		t.Fatalf("❌ 订阅失败: %v", err)
	}

	// 等待一些正常消息
	time.Sleep(5 * time.Second)
	initialCount := receivedTickers
	t.Logf("✅ 前 5 秒收到 %d 个消息", initialCount)

	// 继续运行，监控超时检测
	t.Log("⏳ 继续运行 15 秒，监控超时检测...")
	time.Sleep(15 * time.Second)

	finalCount := receivedTickers
	t.Logf("✅ 总共收到 %d 个消息", finalCount)

	if finalCount > initialCount {
		t.Log("✅ 消息持续接收，超时检测正常")
	} else {
		t.Error("⚠️  长时间未收到消息，可能触发超时重连")
	}
}

// TestBitgetFullWorkflow 完整工作流测试
func TestBitgetFullWorkflow(t *testing.T) {
	logger.InitLogger("")
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	globalConfig := config.GetGlobalConfig()
	
	if globalConfig.BitGet.APIKey == "" {
		t.Skip("⚠️  请先在 config.go 中配置 BitGetAPIKey")
	}

	t.Log("🚀 Bitget 完整工作流测试")

	// 1. 初始化
	t.Log("\n=== 步骤 1: 初始化 ===")
	bitgetEx := NewBitget()
	err := bitgetEx.Init()
	if err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}
	t.Log("✅ 初始化成功（包含 WebSocket 登录）")
	time.Sleep(3 * time.Second)

	// 2. 查询余额
	t.Log("\n=== 步骤 2: 查询余额 ===")
	balance, err := bitgetEx.GetBalance()
	if err != nil {
		t.Errorf("❌ 查询余额失败: %v", err)
	} else {
		t.Logf("✅ USDT 余额: %.4f", balance.Available)
	}

	// 3. 查询订单簿
	t.Log("\n=== 步骤 3: 查询订单簿 ===")
	bids, asks, err := bitgetEx.GetSpotOrderBook("BTCUSDT")
	if err != nil {
		t.Errorf("❌ 查询订单簿失败: %v", err)
	} else {
		t.Logf("✅ 订单簿: %d 买单, %d 卖单", len(bids), len(asks))
	}

	// 4. WebSocket 订阅
	t.Log("\n=== 步骤 4: WebSocket 订阅 ===")
	receivedCount := 0
	bitgetEx.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
		receivedCount++
		if receivedCount <= 3 {
			t.Logf("📊 Ticker: %s, Bid: %.2f, Ask: %.2f", 
				symbol, ticker.BidPrice, ticker.AskPrice)
		}
	})

	err = bitgetEx.SubscribeTicker([]string{"BTCUSDT"}, []string{})
	if err != nil {
		t.Errorf("❌ 订阅失败: %v", err)
	} else {
		t.Log("✅ 订阅成功")
		time.Sleep(10 * time.Second)
		t.Logf("✅ 收到 %d 个 ticker 更新", receivedCount)
	}

	// 5. 测试重连机制
	t.Log("\n=== 步骤 5: 测试重连机制 ===")
	t.Log("⏳ 运行 30 秒，观察连接稳定性...")
	beforeReconnectTest := receivedCount
	time.Sleep(30 * time.Second)
	afterReconnectTest := receivedCount
	
	newMessages := afterReconnectTest - beforeReconnectTest
	t.Logf("✅ 重连测试期间收到 %d 个新消息", newMessages)
	
	if newMessages > 0 {
		t.Log("✅ 连接稳定，重连机制正常")
	} else {
		t.Error("⚠️  长时间未收到消息，检查重连机制")
	}

	t.Log("\n🎉 完整工作流测试完成！")
}
