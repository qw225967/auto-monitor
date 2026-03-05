package model

import "time"

// ExchangeWalletInfo 单个交易所的钱包信息
type ExchangeWalletInfo struct {
	// 交易所类型（如 "binance", "bybit" 等）
	ExchangeType string `json:"exchange_type"`

	// 现货账户余额信息（按资产类型索引，如 USDT, BTC, ETH 等）
	SpotBalances map[string]*Balance `json:"spot_balances"`

	// 合约账户余额信息（按资产类型索引，如 USDT, BTC, ETH 等）
	FuturesBalances map[string]*Balance `json:"futures_balances"`

	// 向后兼容字段（保留原有字段）
	// 注意：如果同时设置了 SpotBalances 和 FuturesBalances，AccountBalances 将自动聚合这两个字段
	AccountBalances map[string]*Balance `json:"account_balances,omitempty"`

	// 持仓列表（按 symbol 索引）
	Positions map[string]*Position `json:"positions"`

	// 该交易所的统计信息
	TotalBalanceValue  float64 `json:"total_balance_value"`  // 该交易所的余额价值
	TotalPositionValue float64 `json:"total_position_value"` // 该交易所的持仓价值
	TotalUnrealizedPnl float64 `json:"total_unrealized_pnl"` // 该交易所的未实现盈亏
	PositionCount      int     `json:"position_count"`       // 该交易所的持仓数量
}

// WalletDetailInfo 钱包详细信息
// 包含多个交易所、链上余额的账户余额、持仓信息以及总账户统计
type WalletDetailInfo struct {
	// 多个交易所的钱包信息（按交易所类型索引，如 "binance", "bybit" 等）
	ExchangeWallets map[string]*ExchangeWalletInfo `json:"exchange_wallets"`

	// 链上余额信息（按链索引，每个链包含多个代币资产）
	// 第一层 key: chainIndex (如 "56" 表示 BSC)
	// 第二层 key: Symbol (代币符号，如 "USDT", "BTC" 等), value: 该代币的资产信息
	OnchainBalances map[string]map[string]OkexTokenAsset `json:"onchain_balances"`

	// 总账户统计信息（聚合所有交易所和链上余额）
	TotalAsset         float64   `json:"total_asset"`          // 总资产（所有交易所余额 + 所有持仓价值 + 所有链上余额，以 USDT 计价）
	TotalUnrealizedPnl float64   `json:"total_unrealized_pnl"` // 总未实现盈亏（所有交易所的未实现盈亏之和）
	TotalPositionValue float64   `json:"total_position_value"` // 总持仓价值（所有交易所的持仓价值之和，以 USDT 计价）
	TotalBalanceValue  float64   `json:"total_balance_value"`  // 总余额价值（所有交易所的余额价值之和，所有币种余额转换为 USDT 后的总和）
	TotalOnchainValue  float64   `json:"total_onchain_value"`  // 总链上余额价值（所有链上代币余额转换为 USDT 后的总和）
	PositionCount      int       `json:"position_count"`       // 总持仓数量（所有交易所的持仓数量之和）
	OnchainChainCount  int       `json:"onchain_chain_count"`  // 总链上余额涉及的链数量（去重后的链数量）
	ExchangeCount      int       `json:"exchange_count"`       // 交易所数量
	UpdateTime         time.Time `json:"update_time"`          // 更新时间

	// 向后兼容字段（保留原有字段，用于兼容旧代码）
	// 这些字段会自动从 ExchangeWallets 和 OnchainBalances 聚合生成
	AccountBalances map[string]*Balance  `json:"account_balances,omitempty"` // 所有交易所的余额聚合（按资产类型）
	Positions       map[string]*Position `json:"positions,omitempty"`        // 所有交易所的持仓聚合（按 symbol）
}

