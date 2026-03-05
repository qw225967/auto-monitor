package wormhole

// EVM chain ID -> Wormhole chain ID 映射
// 参考：https://docs.wormhole.com/docs/products/reference/chain-ids
var evmChainIDToWormholeChainID = map[string]uint16{
	"1":      2,  // Ethereum Mainnet
	"56":     4,  // BNB Smart Chain (BSC)
	"137":    5,  // Polygon
	"43114":  6,  // Avalanche
	"42161":  23, // Arbitrum
	"10":     24, // Optimism
	"8453":   30, // Base
	"250":    10, // Fantom
	"100":    25, // Gnosis
}

// 各链 Wormhole Token Bridge 合约地址（主网）
// 参考：https://docs.wormhole.com/docs/products/reference/contract-addresses
var tokenBridgeAddresses = map[string]string{
	"1":      "0x3ee18B2214AFF97000D974cf647E7C347E8fa585", // Ethereum
	"56":     "0x98f3c9e6E3fAce36bAAd05FE09d375Ef1464288B", // BSC
	"137":    "0x5a58505a96D1dbf8dF91cB21B54419FC36e93fdE", // Polygon
	"43114":  "0x0e082F06FF657D94310cB8cE8B0D9a04541d8052", // Avalanche
	"42161":  "0x0b2402144Bb366A632D14B83F244D2e0e21bD39c", // Arbitrum
	"10":     "0xB6F6D86a8f9879A9c87f643768d9efc38c1Da6E7", // Optimism
	"8453":   "0xb255F9E152e3aebcdc02E0B6b2564e1D90a6BD37", // Base
	"250":    "0x7C9Fc5741288cDFdD83CeB07f3e7E8892d002266", // Fantom
}

// GetWormholeChainID 根据 EVM chain ID 返回 Wormhole chain ID，不支持则返回 0
func GetWormholeChainID(evmChainID string) uint16 {
	if id, ok := evmChainIDToWormholeChainID[evmChainID]; ok {
		return id
	}
	return 0
}

// GetTokenBridgeAddress 返回指定链的 Token Bridge 合约地址
func GetTokenBridgeAddress(chainID string) string {
	if addr, ok := tokenBridgeAddresses[chainID]; ok {
		return addr
	}
	return ""
}
