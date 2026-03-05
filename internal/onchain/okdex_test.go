package onchain

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain/bundler"
	"auto-arbitrage/internal/utils/test"
)

// TestOkdex_Init 测试初始化功能
func TestOkdex_Init(t *testing.T) {
	okdex := NewOkdex()
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}
}

// TestOkdex_StartSwap_PriceQuery 测试询价功能
func TestOkdex_StartSwap_PriceQuery(t *testing.T) {
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:9876")()

	okdex := NewOkdex()

	// 初始化
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 用于收集价格数据的通道和锁
	var (
		priceInfos []*model.ChainPriceInfo
		priceMu    sync.Mutex
		wg         sync.WaitGroup
	)

	// 设置价格回调
	okdex.SetPriceCallback(func(priceInfo *model.ChainPriceInfo) {
		priceMu.Lock()
		defer priceMu.Unlock()

		priceInfos = append(priceInfos, priceInfo)
	})

	// 创建 SwapInfo（示例：USDT -> ETH）
	swapInfo := &model.SwapInfo{
		FromTokenSymbol:          "USDT",
		ToTokenSymbol:            "AIA",
		FromTokenContractAddress: "0x55d398326f99059ff775485246999027b3197955", // USDT 合约地址（示例）
		ToTokenContractAddress:   "0x48a18a4782b65a0fbed4dca608bb28038b7be339", // AIA 合约地址（示例）
		ChainIndex:               "56",                                         // bsc
		Amount:                   "100",                                        // 100 USDT
		DecimalsFrom:             "18",                                         // USDT 精度
		DecimalsTo:               "9",                                          // ETH 精度
		SwapMode:                 "exactIn",
		Slippage:                 "0.01", // 1% 滑点
		GasLimit:                 "300000",
		WalletAddress:            "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a", // 示例钱包地址
	}

	// 启动 Swap 询价
	wg.Add(1)
	go func() {
		defer wg.Done()
		okdex.StartSwap(swapInfo)
	}()

	// 等待一段时间接收价格数据
	timeout := 600 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// 等待价格数据或超时
	priceTimer := time.NewTicker(1 * time.Second)
	defer priceTimer.Stop()

	receivedCount := 0
	for {
		select {
		case <-timer.C:
			t.Logf("Test timeout after %v", timeout)
			goto done
		case <-priceTimer.C:
			priceMu.Lock()
			currentCount := len(priceInfos)
			priceMu.Unlock()

			if currentCount > receivedCount {
				receivedCount = currentCount
			}

			// 如果收到至少一个价格更新，可以提前结束
			if receivedCount >= 1 {
				goto done
			}
		}
	}

done:
	priceMu.Lock()
	finalCount := len(priceInfos)
	priceMu.Unlock()

	if finalCount == 0 {
		t.Errorf("No price updates received within %v", timeout)
	}

	// 验证价格数据的格式
	if finalCount > 0 {
		priceMu.Lock()
		firstPrice := priceInfos[0]
		priceMu.Unlock()

		if firstPrice == nil {
			t.Error("Price info is nil")
		} else {
			if firstPrice.CoinSymbol == "" {
				t.Error("Price info missing CoinSymbol")
			}
			if firstPrice.ChainPriceBuy == "" {
				t.Error("Price info missing ChainPriceBuy")
			}
			if firstPrice.ChainPriceSell == "" {
				t.Error("Price info missing ChainPriceSell")
			}
			if firstPrice.ChainId == "" {
				t.Error("Price info missing ChainId")
			}
		}
	}
}

// TestOkdex_StartSwap_MultipleQueries 测试多次询价
func TestOkdex_StartSwap_MultipleQueries(t *testing.T) {

	okdex := NewOkdex()

	// 初始化
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 用于收集价格数据的通道和锁
	var (
		priceInfos []*model.ChainPriceInfo
		priceMu    sync.Mutex
	)

	// 设置价格回调
	okdex.SetPriceCallback(func(priceInfo *model.ChainPriceInfo) {
		priceMu.Lock()
		defer priceMu.Unlock()

		priceInfos = append(priceInfos, priceInfo)
	})

	// 创建 SwapInfo
	swapInfo := &model.SwapInfo{
		FromTokenSymbol:          "USDT",
		ToTokenSymbol:            "ETH",
		FromTokenContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		ToTokenContractAddress:   "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		ChainIndex:               "1",
		Amount:                   "50", // 较小的金额
		DecimalsFrom:             "6",
		DecimalsTo:               "18",
		SwapMode:                 "exactIn",
		Slippage:                 "0.01",
		GasLimit:                 "300000",
		WalletAddress:            "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a",
	}

	// 启动 Swap 询价
	okdex.StartSwap(swapInfo)

	// 等待接收多个价格更新（每250ms一次，等待5秒应该能收到约20个更新）
	timeout := 5 * time.Second
	time.Sleep(timeout)

	priceMu.Lock()
	finalCount := len(priceInfos)
	priceMu.Unlock()

	if finalCount == 0 {
		t.Error("No price updates received")
	}
}

// TestOkdex_StartSwap_InvalidSwapInfo 测试无效 SwapInfo
func TestOkdex_StartSwap_InvalidSwapInfo(t *testing.T) {
	okdex := NewOkdex()

	// 初始化
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 测试 nil SwapInfo
	okdex.StartSwap(nil)

	// 测试未初始化的情况
	okdexUninit := NewOkdex()
	swapInfo := &model.SwapInfo{
		FromTokenSymbol:          "USDT",
		ToTokenSymbol:            "ETH",
		FromTokenContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		ToTokenContractAddress:   "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		ChainIndex:               "1",
		Amount:                   "100",
		DecimalsFrom:             "6",
		DecimalsTo:               "18",
		SwapMode:                 "exactIn",
		Slippage:                 "0.01",
		GasLimit:                 "300000",
		WalletAddress:            "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEb",
	}

	okdexUninit.StartSwap(swapInfo)
}

// TestOkdex_SetPriceCallback 测试设置价格回调
func TestOkdex_SetPriceCallback(t *testing.T) {
	okdex := NewOkdex()

	// 测试设置回调
	okdex.SetPriceCallback(func(price *model.ChainPriceInfo) {
		_ = price
	})

	if okdex == nil {
		t.Error("Okdex instance is nil")
	}
}

// setupBundler 设置 bundler（如果配置了的话）
func setupBundler(t *testing.T) *bundler.Manager {
	bundlerMgr := bundler.NewManager()
	cfg := config.GetGlobalConfig()
	if cfg == nil {
		return nil
	}
	b := &cfg.Bundler

	if b.FlashbotsPrivateKey != "" {
		if fb, err := bundler.NewFlashbotsBundler(b.FlashbotsPrivateKey, ""); err == nil {
			bundlerMgr.AddBundler(fb)
			t.Logf("✅ Flashbots bundler added")
		}
	}

	if fb48, err := bundler.NewFortyEightClubBundler(b.FortyEightClubAPIKey, "", b.FortyEightSoulPointPrivateKey); err == nil {
		bundlerMgr.AddBundler(fb48)
		if b.FortyEightSoulPointPrivateKey != "" {
			t.Logf("✅ 48club bundler added (with 48SoulPoint)")
		} else {
			t.Logf("✅ 48club bundler added")
		}
	}

	if len(bundlerMgr.GetAllBundlers()) == 0 {
		t.Logf("ℹ️  No bundlers configured, will use normal broadcast")
		return nil
	}

	return bundlerMgr
}

// TestOkdex_SwapAndBroadcast 测试完整的下单兑换流程（只执行一次交易）
// 包括：询价 -> 获取交易数据 -> 广播交易 -> 查询交易结果
// 注意：这是一个真实交易测试，会实际广播交易到链上，请确保有足够的余额和正确的配置
func TestOkdex_SwapAndBroadcast(t *testing.T) {
	config.InitSelfConfigFromDefault()
	shouldBroadcast := true

	client := NewOkdex()

	// 初始化
	err := client.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 设置 bundler（如果配置了）
	bundlerMgr := setupBundler(t)
	if bundlerMgr != nil && config.GetGlobalConfig() != nil && config.GetGlobalConfig().Bundler.UseBundler {
		if okdexClient, ok := client.(*okdex); ok {
			okdexClient.SetBundlerManager(bundlerMgr, true)
			t.Logf("✅ Bundler enabled for this test")
		}
	}

	// 用于收集交易数据的通道和锁
	var (
		priceMu        sync.Mutex
		latestBuyTx    string
		latestSellTx   string
		chainIndex     string
		txDataReceived bool
		done           = make(chan bool, 1) // 用于通知主线程已获取到交易数据
	)

	// 设置价格回调，收集买入和卖出交易数据
	client.SetPriceCallback(func(priceInfo *model.ChainPriceInfo) {
		priceMu.Lock()
		defer priceMu.Unlock()

		// 如果已经获取到交易数据，不再处理
		if txDataReceived {
			return
		}

		// 保存交易数据（需要同时有买入和卖出交易数据）
		if priceInfo.ChainBuyTx != "" {
			latestBuyTx = priceInfo.ChainBuyTx
		}
		if priceInfo.ChainSellTx != "" {
			latestSellTx = priceInfo.ChainSellTx
		}
		chainIndex = priceInfo.ChainId

		// 只有当买入和卖出交易数据都有时，才通知主线程
		if latestBuyTx != "" && latestSellTx != "" {
			txDataReceived = true
			select {
			case done <- true:
			default:
			}
		}
	})

	// 创建 SwapInfo（示例：USDT -> Token）
	// 注意：请根据实际情况修改代币地址、链ID、钱包地址等参数
	swapInfo := &model.SwapInfo{
		FromTokenSymbol:          "USDT",
		ToTokenSymbol:            "RIVER",
		FromTokenContractAddress: "0x55d398326f99059ff775485246999027b3197955", // USDT 合约地址（BSC）
		ToTokenContractAddress:   "0xda7ad9dea9397cffddae2f8a052b82f1484252b3", // AIA 合约地址（BSC）
		ChainIndex:               "56",                                         // BSC 链
		Amount:                   "4",                                          // 10 USDT（测试用较小金额）
		DecimalsFrom:             "18",                                         // USDT 精度
		DecimalsTo:               "18",                                          // Token 精度
		SwapMode:                 "exactIn",
		Slippage:                 "0.1", // 0.1% 滑点
		GasLimit:                 "500000",
		WalletAddress:            "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a", // 钱包地址
	}

	// 启动 Swap 询价
	client.StartSwap(swapInfo)

	// 等待获取第一个交易数据（最多等待30秒）
	timeout := 30 * time.Second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		t.Fatalf("Timeout: Failed to receive transaction data within %v", timeout)
	case <-done:
		// 获取到交易数据，停止询价循环
		if o, ok := client.(*okdex); ok {
			o.mu.Lock()
			o.swapRunning = false
			o.mu.Unlock()
		}

		// 获取交易数据
		priceMu.Lock()
		buyTx := latestBuyTx
		sellTx := latestSellTx
		chainIdx := chainIndex
		priceMu.Unlock()

		if buyTx == "" || sellTx == "" {
			t.Fatalf("Transaction data incomplete: buyTx=%v, sellTx=%v", buyTx != "", sellTx != "")
		}

		// 如果设置了 shouldBroadcast=true，则执行真实的广播
		if shouldBroadcast {
			// 检查是否使用 bundler
			useBundler := false
			if okdexClient, ok := client.(*okdex); ok {
				okdexClient.mu.RLock()
				useBundler = okdexClient.useBundler
				okdexClient.mu.RUnlock()
			}
			if useBundler {
				t.Logf("📦 Using bundler to reduce gas fees")
			}

			// 第一步：执行买入交易 USDT -> AIA
			t.Logf("Step 1: Broadcasting BUY transaction (USDT -> AIA)...")
			buyTxHash, err := client.BroadcastSwapTx(SwapDirectionBuy)
			if err != nil {
				t.Fatalf("Failed to broadcast BUY transaction: %v", err)
			}
			if useBundler {
				t.Logf("BUY transaction sent via bundler, bundleHash: %s", buyTxHash)
			} else {
				t.Logf("BUY transaction broadcasted, txHash: %s", buyTxHash)
			}

			// 等待买入交易确认
			t.Logf("Waiting for BUY transaction confirmation...")
			buySuccess := waitForTxConfirmation(t, client, buyTxHash, chainIdx, 30*time.Second)
			if !buySuccess {
				t.Fatalf("BUY transaction failed or timeout, cannot proceed with SELL transaction")
			}

			// 买入成功后，重新获取最新的卖出交易数据
			t.Logf("Re-fetching latest SELL transaction data...")
			_, err = reFetchSellTxData(t, client, swapInfo, 30*time.Second)
			if err != nil {
				t.Fatalf("Failed to re-fetch SELL transaction data: %v", err)
			}

			// 第二步：执行卖出交易 AIA -> USDT
			t.Logf("Step 2: Broadcasting SELL transaction (AIA -> USDT)...")
			sellTxHash, err := client.BroadcastSwapTx(SwapDirectionSell)
			if err != nil {
				t.Fatalf("Failed to broadcast SELL transaction: %v", err)
			}
			// 检查是否使用 bundler（useBundler 已在前面声明）
			if okdexClient, ok := client.(*okdex); ok {
				okdexClient.mu.RLock()
				useBundler = okdexClient.useBundler
				okdexClient.mu.RUnlock()
			}
			if useBundler {
				t.Logf("SELL transaction sent via bundler, bundleHash: %s", sellTxHash)
			} else {
				t.Logf("SELL transaction broadcasted, txHash: %s", sellTxHash)
			}

			// 等待卖出交易确认
			t.Logf("Waiting for SELL transaction confirmation...")
			waitForTxConfirmation(t, client, sellTxHash, chainIdx, 30*time.Second)

			t.Logf("✅ Round-trip swap test completed: USDT -> AIA -> USDT")
		}
	}
}

// waitForTxConfirmation 等待交易确认，返回是否成功
func waitForTxConfirmation(t *testing.T, client OnchainClient, txHash, chainIndex string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		<-ticker.C
		result, err := client.GetTxResult(txHash, chainIndex)
		if err != nil {
			if err.Error() == "pending" {
				continue
			}
			// 如果是其他错误（如 out of gas），记录并返回 false
			t.Logf("Error querying transaction: %v", err)
			return false
		}

		if result.Status == constants.TradeStatusSuccess {
			t.Logf("Transaction confirmed: Status=%s, AmountIn=%s, AmountOut=%s",
				result.Status, result.AmountIn, result.AmountOut)
			return true
		} else if result.Status == constants.TradeStatusFailed {
			t.Logf("Transaction failed: %s", result.ErrorMsg)
			return false
		}
	}

	t.Logf("Transaction still pending after %v", timeout)
	return false
}

// reFetchSellTxData 重新获取最新的卖出交易数据
func reFetchSellTxData(t *testing.T, client OnchainClient, swapInfo *model.SwapInfo, timeout time.Duration) (string, error) {
	var (
		sellTxMu   sync.Mutex
		sellTx     string
		done       = make(chan bool, 1)
		txReceived bool
	)

	// 设置价格回调
	client.SetPriceCallback(func(priceInfo *model.ChainPriceInfo) {
		sellTxMu.Lock()
		defer sellTxMu.Unlock()

		if txReceived {
			return
		}

		if priceInfo.ChainSellTx != "" {
			sellTx = priceInfo.ChainSellTx
			txReceived = true
			select {
			case done <- true:
			default:
			}
		}
	})

	// 重新启动价格查询循环（使用 StartSwap 确保 swapInfo 被正确设置）
	if o, ok := client.(*okdex); ok {
		o.mu.Lock()
		// 确保 swapInfo 还在
		if o.swapInfo == nil {
			o.swapInfo = swapInfo
		}
		o.swapRunning = true
		o.mu.Unlock()
		// 如果循环没有运行，启动它
		go o.processSwapPriceQueriesLoop()
	} else {
		// 如果不是 okdex 类型，直接调用 StartSwap
		client.StartSwap(swapInfo)
	}

	// 等待获取交易数据
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-timer.C:
		// 停止价格查询循环
		if o, ok := client.(*okdex); ok {
			o.mu.Lock()
			o.swapRunning = false
			o.mu.Unlock()
		}
		return "", fmt.Errorf("timeout waiting for SELL transaction data")
	case <-done:
		sellTxMu.Lock()
		tx := sellTx
		sellTxMu.Unlock()

		// 停止价格查询循环
		if o, ok := client.(*okdex); ok {
			o.mu.Lock()
			o.swapRunning = false
			o.mu.Unlock()
		}

		if tx == "" {
			return "", fmt.Errorf("SELL transaction data is empty")
		}
		return tx, nil
	}
}

// todo mzx
// TestOkdex_GetAllTokenBalances 测试获取所有代币余额功能
func TestOkdex_GetAllTokenBalances(t *testing.T) {
	// 设置代理（如果需要）
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:9876")()

	// 创建并初始化 OKEx DEX 客户端
	okdex := NewOkdex()
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 测试用例：查询 BSC 链上的代币余额
	// 使用示例钱包地址（这是测试中常用的地址）
	testAddress := "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a"
	testChains := "56" // BSC 链
	excludeRiskToken := true

	t.Logf("Testing GetAllTokenBalances with address: %s, chains: %s", testAddress, testChains)

	// 调用 GetAllTokenBalances
	assets, err := okdex.GetAllTokenBalances(testAddress, testChains, excludeRiskToken)
	if err != nil {
		t.Fatalf("GetAllTokenBalances failed: %v", err)
	}

	// 验证返回结果
	if assets == nil {
		t.Fatal("GetAllTokenBalances returned nil")
	}

	t.Logf("Successfully retrieved %d token assets", len(assets))

	// 验证资产数据结构
	for i, asset := range assets {
		if asset.ChainIndex == "" {
			t.Errorf("Asset[%d]: ChainIndex is empty", i)
		}
		if asset.Symbol == "" {
			t.Errorf("Asset[%d]: Symbol is empty", i)
		}
		if asset.Address == "" {
			t.Errorf("Asset[%d]: Address is empty", i)
		}
		if asset.Balance == "" {
			t.Errorf("Asset[%d]: Balance is empty", i)
		}

		// 打印资产信息
		t.Logf("Asset[%d]: Chain=%s, Symbol=%s, Balance=%s, Price=%s USD, Contract=%s, RiskToken=%v",
			i,
			asset.ChainIndex,
			asset.Symbol,
			asset.Balance,
			asset.TokenPrice,
			asset.TokenContractAddress,
			asset.IsRiskToken,
		)
	}

	// 验证返回的链ID是否匹配
	for _, asset := range assets {
		if asset.ChainIndex != testChains {
			t.Errorf("Asset chain mismatch: expected %s, got %s", testChains, asset.ChainIndex)
		}
	}

	// 如果过滤风险代币，验证没有风险代币
	if excludeRiskToken {
		for i, asset := range assets {
			if asset.IsRiskToken {
				t.Errorf("Asset[%d] is a risk token but should be filtered: %s", i, asset.Symbol)
			}
		}
	}
}

// TestOkdex_GetAllTokenBalances_MultipleChains 测试多链查询
func TestOkdex_GetAllTokenBalances_MultipleChains(t *testing.T) {
	// 设置代理（如果需要）
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:9876")()

	// 创建并初始化 OKEx DEX 客户端
	okdex := NewOkdex()
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 测试用例：查询多条链上的代币余额
	testAddress := "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a"
	testChains := "1,56,8453" // Ethereum, BSC, Base
	excludeRiskToken := true

	t.Logf("Testing GetAllTokenBalances with multiple chains: %s", testChains)

	// 调用 GetAllTokenBalances
	assets, err := okdex.GetAllTokenBalances(testAddress, testChains, excludeRiskToken)
	if err != nil {
		t.Fatalf("GetAllTokenBalances failed: %v", err)
	}

	// 验证返回结果
	if assets == nil {
		t.Fatal("GetAllTokenBalances returned nil")
	}

	t.Logf("Successfully retrieved %d token assets across multiple chains", len(assets))

	// 统计每个链的资产数量
	chainCount := make(map[string]int)
	for _, asset := range assets {
		chainCount[asset.ChainIndex]++
	}

	// 打印每个链的资产数量
	for chain, count := range chainCount {
		t.Logf("Chain %s: %d assets", chain, count)
	}

	// 验证至少有一个资产
	if len(assets) == 0 {
		t.Log("Warning: No assets found (this might be normal if the address has no balance)")
	}
}

// TestOkdex_GetAllTokenBalances_InvalidAddress 测试无效地址
func TestOkdex_GetAllTokenBalances_InvalidAddress(t *testing.T) {
	// 设置代理（如果需要）
	// 使用测试辅助函数设置代理
	defer test.SetupProxyForTest("http://127.0.0.1:9876")()

	// 创建并初始化 OKEx DEX 客户端
	okdex := NewOkdex()
	err := okdex.Init()
	if err != nil {
		t.Fatalf("Failed to init okdex: %v", err)
	}

	// 测试用例：使用无效地址
	invalidAddress := "0xinvalid"
	testChains := "56"
	excludeRiskToken := true

	t.Logf("Testing GetAllTokenBalances with invalid address: %s", invalidAddress)

	// 调用 GetAllTokenBalances，期望返回错误
	assets, err := okdex.GetAllTokenBalances(invalidAddress, testChains, excludeRiskToken)
	if err == nil {
		// API 可能会返回空数组而不是错误，这也是可以接受的
		t.Logf("API returned empty result instead of error (this is acceptable): %d assets", len(assets))
	} else {
		t.Logf("API correctly returned error for invalid address: %v", err)
	}
}

// TestOkdex_GetAllTokenBalances_NotInitialized 测试未初始化的情况
func TestOkdex_GetAllTokenBalances_NotInitialized(t *testing.T) {
	// 创建但不初始化 OKEx DEX 客户端
	okdex := NewOkdex()

	// 测试用例：未初始化就调用 GetAllTokenBalances
	testAddress := "0xd0d8baef8fa6d10c91302bcfb511065ad598ff7a"
	testChains := "56"
	excludeRiskToken := true

	// 应该返回未初始化错误
	assets, err := okdex.GetAllTokenBalances(testAddress, testChains, excludeRiskToken)
	if err == nil {
		t.Fatal("Expected error for uninitialized client, but got nil")
	}

	if assets != nil {
		t.Fatal("Expected nil assets for uninitialized client")
	}

	if !strings.Contains(err.Error(), "not initialized") {
		t.Errorf("Expected 'not initialized' error, but got: %v", err)
	}

	t.Logf("Correctly returned error for uninitialized client: %v", err)
}

// TestQueryTxResult_QueryUntilComplete 仅测试 queryTxResult：用曾「确认成功但数量为空」的 txHash 循环调用，
// 看 OKEx DEX API 是否延迟返回 fromTokenDetails/toTokenDetails，用于定位数量为空问题。
// 运行：OKEX_TX_RESULT_TEST=1 go test -v -run TestQueryTxResult_QueryUntilComplete ./internal/onchain/...
func TestQueryTxResult_QueryUntilComplete(t *testing.T) {
	if os.Getenv("OKEX_TX_RESULT_TEST") != "1" {
		t.Skip("跳过：设置 OKEX_TX_RESULT_TEST=1 并配置 OKEx DEX 后运行")
	}

	client := NewOkdex()
	if err := client.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	o := client.(*okdex)

	chainIndex := "56"
	txHashes := []string{
		"0x90485ef3c8280d80928296a0199c8e0f742eef6dffcf300c0038dae86a171c38",
		"0x7bd641fb7b6a5a41ca81cbbb7c432690e4f3e055c7af0e97c1471dbaa9fd843b",
		"0x20d3e29fbfe952971b9406701d275585b92cac98c8958aada53429193c7c1b8a",
		"0xae3bce49b80012e2da342acc1e213b6041b32f6e926f752821b9465a2c19e87f",
		"0x922b7a9506ea213310b64ca7abbdb7260ac6f0c194dcdcbd90253147745270c0",
		"0x06fdc27aaebc341a613f249da51594eb7e5b3d9e1742beb4bc55d79b80fad2a2",
		"0xff291ff97b8b719c56244f60bb5c2e6e7aab1868c9a6019b26d334c35b1b1fc5",
	}

	const maxRounds = 5
	const interval = 2 * time.Second

	for _, txHash := range txHashes {
		t.Run(txHash, func(t *testing.T) {
			for round := 0; round < maxRounds; round++ {
				if round > 0 {
					time.Sleep(interval)
				}
				result, err := o.queryTxResult(txHash, chainIndex)
				complete := result.AmountIn != "" && result.AmountOut != ""
				t.Logf("  round %d: status=%s err=%v | AmountIn=%q AmountOut=%q | complete=%v",
					round+1, result.Status, err, result.AmountIn, result.AmountOut, complete)
				if err == nil && result.Status == constants.TradeStatusSuccess && complete {
					t.Logf("  结论: 已获取完整兑换记录")
					return
				}
			}
			t.Logf("  结论: %d 轮内未拿到完整 AmountIn/AmountOut，可能 API 未返回 tokenDetails 或需更长延迟", maxRounds)
		})
	}
}
