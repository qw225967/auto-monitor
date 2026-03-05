package position

import (
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"math"
	"strconv"
	"strings"
	"time"
)

// getAssetPriceUSDT 获取资产对 USDT 的中间价，失败返回 0
func getAssetPriceUSDT(ex exchange.Exchange, asset string) float64 {
	symbol := asset + "USDT"
	bids, asks, err := ex.GetSpotOrderBook(symbol)
	if err != nil || len(bids) == 0 || len(asks) == 0 {
		bids, asks, err = ex.GetFuturesOrderBook(symbol)
	}
	if err != nil || len(bids) == 0 || len(asks) == 0 {
		return 0
	}
	bid, _ := strconv.ParseFloat(bids[0][0], 64)
	ask, _ := strconv.ParseFloat(asks[0][0], 64)
	if bid <= 0 && ask <= 0 {
		return 0
	}
	if bid <= 0 {
		return ask
	}
	if ask <= 0 {
		return bid
	}
	return (bid + ask) / 2
}

// nativeWrappedPairs 同一链上原生币与包装币的对应关系，用于去重统计（避免 BNB+WBNB 或 ETH+WETH 双重计入）
var nativeWrappedPairs = map[string][]string{
	"56":  {"BNB", "WBNB"},   // BSC
	"1":   {"ETH", "WETH"},   // Ethereum
	"137": {"MATIC", "WMATIC"}, // Polygon
	"43114": {"AVAX", "WAVAX"}, // Avalanche
	"250": {"FTM", "WFTM"},   // Fantom
}

// UpdateStatistics 更新所有统计信息
func UpdateStatistics(w *model.WalletDetailInfo) {
	if w == nil {
		return
	}
	CalculateTotalPositionValue(w)
	CalculateTotalBalanceValue(w)
	CalculateTotalOnchainValue(w)
	CalculateTotalUnrealizedPnl(w)
	CalculateTotalAsset(w)

	w.PositionCount = 0
	if w.ExchangeWallets != nil {
		for _, exchangeWallet := range w.ExchangeWallets {
			if exchangeWallet != nil {
				w.PositionCount += exchangeWallet.PositionCount
			}
		}
	}

	w.OnchainChainCount = len(w.OnchainBalances)
	w.UpdateTime = time.Now()
}

// calculateExchangeWalletStatistics 计算单个交易所钱包的统计信息
// ex 用于获取非 USDT 资产的价格（转为 USDT 价值），为 nil 时仅计入 USDT
func calculateExchangeWalletStatistics(exchangeWallet *model.ExchangeWalletInfo, ex exchange.Exchange) {
	if exchangeWallet == nil {
		return
	}

	// 计算余额价值：USDT 直接计入，其他币种按市价折算为 USDT
	exchangeWallet.TotalBalanceValue = 0
	if exchangeWallet.AccountBalances != nil {
		for asset, balance := range exchangeWallet.AccountBalances {
			if balance == nil || balance.Total <= 0 {
				continue
			}
			if asset == "USDT" || asset == "USDC" || asset == "BUSD" || asset == "DAI" || asset == "TUSD" {
				exchangeWallet.TotalBalanceValue += balance.Total
				continue
			}
			if ex != nil {
				if price := getAssetPriceUSDT(ex, asset); price > 0 {
					exchangeWallet.TotalBalanceValue += balance.Total * price
				}
			}
		}
		// 兼容：无 exchange 时仅计入 USDT
		if ex == nil && exchangeWallet.TotalBalanceValue == 0 {
			if balance, exists := exchangeWallet.AccountBalances["USDT"]; exists && balance != nil {
				exchangeWallet.TotalBalanceValue = balance.Total
			}
		}
	}

	// 计算持仓价值和未实现盈亏
	exchangeWallet.TotalPositionValue = 0
	exchangeWallet.TotalUnrealizedPnl = 0
	if exchangeWallet.Positions != nil {
		for _, pos := range exchangeWallet.Positions {
			if pos != nil {
				exchangeWallet.TotalPositionValue += pos.Size * pos.MarkPrice
				exchangeWallet.TotalUnrealizedPnl += pos.UnrealizedPnl
			}
		}
	}
}

// CalculateTotalBalanceValue 计算总余额价值
func CalculateTotalBalanceValue(w *model.WalletDetailInfo) {
	if w == nil {
		w.TotalBalanceValue = 0
		return
	}
	w.TotalBalanceValue = 0
	if w.ExchangeWallets != nil {
		for _, exchangeWallet := range w.ExchangeWallets {
			if exchangeWallet != nil {
				w.TotalBalanceValue += exchangeWallet.TotalBalanceValue
			}
		}
	}
}

// CalculateTotalPositionValue 计算总持仓价值
func CalculateTotalPositionValue(w *model.WalletDetailInfo) {
	if w == nil {
		return
	}
	w.TotalPositionValue = 0
	if w.ExchangeWallets != nil {
		for _, exchangeWallet := range w.ExchangeWallets {
			if exchangeWallet != nil {
				w.TotalPositionValue += exchangeWallet.TotalPositionValue
			}
		}
	}
}

// CalculateTotalUnrealizedPnl 计算总未实现盈亏
func CalculateTotalUnrealizedPnl(w *model.WalletDetailInfo) {
	if w == nil {
		return
	}
	w.TotalUnrealizedPnl = 0
	if w.ExchangeWallets != nil {
		for _, exchangeWallet := range w.ExchangeWallets {
			if exchangeWallet != nil {
				w.TotalUnrealizedPnl += exchangeWallet.TotalUnrealizedPnl
			}
		}
	}
}

// CalculateTotalOnchainValue 计算总链上余额价值
// 为避免 BNB+WBNB、ETH+WETH 等原生/包装币双重计入，当同一链上两者余额近似相等时只取较大值
func CalculateTotalOnchainValue(w *model.WalletDetailInfo) {
	if w == nil {
		w.TotalOnchainValue = 0
		return
	}
	w.TotalOnchainValue = CalculateTotalOnchainValueFromBalances(w.OnchainBalances)
}

// CalculateTotalOnchainValueFromBalances 从 OnchainBalances 计算总链上价值（供 statistics 与 wallet_info 共用）
func CalculateTotalOnchainValueFromBalances(onchainBalances map[string]map[string]model.OkexTokenAsset) float64 {
	if onchainBalances == nil {
		return 0
	}
	var total float64
	for chainIndex, symbolMap := range onchainBalances {
		if symbolMap == nil {
			continue
		}
		deduped := deduplicateNativeWrapped(chainIndex, symbolMap)
		chainTotal := 0.0
		for _, asset := range deduped {
			balance, err := strconv.ParseFloat(asset.Balance, 64)
			if err != nil || balance <= 0 {
				continue
			}
			var price float64
			if asset.TokenPrice != "" {
				price, _ = strconv.ParseFloat(asset.TokenPrice, 64)
			}
			var val float64
			if asset.Symbol == "USDT" {
				val = balance
			} else if price > 0 {
				val = balance * price
			}
			total += val
			chainTotal += val
		}
	}
	return total
}

// deduplicateNativeWrapped 对同一链上的原生币与包装币去重，避免双重计入
// 当 BNB 与 WBNB（或 ETH 与 WETH）余额近似相等时，只保留较大者
func deduplicateNativeWrapped(chainIndex string, symbolMap map[string]model.OkexTokenAsset) map[string]model.OkexTokenAsset {
	pairs, ok := nativeWrappedPairs[chainIndex]
	if !ok || len(pairs) < 2 {
		return symbolMap
	}
	nativeSym, wrappedSym := pairs[0], pairs[1]
	nativeAsset, hasNative := symbolMap[nativeSym]
	wrappedAsset, hasWrapped := symbolMap[wrappedSym]
	if !hasNative || !hasWrapped {
		return symbolMap
	}
	nativeBal, _ := strconv.ParseFloat(nativeAsset.Balance, 64)
	wrappedBal, _ := strconv.ParseFloat(wrappedAsset.Balance, 64)
	if nativeBal <= 0 && wrappedBal <= 0 {
		return symbolMap
	}
	// 若两者余额近似相等（误差 < 1%），视为同一资产重复统计，只取较大值
	maxBal := math.Max(nativeBal, wrappedBal)
	minBal := math.Min(nativeBal, wrappedBal)
	if maxBal > 0 && (maxBal-minBal)/maxBal < 0.01 {
		result := make(map[string]model.OkexTokenAsset)
		for k, v := range symbolMap {
			lower := strings.ToLower(k)
			if lower == strings.ToLower(nativeSym) || lower == strings.ToLower(wrappedSym) {
				continue
			}
			result[k] = v
		}
		// 保留较大余额的那一项
		if nativeBal >= wrappedBal {
			result[nativeSym] = nativeAsset
		} else {
			result[wrappedSym] = wrappedAsset
		}
		return result
	}
	return symbolMap
}

// CalculateTotalAsset 计算总资产
func CalculateTotalAsset(w *model.WalletDetailInfo) {
	if w == nil {
		return
	}
	// 修正：总资产 = 余额价值 + 未实现盈亏 + 链上资产
	// 之前的计算包含了 PositionValue（名义价值），导致开仓时资产虚增，无法反映净值
	w.TotalAsset = w.TotalBalanceValue + w.TotalUnrealizedPnl + w.TotalOnchainValue
}
