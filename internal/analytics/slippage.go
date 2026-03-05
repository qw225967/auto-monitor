package analytics

import (
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/trader"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
	"math/big"
	"strconv"
)

// CalculateSlippage 计算交易所滑点百分比并存储结果
func (a *Analytics) CalculateSlippage(t trader.Trader, symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	slippage, maxSize := CalculateTraderSlippage(t, symbol, amount, isFutures, side, slippageLimit)

	// 存储计算结果并平滑 maxSize
	// 若用户已通过 SetMaxSize 配置该方向的 size（configuredMaxSizeAB/BA > 0），则不覆盖，保证 swapInfo.Amount 与 Web 设置一致
	a.slippageMu.Lock()
	if side == model.OrderSideBuy {
		a.slippageData.ExchangeBuy = slippage
		if a.configuredMaxSizeBA <= 0 {
			if a.slippageData.ExchangeBuyMaxSize == 0 {
				a.slippageData.ExchangeBuyMaxSize = maxSize
			} else {
				a.slippageData.ExchangeBuyMaxSize = a.maxSizeSmoothingAlpha*maxSize + (1-a.maxSizeSmoothingAlpha)*a.slippageData.ExchangeBuyMaxSize
			}
		}
	} else {
		a.slippageData.ExchangeSell = slippage
		if a.configuredMaxSizeAB <= 0 {
			if a.slippageData.ExchangeSellMaxSize == 0 {
				a.slippageData.ExchangeSellMaxSize = maxSize
			} else {
				a.slippageData.ExchangeSellMaxSize = a.maxSizeSmoothingAlpha*maxSize + (1-a.maxSizeSmoothingAlpha)*a.slippageData.ExchangeSellMaxSize
			}
		}
	}
	a.slippageMu.Unlock()

	return slippage, maxSize
}

// CalculateTraderSlippage 计算 Trader 滑点百分比（包级辅助函数，供 trader.Trader 实现使用）
// 统一处理交易所和链上的滑点计算：如果是链上 trader，使用链上滑点计算；否则使用交易所滑点计算
func CalculateTraderSlippage(t trader.Trader, symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	log := logger.GetLoggerInstance().Named("slippage").Sugar()

	if t == nil {
		log.Warn("trader is nil, cannot calculate slippage")
		return 0, 0
	}

	// 判断是链上 trader 还是交易所 trader
	if onchainTrader, ok := t.(trader.OnchainTrader); ok {
		// 链上 trader：使用链上滑点计算
		slippage := calculateOnchainSlippage(onchainTrader, side)
		return slippage, 0 // 链上滑点没有 maxSize 概念，返回 0
	}

	// 交易所 trader：使用交易所滑点计算
	return t.CalculateSlippage(symbol, amount, isFutures, side, slippageLimit)
}

// CalculateExchangeSlippage 计算交易所滑点百分比（向后兼容，供 exchange.Exchange 实现使用）
func CalculateExchangeSlippage(exch exchange.Exchange, symbol string, amount float64, isFutures bool, side model.OrderSide, slippageLimit float64) (float64, float64) {
	log := logger.GetLoggerInstance().Named("slippage").Sugar()

	if exch == nil {
		log.Warn("exchange is nil, cannot calculate slippage")
		return 0, 0
	}

	var slippage, maxSize float64
	if isFutures {
		slippage, maxSize = calculateFuturesSlippage(exch, symbol, amount, side, slippageLimit)
	} else {
		slippage, maxSize = calculateSpotSlippage(exch, symbol, amount, side, slippageLimit)
	}

	if slippage == 0 {
		return 0, 0
	}

	marketType := "现货"
	if isFutures {
		marketType = "合约"
	}
	sideStr := "买入"
	if side == model.OrderSideSell {
		sideStr = "卖出"
	}
	log.Debugf("%s%s滑点 - symbol: %s, amount: %.6f, slippage: %.4f%%, maxSize: %.6f",
		marketType, sideStr, symbol, amount, slippage, maxSize)

	return slippage, maxSize
}

// calculateSpotSlippage 计算现货滑点百分比
func calculateSpotSlippage(exch exchange.Exchange, symbol string, amount float64, side model.OrderSide, slippageLimit float64) (float64, float64) {
	log := logger.GetLoggerInstance().Named("slippage").Sugar()

	bids, asks, err := exch.GetSpotOrderBook(symbol)
	if err != nil {
		log.Errorf("get spot order book failed: %v", err)
		return 0, 0
	}

	if side == model.OrderSideBuy {
		if len(asks) == 0 {
			return 0, 0
		}
		firstPrice, _ := strconv.ParseFloat(asks[0][0], 64)
		avgPrice := calculateAvgPrice(asks, amount, true)
		if avgPrice == 0 || firstPrice == 0 {
			return 0, 0
		}
		slippage := (avgPrice - firstPrice) / firstPrice * 100
		maxSize := calculateMaxSizeForSlippageLimitAsks(asks, firstPrice, slippageLimit)
		return slippage, maxSize
	} else {
		if len(bids) == 0 {
			return 0, 0
		}
		firstPrice, _ := strconv.ParseFloat(bids[0][0], 64)
		avgPrice := calculateAvgPrice(bids, amount, false)
		if avgPrice == 0 || firstPrice == 0 {
			return 0, 0
		}
		slippage := (firstPrice - avgPrice) / firstPrice * 100
		maxSize := calculateMaxSizeForSlippageLimitBids(bids, firstPrice, slippageLimit)
		return slippage, maxSize
	}
}

// calculateAvgPrice 计算平均成交价
func calculateAvgPrice(book [][]string, amount float64, isBuy bool) float64 {
	total := new(big.Float).SetFloat64(0)
	remainingAmount := new(big.Float).SetFloat64(amount)

	for _, order := range book {
		if remainingAmount.Cmp(big.NewFloat(0)) <= 0 {
			break
		}
		price, _ := new(big.Float).SetString(order[0])
		qty, _ := new(big.Float).SetString(order[1])
		if qty.Cmp(remainingAmount) > 0 {
			qty = new(big.Float).Set(remainingAmount)
		}
		cost := new(big.Float).Mul(price, qty)
		total.Add(total, cost)
		remainingAmount.Sub(remainingAmount, qty)
	}

	if remainingAmount.Cmp(big.NewFloat(0)) > 0 {
		return 0
	}

	totalFloat, _ := total.Float64()
	return totalFloat / amount
}

// calculateFuturesSlippage 计算合约滑点百分比
func calculateFuturesSlippage(exch exchange.Exchange, symbol string, amount float64, side model.OrderSide, slippageLimit float64) (float64, float64) {
	log := logger.GetLoggerInstance().Named("slippage").Sugar()

	bids, asks, err := exch.GetFuturesOrderBook(symbol)
	if err != nil {
		log.Errorf("get futures order book failed: %v", err)
		return 0, 0
	}

	if side == model.OrderSideBuy {
		if len(asks) == 0 {
			return 0, 0
		}
		firstPrice, _ := strconv.ParseFloat(asks[0][0], 64)
		avgPrice := calculateAvgPrice(asks, amount, true)
		if avgPrice == 0 || firstPrice == 0 {
			return 0, 0
		}
		slippage := (avgPrice - firstPrice) / firstPrice * 100
		maxSize := calculateMaxSizeForSlippageLimitFuturesAsks(asks, firstPrice, slippageLimit)
		return slippage, maxSize
	} else {
		if len(bids) == 0 {
			return 0, 0
		}
		firstPrice, _ := strconv.ParseFloat(bids[0][0], 64)
		avgPrice := calculateAvgPrice(bids, amount, false)
		if avgPrice == 0 || firstPrice == 0 {
			return 0, 0
		}
		slippage := (firstPrice - avgPrice) / firstPrice * 100
		maxSize := calculateMaxSizeForSlippageLimitFuturesBids(bids, firstPrice, slippageLimit)
		return slippage, maxSize
	}
}

// calculateMaxSizeForSlippageLimitAsks 计算买入符合滑点限制的最大size
func calculateMaxSizeForSlippageLimitAsks(asks [][]string, firstPrice float64, slippageLimit float64) float64 {
	if len(asks) == 0 || firstPrice == 0 {
		return 0
	}

	maxAvgPrice := firstPrice * (1 + slippageLimit/100)

	totalCost := new(big.Float).SetFloat64(0)
	totalAmount := new(big.Float).SetFloat64(0)

	for _, ask := range asks {
		if len(ask) < 2 {
			continue
		}
		price, _ := new(big.Float).SetString(ask[0])
		qty, _ := new(big.Float).SetString(ask[1])

		cost := new(big.Float).Mul(price, qty)
		totalCost.Add(totalCost, cost)
		totalAmount.Add(totalAmount, qty)

		if totalAmount.Cmp(big.NewFloat(0)) > 0 {
			avgPrice := new(big.Float).Quo(totalCost, totalAmount)
			avgPriceFloat, _ := avgPrice.Float64()

			if avgPriceFloat > maxAvgPrice {
				totalCost.Sub(totalCost, cost)
				totalAmount.Sub(totalAmount, qty)
				break
			}
		}
	}

	maxSize, _ := totalAmount.Float64()
	return maxSize
}

// calculateMaxSizeForSlippageLimitBids 计算卖出符合滑点限制的最大size
func calculateMaxSizeForSlippageLimitBids(bids [][]string, firstPrice float64, slippageLimit float64) float64 {
	if len(bids) == 0 || firstPrice == 0 {
		return 0
	}

	minAvgPrice := firstPrice * (1 - slippageLimit/100)

	totalRevenue := new(big.Float).SetFloat64(0)
	totalAmount := new(big.Float).SetFloat64(0)

	for _, bid := range bids {
		if len(bid) < 2 {
			continue
		}
		price, _ := new(big.Float).SetString(bid[0])
		qty, _ := new(big.Float).SetString(bid[1])

		revenue := new(big.Float).Mul(price, qty)
		totalRevenue.Add(totalRevenue, revenue)
		totalAmount.Add(totalAmount, qty)

		if totalAmount.Cmp(big.NewFloat(0)) > 0 {
			avgPrice := new(big.Float).Quo(totalRevenue, totalAmount)
			avgPriceFloat, _ := avgPrice.Float64()

			if avgPriceFloat < minAvgPrice {
				totalRevenue.Sub(totalRevenue, revenue)
				totalAmount.Sub(totalAmount, qty)
				break
			}
		}
	}

	maxSize, _ := totalAmount.Float64()
	return maxSize
}

// calculateMaxSizeForSlippageLimitFuturesAsks 计算合约买入符合滑点限制的最大size
func calculateMaxSizeForSlippageLimitFuturesAsks(asks [][]string, firstPrice float64, slippageLimit float64) float64 {
	if len(asks) == 0 || firstPrice == 0 {
		return 0
	}

	maxAvgPrice := firstPrice * (1 + slippageLimit/100)
	totalCost := new(big.Float).SetFloat64(0)
	totalAmount := new(big.Float).SetFloat64(0)

	for _, ask := range asks {
		price, _ := new(big.Float).SetString(ask[0])
		qty, _ := new(big.Float).SetString(ask[1])

		cost := new(big.Float).Mul(price, qty)
		totalCost.Add(totalCost, cost)
		totalAmount.Add(totalAmount, qty)

		if totalAmount.Cmp(big.NewFloat(0)) > 0 {
			avgPrice := new(big.Float).Quo(totalCost, totalAmount)
			avgPriceFloat, _ := avgPrice.Float64()

			if avgPriceFloat > maxAvgPrice {
				totalCost.Sub(totalCost, cost)
				totalAmount.Sub(totalAmount, qty)
				break
			}
		}
	}

	maxSize, _ := totalAmount.Float64()
	return maxSize
}

// calculateMaxSizeForSlippageLimitFuturesBids 计算合约卖出符合滑点限制的最大size
func calculateMaxSizeForSlippageLimitFuturesBids(bids [][]string, firstPrice float64, slippageLimit float64) float64 {
	if len(bids) == 0 || firstPrice == 0 {
		return 0
	}

	minAvgPrice := firstPrice * (1 - slippageLimit/100)
	totalRevenue := new(big.Float).SetFloat64(0)
	totalAmount := new(big.Float).SetFloat64(0)

	for _, bid := range bids {
		price, _ := new(big.Float).SetString(bid[0])
		qty, _ := new(big.Float).SetString(bid[1])

		revenue := new(big.Float).Mul(price, qty)
		totalRevenue.Add(totalRevenue, revenue)
		totalAmount.Add(totalAmount, qty)

		if totalAmount.Cmp(big.NewFloat(0)) > 0 {
			avgPrice := new(big.Float).Quo(totalRevenue, totalAmount)
			avgPriceFloat, _ := avgPrice.Float64()

			if avgPriceFloat < minAvgPrice {
				totalRevenue.Sub(totalRevenue, revenue)
				totalAmount.Sub(totalAmount, qty)
				break
			}
		}
	}

	maxSize, _ := totalAmount.Float64()
	return maxSize
}

// CalculateOnchainSlippage 计算链上滑点百分比并存储结果
func (a *Analytics) CalculateOnchainSlippage(onchainTrader trader.OnchainTrader, side model.OrderSide) float64 {
	slippage := calculateOnchainSlippage(onchainTrader, side)

	// 存储计算结果
	a.slippageMu.Lock()
	if side == model.OrderSideBuy {
		a.slippageData.OnChainBuy = slippage
	} else {
		a.slippageData.OnChainSell = slippage
	}
	a.slippageMu.Unlock()

	return slippage
}

// calculateOnchainSlippage 计算链上滑点百分比（包级辅助函数）
func calculateOnchainSlippage(onchainTrader trader.OnchainTrader, side model.OrderSide) float64 {
	log := logger.GetLoggerInstance().Named("slippage").Sugar()

	if onchainTrader == nil {
		log.Warn("onchain trader is nil, cannot calculate slippage")
		return 0
	}

	latestSwapTx := onchainTrader.GetLatestSwapTx()
	if latestSwapTx == nil {
		return 0
	}

	swapResp, ok := latestSwapTx.(*model.OkexDexSwapResponse)
	if !ok {
		if resp, ok2 := latestSwapTx.(model.OkexDexSwapResponse); ok2 {
			swapResp = &resp
		} else {
			log.Warn("latestSwapTx is not OkexDexSwapResponse type")
			return 0
		}
	}

	if len(swapResp.Data) == 0 {
		return 0
	}

	slippageStr := swapResp.Data[0].Tx.SlippagePercent
	if slippageStr == "" {
		return 0
	}

	slippage, err := strconv.ParseFloat(slippageStr, 64)
	if err != nil {
		log.Warnf("parse slippagePercent from swap response failed: %v", err)
		return 0
	}

	if slippage > 0 {
		sideStr := "买入"
		if side == model.OrderSideSell {
			sideStr = "卖出"
		}
		log.Debugf("链上%s滑点 - slippage: %.4f%%", sideStr, slippage)
	}

	return slippage
}
