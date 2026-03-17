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
		// 链级增量：地址/精度变化时更新；即使未变化也刷新 UpdatedAt，避免 TTL 到期后重复全量请求
		info := TokenChainInfo{
			Address:                ti.Address,
			Decimals:               ti.Decimals,
			Symbol:                 ti.Symbol,
			UpdatedAt:              now,
			ReserveUSD:             existing.ReserveUSD,
			LiquidityNegativeUntil: existing.LiquidityNegativeUntil,
			LiquidityNegativeReason: existing.LiquidityNegativeReason,
		}
		if existing.Address != info.Address ||
			existing.Decimals != info.Decimals ||
			existing.Symbol != info.Symbol ||
			existing.UpdatedAt != info.UpdatedAt {
			rd.Assets[asset][chainID] = info
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

// NeedRefreshAssetByTTL 资产级刷新判断：
// - 本地不存在该资产：需要刷新
// - 任一链记录缺失/过期：需要刷新
// - 全部链都在 TTL 内：不刷新
func NeedRefreshAssetByTTL(rd *RegistryData, asset string, ttl time.Duration, now time.Time) bool {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	chains := rd.Assets[asset]
	if len(chains) == 0 {
		return true
	}
	if ttl <= 0 {
		return true
	}
	for _, info := range chains {
		ts := strings.TrimSpace(info.UpdatedAt)
		if ts == "" {
			return true
		}
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return true
		}
		if now.Sub(t) >= ttl {
			return true
		}
	}
	return false
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
