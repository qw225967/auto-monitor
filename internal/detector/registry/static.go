package registry

import (
	"strings"

	"github.com/qw225967/auto-monitor/internal/model"
)

// 常用链 ID（与 pipeline/跨链协议一致）
const (
	ChainETH     = "1"
	ChainBSC     = "56"
	ChainTRON    = "195"
	ChainPolygon = "137"
	ChainArbitrum = "42161"
	ChainOptimism = "10"
	ChainBase    = "8453"
	ChainAvalanche = "43114"
)

// staticExchangeNetworks 交易所 -> 资产 -> 支持的链 ID 列表（提现/充币通用，静态配置）
// 基于主流交易所 USDT 常见支持网络，无 API 调用
var staticExchangeNetworks = map[string]map[string][]string{
	"binance": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism, ChainBase, ChainAvalanche},
		"USDC": {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
	},
	"bybit": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism, ChainBase},
		"USDC": {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon},
	},
	"bitget": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism},
		"USDC": {ChainETH, ChainBSC, ChainPolygon},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon},
	},
	"gate": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism, ChainBase, ChainAvalanche},
		"USDC": {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
	},
	"okex": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism, ChainBase},
		"USDC": {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon},
	},
	"okx": {
		"USDT": {ChainETH, ChainBSC, ChainTRON, ChainPolygon, ChainArbitrum, ChainOptimism, ChainBase},
		"USDC": {ChainETH, ChainBSC, ChainPolygon, ChainArbitrum},
		"ETH":  {ChainETH, ChainBSC, ChainPolygon},
	},
	"hyperliquid": {
		"USDT": {ChainETH, ChainArbitrum},
		"ETH":  {ChainETH},
	},
	"lighter": {
		"USDT": {ChainETH, ChainArbitrum},
		"ETH":  {ChainETH},
	},
	"aster": {
		"USDT": {ChainETH, ChainArbitrum},
		"ETH":  {ChainETH},
	},
}

// StaticRegistry 静态配置的交易所充提网络注册表（无 API 调用）
type StaticRegistry struct{}

// NewStaticRegistry 创建静态注册表
func NewStaticRegistry() *StaticRegistry {
	return &StaticRegistry{}
}

// GetWithdrawNetworks 获取交易所在某资产上支持的提现网络
func (s *StaticRegistry) GetWithdrawNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	return s.getNetworks(exchangeType, asset)
}

// GetDepositNetworks 获取交易所在某资产上支持的充币网络（静态配置与提现相同）
func (s *StaticRegistry) GetDepositNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	return s.getNetworks(exchangeType, asset)
}

func (s *StaticRegistry) getNetworks(exchangeType, asset string) ([]model.WithdrawNetworkInfo, error) {
	ex := strings.ToLower(strings.TrimSpace(exchangeType))
	if ex == "okx" {
		ex = "okex"
	}
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	if assetUpper == "" {
		assetUpper = "USDT"
	}

	assets, ok := staticExchangeNetworks[ex]
	if !ok {
		return nil, nil
	}
	chainIDs, ok := assets[assetUpper]
	if !ok {
		// 未显式配置的资产不假定支持，避免误报（如 POWER 在 Bybit 可能不支持充提）
		return nil, nil
	}
	if len(chainIDs) == 0 {
		return nil, nil
	}

	out := make([]model.WithdrawNetworkInfo, 0, len(chainIDs))
	for _, cid := range chainIDs {
		if cid == "" {
			continue
		}
		out = append(out, model.WithdrawNetworkInfo{
			Network:        chainIDToNetworkName(cid),
			ChainID:        cid,
			WithdrawEnable: true,
			IsDefault:      cid == ChainBSC || cid == ChainETH,
		})
	}
	return out, nil
}

func chainIDToNetworkName(chainID string) string {
	switch chainID {
	case ChainETH:
		return "ETH"
	case ChainBSC:
		return "BSC"
	case ChainTRON:
		return "TRC20"
	case ChainPolygon:
		return "Polygon"
	case ChainArbitrum:
		return "Arbitrum"
	case ChainOptimism:
		return "Optimism"
	case ChainBase:
		return "Base"
	case ChainAvalanche:
		return "Avalanche"
	default:
		return chainID
	}
}
