package position

import (
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"fmt"
	"time"
)

// getSingleExchangeWalletInfo 获取单个交易所的钱包详细信息
func getSingleExchangeWalletInfo(ex exchange.Exchange) (*model.WalletDetailInfo, error) {
	if ex == nil {
		return nil, fmt.Errorf("exchange is nil")
	}

	// 统一使用通用接口方法（所有交易所都实现了 GetSpotBalances 和 GetFuturesBalances）
	walletInfo, err := getWalletDetailInfoGeneric(ex)

	if err != nil {
		fmt.Println("get wallet detail info failed: ", err)
		return nil, err
	}

	return walletInfo, nil
}

// getWalletDetailInfoGeneric 通用钱包信息获取实现（所有交易所统一使用）
func getWalletDetailInfoGeneric(ex exchange.Exchange) (*model.WalletDetailInfo, error) {
	_, err := ex.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}

	positions, err := ex.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("failed to get positions: %w", err)
	}

	positionsMap := make(map[string]*model.Position)
	for _, pos := range positions {
		if pos != nil && pos.Symbol != "" {
			positionsMap[pos.Symbol] = pos
		}
	}

	// 分别获取现货和合约余额
	spotBalances, err := ex.GetSpotBalances()
	if err != nil {
		// 如果获取失败，使用统一余额作为后备
		spotBalances, _ = ex.GetAllBalances()
	}

	futuresBalances, err := ex.GetFuturesBalances()
	if err != nil {
		// 如果获取失败，使用统一余额作为后备
		futuresBalances, _ = ex.GetAllBalances()
	}

	// 为了向后兼容，AccountBalances 聚合现货和合约余额
	accountBalances := make(map[string]*model.Balance)
	// 合并现货和合约余额（相同币种取较大值）
	for asset, balance := range spotBalances {
		if balance != nil {
			accountBalances[asset] = balance
		}
	}
	for asset, balance := range futuresBalances {
		if balance != nil {
			if existing, exists := accountBalances[asset]; exists && existing != nil {
				// 如果已存在，取总余额的较大值
				if balance.Total > existing.Total {
					accountBalances[asset] = balance
				}
			} else {
				accountBalances[asset] = balance
			}
		}
	}

	walletInfo := &model.WalletDetailInfo{
		ExchangeWallets: make(map[string]*model.ExchangeWalletInfo),
		OnchainBalances: make(map[string]map[string]model.OkexTokenAsset),
	}

	exchangeType := ex.GetType()
	exchangeWalletInfo := &model.ExchangeWalletInfo{
		ExchangeType:    exchangeType,
		SpotBalances:    spotBalances,      // 现货余额
		FuturesBalances: futuresBalances,   // 合约余额
		AccountBalances: accountBalances,   // 向后兼容字段（聚合后的余额）
		Positions:       positionsMap,
		PositionCount:   len(positionsMap),
	}
	walletInfo.ExchangeWallets[exchangeType] = exchangeWalletInfo
	walletInfo.ExchangeCount = 1

	calculateExchangeWalletStatistics(exchangeWalletInfo, ex)
	UpdateStatistics(walletInfo)

	return walletInfo, nil
}

// aggregateWalletInfo 聚合钱包信息并更新统计
func aggregateWalletInfo(walletInfo *model.WalletDetailInfo) {
	if walletInfo == nil {
		return
	}

	walletInfo.TotalBalanceValue = 0
	walletInfo.TotalPositionValue = 0
	walletInfo.TotalUnrealizedPnl = 0
	walletInfo.TotalOnchainValue = 0
	walletInfo.PositionCount = 0

	for _, exchangeWallet := range walletInfo.ExchangeWallets {
		if exchangeWallet != nil {
			walletInfo.TotalBalanceValue += exchangeWallet.TotalBalanceValue
			walletInfo.TotalPositionValue += exchangeWallet.TotalPositionValue
			walletInfo.TotalUnrealizedPnl += exchangeWallet.TotalUnrealizedPnl
			walletInfo.PositionCount += exchangeWallet.PositionCount
		}
	}

	// 计算链上余额总价值（使用统一逻辑，含 BNB/WBNB 等原生/包装币去重）
	walletInfo.TotalOnchainValue = CalculateTotalOnchainValueFromBalances(walletInfo.OnchainBalances)

	// 总资产 = 余额 + 未实现盈亏 + 链上资产。不包含持仓名义价值（PositionValue），因合约有杠杆，名义价值≠实际占用保证金，计入会导致虚增
	walletInfo.TotalAsset = walletInfo.TotalBalanceValue + walletInfo.TotalUnrealizedPnl + walletInfo.TotalOnchainValue
	walletInfo.ExchangeCount = len(walletInfo.ExchangeWallets)
	walletInfo.OnchainChainCount = len(walletInfo.OnchainBalances)
	walletInfo.UpdateTime = time.Now()
}
