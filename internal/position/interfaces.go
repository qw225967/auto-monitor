package position

import "auto-arbitrage/internal/model"

// WalletDetailInfoProvider 钱包详细信息提供者接口
type WalletDetailInfoProvider interface {
	GetType() string
	GetAllBalances() (map[string]*model.Balance, error)
	GetPositions() ([]*model.Position, error)
}
