package trigger

import (
	"auto-arbitrage/internal/model"
	"errors"
)

type DiffCalculator struct {
}

// CalculateDiff 计算两个方向的价差
func CalculateDiff(priceAB, priceBA *model.PriceData) (*model.DiffResult, error) {
	// 注意:
	// priceAB中 为A的ask B的bid
	// priceBA中 为B的ask A的bid
	if (priceAB.BidPrice + priceAB.AskPrice) == 0 ||
		(priceBA.BidPrice + priceBA.AskPrice) == 0 {
		return nil, errors.New("价格数据无效：分母为0")
	}

	res := &model.DiffResult{
		// AB的价差为: (B Bid - A Ask) * 2 / (B Bid + A Ask) * 100
		DiffAB: (priceAB.BidPrice - priceAB.AskPrice) * 2 / (priceAB.BidPrice + priceAB.AskPrice) * 100,
		// BA的价差为: (A Bid - B Ask) * 2 / (A Bid + B Ask) * 100
		DiffBA: (priceBA.BidPrice - priceBA.AskPrice) * 2 / (priceBA.BidPrice + priceBA.AskPrice) * 100,
		AAsk: priceAB.AskPrice,
		ABid: priceBA.BidPrice,
		BAsk: priceBA.AskPrice,
		BBid: priceAB.BidPrice,
	}

	return res, nil
}
