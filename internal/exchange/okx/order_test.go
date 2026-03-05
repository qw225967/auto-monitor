package okx

import (
	"os"
	"testing"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/test"
)

// ⚠️ 使用说明：
// 1. 本测试文件用于测试 order.go（PlaceOrder 现货/合约）
// 2. 请在下方常量中填写你的 OKX API Key、Secret、Passphrase（或保持占位符则跳过）
// 3. 运行：go test -v -run TestOkxOrder ./internal/exchange/okx/
// 4. 注意：下单测试会向交易所发送真实订单请求（限价单挂单或市价单成交），请使用小金额并谨慎执行

// 硬编码的 OKX API Key 信息（仅用于本文件下单测试）
const (
	orderTestOKXAPIKey     = "your-api-key-here" // 请替换为你的 OKX API Key
	orderTestOKXSecretKey  = ""                  // 请替换为你的 OKX Secret Key
	orderTestOKXPassphrase = ""                  // 请替换为你的 OKX Passphrase
)

// setupTestOkxForOrder 创建并初始化用于下单测试的 OKX 实例（使用本文件硬编码的 API Key）
func setupTestOkxForOrder(t *testing.T) *okx {
	logger.InitLogger("")
	config.InitSelfConfigFromDefault()

	okxEx := NewOkx().(*okx)
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		t.Fatalf("无法获取全局配置")
	}
	globalConfig.OkEx.KeyList = []config.OkExKeyRecord{
		{
			APIKey:       orderTestOKXAPIKey,
			Secret:       orderTestOKXSecretKey,
			Passphrase:   orderTestOKXPassphrase,
			CanBroadcast: true,
		},
	}
	if err := okxEx.Init(); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}
	return okxEx
}

func skipIfNoOrderKey(t *testing.T) {
	if orderTestOKXAPIKey == "your-api-key-here" || orderTestOKXSecretKey == "your-secret-key-here" {
		t.Skip("请先在 order_test.go 中配置 orderTestOKXAPIKey, orderTestOKXSecretKey, orderTestOKXPassphrase")
	}
}

// TestOkxOrderPlaceSpot 测试现货下单 PlaceOrder（MarketTypeSpot）
// 使用限价单、极小数量、远离市价的价格，以降低成交概率（仅验证接口与解析）
func TestOkxOrderPlaceSpot(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoOrderKey(t)

	okxEx := setupTestOkxForOrder(t)

	req := &model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   0.0001,
		Price:      1000, // 远低于市价，挂单不成交
		MarketType: model.MarketTypeSpot,
	}

	order, err := okxEx.PlaceOrder(req)
	if err != nil {
		t.Fatalf("现货 PlaceOrder 失败: %v", err)
	}
	if order == nil {
		t.Fatal("PlaceOrder 返回 nil")
	}
	t.Logf("✅ 现货下单成功: OrderID=%s, Symbol=%s, Side=%s, Type=%s, Status=%s, Quantity=%.6f, Price=%.2f",
		order.OrderID, order.Symbol, order.Side, order.Type, order.Status, order.Quantity, order.Price)
}

// TestOkxOrderPlaceFutures 测试合约下单 PlaceOrder（MarketTypeFutures）
// 使用限价单、1 张、远离市价，以降低成交概率（仅验证接口与解析）
func TestOkxOrderPlaceFutures(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
	skipIfNoOrderKey(t)

	okxEx := setupTestOkxForOrder(t)

	req := &model.PlaceOrderRequest{
		Symbol:     "BTCUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeLimit,
		Quantity:   1,    // 1 张合约（OKX 永续 sz 为张数）
		Price:      1000, // 远低于市价，挂单不成交
		MarketType: model.MarketTypeFutures,
	}

	order, err := okxEx.PlaceOrder(req)
	if err != nil {
		t.Fatalf("合约 PlaceOrder 失败: %v", err)
	}
	if order == nil {
		t.Fatal("PlaceOrder 返回 nil")
	}
	t.Logf("✅ 合约下单成功: OrderID=%s, Symbol=%s, Side=%s, Type=%s, Status=%s, Quantity=%.2f, Price=%.2f",
		order.OrderID, order.Symbol, order.Side, order.Type, order.Status, order.Quantity, order.Price)
}

// TestOkxQueryOrder_Real 使用真实订单 ID 查询订单（集成测试）
// 环境变量: OKX_ORDER_TEST_ORDER_ID（必填）, OKX_ORDER_TEST_SYMBOL（默认 BTCUSDT）, OKX_ORDER_TEST_MARKET（spot|futures，默认 futures）
// 运行: go test -v -run TestOkxQueryOrder_Real ./internal/exchange/okx/
func TestOkxQueryOrder_Real(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	cfg := config.GetGlobalConfig()
	if cfg == nil {
		t.Skip("⚠️  请先加载配置后再运行本测试")
	}
	// 有任一种 OKX 配置即可
	hasKey := (cfg.OKX.APIKey != "" && cfg.OKX.APIKey != "请添加") || len(cfg.OkEx.KeyList) > 0
	if !hasKey {
		t.Skip("⚠️  请先配置 OKX API Key（或 OkEx.KeyList）后再运行本测试")
	}

	orderID := os.Getenv("OKX_ORDER_TEST_ORDER_ID")
	if orderID == "" {
		t.Skip("⚠️  请设置环境变量 OKX_ORDER_TEST_ORDER_ID 为要查询的订单 ID")
	}
	symbol := os.Getenv("OKX_ORDER_TEST_SYMBOL")
	if symbol == "" {
		symbol = "BTCUSDT"
	}
	market := os.Getenv("OKX_ORDER_TEST_MARKET")
	if market == "" {
		market = "futures"
	}

	okxEx := NewOkx()
	if err := okxEx.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	ex, ok := okxEx.(*okx)
	if !ok {
		t.Fatal("❌ 类型断言 *okx 失败")
	}

	var order *model.Order
	var err error
	if market == "spot" {
		t.Logf("🔍 查询 OKX 现货订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QuerySpotOrder(symbol, orderID)
	} else {
		t.Logf("🔍 查询 OKX 合约订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QueryFuturesOrder(symbol, orderID)
	}
	if err != nil {
		t.Fatalf("❌ QueryOrder 失败: %v", err)
	}
	if order == nil {
		t.Fatal("❌ 返回订单为 nil")
	}

	t.Logf("✅ 订单查询成功: OrderID=%s, Symbol=%s, Status=%s, FilledQty=%f, FilledPrice=%f",
		order.OrderID, order.Symbol, order.Status, order.FilledQty, order.FilledPrice)
	if order.Status == model.OrderStatusFilled && (order.FilledQty <= 0 || order.FilledPrice <= 0) {
		t.Logf("⚠️  订单状态为 Filled 但 FilledQty 或 FilledPrice 为空")
	}
}
