package binance

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/test"
)

func getBinanceTestKeys(t *testing.T) (apiKey, secretKey string) {
	c := config.GetGlobalConfig()
	if c == nil {
		return "", ""
	}
	return c.Binance.APIKey, c.Binance.SecretKey
}

func newBinanceForTest(t *testing.T) exchange.Exchange {
	k, s := getBinanceTestKeys(t)
	return NewBinance(k, s)
}

// TestBinanceTicker_Spot 测试现货ticker订阅
func TestBinanceTicker_Spot(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	binance := newBinanceForTest(t)

	// 初始化
	err := binance.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
	}

	// 用于收集ticker数据的通道和锁
	var (
		tickers   []*model.Ticker
		tickersMu sync.Mutex
		wg        sync.WaitGroup
	)

	// 设置ticker回调
	binance.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
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
		err := binance.SubscribeTicker(spotSymbols, nil)
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

// TestBinanceTicker_Futures 测试合约ticker订阅
func TestBinanceTicker_Futures(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	binance := newBinanceForTest(t)

	// 初始化
	err := binance.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
	}

	// 用于收集ticker数据的通道和锁
	var (
		tickers   []*model.Ticker
		tickersMu sync.Mutex
	)

	// 设置ticker回调
	binance.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
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

	err = binance.SubscribeTicker(nil, futuresSymbols)
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

// TestBinanceTicker_SpotAndFutures 测试同时订阅现货和合约
func TestBinanceTicker_SpotAndFutures(t *testing.T) {
	// 创建币安实例
	binance := newBinanceForTest(t)

	// 初始化
	err := binance.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
	}

	// 用于收集ticker数据的map（按symbol分组）
	var (
		spotTickers    = make(map[string][]*model.Ticker)
		futuresTickers = make(map[string][]*model.Ticker)
		tickersMu      sync.Mutex
	)

	// 设置ticker回调
	binance.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
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

	err = binance.SubscribeTicker(spotSymbols, futuresSymbols)
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

// TestBinanceTicker_MultipleSymbols 测试多个ticker订阅（现货和合约）
// 这个测试用例会同时订阅多个交易对，并统计每个交易对接收的数据量
func TestBinanceTicker_MultipleSymbols(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	binance := newBinanceForTest(t)

	// 初始化
	err := binance.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
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
	binance.SetTickerCallback(func(symbol string, ticker *model.Ticker, marketType string) {
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
	err = binance.SubscribeTicker(spotSymbols, futuresSymbols)
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
//	b := newBinanceForTest(t)
//
//	// 初始化
//	err := b.Init()
//	if err != nil {
//		t.Fatalf("Failed to init binance: %v", err)
//	}
//
//	// 转换为具体类型以访问 orderSpotBook 方法
//	binanceImpl, ok := b.(*binance)
//	if !ok {
//		t.Fatal("Failed to convert to binance type")
//	}
//
//	// 调用 orderSpotBook 方法（测试买入 1 BTC 的平均价格）
//	t.Log("Calling orderSpotBook() for BTCUSDT with amount 1.0...")
//	binanceImpl.orderSpotBook("BTCUSDT", 1.0)
//
//	t.Log("Spot order book query completed successfully")
//}

// TestOrderFuturesBook 测试合约订单簿查询
//func TestOrderFuturesBook(t *testing.T) {
// 使用测试辅助函数设置代理
//	defer test.SetupProxyForTest("http://127.0.0.1:7897")()
//
//	// 创建币安实例
//	b := newBinanceForTest(t)
//
//	// 初始化
//	err := b.Init()
//	if err != nil {
//		t.Fatalf("Failed to init binance: %v", err)
//	}
//
//	// 转换为具体类型以访问 orderFuturesBook 方法
//	binanceImpl, ok := b.(*binance)
//	if !ok {
//		t.Fatal("Failed to convert to binance type")
//	}
//
//	// 调用 orderFuturesBook 方法（测试买入 1 BTC 的平均价格）
//	t.Log("Calling orderFuturesBook() for BTCUSDT with amount 1.0...")
//	binanceImpl.orderFuturesBook("AIAUSDT", 3.0)
//
//	t.Log("Futures order book query completed successfully")
//}

// todo mzx
// TestGetBalance 测试查询账户余额
func TestGetBalance(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	// 创建币安实例
	b := newBinanceForTest(t)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
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
	b := newBinanceForTest(t)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
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
	b := newBinanceForTest(t)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
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
	b := newBinanceForTest(t)

	// 初始化
	err := b.Init()
	if err != nil {
		t.Fatalf("Failed to init binance: %v", err)
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

// TestBinanceQueryOrder_Real 使用真实订单 ID + symbol 查询订单（集成测试，支持现货/合约）
// 必填环境变量: BINANCE_ORDER_TEST_ORDER_ID
// 可选环境变量: BINANCE_ORDER_TEST_SYMBOL（默认 BTCUSDT）, BINANCE_ORDER_TEST_MARKET（spot|futures，默认 futures）
// 运行: go test -v -run TestBinanceQueryOrder_Real ./internal/exchange/binance/
func TestBinanceQueryOrder_Real(t *testing.T) {
	defer test.SetupProxyForTest("http://127.0.0.1:7897")()

	config.InitSelfConfigFromDefault()
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Binance.APIKey == "" {
		t.Skip("⚠️  请先配置 Binance API Key 后再运行本测试")
	}

	orderID := os.Getenv("BINANCE_ORDER_TEST_ORDER_ID")
	if orderID == "" {
		t.Skip("⚠️  请设置环境变量 BINANCE_ORDER_TEST_ORDER_ID 为要查询的订单 ID")
	}
	symbol := os.Getenv("BINANCE_ORDER_TEST_SYMBOL")
	if symbol == "" {
		symbol = "BTCUSDT"
	}
	market := os.Getenv("BINANCE_ORDER_TEST_MARKET")
	if market == "" {
		market = "futures"
	}

	bn := newBinanceForTest(t)
	if err := bn.Init(); err != nil {
		t.Fatalf("❌ 初始化失败: %v", err)
	}

	ex, ok := bn.(*binance)
	if !ok {
		t.Fatal("❌ 类型断言 *binance 失败")
	}

	var order *model.Order
	var err error
	if market == "spot" {
		t.Logf("🔍 查询 Binance 现货订单: symbol=%s, orderId=%s", symbol, orderID)
		order, err = ex.QuerySpotOrder(symbol, orderID)
	} else {
		t.Logf("🔍 查询 Binance 合约订单: symbol=%s, orderId=%s", symbol, orderID)
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
