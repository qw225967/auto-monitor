package aster

import (
	"fmt"

	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
)

var _ exchange.DepositWithdrawProvider = (*aster)(nil)
var _ exchange.WithdrawNetworkLister = (*aster)(nil)

// Deposit 获取充币地址
// 注意：Aster 是 DEX，可能不支持传统充提功能
func (a *aster) Deposit(asset string, network string) (*model.DepositAddress, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Aster DEX 可能不支持传统充币，返回错误
	return nil, fmt.Errorf("Aster DEX does not support traditional deposit functionality")
}

// Withdraw 提币
// 注意：Aster 是 DEX，可能不支持传统充提功能
func (a *aster) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	// Aster DEX 可能不支持传统提币，返回错误
	return nil, fmt.Errorf("Aster DEX does not support traditional withdraw functionality")
}

// GetDepositHistory 查询充币记录
func (a *aster) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Aster DEX 可能不支持传统充币，返回空列表
	return []model.DepositRecord{}, nil
}

// GetWithdrawHistory 查询提币记录
func (a *aster) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Aster DEX 可能不支持传统提币，返回空列表
	return []model.WithdrawRecord{}, nil
}

// asterNetworkToChainID Aster API 返回的 network 名 → 链 ID（与跨链协议一致）
var asterNetworkToChainID = map[string]string{
	"ETH":      "1",     // Ethereum
	"ERC20":    "1",     // Ethereum
	"BSC":      "56",     // BNB Chain
	"BEP20":    "56",     // BNB Chain
	"BNB":      "56",     // BNB Chain
	"MATIC":    "137",    // Polygon
	"POLYGON":  "137",    // Polygon
	"ARBITRUM": "42161",  // Arbitrum One
	"ARB":      "42161",
	"OPTIMISM": "10",     // Optimism
	"OP":       "10",
	"AVAX":     "43114",  // Avalanche C-Chain
	"AVAXC":    "43114",
	"BASE":     "8453",   // Base
	"FTM":      "250",    // Fantom
	"TRX":      "",       // Tron 无统一 chainId
	"TRC20":    "",       // Tron
	"SOL":      "",       // Solana 留空
}

// GetWithdrawNetworks 查询某资产在 Aster 支持的提现网络
// 注意：Aster 是 DEX，可能不支持传统提现，返回空列表
func (a *aster) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if !a.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Aster DEX 可能不支持传统提现，返回空列表
	return []model.WithdrawNetworkInfo{}, nil
}
