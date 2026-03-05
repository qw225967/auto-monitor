package model

import "time"

// SwapInfo Swap 交易信息
type SwapInfo struct {
	FromTokenSymbol          string // 兑换源币 symbol
	ToTokenSymbol            string // 兑换目标币 symbol
	FromTokenContractAddress string // 源币合约地址
	ToTokenContractAddress   string // 目标币合约地址
	ChainIndex               string // 链 ID
	Amount                   string // 数量
	DecimalsFrom             string // 源币精度
	DecimalsTo               string // 目标币精度
	SwapMode                 string // 买入模式，目前固定：exactIn
	Slippage                 string // 滑点配置
	GasLimit                 string // 最低 gas 限制
	WalletAddress            string // 钱包地址
}

// SwapTask 内部 swap 查询任务结构
type SwapTask struct {
	FromTokenSymbol          string // 兑换源币 symbol
	ToTokenSymbol            string // 兑换目标币 symbol
	FromTokenContractAddress string // 源币合约地址
	ToTokenContractAddress   string // 目标币合约地址
	FromTokenDecimals        string // 源币精度
	ToTokenDecimals          string // 目标币精度
	ChainIndex               string // 链 ID
	Amount                   string // 数量（已转换为最小单位）
	SwapMode                 string // 买入模式，目前固定：exactIn
	Slippage                 string // 滑点配置
	GasLimit                 string // 最低 gas 限制
	WalletAddress            string // 钱包地址
}

// QuoteRequest 询价请求
type QuoteRequest struct {
	FromToken string // 源代币地址或符号（如 USDT）
	ToToken   string // 目标代币地址或符号（如 BTC）
	Amount    string // 交易数量（字符串格式，支持大数）
	ChainID   string // 链ID（如 "56" 表示 BSC）
	Slippage  string // 滑点容忍度（如 "0.005" 表示 0.5%）
}

// Quote 询价响应
type Quote struct {
	FromToken   string   // 源代币
	ToToken     string   // 目标代币
	FromAmount  string   // 输入数量
	ToAmount    string   // 输出数量
	Price       float64  // 价格
	PriceImpact string   // 价格影响
	Slippage    string   // 滑点
	GasEstimate string   // Gas估算
	Route       []string // 交易路径（DEX路由）
	ChainID     string   // 链ID
}

// SwapRequest Swap交易请求
type SwapRequest struct {
	FromToken    string // 源代币地址
	ToToken      string // 目标代币地址
	Amount       string // 交易数量
	MinAmountOut string // 最小输出数量（滑点保护）
	ChainID      string // 链ID
	Slippage     string // 滑点容忍度
	Recipient    string // 接收地址（可选，默认使用钱包地址）
	Deadline     int64  // 交易截止时间（Unix时间戳）
}

// SwapResponse Swap交易响应
type SwapResponse struct {
	TxHash       string // 交易哈希
	Status       string // 交易状态
	ChainID      string // 链ID
	FromToken    string // 源代币
	ToToken      string // 目标代币
	Amount       string // 交易数量
	EstimatedGas string // 预估Gas费用
}

// SwapStatus 交易状态
type SwapStatus string

const (
	SwapStatusPending   SwapStatus = "PENDING"   // 待确认
	SwapStatusConfirmed SwapStatus = "CONFIRMED" // 已确认
	SwapStatusFailed    SwapStatus = "FAILED"    // 失败
)

// TokenBalance 代币余额
type TokenBalance struct {
	TokenAddress string // 代币合约地址
	TokenSymbol  string // 代币符号（如 USDT）
	Balance      string // 余额（字符串格式，支持大数）
	Decimals     int    // 精度
	ChainID      string // 链ID
}

// Transaction 交易信息
type Transaction struct {
	TxHash      string // 交易哈希
	Status      string // 状态
	From        string // 发送地址
	To          string // 接收地址
	Value       string // 交易金额
	GasUsed     string // 使用的Gas
	GasPrice    string // Gas价格
	BlockNumber string // 区块号
	BlockHash   string // 区块哈希
	ChainID     string // 链ID
	Timestamp   int64  // 时间戳
}

// OkexKeyRecord OKEx API Key 记录
type OkexKeyRecord struct {
	AppKey       string // API Key
	SecretKey    string // Secret Key
	Passphrase   string // Passphrase
	Index        int    // 索引
	CanBroadcast bool   // 是否可以广播交易
}

// OkexDexTokenDetail OKEx DEX 代币详情
type OkexDexTokenDetail struct {
	TokenSymbol          string `json:"tokenSymbol"`          // 代币符号
	TokenContractAddress string `json:"tokenContractAddress"` // 代币合约地址
	Decimals             string `json:"decimal"`              // 精度
	ChainId              string `json:"chainId,omitempty"`    // 链ID
	IsHoneyPot           bool   `json:"isHoneyPot"`           // 是否为蜜罐代币
	TaxRate              string `json:"taxRate"`              // 税率
	TokenUnitPrice       string `json:"tokenUnitPrice"`       // 代币单价
}

// OkexDexSwapResponse OKEx DEX Swap 响应
type OkexDexSwapResponse struct {
	Code  string                `json:"code"` // 响应代码，"0" 表示成功
	Msg   string                `json:"msg"`  // 响应消息
	Data  []OkexDexSwapDataItem `json:"data"` // 数据数组
	Index int                   `json:"-"`    // API Key 索引（不序列化）
}

// OkexDexSwapDataItem Swap 数据项
type OkexDexSwapDataItem struct {
	RouterResult OkexDexRouterResult `json:"routerResult"` // 路由结果
	Tx           OkexDexTx           `json:"tx"`           // 交易数据
}

// OkexDexRouterResult 路由结果（v6 API）
type OkexDexRouterResult struct {
	ChainIndex         string              `json:"chainIndex"`         // 链索引（v6 只有 chainIndex，没有 chainId）
	ContextSlot        int                 `json:"contextSlot"`        // 上下文槽位
	DexRouterList      []OkexDexRouterList `json:"dexRouterList"`      // DEX路由列表（v6 结构变化）
	EstimateGasFee     string              `json:"estimateGasFee"`     // 预估Gas费用
	FromToken          OkexDexTokenDetail  `json:"fromToken"`          // 源代币详情
	FromTokenAmount    string              `json:"fromTokenAmount"`    // 输入代币数量
	PriceImpactPercent string              `json:"priceImpactPercent"` // 价格影响百分比（v6：priceImpactPercent，不是 priceImpactPercentage）
	Router             string              `json:"router"`             // 路由地址（v6 新增）
	SwapMode           string              `json:"swapMode"`           // 交换模式（exactIn/exactOut）
	ToToken            OkexDexTokenDetail  `json:"toToken"`            // 目标代币详情
	ToTokenAmount      string              `json:"toTokenAmount"`      // 输出代币数量
	TradeFee           string              `json:"tradeFee"`           // 交易手续费
	// v6 移除了字段：
	// ChainId (已移除)
	// QuoteCompareList (已移除)
}

// OkexDexRouterList DEX路由列表项（v6 API）
type OkexDexRouterList struct {
	DexProtocol    OkexDexProtocol    `json:"dexProtocol"`    // DEX协议（v6：单个对象，不是数组）
	FromToken      OkexDexTokenDetail `json:"fromToken"`      // 源代币
	FromTokenIndex string             `json:"fromTokenIndex"` // 源代币索引（v6 新增）
	ToToken        OkexDexTokenDetail `json:"toToken"`        // 目标代币
	ToTokenIndex   string             `json:"toTokenIndex"`   // 目标代币索引（v6 新增）
	// v6 移除了字段：
	// Router (已移除，移动到 routerResult.router)
	// RouterPercent (已移除)
	// SubRouterList (已移除，结构扁平化)
}

// OkexDexProtocol DEX协议
type OkexDexProtocol struct {
	DexName string `json:"dexName"` // DEX名称
	Percent string `json:"percent"` // 百分比
}

// OkexDexQuoteCompare 报价比较项
type OkexDexQuoteCompare struct {
	AmountOut string `json:"amountOut"` // 输出数量
	DexLogo   string `json:"dexLogo"`   // DEX Logo URL
	DexName   string `json:"dexName"`   // DEX名称
	TradeFee  string `json:"tradeFee"`  // 交易手续费
}

// OkexDexTx 交易数据（v6 API）
type OkexDexTx struct {
	Data                 string   `json:"data"`                 // 交易数据（hex 字符串）
	From                 string   `json:"from"`                 // 发送地址
	Gas                  string   `json:"gas"`                  // Gas限制
	GasPrice             string   `json:"gasPrice"`             // Gas价格
	MaxPriorityFeePerGas string   `json:"maxPriorityFeePerGas"` // 最大优先费用
	MaxSpendAmount       string   `json:"maxSpendAmount"`       // 最大支出金额（v6 可能为空字符串）
	MinReceiveAmount     string   `json:"minReceiveAmount"`     // 最小接收金额
	SignatureData        []string `json:"signatureData"`        // 签名数据（v6：数组，可能包含空字符串）
	SlippagePercent      string   `json:"slippagePercent"`      // 滑点百分比（v6：slippagePercent，不是 slippage）
	To                   string   `json:"to"`                   // 接收地址（聚合器合约地址）
	Value                string   `json:"value"`                // 交易金额（原生代币）
}

// OkexDexApproveTransactionResponse 授权交易响应
type OkexDexApproveTransactionResponse struct {
	Code string                          // 响应代码，"0" 表示成功
	Msg  string                          // 响应消息
	Data []OkexDexApproveTransactionData // 数据数组
}

// OkexDexApproveTransactionData 授权交易数据
type OkexDexApproveTransactionData struct {
	GasPrice string // Gas 价格
	GasLimit string
	Data     string // 交易数据（hex 字符串）
	To       string // 目标地址（聚合器合约地址，可选）
}

// OkexBroadcastTxRequest 广播交易请求
type OkexBroadcastTxRequest struct {
	SignedTx   string `json:"signedTx"`   // 签名后的交易数据
	ChainIndex string `json:"chainIndex"` // 链 ID
	Address    string `json:"address"`    // 钱包地址
}

// OkexBroadcastTxResponse 广播交易响应
type OkexBroadcastTxResponse struct {
	Code string                    // 响应代码，"0" 表示成功
	Msg  string                    // 响应消息
	Data []OkexBroadcastTxDataItem // 数据数组
}

// OkexBroadcastTxDataItem 广播交易数据项
type OkexBroadcastTxDataItem struct {
	TxHash  string // 交易哈希
	OrderId string // 订单 ID
}

// OkexSwapTxDetail Swap 交易详情（用于签名）
type OkexSwapTxDetail struct {
	GasPrice string // Gas 价格
	Gas      string // Gas 限制
	Data     string // 交易数据
	To       string // 目标地址
	Value    string // 交易金额（原生代币）
}

// 交易随机数（Nonce）响应
type OkexNonceResponse struct {
	Code string          `json:"code"`
	Data []OkexNonceData `json:"data"`
	Msg  string          `json:"msg"`
}

type OkexNonceData struct {
	Nonce string `json:"nonce"` // 随机数（交易次数）
}

// PriceInfo 价格信息结构体
type ChainPriceInfo struct {
	CoinSymbol     string // 币对符号（如 BTCUSDT）
	ChainPriceBuy  string // 买一价
	ChainPriceSell string // 卖一价
	ChainBuyTx     string // 买入交易数据
	ChainSellTx    string // 卖出交易数据
	ChainId        string // 链ID
}

// TradeResult 交易结果（查询广播后的执行状态）
type TradeResult struct {
	Status    string // constants.TradeStatus*
	AmountIn  string // 输入数量
	AmountOut string // 输出数量
	ErrorMsg  string // 失败原因
	GasUsed   string // 实际消耗 Gas（链上查询）
	TxFee     string // 实际交易手续费/Gas 费（链上查询，单位通常为 native token 最小单位或已换算）
}

// OkexTxStatusResponse OKEx DEX 历史/交易状态查询响应
type OkexTxStatusResponse struct {
	Code string               `json:"code"`
	Msg  string               `json:"msg"`
	Data OkexTxStatusDataBody `json:"data"`
}

type OkexTxStatusDataBody struct {
	ChainId          string                   `json:"chainId"`
	ChainIndex       string                   `json:"chainIndex"`
	DexRouter        string                   `json:"dexRouter"`
	ErrorMsg         string                   `json:"errorMsg"`
	FromAddress      string                   `json:"fromAddress"`
	FromTokenDetails OkexTxStatusTokenDetails `json:"fromTokenDetails"`
	GasLimit         string                   `json:"gasLimit"`
	GasPrice         string                   `json:"gasPrice"`
	GasUsed          string                   `json:"gasUsed"`
	Height           string                   `json:"height"`
	ReferralAmount   string                   `json:"referralAmount"`
	Status           string                   `json:"status"` // success, pending, fail
	ToAddress        string                   `json:"toAddress"`
	ToTokenDetails   OkexTxStatusTokenDetails `json:"toTokenDetails"`
	TxFee            string                   `json:"txFee"`
	TxHash           string                   `json:"txHash"`
	TxTime           string                   `json:"txTime"`
	TxType           string                   `json:"txType"`
}

type OkexTxStatusTokenDetails struct {
	Amount       string `json:"amount"`
	Symbol       string `json:"symbol"`
	TokenAddress string `json:"tokenAddress"`
}

// OkexTokenBalanceResponse OKEx 代币余额查询响应
type OkexTokenBalanceResponse struct {
	Code string                 `json:"code"` // 响应代码，"0" 表示成功
	Msg  string                 `json:"msg"`  // 响应消息
	Data []OkexTokenBalanceData `json:"data"` // 数据数组
}

// OkexTokenBalanceData 代币余额数据
type OkexTokenBalanceData struct {
	TokenAssets []OkexTokenAsset `json:"tokenAssets"` // 代币资产列表
}

// OkexTokenAsset 代币资产详情
type OkexTokenAsset struct {
	ChainIndex           string `json:"chainIndex"`           // 链唯一标识
	TokenContractAddress string `json:"tokenContractAddress"` // 合约地址
	Address              string `json:"address"`              // 地址
	Symbol               string `json:"symbol"`               // 代币简称
	Balance              string `json:"balance"`              // 代币数量
	RawBalance           string `json:"rawBalance"`           // 代币的原始数量（对于不支持的链，该字段为空）
	TokenPrice           string `json:"tokenPrice"`           // 币种单位价值，以美元计价
	IsRiskToken          bool   `json:"isRiskToken"`          // true：命中风险空投代币和貔貅盘代币 false：未命中
}

// BridgeRequest 跨链转账请求
type BridgeRequest struct {
	FromChain string // 源链ID（如 "1" 表示 Ethereum）
	ToChain   string // 目标链ID（如 "56" 表示 BSC）
	FromToken string // 源代币地址或符号
	ToToken   string // 目标代币地址或符号
	Amount    string // 转账数量
	Recipient string // 接收地址（可选，默认使用钱包地址）
	Protocol  string // 协议选择（"auto", "layerzero", "wormhole"）
}

// BridgeResponse 跨链转账响应
type BridgeResponse struct {
	TxHash        string    // 源链交易哈希
	BridgeID      string    // 跨链ID（用于查询状态）
	Protocol      string    // 使用的协议
	EstimatedTime int64     // 预估完成时间（秒）
	Fee           string    // 跨链费用
	CreateTime    time.Time // 创建时间
}

// BridgeStatus 跨链状态
type BridgeStatus struct {
	BridgeID     string     // 跨链ID
	Status       string     // 状态（PENDING, IN_PROGRESS, COMPLETED, FAILED）
	FromTxHash   string     // 源链交易哈希
	ToTxHash     string     // 目标链交易哈希（完成时）
	FromChain    string     // 源链ID
	ToChain      string     // 目标链ID
	Amount       string     // 转账数量
	Protocol     string     // 使用的协议
	CreateTime   time.Time  // 创建时间
	CompleteTime *time.Time // 完成时间（完成时，可为 nil）
}

// BridgeQuoteRequest 跨链报价请求
type BridgeQuoteRequest struct {
	FromChain string // 源链ID
	ToChain   string // 目标链ID
	FromToken string // 源代币地址或符号
	ToToken   string // 目标代币地址或符号
	Amount    string // 转账数量
}

// BridgeQuote 跨链报价
type BridgeQuote struct {
	Protocols []ProtocolQuote // 各协议的报价
}

// ProtocolQuote 协议报价
type ProtocolQuote struct {
	Protocol      string                 // 协议名称（"layerzero", "wormhole"）
	Fee           string                 // 费用
	EstimatedTime int64                  // 预估时间（秒）
	MinAmount     string                 // 最小转账数量
	MaxAmount     string                 // 最大转账数量
	Supported     bool                   // 是否支持该链对
	RawInfo       map[string]interface{} `json:"rawInfo,omitempty"` // 不可用原因等，便于前端展示
}

