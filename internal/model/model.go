package model

import (
	"time"
)

// 数据源输出类型
const (
	DataTypeSpread     = "spread"
	DataTypeSymbolList = "symbol_list"
)

// SpreadItem 价差数据单条（SeeingStone API 返回）
type SpreadItem struct {
	Symbol        string  `json:"symbol"`
	BuyExchange   string  `json:"buy_exchange"`
	SellExchange  string  `json:"sell_exchange"`
	SpreadPercent float64 `json:"spread_percent"`
	UpdatedAt     string  `json:"updated_at"`
	// 若 API 返回价格字段则解析，用于 CEX-DEX 对比
	BuyPrice  float64 `json:"buy_price,omitempty"`
	SellPrice float64 `json:"sell_price,omitempty"`
}

// SpreadAPIResponse SeeingStone API 响应
type SpreadAPIResponse struct {
	Success bool         `json:"success"`
	Count   int          `json:"count"`
	Data    []SpreadItem `json:"data"`
}

// PathItem 聚合后的路径项
type PathItem struct {
	Symbol        string
	BuyExchange   string
	SellExchange  string
	SpreadPercent float64
}

// Hop 物理路径中的单跳
type Hop struct {
	FromNode string
	EdgeDesc string
	ToNode   string
	Status   string // ok | maintenance | unavailable
}

// PhysicalPath 物理通路
type PhysicalPath struct {
	PathID        string
	Hops         []Hop
	OverallStatus string // ok | maintenance | unavailable
}

// PhysicalFlow 物理流描述（用于前端展示）
// 格式: BITGET → (提现BSC) → BSC链 → (跨链桥A) → ETH链 → (充值ETH) → GATE
func (p *PhysicalPath) PhysicalFlow() string {
	if len(p.Hops) == 0 {
		return ""
	}
	s := p.Hops[0].FromNode
	for _, h := range p.Hops {
		s += " → (" + h.EdgeDesc + ") → " + h.ToNode
	}
	return s
}

// AggregatedPaths 按 symbol 分组的路径
type AggregatedPaths map[string][]PathItem

// 套利机会类型
const (
	OppTypeCexCex  = "cex_cex"  // 交易所-交易所
	OppTypeCexDex  = "cex_dex"  // 交易所-链
	OppTypeDexDex  = "dex_dex"  // 链-链
)

// OverviewRow 主表单行（支持三种套利类型）
type OverviewRow struct {
	Type               string          `json:"type"`                 // cex_cex | cex_dex | dex_dex
	Symbol             string          `json:"symbol"`
	PathDisplay        string          `json:"path_display"`
	ChainLiquidity     string          `json:"chain_liquidity,omitempty"` // 链流动性展示，如 "ETH: 100万" 或 "ETH: 100万 | BSC: 50万"
	BuyExchange        string          `json:"buy_exchange"`         // CEX 名或 Chain_56
	SellExchange       string          `json:"sell_exchange"`        // CEX 名或 Chain_1
	SpreadPercent      float64         `json:"spread_percent"`
	GrossSpreadPercent float64         `json:"gross_spread_percent,omitempty"`  // 估算前毛价差（%）
	EstimatedCostPercent float64       `json:"estimated_cost_percent,omitempty"` // 静态成本估算（%）
	NetSpreadPercent   float64         `json:"net_spread_percent,omitempty"`    // 估算后净价差（%）
	ConfidenceScore    float64         `json:"confidence_score,omitempty"`       // 置信度评分 [0,1]
	AvailablePathCount int             `json:"available_path_count"`
	DetailPaths        []DetailPathRow `json:"detail_paths"`
}

// DetailPathRow 下钻详情单行
type DetailPathRow struct {
	PathID       string `json:"path_id"`
	PhysicalFlow string `json:"physical_flow"`
	Status       string `json:"status"`
}

// OverviewResponse API 返回结构
type OverviewResponse struct {
	Overview []OverviewRow `json:"overview"`
	// LiquidityThreshold 当前生效的流动性阈值（USDT），0 表示不限制
	LiquidityThreshold float64 `json:"liquidity_threshold,omitempty"`
	OverviewUpdatedAt  string  `json:"overview_updated_at,omitempty"`
	ChainPricesUpdatedAt string `json:"chain_prices_updated_at,omitempty"`
	LiquidityUpdatedAt string  `json:"liquidity_updated_at,omitempty"`
	OverviewAgeSec     int64   `json:"overview_age_sec,omitempty"`
	ChainPricesAgeSec  int64   `json:"chain_prices_age_sec,omitempty"`
	LiquidityAgeSec    int64   `json:"liquidity_age_sec,omitempty"`
	// LastDetectError 最近一次通路探测失败时的错误信息（空表示成功或从未探测）
	LastDetectError string `json:"last_detect_error,omitempty"`
}

// OpportunityItem 机会发现页面返回的单条记录
type OpportunityItem struct {
	Symbol                string  `json:"symbol"`
	SpotExchange          string  `json:"spot_exchange"`
	FuturesExchange       string  `json:"futures_exchange"`
	SpreadPercent         float64 `json:"spread_percent"`
	SpotOrderbookDepth    float64 `json:"spot_orderbook_depth"`
	FuturesOrderbookDepth float64 `json:"futures_orderbook_depth"`
	PriceSlope5m          float64 `json:"price_slope_5m"`
	VolumeSpike           bool    `json:"volume_spike"`
	Confidence            int     `json:"confidence"`
	UpdatedAt             string  `json:"updated_at"`
}

// FunnelStats 漏斗筛选统计
type FunnelStats struct {
	TotalSymbols        int `json:"total_symbols"`
	AfterNegativeSpread int `json:"after_negative_spread"`
	AfterSpotDepth     int `json:"after_spot_depth"`
	AfterPriceSlope    int `json:"after_price_slope"`
	AfterVolume        int `json:"after_volume"`
	AfterBothDepth     int `json:"after_both_depth"`
}

// OpportunitiesResponse 机会发现 API 响应
type OpportunitiesResponse struct {
	Opportunities []OpportunityItem `json:"opportunities"`
	FunnelStats   FunnelStats       `json:"funnel_stats"`
	UpdatedAt     string           `json:"updated_at"`
}

// PricePoint 价格历史点（用于斜率计算）
type PricePoint struct {
	Price     float64   `json:"price"`
	Timestamp time.Time `json:"timestamp"`
	Volume    float64   `json:"volume"`
}
