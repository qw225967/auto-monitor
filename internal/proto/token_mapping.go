package proto

// TokenMappingEntry 单条 Token 映射（用于 API 返回）
type TokenMappingEntry struct {
	Address string `json:"address"`
	Symbol  string `json:"symbol"`
	ChainId string `json:"chainId,omitempty"`
}

// TokenMappingManager 定义 Token 映射管理器的接口（按链存储 symbol -> address）
type TokenMappingManager interface {
	// GetAllMappings 获取所有映射关系（含链 ID）
	GetAllMappings() []TokenMappingEntry

	// AddMapping 添加或更新一条按链映射
	// chainId 为空时表示不区分链的旧格式
	AddMapping(contractAddress, symbol, chainId string) error

	// GetAddressBySymbol 根据代币符号和链 ID 获取合约地址
	GetAddressBySymbol(symbol, chainId string) (string, error)

	// RemoveMapping 删除映射；chainId 为空时删除该地址或 symbol 在所有链上的映射
	RemoveMapping(contractAddressOrSymbol, chainId string) error

	SaveToFile() error
	LoadFromFile() error
}
