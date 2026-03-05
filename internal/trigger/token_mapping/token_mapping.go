package token_mapping

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/utils/logger"
	"go.uber.org/zap"
)

// TokenMappingManager Token 合约地址和符号的按链映射管理器
// 存储 (symbol, chainId) -> contractAddress，支持同一 symbol 在不同链上有不同地址
type TokenMappingManager struct {
	// symbolChainToAddress: "symbol:chainId" -> 合约地址（symbol 大写）
	symbolChainToAddress map[string]string
	// addressChainToSymbol: "chainId:normalizedAddr" -> symbol，用于按地址反查
	addressChainToSymbol map[string]string
	mu                   sync.RWMutex
	filePath             string
	logger               *zap.SugaredLogger
}

var (
	tokenMappingManagerInstance *TokenMappingManager
	tokenMappingManagerOnce     sync.Once
)

func symbolChainKey(symbol, chainId string) string {
	return strings.ToUpper(strings.TrimSpace(symbol)) + ":" + strings.TrimSpace(chainId)
}

func addressChainKey(chainId, normalizedAddr string) string {
	return strings.TrimSpace(chainId) + ":" + normalizedAddr
}

// GetTokenMappingManager 获取 Token 映射管理器单例
func GetTokenMappingManager() *TokenMappingManager {
	tokenMappingManagerOnce.Do(func() {
		// 默认文件路径：项目根目录下的 data/token_mapping.json
		defaultPath := filepath.Join("data", "token_mapping.json")
		tokenMappingManagerInstance = &TokenMappingManager{
			symbolChainToAddress: make(map[string]string),
			addressChainToSymbol: make(map[string]string),
			filePath:             defaultPath,
			logger:               logger.GetLoggerInstance().Named("token_mapping").Sugar(),
		}
	})
	return tokenMappingManagerInstance
}

// SetFilePath 设置映射文件的存储路径（可选，用于自定义路径）
func (m *TokenMappingManager) SetFilePath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.filePath = path
}

// LoadFromFile 从文件加载映射关系到内存
// 支持带 chainId 的新格式与不带 chainId 的旧格式（旧格式 chainId 视为 ""）
func (m *TokenMappingManager) LoadFromFile() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
		m.logger.Infof("映射文件不存在，将创建新文件: %s", m.filePath)
		if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
			return fmt.Errorf("创建目录失败: %w", err)
		}
		m.symbolChainToAddress = make(map[string]string)
		m.addressChainToSymbol = make(map[string]string)
		return nil
	}

	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return fmt.Errorf("读取映射文件失败: %w", err)
	}

	if len(data) == 0 {
		m.symbolChainToAddress = make(map[string]string)
		m.addressChainToSymbol = make(map[string]string)
		return nil
	}

	var mappings []TokenMapping
	if err := json.Unmarshal(data, &mappings); err != nil {
		return fmt.Errorf("解析映射文件失败: %w", err)
	}

	m.symbolChainToAddress = make(map[string]string)
	m.addressChainToSymbol = make(map[string]string)
	for _, mapping := range mappings {
		chainId := strings.TrimSpace(mapping.ChainId)
		normalizedAddr := normalizeAddress(mapping.ContractAddress)
		symbol := strings.ToUpper(strings.TrimSpace(mapping.Symbol))
		if symbol == "" || normalizedAddr == "" {
			continue
		}
		key := symbolChainKey(symbol, chainId)
		m.symbolChainToAddress[key] = normalizedAddr
		m.addressChainToSymbol[addressChainKey(chainId, normalizedAddr)] = symbol
	}

	m.logger.Infof("成功加载 %d 条 Token 映射关系（按链）", len(mappings))
	return nil
}

// SaveToFile 将内存中的映射关系保存到文件
func (m *TokenMappingManager) SaveToFile() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	mappings := m.buildMappingsList()
	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化映射数据失败: %w", err)
	}

	if err := os.WriteFile(m.filePath, data, 0644); err != nil {
		return fmt.Errorf("写入映射文件失败: %w", err)
	}

	m.logger.Infof("成功保存 %d 条 Token 映射关系到文件: %s", len(mappings), m.filePath)
	return nil
}

func (m *TokenMappingManager) buildMappingsList() []TokenMapping {
	seen := make(map[string]bool)
	var list []TokenMapping
	for key, addr := range m.symbolChainToAddress {
		if seen[key] {
			continue
		}
		seen[key] = true
		parts := strings.SplitN(key, ":", 2)
		symbol := ""
		chainId := ""
		if len(parts) >= 1 {
			symbol = parts[0]
		}
		if len(parts) >= 2 {
			chainId = parts[1]
		}
		list = append(list, TokenMapping{
			ContractAddress: addr,
			Symbol:          symbol,
			ChainId:         chainId,
		})
	}
	return list
}

// AddMapping 添加或更新一条按链映射
// contractAddress: 代币合约地址
// symbol: 代币符号
// chainId: 链 ID（如 "1"、"56"），空字符串表示不区分链的旧格式
func (m *TokenMappingManager) AddMapping(contractAddress, symbol, chainId string) error {
	if contractAddress == "" || symbol == "" {
		return fmt.Errorf("合约地址和代币符号不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	chainId = strings.TrimSpace(chainId)
	normalizedAddr := normalizeAddress(contractAddress)
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	key := symbolChainKey(symbol, chainId)
	addrChainKey := addressChainKey(chainId, normalizedAddr)

	// 若该 symbol:chainId 已指向其他地址，删除旧的 addressChainToSymbol
	if oldAddr, exists := m.symbolChainToAddress[key]; exists && oldAddr != normalizedAddr {
		delete(m.addressChainToSymbol, addressChainKey(chainId, oldAddr))
	}
	// 若该 chainId:address 已指向其他 symbol，删除旧的 symbolChainToAddress
	if oldSymbol, exists := m.addressChainToSymbol[addrChainKey]; exists && oldSymbol != symbol {
		delete(m.symbolChainToAddress, symbolChainKey(oldSymbol, chainId))
	}

	m.symbolChainToAddress[key] = normalizedAddr
	m.addressChainToSymbol[addrChainKey] = symbol

	m.logger.Debugf("添加映射: %s @ chain %s <-> %s", symbol, chainId, normalizedAddr)
	return nil
}

// GetSymbolByAddress 根据合约地址和链 ID 获取代币符号
func (m *TokenMappingManager) GetSymbolByAddress(contractAddress, chainId string) (string, error) {
	if contractAddress == "" {
		return "", fmt.Errorf("合约地址不能为空")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	normalizedAddr := normalizeAddress(contractAddress)
	chainId = strings.TrimSpace(chainId)
	symbol, exists := m.addressChainToSymbol[addressChainKey(chainId, normalizedAddr)]
	if !exists {
		return "", fmt.Errorf("未找到合约地址对应的代币符号: %s (chain %s)", contractAddress, chainId)
	}
	return symbol, nil
}

// GetAddressBySymbol 根据代币符号和链 ID 获取合约地址
// 若指定 chainId 未找到，会尝试 chainId 为空的旧格式兜底（兼容历史数据）
func (m *TokenMappingManager) GetAddressBySymbol(symbol, chainId string) (string, error) {
	if symbol == "" {
		return "", fmt.Errorf("代币符号不能为空")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	chainId = strings.TrimSpace(chainId)

	// 先查精确 symbol:chainId
	if addr, exists := m.symbolChainToAddress[symbolChainKey(symbol, chainId)]; exists {
		return addr, nil
	}
	// 兜底：查旧格式 symbol:""（不区分链）
	if chainId != "" {
		if addr, exists := m.symbolChainToAddress[symbolChainKey(symbol, "")]; exists {
			return addr, nil
		}
	}

	return "", fmt.Errorf("未找到代币符号对应的合约地址: %s (chain %s)", symbol, chainId)
}

// RemoveMapping 删除映射
// contractAddressOrSymbol: 合约地址或代币符号
// chainId: 链 ID；若为空则删除该地址或该 symbol 在所有链上的映射
func (m *TokenMappingManager) RemoveMapping(contractAddressOrSymbol, chainId string) error {
	if contractAddressOrSymbol == "" {
		return fmt.Errorf("参数不能为空")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	contractAddressOrSymbol = strings.TrimSpace(contractAddressOrSymbol)
	chainId = strings.TrimSpace(chainId)
	normalizedAddr := normalizeAddress(contractAddressOrSymbol)

	// 按地址删除：chainId:addr
	if normalizedAddr != "" {
		if chainId != "" {
			key := addressChainKey(chainId, normalizedAddr)
			if symbol, exists := m.addressChainToSymbol[key]; exists {
				delete(m.addressChainToSymbol, key)
				delete(m.symbolChainToAddress, symbolChainKey(symbol, chainId))
				m.logger.Debugf("删除映射: %s @ chain %s <-> %s", symbol, chainId, normalizedAddr)
				return nil
			}
		} else {
			// 删除该地址在所有链上的映射
			var deleted bool
			for k, symbol := range m.addressChainToSymbol {
				if strings.HasSuffix(k, ":"+normalizedAddr) {
					parts := strings.SplitN(k, ":", 2)
					cid := ""
					if len(parts) == 2 {
						cid = parts[0]
					}
					delete(m.addressChainToSymbol, k)
					delete(m.symbolChainToAddress, symbolChainKey(symbol, cid))
					m.logger.Debugf("删除映射: %s @ chain %s <-> %s", symbol, cid, normalizedAddr)
					deleted = true
				}
			}
			if deleted {
				return nil
			}
		}
	}

	// 按符号删除
	symbol := strings.ToUpper(contractAddressOrSymbol)
	if chainId != "" {
		key := symbolChainKey(symbol, chainId)
		if addr, exists := m.symbolChainToAddress[key]; exists {
			delete(m.symbolChainToAddress, key)
			delete(m.addressChainToSymbol, addressChainKey(chainId, addr))
			m.logger.Debugf("删除映射: %s @ chain %s <-> %s", symbol, chainId, addr)
			return nil
		}
	} else {
		// 删除该 symbol 在所有链上的映射
		var deleted bool
		for k, addr := range m.symbolChainToAddress {
			if strings.HasPrefix(k, symbol+":") {
				parts := strings.SplitN(k, ":", 2)
				cid := ""
				if len(parts) == 2 {
					cid = parts[1]
				}
				delete(m.symbolChainToAddress, k)
				delete(m.addressChainToSymbol, addressChainKey(cid, addr))
				m.logger.Debugf("删除映射: %s @ chain %s <-> %s", symbol, cid, addr)
				deleted = true
			}
		}
		if deleted {
			return nil
		}
	}

	return fmt.Errorf("未找到映射关系: %s (chain %s)", contractAddressOrSymbol, chainId)
}

// GetAllMappings 返回所有映射（实现 proto.TokenMappingManager，用于 API 展示）
func (m *TokenMappingManager) GetAllMappings() []proto.TokenMappingEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := m.buildMappingsList()
	out := make([]proto.TokenMappingEntry, 0, len(list))
	for _, e := range list {
		out = append(out, proto.TokenMappingEntry{
			Address: e.ContractAddress,
			Symbol:  e.Symbol,
			ChainId: e.ChainId,
		})
	}
	return out
}

// TokenMapping 单条映射（用于 JSON 序列化与 API）
type TokenMapping struct {
	ContractAddress string `json:"contractAddress"`
	Symbol          string `json:"symbol"`
	ChainId         string `json:"chainId,omitempty"` // 链 ID，如 "1"、"56"；空表示旧格式不区分链
}

// normalizeAddress 标准化地址格式（统一转为小写，处理 0x 前缀）
func normalizeAddress(addr string) string {
	if len(addr) == 0 {
		return addr
	}
	addr = strings.TrimSpace(addr)
	addr = strings.ToLower(addr)
	if !strings.HasPrefix(addr, "0x") {
		addr = "0x" + addr
	}
	return addr
}
