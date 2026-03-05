package contract_mapping

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/utils/logger"
	"go.uber.org/zap"
)

// ContractMappingManager 合约映射管理器
type ContractMappingManager struct {
	// symbolToMapping: symbol -> ContractMapping
	symbolToMapping map[string]*proto.ContractMapping
	mu              sync.RWMutex
	filePath        string
	logger          *zap.SugaredLogger
}

var (
	contractMappingManagerInstance *ContractMappingManager
	contractMappingManagerOnce     sync.Once
)

// GetContractMappingManager 获取合约映射管理器单例
func GetContractMappingManager() *ContractMappingManager {
	contractMappingManagerOnce.Do(func() {
		// 默认文件路径：项目根目录下的 data/contract_mapping.json
		defaultPath := filepath.Join("data", "contract_mapping.json")
		contractMappingManagerInstance = &ContractMappingManager{
			symbolToMapping: make(map[string]*proto.ContractMapping),
			filePath:        defaultPath,
			logger:          logger.GetLoggerInstance().Named("contract_mapping").Sugar(),
		}
		// 自动加载文件
		if err := contractMappingManagerInstance.LoadFromFile(); err != nil {
			contractMappingManagerInstance.logger.Warnf("加载合约映射文件失败: %v", err)
		}
	})
	return contractMappingManagerInstance
}

// SetFilePath 设置映射文件的存储路径（可选，用于自定义路径）
func (m *ContractMappingManager) SetFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filePath = path
}

// LoadFromFile 从文件加载映射关系到内存
func (m *ContractMappingManager) LoadFromFile() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果文件不存在，创建空映射
	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		m.logger.Infof("映射文件不存在，将创建新文件: %s", m.filePath)
		// 确保目录存在
		if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		// 初始化空映射
		m.symbolToMapping = make(map[string]*proto.ContractMapping)
		return nil
	}

	// 读取文件内容
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return fmt.Errorf("读取映射文件失败: %w", err)
	}

	// 如果文件为空，初始化空映射
	if len(data) == 0 {
		m.symbolToMapping = make(map[string]*proto.ContractMapping)
		return nil
	}

	// 解析 JSON
	var mappings []proto.ContractMapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return fmt.Errorf("解析映射文件失败: %w", err)
	}

	// 加载到内存
	m.symbolToMapping = make(map[string]*proto.ContractMapping)
	for i := range mappings {
		mapping := &mappings[i]
		m.symbolToMapping[mapping.Symbol] = mapping
	}

	m.logger.Infof("成功加载 %d 个合约映射关系", len(mappings))
	return nil
}

// SaveToFile 将内存中的映射关系保存到文件
func (m *ContractMappingManager) SaveToFile() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 构建映射列表
	mappings := make([]proto.ContractMapping, 0, len(m.symbolToMapping))
	for _, mapping := range m.symbolToMapping {
		mappings = append(mappings, *mapping)
	}

	// 序列化为 JSON（格式化输出）
	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化映射数据失败: %w", err)
	}

	// 写入文件
	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("写入映射文件失败: %w", err)
	}

	m.logger.Infof("成功保存 %d 个合约映射关系到文件: %s", len(mappings), m.filePath)
	return nil
}

// GetAllMappings 获取所有映射关系
func (m *ContractMappingManager) GetAllMappings() []proto.ContractMapping {
	m.mu.RLock()
	defer m.mu.RUnlock()

	mappings := make([]proto.ContractMapping, 0, len(m.symbolToMapping))
	for _, mapping := range m.symbolToMapping {
		mappings = append(mappings, *mapping)
	}
	return mappings
}

// GetMapping 根据 symbol 获取映射关系
func (m *ContractMappingManager) GetMapping(symbol string) (*proto.ContractMapping, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol 不能为空")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	mapping, exists := m.symbolToMapping[symbol]
	if !exists {
		return nil, fmt.Errorf("未找到 symbol 对应的映射: %s", symbol)
	}

	// 返回副本，避免外部修改
	result := *mapping
	return &result, nil
}

// AddMapping 添加映射关系
func (m *ContractMappingManager) AddMapping(symbol, traderAType, traderBType string) error {
	if symbol == "" {
		return fmt.Errorf("symbol 不能为空")
	}
	if traderAType == "" {
		return fmt.Errorf("traderAType 不能为空")
	}
	if traderBType == "" {
		return fmt.Errorf("traderBType 不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	mapping := &proto.ContractMapping{
		Symbol:      symbol,
		TraderAType: traderAType,
		TraderBType: traderBType,
	}

	// 如果已存在，则更新
	if existing, exists := m.symbolToMapping[symbol]; exists {
		m.logger.Debugf("更新映射: %s (A: %s -> %s, B: %s -> %s)",
			symbol, existing.TraderAType, traderAType, existing.TraderBType, traderBType)
	} else {
		m.logger.Debugf("添加映射: %s (A: %s, B: %s)", symbol, traderAType, traderBType)
	}

	m.symbolToMapping[symbol] = mapping
	return nil
}

// UpdateMapping 更新映射关系
func (m *ContractMappingManager) UpdateMapping(symbol, traderAType, traderBType string) error {
	// UpdateMapping 和 AddMapping 逻辑相同，都支持添加和更新
	return m.AddMapping(symbol, traderAType, traderBType)
}

// RemoveMapping 删除映射关系
func (m *ContractMappingManager) RemoveMapping(symbol string) error {
	if symbol == "" {
		return fmt.Errorf("symbol 不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.symbolToMapping[symbol]; !exists {
		return fmt.Errorf("未找到映射关系: %s", symbol)
	}

	delete(m.symbolToMapping, symbol)
	m.logger.Debugf("删除映射: %s", symbol)
	return nil
}
