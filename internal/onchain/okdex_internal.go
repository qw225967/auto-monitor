package onchain

import (
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/parallel"
	"fmt"
	"math/big"
	"strings"
	"time"
)

func writeOkdexDebug(location, message, hypothesisId string, data map[string]interface{}) {}

// processSwapPriceQueriesLoop 循环处理 swap 价格查询
// 注意：每个 okdex 实例只有一个协程，循环持续运行
func (o *okdex) processSwapPriceQueriesLoop() {
	ticker := time.NewTicker(500 * time.Millisecond) // 每500ms执行一次
	defer ticker.Stop()

	for {
		o.mu.RLock()
		running := o.swapRunning
		o.mu.RUnlock()

		// 如果 swapRunning 被设置为 false，退出循环
		if !running {
			return
		}

		<-ticker.C
		o.processSwapPriceQueries()
	}
}

// processSwapPriceQueries 处理当前 swap 价格查询
func (o *okdex) processSwapPriceQueries() {
	o.mu.RLock()
	swapInfo := o.swapInfo
	callback := o.priceCallback
	o.mu.RUnlock()

	if !o.isInitialized || swapInfo == nil || callback == nil {
		return
	}

	// 处理当前 swap 查询
	parallel.GoSafe(func() {
		o.processSingleSwapQuery(swapInfo, callback)
	})
}

// processSingleSwapQuery 处理当前 swap 查询
// swapInfo.Amount 现在表示币的数量（ToToken）
// 先查询卖出价格（Coin -> USDT），再查询买入价格（USDT -> Coin）
// 两个方向都使用 exactIn 模式（API 只支持 exactIn）
func (o *okdex) processSingleSwapQuery(swapInfo *model.SwapInfo, callback PriceCallback) {
	if swapInfo == nil {
		return
	}

	// 从 o.swapInfo 读取最新的 Amount，确保使用更新后的值
	o.mu.RLock()
	latestSwapInfo := o.swapInfo
	o.mu.RUnlock()

	// swapInfo.Amount 现在表示币的数量（ToToken）
	// 优先使用最新的 swapInfo.Amount，如果不存在则使用传入的参数
	var coinAmountStr string
	if latestSwapInfo != nil && latestSwapInfo.Amount != "" {
		coinAmountStr = latestSwapInfo.Amount
	} else {
		coinAmountStr = swapInfo.Amount
	}
	convertedCoinAmount, err := o.convertToDecimals(coinAmountStr, swapInfo.DecimalsTo)
	if err != nil {
		return
	}

	// 1. 先查询卖出价格：Coin -> USDT，使用 exactIn 模式（输入币的数量）
	CoinToUSDTTask := &model.SwapTask{
		FromTokenSymbol:          swapInfo.ToTokenSymbol,
		ToTokenSymbol:            swapInfo.FromTokenSymbol,
		FromTokenContractAddress: swapInfo.ToTokenContractAddress,
		ToTokenContractAddress:   swapInfo.FromTokenContractAddress,
		FromTokenDecimals:        swapInfo.DecimalsTo,
		ToTokenDecimals:          swapInfo.DecimalsFrom,
		ChainIndex:               swapInfo.ChainIndex,
		Amount:                   convertedCoinAmount, // 币的数量（输入）
		SwapMode:                 "exactIn",           // 使用 exactIn 模式（API 只支持 exactIn）
		Slippage:                 swapInfo.Slippage,
		GasLimit:                 swapInfo.GasLimit,
		WalletAddress:            swapInfo.WalletAddress,
	}

	CoinToUSDTPrice, sellTx, sellSwapResp, err := o.queryDexSwapPrice(CoinToUSDTTask)
	if err != nil || sellTx == "" {
		CoinToUSDTPrice = "0"
		sellTx = ""
		sellSwapResp = nil
		CoinToUSDTTask = nil
	}

	// 2. 再查询买入价格：USDT -> Coin，使用 exactIn 模式
	// 目标：买入得到接近 swapInfo.Amount 个币（20 个币）
	// 由于只能使用 exactIn，需要先估算需要的 USDT 数量，然后根据返回结果调整
	var convertedUSDTAmount string

	// 先从卖出查询结果中获取 USDT 数量，作为初始估算
	if sellSwapResp != nil {
		if swapResp, ok := sellSwapResp.(model.OkexDexSwapResponse); ok && len(swapResp.Data) > 0 {
			usdtAmountFromSell := swapResp.Data[0].RouterResult.ToTokenAmount
			if usdtAmountFromSell != "" {
				convertedUSDTAmount = usdtAmountFromSell
			}
		}
	}

	// 如果无法从卖出结果获取，不能把 convertedCoinAmount（币的最小单位，如 18 位）当作 USDT 数量：
	// USDT 为 6 位小数，会导致链上按「币的 wei 数」当 USDT 花费，数额巨大并触发余额不足。
	// 此处不再 fallback，留空让买入询价失败，由上层得到明确错误。
	usdtFromSell := convertedUSDTAmount != ""
	if convertedUSDTAmount == "" {
		// 不再使用 convertedUSDTAmount = convertedCoinAmount
		// 若需估算，应使用「币数量(人类)*价格」再按 USDT decimals 转换，此处无价格故不估算
	}
	// #region agent log
	writeOkdexDebug("okdex_internal.go:processSingleSwapQuery", "buy amount", "H2", map[string]interface{}{
		"coinAmountStr": coinAmountStr, "convertedCoinAmount": convertedCoinAmount, "convertedUSDTAmount": convertedUSDTAmount, "usdtFromSell": usdtFromSell,
	})
	// #endregion

	// 第一次查询买入价格（使用估算的 USDT 数量）
	USDTToCoinTask := &model.SwapTask{
		FromTokenSymbol:          swapInfo.FromTokenSymbol,
		ToTokenSymbol:            swapInfo.ToTokenSymbol,
		FromTokenContractAddress: swapInfo.FromTokenContractAddress,
		ToTokenContractAddress:   swapInfo.ToTokenContractAddress,
		FromTokenDecimals:        swapInfo.DecimalsFrom,
		ToTokenDecimals:          swapInfo.DecimalsTo,
		ChainIndex:               swapInfo.ChainIndex,
		Amount:                   convertedUSDTAmount, // USDT 数量（输入）
		SwapMode:                 "exactIn",           // 使用 exactIn 模式（API 只支持 exactIn）
		Slippage:                 swapInfo.Slippage,
		GasLimit:                 swapInfo.GasLimit,
		WalletAddress:            swapInfo.WalletAddress,
	}

	// 查询买入价格（直接使用查询结果，不做调整）
	USDTToCoinPrice, buyTx, buySwapResp, err := o.queryDexSwapPrice(USDTToCoinTask)
	if err != nil || buyTx == "" {
		USDTToCoinPrice = "0"
		buyTx = ""
		buySwapResp = nil
	}

	// 验证并修复 API 返回的 token 地址
	if sellTx != "" && sellSwapResp != nil && CoinToUSDTTask != nil {
		if swapResp, ok := sellSwapResp.(model.OkexDexSwapResponse); ok {
			if len(swapResp.Data) > 0 {
				routerResult := swapResp.Data[0].RouterResult
				fromTokenAddr := o.normalizeTokenAddress(routerResult.FromToken.TokenContractAddress)
				toTokenAddr := o.normalizeTokenAddress(routerResult.ToToken.TokenContractAddress)
				expectedFromAddr := o.normalizeTokenAddress(CoinToUSDTTask.FromTokenContractAddress)
				expectedToAddr := o.normalizeTokenAddress(CoinToUSDTTask.ToTokenContractAddress)

				if fromTokenAddr != expectedFromAddr || toTokenAddr != expectedToAddr {
					// 修复地址
					routerResult.FromToken.TokenContractAddress = "0x" + fromTokenAddr
					swapResp.Data[0].RouterResult.FromToken.TokenContractAddress = "0x" + fromTokenAddr
					routerResult.ToToken.TokenContractAddress = "0x" + toTokenAddr
					swapResp.Data[0].RouterResult.ToToken.TokenContractAddress = "0x" + toTokenAddr
				}
			}
		}
	}

	// 6. 分别保存买入和卖出的最新交易数据，并缓存 TxDetail
	o.mu.Lock()
	if buyTx != "" && buySwapResp != nil {
		o.latestBuySwapTx = buySwapResp // 买入：USDT -> Coin
		// 缓存 TxDetail（用于直接广播）
		if swapResp, ok := buySwapResp.(model.OkexDexSwapResponse); ok {
			if len(swapResp.Data) > 0 {
				o.latestBuyTxDetail = &swapResp.Data[0].Tx
			}
		}
	}
	if sellTx != "" && sellSwapResp != nil {
		o.latestSellSwapTx = sellSwapResp // 卖出：Coin -> USDT
		// 缓存 TxDetail（用于直接广播）
		if swapResp, ok := sellSwapResp.(model.OkexDexSwapResponse); ok {
			if len(swapResp.Data) > 0 {
				o.latestSellTxDetail = &swapResp.Data[0].Tx
			}
		}
	}
	o.mu.Unlock()

	// 6.5. 从 API 返回结果中提取实际的 Decimals 并更新 swapInfo
	o.updateSwapInfoDecimalsFromResponse(buySwapResp, sellSwapResp, swapInfo)

	// 7. 构建 PriceInfo 并通过回调上报
	symbol := swapInfo.ToTokenSymbol + swapInfo.FromTokenSymbol
	priceInfo := &model.ChainPriceInfo{
		CoinSymbol:     symbol,
		ChainPriceBuy:  CoinToUSDTPrice, // 卖一价：用 USDT 买入币的价格
		ChainPriceSell: USDTToCoinPrice, // 买一价：卖出币得到 USDT 的价格
		ChainBuyTx:     buyTx,
		ChainSellTx:    sellTx,
		ChainId:        swapInfo.ChainIndex,
	}

	// 通过回调上报
	if callback != nil {
		callback(priceInfo)
	}
}

// updateSwapInfoDecimalsFromResponse 从 API 返回结果中提取实际的 Decimals 并更新 swapInfo
func (o *okdex) updateSwapInfoDecimalsFromResponse(buySwapResp, sellSwapResp interface{}, swapInfo *model.SwapInfo) {
	if swapInfo == nil {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.swapInfo == nil {
		return
	}

	// 获取 USDT 和 Coin 的地址用于匹配
	usdtAddr := o.normalizeTokenAddress(swapInfo.FromTokenContractAddress)
	coinAddr := o.normalizeTokenAddress(swapInfo.ToTokenContractAddress)

	// 辅助函数：从响应中提取 Decimals
	extractDecimals := func(routerResult *model.OkexDexRouterResult) {
		if routerResult == nil {
			return
		}

		fromAddr := o.normalizeTokenAddress(routerResult.FromToken.TokenContractAddress)
		toAddr := o.normalizeTokenAddress(routerResult.ToToken.TokenContractAddress)

		// 通过地址匹配来确定哪个是 USDT，哪个是 Coin
		if fromAddr == usdtAddr && routerResult.FromToken.Decimals != "" {
			// FromToken 是 USDT
			o.swapInfo.DecimalsFrom = routerResult.FromToken.Decimals
		} else if toAddr == usdtAddr && routerResult.ToToken.Decimals != "" {
			// ToToken 是 USDT
			o.swapInfo.DecimalsFrom = routerResult.ToToken.Decimals
		}

		if fromAddr == coinAddr && routerResult.FromToken.Decimals != "" {
			// FromToken 是 Coin
			o.swapInfo.DecimalsTo = routerResult.FromToken.Decimals
		} else if toAddr == coinAddr && routerResult.ToToken.Decimals != "" {
			// ToToken 是 Coin
			o.swapInfo.DecimalsTo = routerResult.ToToken.Decimals
		}
	}

	// 优先从买入响应中获取（USDT -> Coin）
	if buySwapResp != nil {
		if swapResp, ok := buySwapResp.(model.OkexDexSwapResponse); ok && len(swapResp.Data) > 0 {
			routerResult := &swapResp.Data[0].RouterResult
			extractDecimals(routerResult)
		}
	}

	// 如果买入响应中没有获取到，尝试从卖出响应中获取（Coin -> USDT）
	if (o.swapInfo.DecimalsFrom == "" || o.swapInfo.DecimalsTo == "") && sellSwapResp != nil {
		if swapResp, ok := sellSwapResp.(model.OkexDexSwapResponse); ok && len(swapResp.Data) > 0 {
			routerResult := &swapResp.Data[0].RouterResult
			extractDecimals(routerResult)
		}
	}
}

// queryDexSwapPrice 查询 DEX swap 价格（内部方法）
// 返回：价格、交易数据、swap 响应、错误
func (o *okdex) queryDexSwapPrice(task *model.SwapTask) (string, string, interface{}, error) {
	if task == nil {
		return "", "", nil, fmt.Errorf("task cannot be nil")
	}

	// 验证代币合约地址
	if task.FromTokenContractAddress == "" {
		return "", "", nil, fmt.Errorf("from token contract address is empty")
	}
	if task.ToTokenContractAddress == "" {
		return "", "", nil, fmt.Errorf("to token contract address is empty")
	}

	// 调用 queryDexSwap 查询 swap 数据
	swapResp, err := o.queryDexSwap(
		task.FromTokenContractAddress,
		task.ToTokenContractAddress,
		task.SwapMode,
		task.ChainIndex,
		task.Amount,
		task.Slippage,
		task.WalletAddress,
		task.GasLimit,
	)

	// 检查错误
	if err != nil {
		return "", "", nil, fmt.Errorf("query swap failed: %w", err)
	}

	// 检查 API 返回的错误码
	if swapResp.Code != "" && swapResp.Code != "0" {
		return "", "", nil, fmt.Errorf("API error: code=%s, msg=%s", swapResp.Code, swapResp.Msg)
	}

	// 检查数据是否为空
	if len(swapResp.Data) == 0 {
		return "", "", nil, fmt.Errorf("get swap data failed: empty data, msg:%s", swapResp.Msg)
	}

	// 验证并修复 API 返回的 token 地址（API 可能返回数字而不是十六进制地址）
	routerResult := swapResp.Data[0].RouterResult
	fromTokenAddr := o.normalizeTokenAddress(routerResult.FromToken.TokenContractAddress)
	toTokenAddr := o.normalizeTokenAddress(routerResult.ToToken.TokenContractAddress)
	expectedFromAddr := o.normalizeTokenAddress(task.FromTokenContractAddress)
	expectedToAddr := o.normalizeTokenAddress(task.ToTokenContractAddress)

	// 验证地址是否匹配
	if fromTokenAddr != expectedFromAddr {
		return "", "", nil, fmt.Errorf("from token address mismatch: expected %s, got %s", expectedFromAddr, fromTokenAddr)
	}
	if toTokenAddr != expectedToAddr {
		return "", "", nil, fmt.Errorf("to token address mismatch: expected %s, got %s", expectedToAddr, toTokenAddr)
	}

	// 修复 API 返回的地址（如果 API 返回的是数字，需要转换为十六进制格式）
	// 确保后续使用地址时格式正确
	if routerResult.FromToken.TokenContractAddress != "0x"+fromTokenAddr {
		routerResult.FromToken.TokenContractAddress = "0x" + fromTokenAddr
		swapResp.Data[0].RouterResult.FromToken.TokenContractAddress = "0x" + fromTokenAddr
	}
	if routerResult.ToToken.TokenContractAddress != "0x"+toTokenAddr {
		routerResult.ToToken.TokenContractAddress = "0x" + toTokenAddr
		swapResp.Data[0].RouterResult.ToToken.TokenContractAddress = "0x" + toTokenAddr
	}

	// 注意：交易数据在 processSingleSwapQuery 中分别保存到 latestBuySwapTx 和 latestSellSwapTx

	// 计算价格（考虑精度）
	fromAmount, err := o.convertFromDecimals(swapResp.Data[0].RouterResult.FromTokenAmount, task.FromTokenDecimals)
	if err != nil {
		return "", "", nil, fmt.Errorf("parse from token amount failed: %w", err)
	}
	toAmount, err := o.convertFromDecimals(swapResp.Data[0].RouterResult.ToTokenAmount, task.ToTokenDecimals)
	if err != nil {
		return "", "", nil, fmt.Errorf("parse to token amount failed: %w", err)
	}

	zero := new(big.Float).SetPrec(256)
	if fromAmount.Cmp(zero) == 0 {
		return "", "", nil, fmt.Errorf("FromTokenAmount cannot be zero")
	}

	priceFloat := new(big.Float).SetPrec(256)
	if strings.EqualFold(task.FromTokenSymbol, "USDT") {
		if toAmount.Cmp(zero) == 0 {
			return "", "", nil, fmt.Errorf("ToTokenAmount cannot be zero when FromToken is USDT")
		}
		priceFloat.Quo(fromAmount, toAmount)
	} else {
		priceFloat.Quo(toAmount, fromAmount)
	}

	priceStr := strings.TrimRight(strings.TrimRight(priceFloat.Text('f', 18), "0"), ".")
	if priceStr == "" {
		priceStr = "0"
	}

	return priceStr, swapResp.Data[0].Tx.Data, swapResp, nil
}
