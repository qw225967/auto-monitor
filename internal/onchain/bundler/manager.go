package bundler

import (
	"fmt"
	"sync"
)

// Manager bundler 管理器
type Manager struct {
	bundlers []Bundler
	mu       sync.RWMutex
}

// NewManager 创建新的 bundler 管理器
func NewManager() *Manager {
	return &Manager{
		bundlers: make([]Bundler, 0),
	}
}

// AddBundler 添加 bundler
func (m *Manager) AddBundler(bundler Bundler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bundlers = append(m.bundlers, bundler)
}

// GetBundler 根据链 ID 获取支持的 bundler
func (m *Manager) GetBundler(chainID string) (Bundler, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, bundler := range m.bundlers {
		if bundler.SupportsChain(chainID) {
			return bundler, nil
		}
	}

	return nil, fmt.Errorf("no bundler supports chain %s", chainID)
}

// GetAllBundlers 获取所有 bundler
func (m *Manager) GetAllBundlers() []Bundler {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]Bundler, len(m.bundlers))
	copy(result, m.bundlers)
	return result
}

// SendBundleToAll 向所有支持的 bundler 发送 bundle
func (m *Manager) SendBundleToAll(signedTx string, chainID string) (map[string]string, []error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make(map[string]string)
	errors := make([]error, 0)

	for _, bundler := range m.bundlers {
		if bundler.SupportsChain(chainID) {
			if hash, err := bundler.SendBundle(signedTx, chainID); err != nil {
				errors = append(errors, fmt.Errorf("%s: %w", bundler.GetName(), err))
			} else {
				results[bundler.GetName()] = hash
			}
		}
	}

	return results, errors
}

