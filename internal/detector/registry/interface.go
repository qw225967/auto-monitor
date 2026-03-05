package registry

import "github.com/qw225967/auto-monitor/internal/model"

// NetworkRegistry 交易所充提网络注册表（用于生成 pipeline 邻接图）
type NetworkRegistry interface {
	// GetWithdrawNetworks 获取交易所在某资产上支持的提现网络（含链 ID）
	GetWithdrawNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error)
	// GetDepositNetworks 获取交易所在某资产上支持的充币网络（含链 ID）
	GetDepositNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error)
}
