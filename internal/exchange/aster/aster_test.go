package aster

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/test"
)

// TestAsterTicker_Spot 测试现货ticker订阅
func TestAsterTicker_Spot(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := aster.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 用于收集ticker数据的通道和锁
	var (
		tickers   []*model.Ticker
		tickersMu sync.Mutex
		wg        sync.WaitGroup
	)

	// 设置ticker回调
	aster.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		tickersMu.Lock()
		defer tickersMu.Unlock()

		tickers = append(tickers, ticker)
		t.Logf("[SPOT] Received ticker - Symbol: %s, Bid: %.2f, Ask: %.2f, LastPrice: %.2f, Timestamp: %s",
			ticker.Symbol,
			ticker.BidPrice,
			ticker.AskPrice,
			ticker.LastPrice,
			ticker.Timestamp.Format(time.RFC3339),
		)
	})

	// 订阅现货交易对（BTC/USDT, ETH/USDT）
	spotSymbols := []string{"BTCUSDT", "ETHUSDT"}

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := aster.SubscribeTicker(spotSymbols, nil)
		if err != nil {
			t.Errorf("Failed to subscribe spot ticker: %v", err)
			return
		}
		t.Logf("Subscribed to spot symbols: %v", spotSymbols)
	}()

	// 等待一段时间接收ticker数据
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 等待ticker数据或超时
	tickerTimer := time.NewTicker(500 * time.Millisecond)
	defer tickerTimer.Stop()

	receivedCount := 0
	for {
		select {
		case <-ctx.Done():
			t.Logf("Test timeout, received %d tickers", receivedCount)
			return
		case <-tickerTimer.C:
			tickersMu.Lock()
			currentCount := len(tickers)
			tickersMu.Unlock()

			if currentCount > receivedCount {
				receivedCount = currentCount
				t.Logf("Received %d ticker updates so far", receivedCount)
			}

			// 如果收到了至少一个ticker，认为测试成功
			if receivedCount >= 1 {
				t.Logf("Test successful! Received %d ticker(s)", receivedCount)
				return
			}
		}
	}
}

// TestAsterTicker_Futures 测试合约ticker订阅
func TestAsterTicker_Futures(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := aster.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 用于收集ticker数据的通道和锁
	var (
		tickers   []*model.Ticker
		tickersMu sync.Mutex
	)

	// 设置ticker回调
	aster.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		tickersMu.Lock()
		defer tickersMu.Unlock()

		tickers = append(tickers, ticker)
		t.Logf("[FUTURES] Received ticker - Symbol: %s, Bid: %.2f, Ask: %.2f, LastPrice: %.2f, Timestamp: %s",
			ticker.Symbol,
			ticker.BidPrice,
			ticker.AskPrice,
			ticker.LastPrice,
			ticker.Timestamp.Format(time.RFC3339),
		)
	})

	// 订阅合约交易对（BTC/USDT永续合约）
	futuresSymbols := []string{"BTCUSDT"}

	err = aster.SubscribeTicker(nil, futuresSymbols)
	if err != nil {
		t.Fatalf("Failed to subscribe futures ticker: %v", err)
	}
	t.Logf("Subscribed to futures symbols: %v", futuresSymbols)

	// 等待一段时间接收ticker数据
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 等待ticker数据或超时
	tickerTimer := time.NewTicker(500 * time.Millisecond)
	defer tickerTimer.Stop()

	receivedCount := 0
	for {
		select {
		case <-ctx.Done():
			t.Logf("Test timeout, received %d tickers", receivedCount)
			return
		case <-tickerTimer.C:
			tickersMu.Lock()
			currentCount := len(tickers)
			tickersMu.Unlock()

			if currentCount > receivedCount {
				receivedCount = currentCount
				t.Logf("Received %d ticker updates so far", receivedCount)
			}

			// 如果收到了至少一个ticker，认为测试成功
			if receivedCount >= 1 {
				t.Logf("Test successful! Received %d ticker(s)", receivedCount)
				return
			}
		}
	}
}

// TestAsterTicker_SpotAndFutures 测试同时订阅现货和合约
func TestAsterTicker_SpotAndFutures(t *testing.T) {
	// 创建币安实例
	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := aster.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 用于收集ticker数据的map（按symbol分组）
	var (
		spotTickers    = make(map[string][]*model.Ticker)
		futuresTickers = make(map[string][]*model.Ticker)
		tickersMu      sync.Mutex
	)

	// 设置ticker回调
	aster.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		tickersMu.Lock()
		defer tickersMu.Unlock()

		// 判断是现货还是合约（这里简化处理，实际应该通过marketType区分）
		// 由于回调中没有marketType，我们根据订阅列表来判断
		if isSpotSymbol(symbol) {
			spotTickers[symbol] = append(spotTickers[symbol], ticker)
			t.Logf("[SPOT] Received ticker - Symbol: %s, Bid: %.2f, Ask: %.2f",
				ticker.Symbol, ticker.BidPrice, ticker.AskPrice)
		} else {
			futuresTickers[symbol] = append(futuresTickers[symbol], ticker)
			t.Logf("[FUTURES] Received ticker - Symbol: %s, Bid: %.2f, Ask: %.2f",
				ticker.Symbol, ticker.BidPrice, ticker.AskPrice)
		}
	})

	// 订阅现货和合约交易对
	spotSymbols := []string{"BTCUSDT"}
	futuresSymbols := []string{"BTCUSDT"}

	err = aster.SubscribeTicker(spotSymbols, futuresSymbols)
	if err != nil {
		t.Fatalf("Failed to subscribe tickers: %v", err)
	}
	t.Logf("Subscribed to spot: %v, futures: %v", spotSymbols, futuresSymbols)

	// 等待一段时间接收ticker数据
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tickerTimer := time.NewTicker(1 * time.Second)
	defer tickerTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			tickersMu.Lock()
			spotCount := 0
			futuresCount := 0
			for _, v := range spotTickers {
				spotCount += len(v)
			}
			for _, v := range futuresTickers {
				futuresCount += len(v)
			}
			tickersMu.Unlock()

			t.Logf("Test completed - Spot: %d tickers, Futures: %d tickers", spotCount, futuresCount)
			return
		case <-tickerTimer.C:
			tickersMu.Lock()
			spotCount := 0
			futuresCount := 0
			for _, v := range spotTickers {
				spotCount += len(v)
			}
			for _, v := range futuresTickers {
				futuresCount += len(v)
			}
			tickersMu.Unlock()

			t.Logf("Current status - Spot: %d tickers, Futures: %d tickers", spotCount, futuresCount)
		}
	}
}

// TestAsterTicker_MultipleSymbols 测试多个ticker订阅（现货和合约）
// 这个测试用例会同时订阅多个交易对，并统计每个交易对接收的数据量
func TestAsterTicker_MultipleSymbols(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := aster.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 定义多个交易对进行测试
	spotSymbols := []string{
		"BTCUSDT",
		"ETHUSDT",
		"BNBUSDT",
		"SOLUSDT",
		"ADAUSDT",
	}

	futuresSymbols := []string{
		"BTCUSDT",
		"ETHUSDT",
		"BNBUSDT",
	}

	// 用于按交易对统计ticker数据
	type SymbolStats struct {
		Count     int
		LastPrice float64
		LastBid   float64
		LastAsk   float64
		FirstTime time.Time
		LastTime  time.Time
	}

	var (
		spotStats    = make(map[string]*SymbolStats)
		futuresStats = make(map[string]*SymbolStats)
		statsMu      sync.Mutex
		totalCount   int
	)

	// 初始化统计map
	statsMu.Lock()
	for _, sym := range spotSymbols {
		spotStats[sym] = &SymbolStats{}
	}
	for _, sym := range futuresSymbols {
		futuresStats[sym] = &SymbolStats{}
	}
	statsMu.Unlock()

	// 设置ticker回调
	aster.SetTickerCallback(func(symbol string, ticker *model.Ticker) {
		statsMu.Lock()
		defer statsMu.Unlock()

		totalCount++

		// 判断是现货还是合约（简化处理，实际应该通过marketType区分）
		if stats, exists := spotStats[symbol]; exists {
			stats.Count++
			stats.LastPrice = ticker.LastPrice
			stats.LastBid = ticker.BidPrice
			stats.LastAsk = ticker.AskPrice
			if stats.FirstTime.IsZero() {
				stats.FirstTime = ticker.Timestamp
			}
			stats.LastTime = ticker.Timestamp

			t.Logf("[SPOT] %s | Bid: %.4f | Ask: %.4f | Last: %.4f | Count: %d",
				symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice, stats.Count)
		} else if stats, exists := futuresStats[symbol]; exists {
			stats.Count++
			stats.LastPrice = ticker.LastPrice
			stats.LastBid = ticker.BidPrice
			stats.LastAsk = ticker.AskPrice
			if stats.FirstTime.IsZero() {
				stats.FirstTime = ticker.Timestamp
			}
			stats.LastTime = ticker.Timestamp

			t.Logf("[FUTURES] %s | Bid: %.4f | Ask: %.4f | Last: %.4f | Count: %d",
				symbol, ticker.BidPrice, ticker.AskPrice, ticker.LastPrice, stats.Count)
		}
	})

	// 订阅多个交易对
	t.Logf("Subscribing to %d spot symbols and %d futures symbols", len(spotSymbols), len(futuresSymbols))
	err = aster.SubscribeTicker(spotSymbols, futuresSymbols)
	if err != nil {
		t.Fatalf("Failed to subscribe tickers: %v", err)
	}

	t.Logf("Spot symbols: %v", spotSymbols)
	t.Logf("Futures symbols: %v", futuresSymbols)

	// 等待一段时间接收ticker数据
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 定期输出统计信息
	statsTimer := time.NewTicker(2 * time.Second)
	defer statsTimer.Stop()

	for {
		select {
		case <-ctx.Done():
			// 测试结束，输出最终统计
			statsMu.Lock()
			t.Log("\n========== Final Statistics ==========")
			t.Logf("Total tickers received: %d\n", totalCount)

			t.Log("--- Spot Market ---")
			for symbol, stats := range spotStats {
				if stats.Count > 0 {
					t.Logf("  %s: Count=%d, LastPrice=%.4f, Bid=%.4f, Ask=%.4f, Spread=%.4f",
						symbol, stats.Count, stats.LastPrice, stats.LastBid, stats.LastAsk,
						stats.LastAsk-stats.LastBid)
				} else {
					t.Logf("  %s: No data received", symbol)
				}
			}

			t.Log("\n--- Futures Market ---")
			for symbol, stats := range futuresStats {
				if stats.Count > 0 {
					t.Logf("  %s: Count=%d, LastPrice=%.4f, Bid=%.4f, Ask=%.4f, Spread=%.4f",
						symbol, stats.Count, stats.LastPrice, stats.LastBid, stats.LastAsk,
						stats.LastAsk-stats.LastBid)
				} else {
					t.Logf("  %s: No data received", symbol)
				}
			}

			// 验证每个交易对是否都收到了数据
			spotWithData := 0
			futuresWithData := 0
			for _, stats := range spotStats {
				if stats.Count > 0 {
					spotWithData++
				}
			}
			for _, stats := range futuresStats {
				if stats.Count > 0 {
					futuresWithData++
				}
			}

			t.Logf("\nSummary: Spot=%d/%d, Futures=%d/%d",
				spotWithData, len(spotSymbols), futuresWithData, len(futuresSymbols))

			// 至少应该收到一些数据
			if totalCount == 0 {
				t.Error("No tickers received at all")
			} else {
				t.Logf("✓ Test completed successfully with %d total tickers", totalCount)
			}

			statsMu.Unlock()
			return

		case <-statsTimer.C:
			// 定期输出当前统计
			statsMu.Lock()
			spotCount := 0
			futuresCount := 0
			for _, stats := range spotStats {
				if stats.Count > 0 {
					spotCount++
				}
			}
			for _, stats := range futuresStats {
				if stats.Count > 0 {
					futuresCount++
				}
			}
			statsMu.Unlock()

			t.Logf("[Progress] Total: %d | Spot active: %d/%d | Futures active: %d/%d",
				totalCount, spotCount, len(spotSymbols), futuresCount, len(futuresSymbols))
		}
	}
}

// TestOrderSpotBook 测试现货订单簿查询
//func TestOrderSpotBook(t *testing.T) {
// 使用测试辅助函数设置代理
// defer test.SetupProxyForTest("http://127.0.0.1:7890")()
//
//	// 创建币安实例
//	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)
//
//	// 初始化
//	err := b.Init()
//	if err != nil {
//		t.Fatalf("Failed to init aster: %v", err)
//	}
//
//	// 转换为具体类型以访问 orderSpotBook 方法
//	asterImpl, ok := b.(*aster)
//	if !ok {
//		t.Fatal("Failed to convert to aster type")
//	}
//
//	// 调用 orderSpotBook 方法（测试买入 1 BTC 的平均价格）
//	t.Log("Calling orderSpotBook() for BTCUSDT with amount 1.0...")
//	asterImpl.orderSpotBook("BTCUSDT", 1.0)
//
//	t.Log("Spot order book query completed successfully")
//}

// TestOrderFuturesBook 测试合约订单簿查询
//func TestOrderFuturesBook(t *testing.T) {
// 使用测试辅助函数设置代理
//	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
//
//	// 创建币安实例
//	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)
//
//	// 初始化
//	err := b.Init()
//	if err != nil {
//		t.Fatalf("Failed to init aster: %v", err)
//	}
//
//	// 转换为具体类型以访问 orderFuturesBook 方法
//	asterImpl, ok := b.(*aster)
//	if !ok {
//		t.Fatal("Failed to convert to aster type")
//	}
//
//	// 调用 orderFuturesBook 方法（测试买入 1 BTC 的平均价格）
//	t.Log("Calling orderFuturesBook() for BTCUSDT with amount 1.0...")
//	asterImpl.orderFuturesBook("AIAUSDT", 3.0)
//
//	t.Log("Futures order book query completed successfully")
//}

// todo mzx
// TestGetBalance 测试查询账户余额
func TestGetBalance(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 查询余额
	balance, err := b.GetBalance()
	if err != nil {
		t.Fatalf("Failed to get balance: %v", err)
	}

	// 验证结果
	if balance == nil {
		t.Fatal("Balance is nil")
	}

	if balance.Asset != "USDT" {
		t.Errorf("Expected asset USDT, got %s", balance.Asset)
	}

	// 输出余额信息
	t.Logf("Balance Info:")
	t.Logf("  Asset: %s", balance.Asset)
	t.Logf("  Available: %.8f", balance.Available)
	t.Logf("  Locked: %.8f", balance.Locked)
	t.Logf("  Total: %.8f", balance.Total)
	t.Logf("  UpdateTime: %s", balance.UpdateTime.Format(time.RFC3339))

	// 验证数值合理性（Available + Locked 应该等于 Total）
	tolerance := 0.00000001
	expectedTotal := balance.Available + balance.Locked
	if abs(expectedTotal-balance.Total) > tolerance {
		t.Errorf("Balance calculation error: Available(%.8f) + Locked(%.8f) = %.8f, but Total = %.8f",
			balance.Available, balance.Locked, expectedTotal, balance.Total)
	}

	t.Log("✓ GetBalance test completed successfully")
}

// TestGetPosition 测试查询单个持仓
func TestGetPosition(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 测试查询 BTCUSDT 持仓
	symbol := "TRADOORUSDT"
	position, err := b.GetPosition(symbol)
	if err != nil {
		t.Fatalf("Failed to get position for %s: %v", symbol, err)
	}

	// 验证结果
	if position == nil {
		t.Fatal("Position is nil")
	}

	if position.Symbol != symbol {
		t.Errorf("Expected symbol %s, got %s", symbol, position.Symbol)
	}

	// 输出持仓信息
	t.Logf("Position Info for %s:", symbol)
	t.Logf("  Symbol: %s", position.Symbol)
	t.Logf("  Side: %s", position.Side)
	t.Logf("  Size: %.8f", position.Size)
	t.Logf("  EntryPrice: %.8f", position.EntryPrice)
	t.Logf("  MarkPrice: %.8f", position.MarkPrice)
	t.Logf("  UnrealizedPnl: %.8f", position.UnrealizedPnl)
	t.Logf("  Leverage: %d", position.Leverage)
	t.Logf("  UpdateTime: %s", position.UpdateTime.Format(time.RFC3339))

	// 验证持仓方向（如果有持仓）
	if position.Size > 0 {
		if position.Side == "" {
			t.Error("Position has size > 0 but Side is empty")
		}
		if position.Side != model.PositionSideLong && position.Side != model.PositionSideShort {
			t.Errorf("Invalid position side: %s (expected LONG or SHORT)", position.Side)
		}
	}

	// 测试空 symbol
	_, err = b.GetPosition("")
	if err == nil {
		t.Error("Expected error for empty symbol, got nil")
	}

	t.Log("✓ GetPosition test completed successfully")
}

// todo mzx
// TestGetPositions 测试查询所有持仓
func TestGetPositions(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 查询所有持仓
	positions, err := b.GetPositions()
	if err != nil {
		t.Fatalf("Failed to get positions: %v", err)
	}

	// 验证结果
	if positions == nil {
		t.Fatal("Positions is nil")
	}

	// 输出持仓信息
	t.Logf("Total positions: %d", len(positions))
	if len(positions) == 0 {
		t.Log("No positions found (this is normal if account has no open positions)")
	} else {
		t.Log("\n=== Positions List ===")
		for i, pos := range positions {
			t.Logf("\nPosition #%d:", i+1)
			t.Logf("  Symbol: %s", pos.Symbol)
			t.Logf("  Side: %s", pos.Side)
			t.Logf("  Size: %.8f", pos.Size)
			t.Logf("  EntryPrice: %.8f", pos.EntryPrice)
			t.Logf("  MarkPrice: %.8f", pos.MarkPrice)
			t.Logf("  UnrealizedPnl: %.8f", pos.UnrealizedPnl)
			t.Logf("  Leverage: %d", pos.Leverage)
			t.Logf("  UpdateTime: %s", pos.UpdateTime.Format(time.RFC3339))

			// 验证每个持仓的数据
			if pos.Symbol == "" {
				t.Errorf("Position #%d has empty symbol", i+1)
			}
			if pos.Size <= 0 {
				t.Errorf("Position #%d (%s) has size <= 0: %.8f", i+1, pos.Symbol, pos.Size)
			}
			if pos.Side == "" {
				t.Errorf("Position #%d (%s) has empty side", i+1, pos.Symbol)
			}
			if pos.Side != model.PositionSideLong && pos.Side != model.PositionSideShort {
				t.Errorf("Position #%d (%s) has invalid side: %s", i+1, pos.Symbol, pos.Side)
			}
		}
	}

	t.Log("✓ GetPositions test completed successfully")
}

// TestGetBalanceAndPositions_Integration 集成测试：同时查询余额和持仓
func TestGetBalanceAndPositions_Integration(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	b := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 查询余额
	t.Log("=== Querying Balance ===")
	balance, err := b.GetBalance()
	if err != nil {
		t.Fatalf("Failed to get balance: %v", err)
	}
	t.Logf("Balance: Available=%.8f, Locked=%.8f, Total=%.8f",
		balance.Available, balance.Locked, balance.Total)

	// 查询所有持仓
	t.Log("\n=== Querying All Positions ===")
	positions, err := b.GetPositions()
	if err != nil {
		t.Fatalf("Failed to get positions: %v", err)
	}
	t.Logf("Total positions: %d", len(positions))

	// 如果有持仓，查询每个持仓的详细信息
	if len(positions) > 0 {
		t.Log("\n=== Querying Individual Positions ===")
		for _, pos := range positions {
			detailPos, err := b.GetPosition(pos.Symbol)
			if err != nil {
				t.Errorf("Failed to get position for %s: %v", pos.Symbol, err)
				continue
			}

			// 验证详细查询的结果与列表查询的结果一致
			if detailPos.Symbol != pos.Symbol {
				t.Errorf("Symbol mismatch: list=%s, detail=%s", pos.Symbol, detailPos.Symbol)
			}
			if detailPos.Size != pos.Size {
				t.Errorf("Size mismatch for %s: list=%.8f, detail=%.8f",
					pos.Symbol, pos.Size, detailPos.Size)
			}

			t.Logf("%s: Side=%s, Size=%.8f, EntryPrice=%.8f, MarkPrice=%.8f, PnL=%.8f",
				detailPos.Symbol, detailPos.Side, detailPos.Size,
				detailPos.EntryPrice, detailPos.MarkPrice, detailPos.UnrealizedPnl)
		}
	}

	t.Log("\n✓ Integration test completed successfully")
}

// abs 返回浮点数的绝对值
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// isSpotSymbol 判断是否是现货交易对（简化实现）
// 注意：由于回调函数中没有传递marketType，这里使用简化逻辑
// 实际使用时，应该修改回调函数或维护订阅列表来区分现货和合约
func isSpotSymbol(symbol string) bool {
	// 由于同一symbol可能同时订阅现货和合约，这里无法准确区分
	// 建议：修改回调函数签名，增加marketType参数
	// 或者分别设置不同的回调函数
	return true // 临时返回true，实际需要根据业务逻辑判断
}

// TestAsterGetFuturesOrderBook 测试合约订单簿查询
func TestAsterGetFuturesOrderBook(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📊 测试 Aster 合约订单簿查询...")

	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	if err := aster.Init(); err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	symbol := "BTCUSDT"
	bids, asks, err := aster.GetFuturesOrderBook(symbol)
	if err != nil {
		t.Fatalf("Failed to get futures order book: %v", err)
	}

	t.Logf("✅ 获取合约订单簿成功: %s", symbol)
	t.Logf("  Bids: %d levels", len(bids))
	t.Logf("  Asks: %d levels", len(asks))
	if len(bids) > 0 {
		t.Logf("  Best bid: 价格=%s, 数量=%s", bids[0][0], bids[0][1])
	}
	if len(asks) > 0 {
		t.Logf("  Best ask: 价格=%s, 数量=%s", asks[0][0], asks[0][1])
	}
}

// TestAsterGetAllBalances 测试所有余额查询
func TestAsterGetAllBalances(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("💰 测试 Aster 所有余额查询...")

	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	if err := aster.Init(); err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	balances, err := aster.GetAllBalances()
	if err != nil {
		t.Fatalf("Failed to get all balances: %v", err)
	}

	if balances == nil {
		t.Fatal("Balances is nil")
	}

	t.Logf("✅ 获取所有余额成功，共 %d 个币种", len(balances))
	for asset, balance := range balances {
		if balance != nil && balance.Total > 0 {
			t.Logf("   %s: Total=%.8f, Available=%.8f, Locked=%.8f",
				asset, balance.Total, balance.Available, balance.Locked)
		}
	}
}

// TestAsterCalculateSlippage 测试滑点计算
func TestAsterCalculateSlippage(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📈 测试 Aster 滑点计算...")

	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	if err := aster.Init(); err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	symbol := "BTCUSDT"
	amount := 0.1
	slippageLimit := 0.5 // 0.5%

	// 测试现货滑点计算
	slippage, maxSize := aster.CalculateSlippage(symbol, amount, false, model.OrderSideBuy, slippageLimit)
	t.Logf("✅ 现货滑点计算: Slippage=%.4f%%, MaxSize=%.8f", slippage, maxSize)

	// 测试合约滑点计算
	slippage, maxSize = aster.CalculateSlippage(symbol, amount, true, model.OrderSideBuy, slippageLimit)
	t.Logf("✅ 合约滑点计算: Slippage=%.4f%%, MaxSize=%.8f", slippage, maxSize)
}

// TestAsterUnsubscribeTicker 测试取消订阅 Ticker
func TestAsterUnsubscribeTicker(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	t.Log("📡 测试 Aster 取消订阅 Ticker...")

	aster := NewAster(config.AsterAPIKey, config.AsterSecretKey)

	if err := aster.Init(); err != nil {
		t.Fatalf("Failed to init aster: %v", err)
	}

	// 先订阅一些交易对
	spotSymbols := []string{"BTCUSDT", "ETHUSDT"}
	futuresSymbols := []string{"BTCUSDT"}

	if err := aster.SubscribeTicker(spotSymbols, futuresSymbols); err != nil {
		t.Fatalf("Failed to subscribe ticker: %v", err)
	}

	t.Logf("✅ 订阅成功: spot=%v, futures=%v", spotSymbols, futuresSymbols)

	// 等待一小段时间确保订阅生效
	time.Sleep(1 * time.Second)

	// 取消订阅
	if err := aster.UnsubscribeTicker(spotSymbols, futuresSymbols); err != nil {
		t.Fatalf("Failed to unsubscribe ticker: %v", err)
	}

	t.Logf("✅ 取消订阅成功: spot=%v, futures=%v", spotSymbols, futuresSymbols)
}
