// Package constants 提供交易所、链等通用常量（迁移自 auto-arbitrage）
package constants

// 交易所连接类型
const (
	ConnectTypeBinance     = "binance"
	ConnectTypeBybit       = "bybit"
	ConnectTypeBitget      = "bitget"
	ConnectTypeBitGet      = "bitget" // 别名，兼容 rest 等
	ConnectTypeGate        = "gate"
	ConnectTypeOKEX        = "okex"
	ConnectTypeHyperliquid = "hyperliquid"
	ConnectTypeLighter     = "lighter"
	ConnectTypeAster       = "aster"
	ConnectTypeBSC         = "bsc"
)

// 交易状态
const (
	TradeStatusInit      = "INIT"
	TradeStatusSwapping  = "SWAPPING"
	TradeStatusSuccess   = "SUCCESS"
	TradeStatusFailed    = "FAILED"
)

// Binance
const (
	BinanceRestBaseSpotUrl         = "https://api.binance.com"
	BinanceDepositAddressPath      = "/sapi/v1/capital/config/getall"
	BinanceWithdrawPath            = "/sapi/v1/capital/withdraw/apply"
	BinanceDepositHistoryPath      = "/sapi/v1/capital/deposit/hisrec"
	BinanceWithdrawHistoryPath     = "/sapi/v1/capital/withdraw/history"
	BinanceSpotOrderPath           = "/api/v3/order"
	BinanceCapitalConfigGetAll     = "/sapi/v1/capital/config/getall"
	BinanceRecvWindow              = 60000
)

// Bitget
const (
	BitgetRestBaseUrl           = "https://api.bitget.com"
	BitgetWsUrl                 = "wss://ws.bitget.com/v2/ws/public"
	BitgetSpotOrderPath         = "/api/v2/spot/trade/place-order"
	BitgetFuturesOrderPath      = "/api/v2/mix/order/place-order"
	BitgetFuturesOrderInfo      = "/api/v2/mix/order/orders-pending"
	BitgetSpotOrderInfo         = "/api/v2/spot/trade/order-info"
	BitgetFuturesAccount        = "/api/v2/mix/account/account"
	BitgetAccountBalancePath    = "/api/v2/spot/account/balances"
	BitgetPositionPath          = "/api/v2/mix/position/single-position"
	BitgetAllPositionsPath      = "/api/v2/mix/position/all-position"
	BitgetSetMarginModePath     = "/api/v2/mix/account/set-margin-mode"
	BitgetSetLeveragePath       = "/api/v2/mix/account/set-leverage"
	BitgetSpotOrderBookPath     = "/api/v2/spot/market/orderbook"
	BitgetFuturesOrderBookPath  = "/api/v2/mix/market/orderbook"
)

// Gate
const (
	GateRestBaseUrl = "https://api.gateio.ws/api/v4"
)

// Bybit
const (
	BybitRestBaseUrl           = "https://api.bybit.com"
	BybitDepositAddressPath    = "/v5/asset/deposit/query-address"
	BybitWithdrawPath          = "/v5/asset/withdraw/create"
	BybitDepositHistoryPath    = "/v5/asset/deposit/query-record"
)

// OKX
const (
	OkexBaseUrl                    = "https://www.okx.com"
	OkexWsPublicUrl                = "wss://ws.okx.com:8443/ws/v5/public"
	OkexPathAccountBalance         = "/api/v5/account/balance"
	OkexPathAccountSetLeverage     = "/api/v5/account/set-leverage"
	OkexPathAssetBalances          = "/api/v5/asset/balances"
	OkexPathAssetTransfer          = "/api/v5/asset/transfer"
	OkexPathTradeOrder             = "/api/v5/trade/order"
	OkexPathMarketBooks            = "/api/v5/market/books"
	OkexDexBaseUrl                 = "https://www.okx.com"
	OkexDexV6BaseUrl               = "https://web3.okx.com" // DEX v6 API（quote/swap 等）
	OkexDexSwap                    = "/api/v5/dex/swap"
	OkexDexTradePrice              = "/api/v6/dex/aggregator/quote" // v6 询价接口
	OkexDexAllTokenBalancesByAddress = "/api/v5/dex/balance"
	OkexDexApproveTransaction      = "/api/v5/dex/approve-transaction"
	OkexDexNonce                   = "/api/v5/dex/nonce"
	OkexDexBroadcastTransaction    = "/api/v5/dex/broadcast-transaction"
	OkexDexPostTransactionOrders   = "/api/v5/dex/order"
	OkexDexBgAccessKey             = "x-bg-access-key"
	OkexDexBgAccessSign            = "x-bg-access-sign"
	OkexDexBgAccessTimestamp       = "x-bg-access-timestamp"
	OkexDexBgAccessPassphrase      = "x-bg-access-passphrase"
	OkexDexContentType             = "Content-Type"
	OkexDexApplicationJson         = "application/json"
	OkexDexProject                 = "x-okex-dex-project"
	// web3.okx.com DEX API 使用 OK-ACCESS-* 标准 header
	OkexAccessKey       = "OK-ACCESS-KEY"
	OkexAccessSign      = "OK-ACCESS-SIGN"
	OkexAccessTimestamp = "OK-ACCESS-TIMESTAMP"
	OkexAccessPassphrase = "OK-ACCESS-PASSPHRASE"
)

// Hyperliquid
const (
	HyperliquidRestBaseUrl      = "https://api.hyperliquid.xyz"
	HyperliquidInfoPath         = "/info"
	HyperliquidExchangePath     = "/exchange"
	HyperliquidWsUrl            = "wss://api.hyperliquid.xyz/ws"
	HyperliquidQueryTypeClearinghouse = "clearinghouse"
	HyperliquidQueryTypeMeta    = "meta"
	HyperliquidActionOrder      = "order"
	HyperliquidActionUpdateLeverage = "updateLeverage"
	DefaultContractLeverage     = 10
)

// Lighter
const (
	LighterRestBaseUrl      = "https://api.lighter.xyz"
	LighterWsUrl            = "wss://api.lighter.xyz/ws"
	LighterAccountPath      = "/account"
	LighterAuthPath         = "/auth"
	LighterBalancesPath     = "/balances"
	LighterPositionsPath    = "/positions"
	LighterOrderPath        = "/order"
	LighterOrderBookPath      = "/orderbook"
	LighterAccountAPIKeysPath = "/account/keys"
)

// RPC URLs for chains (chainID -> primary URL)
var defaultRPCURLs = map[string]string{
	"1":     "https://eth.llamarpc.com",
	"56":    "https://bsc-dataseed.binance.org",
	"137":   "https://polygon-rpc.com",
	"42161": "https://arb1.arbitrum.io/rpc",
	"10":    "https://mainnet.optimism.io",
	"43114": "https://api.avax.network/ext/bc/C/rpc",
	"195":   "https://api.trongrid.io", // TRON
}

// ChainIDToChainIndex OKEx DEX 使用的 chainIndex，多数 EVM 链与 chainID 一致
var ChainIDToChainIndex = map[string]string{
	"1":     "1",     // Ethereum
	"56":    "56",    // BSC
	"137":   "137",   // Polygon
	"42161": "42161", // Arbitrum
	"10":    "10",    // Optimism
	"43114": "43114", // Avalanche
	"8453":  "8453",  // Base
	"324":   "324",   // zkSync Era
	"5000":  "5000",  // Mantle
	"59144": "59144", // Linea
	"534352": "534352", // Scroll
	"195":   "195",   // Tron
	"1101":  "1101",  // Polygon zkEVM
	"66":    "66",    // OKT Chain
	"250":   "250",   // Fantom
}

// OkxSupportedChainIndex OKX DEX 聚合器支持的 chainIndex，不在此列表的链会报 51000
var OkxSupportedChainIndex = map[string]bool{
	"1": true, "10": true, "56": true, "137": true, "195": true,
	"324": true, "250": true, "42161": true, "43114": true,
	"5000": true, "8453": true, "59144": true, "534352": true,
	"66": true, "1101": true, "169": true, "1088": true,
}

// OKXChainSupported 检查 chainID 是否被 OKX DEX 支持
func OKXChainSupported(chainID string) bool {
	idx := GetChainIndex(chainID)
	return OkxSupportedChainIndex[idx]
}

// GetChainIndex 获取 OKEx DEX chainIndex，无映射时返回 chainID 本身
func GetChainIndex(chainID string) string {
	if idx, ok := ChainIDToChainIndex[chainID]; ok {
		return idx
	}
	return chainID
}

// GetDefaultRPCURL 获取链的默认 RPC URL
func GetDefaultRPCURL(chainID string) string {
	if u, ok := defaultRPCURLs[chainID]; ok {
		return u
	}
	return ""
}

// GetDefaultRPCURLs 获取链的备用 RPC URL 列表
func GetDefaultRPCURLs(chainID string) []string {
	u := GetDefaultRPCURL(chainID)
	if u == "" {
		return nil
	}
	return []string{u}
}

// GetAllDefaultRPCURLs 返回所有链的 RPC 配置（用于测试）
func GetAllDefaultRPCURLs() map[string][]string {
	out := make(map[string][]string)
	for cid := range defaultRPCURLs {
		out[cid] = GetDefaultRPCURLs(cid)
	}
	return out
}

// HTTP
const (
	HttpMethodGet  = "GET"
	HttpMethodPost = "POST"
)
