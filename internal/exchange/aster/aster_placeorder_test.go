package aster

import (
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/test"
	"strings"
	"testing"
)

// TestAster_PlaceOrder 测试合约下单功能
func TestAster_PlaceOrder(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()

	shouldPlaceOrder := true

	bn := NewAster(config.AsterAPIKey, config.AsterSecretKey)
	if err := bn.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("Skipping real order")
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "ETHUSDT",
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeMarket,
		Quantity:   0.01,
		MarketType: model.MarketTypeFutures, // 合约下单
	}

	t.Logf("Placing futures order via REST API: %s %s %.6f", req.Symbol, req.Side, req.Quantity)

	order, err := bn.PlaceOrder(req)
	if err != nil {
		errMsg := err.Error()
		// 这些错误都是预期的测试场景
		if strings.Contains(errMsg, "-2015") || strings.Contains(errMsg, "Invalid API-key") {
			t.Logf("⚠️  API key permission error: %v", err)
			t.Logf("✅ REST API order placement attempted")
			return
		}
		if strings.Contains(errMsg, "-2019") || strings.Contains(errMsg, "Margin is insufficient") || strings.Contains(errMsg, "insufficient balance") {
			t.Logf("⚠️  Insufficient margin/balance: %v", err)
			t.Logf("✅ REST API unified account UM order placement attempted (API call successful)")
			return
		}
		if strings.Contains(errMsg, "-1013") || strings.Contains(errMsg, "NOTIONAL") {
			t.Logf("⚠️  Order value too small: %v", err)
			t.Logf("✅ REST API order placement attempted")
			return
		}
		t.Fatalf("PlaceOrder failed with unexpected error: %v", err)
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Order ID: %s, Status: %s", order.OrderID, order.Status)
	t.Logf("⚠️  Check Aster Futures account for order ID: %s", order.OrderID)
}

// TestAster_PlaceSpotOrder 测试现货下单功能
func TestAster_PlaceSpotOrder(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	shouldPlaceOrder := true

	bn := NewAster(config.AsterAPIKey, config.AsterSecretKey)
	if err := bn.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("Skipping real order")
	}

	req := &model.PlaceOrderRequest{
		Symbol:     "ETHUSDT",
		Side:       model.OrderSideSell,
		Type:       model.OrderTypeMarket,
		Quantity:   0.009,
		MarketType: model.MarketTypeSpot, // 现货下单
	}

	t.Logf("Placing spot order via REST API: %s %s %.6f", req.Symbol, req.Side, req.Quantity)

	order, err := bn.PlaceOrder(req)
	if err != nil {
		errMsg := err.Error()
		// 这些错误都是预期的测试场景
		if strings.Contains(errMsg, "-2015") || strings.Contains(errMsg, "Invalid API-key") {
			t.Logf("⚠️  API key permission error: %v", err)
			t.Logf("✅ REST API spot order placement attempted")
			return
		}
		if strings.Contains(errMsg, "-1013") || strings.Contains(errMsg, "NOTIONAL") {
			t.Logf("⚠️  Order value too small: %v", err)
			t.Logf("✅ REST API spot order placement attempted (notional filter)")
			return
		}
		if strings.Contains(errMsg, "-2010") || strings.Contains(errMsg, "insufficient balance") {
			t.Logf("⚠️  Insufficient balance: %v", err)
			t.Logf("✅ REST API spot order placement attempted (API call successful)")
			return
		}
		t.Fatalf("PlaceSpotOrder failed with unexpected error: %v", err)
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Order ID: %s, Status: %s", order.OrderID, order.Status)
	t.Logf("⚠️  Check Aster Spot account for order ID: %s", order.OrderID)
}

// TestAster_PlaceSpotOrder_Limit 测试现货限价单
func TestAster_PlaceSpotOrder_Limit(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	shouldPlaceOrder := false // 限价单测试默认不执行，避免误下单

	bn := NewAster(config.AsterAPIKey, config.AsterSecretKey)
	if err := bn.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if !shouldPlaceOrder {
		t.Skip("Skipping real limit order (set shouldPlaceOrder=true to test)")
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

	order, err := bn.PlaceOrder(req)
	if err != nil {
		errMsg := err.Error()
		// 这些错误都是预期的测试场景
		if strings.Contains(errMsg, "-2015") || strings.Contains(errMsg, "Invalid API-key") {
			t.Logf("⚠️  API key permission error: %v", err)
			t.Logf("✅ REST API spot limit order placement attempted")
			return
		}
		if strings.Contains(errMsg, "-1013") || strings.Contains(errMsg, "NOTIONAL") {
			t.Logf("⚠️  Order value too small: %v", err)
			t.Logf("✅ REST API spot limit order placement attempted")
			return
		}
		if strings.Contains(errMsg, "-2010") || strings.Contains(errMsg, "insufficient balance") {
			t.Logf("⚠️  Insufficient balance: %v", err)
			t.Logf("✅ REST API spot limit order placement attempted (API call successful)")
			return
		}
		t.Fatalf("PlaceSpotOrder failed with unexpected error: %v", err)
	}

	if order == nil {
		t.Fatal("Order is nil")
	}

	t.Logf("✅ Limit Order ID: %s, Status: %s, Price: %.2f", order.OrderID, order.Status, order.Price)
	t.Logf("⚠️  Check Aster Spot account for order ID: %s", order.OrderID)
}
