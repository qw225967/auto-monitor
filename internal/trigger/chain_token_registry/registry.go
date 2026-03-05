package chain_token_registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"auto-arbitrage/internal/utils/logger"
	"go.uber.org/zap"
)

// TokenInfo 单条链上的 token 合约信息
type TokenInfo struct {
	Address   string    `json:"address"`
	Decimals  int       `json:"decimals"`
	Verified  bool      `json:"verified"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ChainTokenRegistry 管理 symbol → chainID → TokenInfo 的全链地址注册表
type ChainTokenRegistry struct {
	mu       sync.RWMutex
	registry map[string]map[string]*TokenInfo // symbol → chainID → TokenInfo
	filePath string
	logger   *zap.SugaredLogger
}

var (
	instance *ChainTokenRegistry
	once     sync.Once
)

func GetRegistry() *ChainTokenRegistry {
	once.Do(func() {
		instance = &ChainTokenRegistry{
			registry: make(map[string]map[string]*TokenInfo),
			filePath: filepath.Join("data", "chain_token_registry.json"),
			logger:   logger.GetLoggerInstance().Named("ChainTokenRegistry").Sugar(),
		}
	})
	return instance
}

func (r *ChainTokenRegistry) SetFilePath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filePath = path
}

// Get 获取指定 symbol 在指定链上的 TokenInfo
func (r *ChainTokenRegistry) Get(symbol, chainID string) (*TokenInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	symbol = strings.ToUpper(symbol)
	chains, ok := r.registry[symbol]
	if !ok {
		return nil, false
	}
	info, ok := chains[chainID]
	return info, ok && info != nil
}

// GetAllChains 获取指定 symbol 在所有链上的地址
func (r *ChainTokenRegistry) GetAllChains(symbol string) map[string]*TokenInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	symbol = strings.ToUpper(symbol)
	chains, ok := r.registry[symbol]
	if !ok {
		return nil
	}
	result := make(map[string]*TokenInfo, len(chains))
	for k, v := range chains {
		result[k] = v
	}
	return result
}

// GetVerifiedAddresses 获取指定 symbol 在所有链上的已验证地址（返回 chainID→address）
func (r *ChainTokenRegistry) GetVerifiedAddresses(symbol string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	symbol = strings.ToUpper(symbol)
	chains, ok := r.registry[symbol]
	if !ok {
		return nil
	}
	result := make(map[string]string)
	for chainID, info := range chains {
		if info != nil && info.Address != "" && info.Verified {
			result[chainID] = info.Address
		}
	}
	return result
}

// GetAllAddresses 获取指定 symbol 在所有链上的地址（含未验证，返回 chainID→address）
func (r *ChainTokenRegistry) GetAllAddresses(symbol string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	symbol = strings.ToUpper(symbol)
	chains, ok := r.registry[symbol]
	if !ok {
		return nil
	}
	result := make(map[string]string)
	for chainID, info := range chains {
		if info != nil && info.Address != "" {
			result[chainID] = info.Address
		}
	}
	return result
}

// Upsert 插入或更新一条记录。source 优先级：manual > exchange-api > bridge-verify > rpc-scan。
// 同源更新或更高优先级来源时覆盖，低优先级来源不覆盖已有高优先级数据。
func (r *ChainTokenRegistry) Upsert(symbol, chainID string, info *TokenInfo) {
	if info == nil || info.Address == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	symbol = strings.ToUpper(symbol)
	info.Address = strings.ToLower(info.Address)

	if r.registry[symbol] == nil {
		r.registry[symbol] = make(map[string]*TokenInfo)
	}

	existing := r.registry[symbol][chainID]
	if existing != nil && sourcePriority(existing.Source) > sourcePriority(info.Source) {
		return
	}

	if info.UpdatedAt.IsZero() {
		info.UpdatedAt = time.Now()
	}
	r.registry[symbol][chainID] = info
}

func sourcePriority(source string) int {
	switch source {
	case "manual":
		return 100
	case "exchange-api":
		return 80
	case "bridge-verify":
		return 60
	case "rpc-scan":
		return 40
	case "walletinfo":
		return 30
	default:
		return 0
	}
}

// LoadFromFile 从 JSON 文件加载注册表
func (r *ChainTokenRegistry) LoadFromFile() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	absPath, err := filepath.Abs(r.filePath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			r.logger.Infof("Registry file not found at %s, starting fresh", absPath)
			return nil
		}
		return fmt.Errorf("read file: %w", err)
	}

	var loaded map[string]map[string]*TokenInfo
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}

	for symbol, chains := range loaded {
		symbol = strings.ToUpper(symbol)
		if r.registry[symbol] == nil {
			r.registry[symbol] = make(map[string]*TokenInfo)
		}
		for chainID, info := range chains {
			if info != nil && info.Address != "" {
				info.Address = strings.ToLower(info.Address)
				r.registry[symbol][chainID] = info
			}
		}
	}

	r.logger.Infof("Loaded %d symbols from %s", len(loaded), absPath)
	return nil
}

// SaveToFile 持久化注册表到 JSON 文件
func (r *ChainTokenRegistry) SaveToFile() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	absPath, err := filepath.Abs(r.filePath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	data, err := json.MarshalIndent(r.registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

// SymbolCount 返回已注册的 symbol 数量
func (r *ChainTokenRegistry) SymbolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.registry)
}
