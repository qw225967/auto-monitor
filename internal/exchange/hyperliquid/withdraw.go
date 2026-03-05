package hyperliquid

import (
	"fmt"

	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
)

var _ exchange.DepositWithdrawProvider = (*hyperliquidExchange)(nil)
var _ exchange.WithdrawNetworkLister = (*hyperliquidExchange)(nil)

// Deposit 获取充币地址
// 注意：Hyperliquid 是 DEX，可能不支持传统充提功能
func (h *hyperliquidExchange) Deposit(asset string, network string) (*model.DepositAddress, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Hyperliquid DEX 可能不支持传统充币，返回错误
	return nil, fmt.Errorf("Hyperliquid DEX does not support traditional deposit functionality")
}

// Withdraw 提币
// 注意：Hyperliquid 是 DEX，可能不支持传统充提功能
func (h *hyperliquidExchange) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	// Hyperliquid DEX 可能不支持传统提币，返回错误
	return nil, fmt.Errorf("Hyperliquid DEX does not support traditional withdraw functionality")
}

// GetDepositHistory 查询充币记录
func (h *hyperliquidExchange) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Hyperliquid DEX 可能不支持传统充币，返回空列表
	return []model.DepositRecord{}, nil
}

// GetWithdrawHistory 查询提币记录
func (h *hyperliquidExchange) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Hyperliquid DEX 可能不支持传统提币，返回空列表
	return []model.WithdrawRecord{}, nil
}

// GetWithdrawNetworks 查询某资产在 Hyperliquid 支持的提现网络
// 注意：Hyperliquid 是 DEX，可能不支持传统提现，返回空列表
func (h *hyperliquidExchange) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	// Hyperliquid DEX 可能不支持传统提现，返回空列表
	return []model.WithdrawNetworkInfo{}, nil
}
