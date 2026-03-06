package tokenregistry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Storage JSON 文件存储，支持增量合并
type Storage struct {
	path string
}

// NewStorage 创建存储，path 为 JSON 文件路径（如 data/token_registry.json）
func NewStorage(path string) *Storage {
	return &Storage{path: path}
}

// Load 加载已有数据，文件不存在时返回空结构
func (s *Storage) Load() (*RegistryData, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RegistryData{Assets: make(map[string]map[string]TokenChainInfo)}, nil
		}
		return nil, fmt.Errorf("read registry: %w", err)
	}
	var rd RegistryData
	if err := json.Unmarshal(data, &rd); err != nil {
		return nil, fmt.Errorf("parse registry: %w", err)
	}
	if rd.Assets == nil {
		rd.Assets = make(map[string]map[string]TokenChainInfo)
	}
	return &rd, nil
}

// Save 保存到文件，自动创建目录
func (s *Storage) Save(rd *RegistryData) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(rd, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// MergeIncremental 增量合并：只添加/更新新数据，不覆盖已有且未变更的
// newInfos: 从外部获取的 TokenInfo 列表
// 返回：本次新增或更新的条目数
func (s *Storage) MergeIncremental(rd *RegistryData, newInfos []TokenInfo) int {
	now := time.Now().Format(time.RFC3339)
	updated := 0
	for _, ti := range newInfos {
		asset := strings.ToUpper(strings.TrimSpace(ti.Asset))
		chainID := strings.TrimSpace(ti.ChainID)
		if asset == "" || chainID == "" {
			continue
		}
		if rd.Assets[asset] == nil {
			rd.Assets[asset] = make(map[string]TokenChainInfo)
		}
		existing := rd.Assets[asset][chainID]
		// 增量：仅当地址或精度变化时更新
		if existing.Address != ti.Address || existing.Decimals != ti.Decimals {
			rd.Assets[asset][chainID] = TokenChainInfo{
				Address:   ti.Address,
				Decimals:  ti.Decimals,
				Symbol:    ti.Symbol,
				UpdatedAt: now,
			}
			updated++
		}
	}
	return updated
}

// HasAsset 本地是否已保存该资产的 token 信息（任意链）
func HasAsset(rd *RegistryData, asset string) bool {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	if rd.Assets[asset] == nil {
		return false
	}
	return len(rd.Assets[asset]) > 0
}

// GetTokenInfo 查询某资产在某链上的信息
func (s *Storage) GetTokenInfo(rd *RegistryData, asset, chainID string) (TokenChainInfo, bool) {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	chainID = strings.TrimSpace(chainID)
	if rd.Assets[asset] == nil {
		return TokenChainInfo{}, false
	}
	info, ok := rd.Assets[asset][chainID]
	return info, ok
}
