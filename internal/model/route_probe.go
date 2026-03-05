package model

// 段类型常量（与 SegmentProbeResult.Type 一致，避免在逻辑中写死字符串）
const (
	SegmentTypeWithdraw            = "withdraw"
	SegmentTypeDeposit             = "deposit"
	SegmentTypeBridge              = "bridge"
	SegmentTypeExchangeToExchange  = "exchange_to_exchange"
)

// RouteProbeRequest 提币路由探测请求
type RouteProbeRequest struct {
	Symbol string   // 币种（如 USDT）
	Path   []string // 节点 ID 列表，仅含资产持有节点，如 ["binance", "onchain:56", "onchain:1", "bitget"]；跨链由相邻 Onchain（不同 chainID）的边表示
	// 当 Path 为空时用于解析
	Source      string // 源节点 ID 或类型
	Destination string // 目标节点 ID 或类型
	// 搬砖场景：与 Direction 同时传入时，从 Trigger 的 pipeline 配置解析 Source/Destination/Symbol
	TriggerSymbol string `json:"triggerSymbol"` // 可选，如 CLOUSDT
	TriggerID     string `json:"triggerId"`     // 可选，同 symbol 多 trigger 时区分
	Direction     string `json:"direction"`     // 可选，"forward" 或 "backward"
	// 桥报价用金额，默认 "100"
	ProbeAmount string // 可选
	// bridge 段协议，与 EdgeConfig.BridgeProtocol 一致（如 layerzero / wormhole / auto）
	BridgeProtocol string // 可选
}

// RouteProbeResult 提币路由探测结果
type RouteProbeResult struct {
	Path                  []string             `json:"path"`                            // 实际使用的节点 ID 列表（首选路径，最多 3 个节点）
	Segments              []SegmentProbeResult `json:"segments"`                        // 每段探测结果
	TotalEstimatedTimeSec int64                `json:"totalEstimatedTimeSec"`           // 总预估耗时（秒）
	TotalFee              string               `json:"totalFee"`                        // 总损耗（主币种）
	TotalFeeByAsset       map[string]string    `json:"totalFeeByAsset,omitempty"`       // 总损耗按币种（可选）
	RecommendedMinAmount  string               `json:"recommendedMinAmount,omitempty"`  // 整条路由建议的最小提币量
	ProbeMinAmountHint    string               `json:"probeMinAmountHint,omitempty"`    // 探测最少消耗提示
	AlternativePaths      []PathProbeSummary   `json:"alternativePaths,omitempty"`      // 多条候选路径
	VerifyWithdrawHint    string               `json:"verifyWithdrawHint,omitempty"`    // 小成本确认提示：需在对应链买入 pipeline数量×5 USDT 现货用于测试提现
	ResolveSummary        string               `json:"resolveSummary,omitempty"`        // 解析说明：如「可提现网络 N 个，生成 M 条路径」
}

// PathProbeSummary 单条路径探测摘要（用于多路径返回）
type PathProbeSummary struct {
	Path                  []string             `json:"path"`
	Segments              []SegmentProbeResult `json:"segments"`
	TotalEstimatedTimeSec int64                `json:"totalEstimatedTimeSec"`
	TotalFee              string               `json:"totalFee"`
	RecommendedMinAmount  string               `json:"recommendedMinAmount"`
	ProbeMinAmountHint     string               `json:"probeMinAmountHint,omitempty"`
}

// SegmentProbeResult 单段探测结果
type SegmentProbeResult struct {
	FromNodeID             string                 `json:"fromNodeID"`                        // 源节点 ID
	ToNodeID               string                 `json:"toNodeID"`                         // 目标节点 ID
	Type                   string                 `json:"type"`                             // "withdraw" | "deposit" | "bridge" | "exchange_to_exchange"
	Fee                    string                 `json:"fee"`                              // 本段费用
	EstimatedTimeSec       int64                  `json:"estimatedTimeSec"`                 // 本段预估耗时（秒）
	MinAmount              string                 `json:"minAmount,omitempty"`              // 最小数量（若有）
	MaxAmount              string                 `json:"maxAmount,omitempty"`              // 最大数量（若有）
	Available              bool                   `json:"available"`                         // 本段是否可用（前端用于绿/红箭头）
	BridgeProtocol         string                 `json:"bridgeProtocol,omitempty"`           // 跨链段使用的协议（如 layerzero / ccip / wormhole）
	WithdrawNetworkChainID string                 `json:"withdrawNetworkChainID,omitempty"`  // 交易所→交易所 段使用的提现网络链 ID（如 "1" 表示 ETH），与 protocol 一样作为边属性展示
	EdgeLabel              string                 `json:"edgeLabel,omitempty"`               // 边上括号内展示的文案：跨链协议名或链名称（充/提链）
	RawInfo                map[string]interface{} `json:"rawInfo,omitempty"`                // 原始信息（如不可用原因）便于前端展示
}
