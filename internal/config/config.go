package config

import (
	"sync"

	"auto-arbitrage/internal/model"
)

// MyLocalMysql MySQL连接字符串
const MyLocalMysql = "root:123456@tcp(127.0.0.1:3306)/taolidb?charset=utf8mb4&parseTime=True&loc=Local"

// OkEx API 密钥（用于 swap 操作，固定硬编码，不在 Web API 中暴露）
const (
	OkExSecret     = "1969798334B8317CE5C64A538341A5A6"
	OkExAPIKey     = "09609bdd-16bb-465b-8a09-70382baa555f"
	OkExPassphrase = "WsmFrq123@"
)

// 套利系统配置
const (
	// DefaultTargetThresholdInterval 默认目标价差阈值区间
	// 表示 AB 阈值和 BA 阈值之和的目标值，用于最优阈值计算
	DefaultTargetThresholdInterval = 0.5
)

var (
	// 缓存 swap 和 broadcast 的 API Key 列表
	swapKeysCache      []model.OkexKeyRecord
	broadcastKeysCache []model.OkexKeyRecord
	keysCacheMutex     sync.RWMutex
	keysInitialized    bool
)

// GetOkexKeyMapForSwap 获取 OKEx API Key 列表（用于 Swap，包含所有 API）
// 线程安全，使用缓存
func GetOkexKeyMapForSwap() []model.OkexKeyRecord {
	keysCacheMutex.RLock()
	if keysInitialized && len(swapKeysCache) > 0 {
		// 返回缓存的副本，避免外部修改
		result := make([]model.OkexKeyRecord, len(swapKeysCache))
		copy(result, swapKeysCache)
		keysCacheMutex.RUnlock()
		return result
	}
	keysCacheMutex.RUnlock()

	// 需要初始化，获取写锁
	keysCacheMutex.Lock()
	defer keysCacheMutex.Unlock()

	// 双重检查，避免并发初始化
	if keysInitialized && len(swapKeysCache) > 0 {
		result := make([]model.OkexKeyRecord, len(swapKeysCache))
		copy(result, swapKeysCache)
		return result
	}

	// 初始化所有 API Keys（使用硬编码的常量）
	var allKeys []model.OkexKeyRecord
	recordMy := model.OkexKeyRecord{
		Index:        0,
		CanBroadcast: true,
		AppKey:       OkExAPIKey,
		SecretKey:    OkExSecret,
		Passphrase:   OkExPassphrase,
	}
	allKeys = append(allKeys, recordMy)
	
	// 如果配置文件中还有额外的密钥，也添加进去
	globalCfg := GetGlobalConfig()
	if globalCfg != nil && len(globalCfg.OkEx.KeyList) > 0 {
		for _, keyRecord := range globalCfg.OkEx.KeyList {
			// 跳过空密钥
			if keyRecord.APIKey == "" || keyRecord.Secret == "" {
				continue
			}
			record := model.OkexKeyRecord{
				Index:        len(allKeys),
				CanBroadcast: keyRecord.CanBroadcast,
				AppKey:       keyRecord.APIKey,
				SecretKey:    keyRecord.Secret,
				Passphrase:   keyRecord.Passphrase,
			}
			allKeys = append(allKeys, record)
		}
	}

	// 缓存所有 keys（用于 swap）
	swapKeysCache = make([]model.OkexKeyRecord, len(allKeys))
	copy(swapKeysCache, allKeys)

	// 初始化可广播的 keys
	var broadcastKeys []model.OkexKeyRecord
	for _, key := range allKeys {
		if key.CanBroadcast {
			broadcastKeys = append(broadcastKeys, key)
		}
	}
	broadcastKeysCache = make([]model.OkexKeyRecord, len(broadcastKeys))
	copy(broadcastKeysCache, broadcastKeys)

	keysInitialized = true

	// 返回副本
	result := make([]model.OkexKeyRecord, len(swapKeysCache))
	copy(result, swapKeysCache)
	return result
}

// GetOkexKeyMapForBroadcast 获取 OKEx API Key 列表（用于广播，只包含可广播的 API）
// 线程安全，使用缓存
func GetOkexKeyMapForBroadcast() []model.OkexKeyRecord {
	keysCacheMutex.RLock()
	if keysInitialized && len(broadcastKeysCache) > 0 {
		// 返回缓存的副本，避免外部修改
		result := make([]model.OkexKeyRecord, len(broadcastKeysCache))
		copy(result, broadcastKeysCache)
		keysCacheMutex.RUnlock()
		return result
	}
	keysCacheMutex.RUnlock()

	// 需要初始化，先获取所有 keys（这会触发初始化）
	_ = GetOkexKeyMapForSwap()

	// 再次读取缓存
	keysCacheMutex.RLock()
	defer keysCacheMutex.RUnlock()

	result := make([]model.OkexKeyRecord, len(broadcastKeysCache))
	copy(result, broadcastKeysCache)
	return result
}

// GetOkexKeyMap 获取 OKEx API Key 列表（兼容旧接口）
// 注意：建议使用 GetOkexKeyMapForSwap() 或 GetOkexKeyMapForBroadcast()
func GetOkexKeyMap() []model.OkexKeyRecord {
	return GetOkexKeyMapForSwap()
}

// ClearOkexKeyCache 清除 OKEx 密钥缓存，强制下次获取时重新从 GlobalConfig 读取
func ClearOkexKeyCache() {
	keysCacheMutex.Lock()
	defer keysCacheMutex.Unlock()
	
	swapKeysCache = nil
	broadcastKeysCache = nil
	keysInitialized = false
}
