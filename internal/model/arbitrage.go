package model

// PriceData 价格数据
type PriceData struct {
	Symbol    string
	BidPrice  float64 // 买一价
	AskPrice  float64 // 卖一价
	Timestamp int64
	Source    string // 价格源标识
}

// DiffResult 价差计算结果
type DiffResult struct {
	DiffAB float64 `json:"diff_ab"` // +A-B 方向价差（A卖一价 - B买一价）
	DiffBA float64 `json:"diff_ba"` // -A+B 方向价差（B卖一价 - A买一价）
	ABid   float64
	AAsk   float64
	BBid   float64
	BAsk   float64
}
