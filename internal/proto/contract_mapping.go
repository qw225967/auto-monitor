package proto

// ContractMapping 合约映射结构
type ContractMapping struct {
	Symbol      string `json:"symbol"`      // 如 "RAVEUSDT"
	TraderAType string `json:"traderAType"` // 如 "onchain:56"
	TraderBType string `json:"traderBType"` // 如 "binance:futures"
}

// ContractMappingManager 定义合约映射管理器的接口
// 这个接口定义了 ContractMappingManager 对外提供的所有方法
type ContractMappingManager interface {
	// GetAllMappings 获取所有映射关系
	GetAllMappings() []ContractMapping

	// GetMapping 根据 symbol 获取映射关系
	GetMapping(symbol string) (*ContractMapping, error)

	// AddMapping 添加映射关系
	AddMapping(symbol, traderAType, traderBType string) error

	// UpdateMapping 更新映射关系
	UpdateMapping(symbol, traderAType, traderBType string) error

	// RemoveMapping 删除映射关系
	RemoveMapping(symbol string) error

	// SaveToFile 将内存中的映射关系保存到文件
	SaveToFile() error

	// LoadFromFile 从文件加载映射关系到内存
	LoadFromFile() error
}
