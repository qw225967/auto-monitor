package analytics

import (
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

// 成本相关常量（写死的）
const (
	// OnChainGasFee 链上 gas 费（USDT）
	OnChainGasFee = 0.5
	// ExchangeFeeRate 交易所手续费率（百分比，如 0.04 表示 0.04%）
	ExchangeFeeRate = 0.075
)

// CostData 成本数据结构
type CostData struct {
	// 滑点成本
	SlippageCost float64 // 滑点成本（USDT）

	// 固定成本
	OnChainGasFee   float64 // 链上 gas 费（USDT）
	ExchangeFee     float64 // 交易所手续费（USDT）
	TotalCost       float64 // 总成本（USDT）
	TotalCostPercent float64 // 总成本百分比（相对于交易金额）
}

// CalculateCost 计算交易成本
// direction: "AB" 表示 +A-B（链上买入，交易所卖出），"BA" 表示 -A+B（链上卖出，交易所买入）
// size: 交易数量（币数量）
// exchangePrice: 交易所当前价格（用于计算滑点成本和手续费）
// onchainPrice: 链上当前价格（用于计算滑点成本）
func (a *Analytics) CalculateCost(direction string, size float64, exchangePrice, onchainPrice float64) *CostData {
	log := logger.GetLoggerInstance().Named("cost").Sugar()

	if size <= 0 {
		log.Warnf("交易数量无效: %.6f", size)
		return &CostData{}
	}

	if exchangePrice <= 0 || onchainPrice <= 0 {
		log.Warnf("价格无效 - 交易所: %.6f, 链上: %.6f", exchangePrice, onchainPrice)
		return &CostData{}
	}

	// 获取滑点数据
	slippageData := a.GetSlippageData()
	if slippageData == nil {
		log.Warn("滑点数据为空，使用默认值 0")
		slippageData = &SlippageData{}
	}

	cost := &CostData{}

	// 计算滑点成本
	// 使用 A 和 B 的滑点数据（新的统一字段）
	var aSlippage, bSlippage float64
	if direction == "AB" {
		// +A-B: 链上买入 A，交易所卖出 B
		// A 的买入滑点（链上买入）
		aSlippage = slippageData.ABuy
		// B 的卖出滑点（交易所卖出）
		bSlippage = slippageData.BSell
		
		// 向后兼容：如果新的字段为 0，尝试使用旧的字段
		if aSlippage == 0 {
			aSlippage = slippageData.OnChainBuy
		}
		if bSlippage == 0 {
			bSlippage = slippageData.ExchangeSell
		}
	} else if direction == "BA" {
		// -A+B: 链上卖出 A，交易所买入 B
		// A 的卖出滑点（链上卖出）
		aSlippage = slippageData.ASell
		// B 的买入滑点（交易所买入）
		bSlippage = slippageData.BBuy
		
		// 向后兼容：如果新的字段为 0，尝试使用旧的字段
		if aSlippage == 0 {
			aSlippage = slippageData.OnChainSell
		}
		if bSlippage == 0 {
			bSlippage = slippageData.ExchangeBuy
		}
	} else {
		log.Warnf("未知的交易方向: %s", direction)
		return &CostData{}
	}
	
	// 兼容旧字段名（用于日志和变量名）
	exchangeSlippage := bSlippage
	onchainSlippage := aSlippage

	// 滑点成本 = (交易所滑点百分比 * 交易所价格 * 数量) + (链上滑点百分比 * 链上价格 * 数量)（保留用于组件展示）
	exchangeSlippageCost := (exchangeSlippage / 100.0) * exchangePrice * size
	onchainSlippageCost := (onchainSlippage / 100.0) * onchainPrice * size
	cost.SlippageCost = exchangeSlippageCost + onchainSlippageCost

	// 固定成本：a 侧（链上）= Gas；b 侧（交易所）= 手续费
	cost.OnChainGasFee = OnChainGasFee
	cost.ExchangeFee = (ExchangeFeeRate / 100.0) * exchangePrice * size

	// 总成本（USDT，保留用于统计与组件展示）
	cost.TotalCost = cost.SlippageCost + cost.OnChainGasFee + cost.ExchangeFee

	// cost 百分比公式：(（a滑点+b滑点）/2) + ((a手续费+b手续费)/买一价) 的百分比形式
	// 即：滑点部分(%) = (a滑点+b滑点)/2；手续费部分(%) = (a手续费+b手续费)/(买一价*size)*100
	// 买一价：AB 为链上买一(onchain Ask)，BA 为交易所买一(exchange Ask)
	var buyOnePrice float64
	if direction == "AB" {
		buyOnePrice = onchainPrice // 链上买入，买一价 = onchain Ask
	} else {
		buyOnePrice = exchangePrice // 交易所买入，买一价 = exchange Ask
	}
	aFee := OnChainGasFee
	bFee := cost.ExchangeFee
	slippagePart := (aSlippage + bSlippage) / 2.0
	feePart := 0.0
	if buyOnePrice > 0 && size > 0 {
		feePart = (aFee + bFee) / (buyOnePrice * size) * 100.0
	}
	cost.TotalCostPercent = slippagePart + feePart

	log.Debugf("成本计算 - 方向: %s, 数量: %.6f, 交易所价格: %.6f, 链上价格: %.6f",
		direction, size, exchangePrice, onchainPrice)
	log.Debugf("成本明细 - 滑点部分: (a=%.4f%%+b=%.4f%%)/2=%.4f%%, 手续费部分: (a=%.4f+b=%.4f USDT)/买一价=%.4f%%, cost%%=%.4f%%",
		aSlippage, bSlippage, slippagePart, aFee, bFee, feePart, cost.TotalCostPercent)

	return cost
}

// CalculateCostForDirection 根据方向配置计算成本（便捷方法）
// direction: "AB" 或 "BA"
// size: 交易数量
// exchangePriceData: 交易所价格数据（包含 BidPrice 和 AskPrice）
// onchainPriceData: 链上价格数据（包含 BidPrice 和 AskPrice）
func (a *Analytics) CalculateCostForDirection(direction string, size float64, exchangePriceData, onchainPriceData *model.PriceData) *CostData {
	if exchangePriceData == nil || onchainPriceData == nil {
		logger.GetLoggerInstance().Named("cost").Sugar().Warn("价格数据为空")
		return &CostData{}
	}

	var exchangePrice, onchainPrice float64

	if direction == "AB" {
		// +A-B: 链上买入（用链上卖一价），交易所卖出（用交易所买一价）
		exchangePrice = exchangePriceData.BidPrice
		onchainPrice = onchainPriceData.AskPrice
	} else if direction == "BA" {
		// -A+B: 链上卖出（用链上买一价），交易所买入（用交易所卖一价）
		exchangePrice = exchangePriceData.AskPrice
		onchainPrice = onchainPriceData.BidPrice
	} else {
		logger.GetLoggerInstance().Named("cost").Sugar().Warnf("未知的交易方向: %s", direction)
		return &CostData{}
	}

	return a.CalculateCost(direction, size, exchangePrice, onchainPrice)
}

// GetCost 获取成本数据（接口方法，供外部调用）
func (a *Analytics) GetCost(direction string, size float64, exchangePrice, onchainPrice float64) *CostData {
	return a.CalculateCost(direction, size, exchangePrice, onchainPrice)
}
