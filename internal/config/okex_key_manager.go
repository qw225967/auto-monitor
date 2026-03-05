package config

import (
	"fmt"
	"sync"

	"auto-arbitrage/internal/model"
)

// OkexKeyManager OKEx API Key 管理器（单例模式）
type OkexKeyManager struct {
	swapKeys       []model.OkexKeyRecord
	broadcastKeys  []model.OkexKeyRecord
	swapIndex      int
	broadcastIndex int
	mu             sync.RWMutex // 保护并发访问
}

var (
	okexKeyManagerInstance *OkexKeyManager
	okexKeyManagerOnce     sync.Once
)

// GetOkexKeyManager 获取 OKEx Key 管理器单例
func GetOkexKeyManager() *OkexKeyManager {
	okexKeyManagerOnce.Do(func() {
		okexKeyManagerInstance = &OkexKeyManager{
			swapKeys:       make([]model.OkexKeyRecord, 0),
			broadcastKeys:  make([]model.OkexKeyRecord, 0),
			swapIndex:      0,
			broadcastIndex: 0,
		}
	})
	return okexKeyManagerInstance
}

// Init 初始化 API Keys（需要在启动时调用）
func (m *OkexKeyManager) Init() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 获取所有 keys（用于 swap）
	swapKeys := GetOkexKeyMapForSwap()
	if len(swapKeys) == 0 {
		return fmt.Errorf("no swap API keys available")
	}

	// 获取可广播的 keys
	broadcastKeys := GetOkexKeyMapForBroadcast()
	if len(broadcastKeys) == 0 {
		return fmt.Errorf("no broadcastable API keys available")
	}

	m.swapKeys = swapKeys
	m.broadcastKeys = broadcastKeys
	m.swapIndex = 0
	m.broadcastIndex = 0
	return nil
}

// GetNextAppKey 获取下一个 API Key（线程安全，循环获取）
// canBroadcast: 是否只获取可广播交易的 Key
func (m *OkexKeyManager) GetNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if canBroadcast {
		return m.getNextBroadcastableKey()
	}

	// 普通循环获取（使用所有 keys）
	if len(m.swapKeys) == 0 {
		return model.OkexKeyRecord{}, fmt.Errorf("no swap app keys available")
	}

	if m.swapIndex >= len(m.swapKeys) {
		m.swapIndex = 0
	}

	record := m.swapKeys[m.swapIndex]
	m.swapIndex++
	return record, nil
}

// getNextBroadcastableKey 获取下一个可广播的 Key（内部方法）
func (m *OkexKeyManager) getNextBroadcastableKey() (model.OkexKeyRecord, error) {
	if len(m.broadcastKeys) == 0 {
		return model.OkexKeyRecord{}, fmt.Errorf("no broadcastable app keys available")
	}

	startIndex := m.broadcastIndex
	// 从当前位置开始查找
	for i := startIndex; i < len(m.broadcastKeys); i++ {
		if m.broadcastKeys[i].CanBroadcast {
			m.broadcastIndex = i + 1
			return m.broadcastKeys[i], nil
		}
	}
	// 如果没找到，从头开始查找
	for i := 0; i < startIndex; i++ {
		if m.broadcastKeys[i].CanBroadcast {
			m.broadcastIndex = i + 1
			return m.broadcastKeys[i], nil
		}
	}
	return model.OkexKeyRecord{}, fmt.Errorf("no broadcastable app key found")
}

// GetKeyCount 获取 Swap Key 总数
func (m *OkexKeyManager) GetKeyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.swapKeys)
}

// GetBroadcastableKeyCount 获取可广播 Key 的数量
func (m *OkexKeyManager) GetBroadcastableKeyCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.broadcastKeys)
}

// GetKeyByIndex 根据索引获取 API Key（线程安全）
func (m *OkexKeyManager) GetKeyByIndex(index int) (model.OkexKeyRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if index < 0 || index >= len(m.swapKeys) {
		return model.OkexKeyRecord{}, fmt.Errorf("invalid key index: %d (available: 0-%d)", index, len(m.swapKeys)-1)
	}

	return m.swapKeys[index], nil
}

// Reinit 重新初始化 API Keys（用于配置更新后）
func (m *OkexKeyManager) Reinit() error {
	// 清除缓存，强制重新从 GlobalConfig 读取
	ClearOkexKeyCache()
	
	// 重新初始化
	return m.Init()
}
