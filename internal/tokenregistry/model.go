package tokenregistry

// TokenChainInfo 某资产在某链上的 token 信息
type TokenChainInfo struct {
	Address   string `json:"address"`   // 合约地址，原生币可为空
	Decimals  int    `json:"decimals"`   // 精度
	Symbol    string `json:"symbol"`    // 链上符号
	UpdatedAt string `json:"updated_at"` // 更新时间，用于增量判断
}

// RegistryData 持久化存储结构：asset -> chainID -> TokenChainInfo
type RegistryData struct {
	Assets map[string]map[string]TokenChainInfo `json:"assets"`
}

// TokenInfo 查询用：资产+链的 token 信息
type TokenInfo struct {
	Asset    string
	ChainID  string
	Address  string
	Decimals int
	Symbol   string
}
