package model

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

// OverviewRow 主表单行
type OverviewRow struct {
	Symbol             string          `json:"symbol"`
	PathDisplay        string          `json:"path_display"`
	BuyExchange        string          `json:"buy_exchange"`
	SellExchange       string          `json:"sell_exchange"`
	SpreadPercent      float64         `json:"spread_percent"`
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
}
