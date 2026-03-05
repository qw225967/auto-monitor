package lighter

import (
	"fmt"

	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
)

var _ exchange.DepositWithdrawProvider = (*lighter)(nil)
var _ exchange.WithdrawNetworkLister = (*lighter)(nil)

// Deposit 获取充币地址
// 注意：Lighter 是 DEX，可能不支持传统充提功能
func (l *lighter) Deposit(asset string, network string) (*model.DepositAddress, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Lighter DEX 可能不支持传统充币，返回错误
	return nil, fmt.Errorf("Lighter DEX does not support traditional deposit functionality")
}

// Withdraw 提币
// 注意：Lighter 是 DEX，可能不支持传统充提功能
func (l *lighter) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	// Lighter DEX 可能不支持传统提币，返回错误
	return nil, fmt.Errorf("Lighter DEX does not support traditional withdraw functionality")
}

// GetDepositHistory 查询充币记录
func (l *lighter) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Lighter DEX 可能不支持传统充币，返回空列表
	return []model.DepositRecord{}, nil
}

// GetWithdrawHistory 查询提币记录
func (l *lighter) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Lighter DEX 可能不支持传统提币，返回空列表
	return []model.WithdrawRecord{}, nil
}

// GetWithdrawNetworks 查询某资产在 Lighter 支持的提现网络
// 注意：Lighter 是 DEX，可能不支持传统提现，返回空列表
func (l *lighter) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if !l.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Lighter DEX 可能不支持传统提现，返回空列表
	return []model.WithdrawNetworkInfo{}, nil
}
