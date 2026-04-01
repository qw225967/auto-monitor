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
	// 内部字段，不序列化：价差偏离均值的标准差倍数（由监控池写入）
	SpreadAnomaly float64 `json:"-"`
}

// SpreadAPIResponse SeeingStone API 响应
type SpreadAPIResponse struct {
	Success bool         `json:"success"`
	Count   int          `json:"count"`
	Data    []SpreadItem `json:"data"`
}

// 套利机会类型
const (
	OppTypeCexCex = "cex_cex" // 交易所-交易所
	OppTypeCexDex = "cex_dex" // 交易所-链
	OppTypeDexDex = "dex_dex" // 链-链
)

// DetailPathRow 下钻详情单行
type DetailPathRow struct {
	PathID       string `json:"path_id"`
	PhysicalFlow string `json:"physical_flow"`
	Status       string `json:"status"`
}

// OverviewRow 主表单行（支持三种套利类型）
type OverviewRow struct {
	Type                 string          `json:"type"` // cex_cex | cex_dex | dex_dex
	Symbol               string          `json:"symbol"`
	PathDisplay          string          `json:"path_display"`
	ChainLiquidity       string          `json:"chain_liquidity,omitempty"`
	BuyExchange          string          `json:"buy_exchange"`
	SellExchange         string          `json:"sell_exchange"`
	SpreadPercent        float64         `json:"spread_percent"`
	GrossSpreadPercent   float64         `json:"gross_spread_percent,omitempty"`
	EstimatedCostPercent float64         `json:"estimated_cost_percent,omitempty"`
	NetSpreadPercent     float64         `json:"net_spread_percent,omitempty"`
	ConfidenceScore      float64         `json:"confidence_score,omitempty"`
	AvailablePathCount   int             `json:"available_path_count"`
	DetailPaths          []DetailPathRow `json:"detail_paths"`
}

// OverviewResponse API 返回结构
type OverviewResponse struct {
	Overview             []OverviewRow `json:"overview"`
	LiquidityThreshold   float64       `json:"liquidity_threshold,omitempty"`
	OverviewUpdatedAt    string        `json:"overview_updated_at,omitempty"`
	ChainPricesUpdatedAt string        `json:"chain_prices_updated_at,omitempty"`
	LiquidityUpdatedAt   string        `json:"liquidity_updated_at,omitempty"`
	OverviewAgeSec       int64         `json:"overview_age_sec,omitempty"`
	ChainPricesAgeSec    int64         `json:"chain_prices_age_sec,omitempty"`
	LiquidityAgeSec      int64         `json:"liquidity_age_sec,omitempty"`
	LastDetectError      string        `json:"last_detect_error,omitempty"`
}

// OpportunityItem 机会发现页面返回的单条记录
type OpportunityItem struct {
	Symbol             string  `json:"symbol"`
	SpotExchange       string  `json:"spot_exchange"`
	FuturesExchange    string  `json:"futures_exchange"`
	SpreadPercent      float64 `json:"spread_percent"`
	SpotOrderbookDepth float64 `json:"spot_orderbook_depth"`
	SpreadAnomaly      float64 `json:"spread_anomaly"`     // 价差偏离均值的标准差倍数
	PriceAccelRatio    float64 `json:"price_accel_ratio"`  // 价格斜率加速比（短窗口/长窗口）
	VolumeAccelScore   float64 `json:"volume_accel_score"` // 量能加权加速分（深度*0.4 + 成交量*0.6）
	Confidence         int     `json:"confidence"`
	UpdatedAt          string  `json:"updated_at"`
}

// FunnelStats 漏斗筛选统计
type FunnelStats struct {
	TotalSymbols       int `json:"total_symbols"`
	AfterSpreadInRange int `json:"after_spread_in_range"` // 本轮价差在 [-1%,1%] 且参与监控池的条数
	WatchPoolSize      int `json:"watch_pool_size"`       // 当前监控池内 symbol 总数
	CoolingPoolSize    int `json:"cooling_pool_size"`
	AfterSpreadAnomaly int `json:"after_spread_anomaly"` // 层1：价差突变 2σ
	AfterPriceAccel    int `json:"after_price_accel"`    // 层2+3：价格+挂单量斜率加速
	AfterDepthVolume   int `json:"after_depth_volume"`    // 层4：挂单量猛增 → 最终机会
}

// WatchPoolEntry 监控池中单个 symbol 的状态（Welford 在线算法）
type WatchPoolEntry struct {
	Symbol       string
	SpreadCount  int
	SpreadMean   float64
	SpreadM2     float64   // Welford M2（方差累积量，variance = M2/count）
	SpreadDebug  []float64 // 最近 10 次价差（环形，仅调试日志用）
	IsActive     bool      // 是否处于活跃（异常）状态
	ActiveSince  time.Time // 活跃开始时间
	NormalRounds int       // 活跃后连续回归正常的轮次计数
	MissedRounds int       // 连续未出现在数据中的轮次计数
	LastSeen     time.Time // 最后一次出现的时间
	LastSpread   float64   // 最近一次价差值
}

// CoolingEntry 冷却列表中的单个 symbol
type CoolingEntry struct {
	Symbol     string    `json:"symbol"`
	KickedAt   time.Time `json:"kicked_at"`
	LastSpread float64   `json:"last_spread"`
	Reason     string    `json:"reason"`
}

// OpportunitiesResponse 机会发现 API 响应
type OpportunitiesResponse struct {
	Opportunities  []OpportunityItem `json:"opportunities"`
	FunnelStats    FunnelStats       `json:"funnel_stats"`
	CoolingSymbols []CoolingEntry    `json:"cooling_symbols"`
	UpdatedAt      string            `json:"updated_at"`
}

// PricePoint 价格历史点（用于斜率计算）
type PricePoint struct {
	Price     float64   `json:"price"`
	Timestamp time.Time `json:"timestamp"`
	Volume    float64   `json:"volume"`
}
