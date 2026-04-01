package opportunities

import (
	"log"
	"math"
	"sync"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

const (
	// WatchPoolSpreadMin/Max 监控池价差区间
	WatchPoolSpreadMin = -1.0
	WatchPoolSpreadMax = 1.0

	// WatchPoolDebugSize 调试环形缓冲区大小
	WatchPoolDebugSize = 10

	// WatchPoolMinHistory 触发异常检测所需的最少历史轮次
	WatchPoolMinHistory = 5

	// AnomalyStdDevK 异常检测的标准差倍数阈值（2σ）
	AnomalyStdDevK = 2.0

	// ActiveNormalRounds 连续正常多少轮后退出活跃状态并移入冷却
	ActiveNormalRounds = 10

	// WatchPoolNotSeenRounds 超过多少轮未出现则从监控池清理
	WatchPoolNotSeenRounds = 30
)

// WatchPool 监控池：维护 [-1%, 1%] 区间内 symbol 的 Welford 在线统计，
// 用于检测价差突变（2σ 异常）并管理冷却列表。
type WatchPool struct {
	mu      sync.Mutex
	entries map[string]*model.WatchPoolEntry
	cooling map[string]*model.CoolingEntry
}

// NewWatchPool 创建监控池
func NewWatchPool() *WatchPool {
	return &WatchPool{
		entries: make(map[string]*model.WatchPoolEntry),
		cooling: make(map[string]*model.CoolingEntry),
	}
}

// Update 更新监控池，返回本轮在监控池内的 SpreadItem 列表。
// 逻辑：
//  1. 构建本轮 symbol->item 映射（取价差绝对值最小的一条）
//  2. 处理本轮出现的 symbol（冷却回归、新加入、Welford 更新、活跃状态管理）
//  3. 处理本轮未出现的 symbol（MissedRounds 计数，超限移入冷却）
func (wp *WatchPool) Update(items []model.SpreadItem) []model.SpreadItem {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	now := time.Now()

	// Step A: 构建本轮 symbol->item 映射（只纳入区间内的 symbol）
	currentMap := make(map[string]model.SpreadItem)
	for _, item := range items {
		spread := item.SpreadPercent
		if spread < WatchPoolSpreadMin || spread > WatchPoolSpreadMax {
			continue
		}
		if existing, ok := currentMap[item.Symbol]; !ok {
			currentMap[item.Symbol] = item
		} else if math.Abs(spread) < math.Abs(existing.SpreadPercent) {
			currentMap[item.Symbol] = item
		}
	}

	// Step B: 处理本轮出现的每个 symbol
	for symbol, item := range currentMap {
		spread := item.SpreadPercent

		// 情况1: 在冷却列表中 → 重新加入监控池
		if _, inCooling := wp.cooling[symbol]; inCooling {
			delete(wp.cooling, symbol)
			wp.entries[symbol] = &model.WatchPoolEntry{
				Symbol:   symbol,
				LastSeen: now,
			}
			log.Printf("[WatchPool] %s 从冷却列表回归监控池", symbol)
		}

		// 情况2: 不在监控池中 → 新加入
		if _, inPool := wp.entries[symbol]; !inPool {
			wp.entries[symbol] = &model.WatchPoolEntry{
				Symbol:   symbol,
				LastSeen: now,
			}
		}

		// 情况3: 更新 Welford 统计
		entry := wp.entries[symbol]
		entry.LastSeen = now
		entry.LastSpread = spread
		entry.MissedRounds = 0

		entry.SpreadCount++
		delta := spread - entry.SpreadMean
		entry.SpreadMean += delta / float64(entry.SpreadCount)
		delta2 := spread - entry.SpreadMean
		entry.SpreadM2 += delta * delta2

		// 调试环形缓冲（最多保留 WatchPoolDebugSize 条）
		entry.SpreadDebug = append(entry.SpreadDebug, spread)
		if len(entry.SpreadDebug) > WatchPoolDebugSize {
			entry.SpreadDebug = entry.SpreadDebug[len(entry.SpreadDebug)-WatchPoolDebugSize:]
		}

		// 活跃状态管理：连续回归正常 ActiveNormalRounds 轮则移入冷却
		if entry.IsActive {
			if spread >= WatchPoolSpreadMin && spread <= WatchPoolSpreadMax {
				entry.NormalRounds++
			} else {
				entry.NormalRounds = 0
			}
			if entry.NormalRounds >= ActiveNormalRounds {
				duration := now.Sub(entry.ActiveSince)
				log.Printf("[WatchPool] %s 行情结束，移入冷却列表（持续%.0f秒）", symbol, duration.Seconds())
				wp.cooling[symbol] = &model.CoolingEntry{
					Symbol:     symbol,
					KickedAt:   now,
					LastSpread: entry.LastSpread,
					Reason:     "recovered",
				}
				delete(wp.entries, symbol)
			}
		}
	}

	// Step C: 处理本轮未出现的 symbol（MissedRounds 计数）
	for symbol, entry := range wp.entries {
		if _, seen := currentMap[symbol]; !seen {
			entry.MissedRounds++
			if entry.MissedRounds >= WatchPoolNotSeenRounds {
				log.Printf("[WatchPool] %s 连续%d轮未出现，移入冷却列表", symbol, entry.MissedRounds)
				wp.cooling[symbol] = &model.CoolingEntry{
					Symbol:     symbol,
					KickedAt:   time.Now(),
					LastSpread: entry.LastSpread,
					Reason:     "not_seen",
				}
				delete(wp.entries, symbol)
			}
		}
	}

	// 返回本轮在监控池内的 items（所有价差区间）
	var poolItems []model.SpreadItem
	for _, item := range items {
		if _, inPool := wp.entries[item.Symbol]; inPool {
			poolItems = append(poolItems, item)
		}
	}
	return poolItems
}

// DetectAnomalies 对监控池内的 items 做 2σ 突变检测。
// 用 entry.LastSpread（监控池记录的最新价差）做检测，而非传入的 item.SpreadPercent。
// 数据不足（< WatchPoolMinHistory）的 symbol 直接跳过（不放行）。
func (wp *WatchPool) DetectAnomalies(items []model.SpreadItem) []model.SpreadItem {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	itemMap := make(map[string]model.SpreadItem)
	for _, item := range items {
		itemMap[item.Symbol] = item
	}

	var anomalies []model.SpreadItem
	for symbol, entry := range wp.entries {
		item, ok := itemMap[symbol]
		if !ok {
			continue
		}

		// 数据积累期：历史不足，跳过（不放行）
		if entry.SpreadCount < WatchPoolMinHistory {
			continue
		}

		variance := entry.SpreadM2 / float64(entry.SpreadCount)
		stdDev := math.Sqrt(variance)

		// 标准差极小时跳过（价差几乎不变，无法判断突变）
		if stdDev < 1e-6 {
			continue
		}

		deviationSigma := math.Abs(entry.LastSpread-entry.SpreadMean) / stdDev
		if deviationSigma >= AnomalyStdDevK {
			if !entry.IsActive {
				entry.IsActive = true
				entry.ActiveSince = time.Now()
				entry.NormalRounds = 0
			}
			item.SpreadAnomaly = deviationSigma
			anomalies = append(anomalies, item)
			log.Printf("[WatchPool] %s 价差突变: 当前=%.2f%% 均值=%.2f%% 偏离=%.1fσ 近%d轮=%v",
				symbol, entry.LastSpread, entry.SpreadMean, deviationSigma,
				len(entry.SpreadDebug), entry.SpreadDebug)
		}
	}
	return anomalies
}

// GetCooling 返回当前冷却列表的快照
func (wp *WatchPool) GetCooling() []model.CoolingEntry {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	result := make([]model.CoolingEntry, 0, len(wp.cooling))
	for _, entry := range wp.cooling {
		result = append(result, *entry)
	}
	return result
}

// GetWatchPoolSize 返回当前监控池中的 symbol 数量
func (wp *WatchPool) GetWatchPoolSize() int {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	return len(wp.entries)
}

// KickToCooling 将 symbol 踢入冷却列表（由外部漏斗层调用）
func (wp *WatchPool) KickToCooling(symbol string, lastSpread float64, reason string) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	wp.cooling[symbol] = &model.CoolingEntry{
		Symbol:     symbol,
		KickedAt:   time.Now(),
		LastSpread: lastSpread,
		Reason:     reason,
	}
	delete(wp.entries, symbol)
}
