package tokenregistry

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type budgetState struct {
	Month     string         `json:"month"`      // YYYY-MM
	Used      int            `json:"used"`       // 当月总消耗
	ByDayUsed map[string]int `json:"by_day_used"` // YYYY-MM-DD -> used
	UpdatedAt string         `json:"updated_at"`
}

type BudgetSnapshot struct {
	Enabled       bool
	Month         string
	Used          int
	MonthlyLimit  int
	Remaining     int
	TodayUsed     int
	TodayCap      int
	RemainingDays int
}

type CoinGeckoBudget struct {
	mu           sync.Mutex
	path         string
	enabled      bool
	monthlyLimit int
	state        budgetState
}

var (
	budgetManagersMu sync.Mutex
	budgetManagers   = map[string]*CoinGeckoBudget{}
)

func GetCoinGeckoBudget(path string, enabled bool, monthlyLimit int) *CoinGeckoBudget {
	key := path
	if key == "" {
		key = "__memory__"
	}

	budgetManagersMu.Lock()
	defer budgetManagersMu.Unlock()
	if bm, ok := budgetManagers[key]; ok {
		bm.mu.Lock()
		bm.enabled = enabled
		bm.monthlyLimit = monthlyLimit
		bm.mu.Unlock()
		return bm
	}
	bm := &CoinGeckoBudget{
		path:         path,
		enabled:      enabled && monthlyLimit > 0,
		monthlyLimit: monthlyLimit,
	}
	_ = bm.load()
	budgetManagers[key] = bm
	return bm
}

func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }
func dayKey(t time.Time) string   { return t.UTC().Format("2006-01-02") }

func daysInMonth(t time.Time) int {
	utc := t.UTC()
	firstOfNext := time.Date(utc.Year(), utc.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	last := firstOfNext.Add(-24 * time.Hour)
	return last.Day()
}

func (b *CoinGeckoBudget) load() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path == "" {
		if b.state.ByDayUsed == nil {
			b.state.ByDayUsed = make(map[string]int)
		}
		return nil
	}
	data, err := os.ReadFile(b.path)
	if err != nil {
		if os.IsNotExist(err) {
			b.state.ByDayUsed = make(map[string]int)
			return nil
		}
		return err
	}
	var st budgetState
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	if st.ByDayUsed == nil {
		st.ByDayUsed = make(map[string]int)
	}
	b.state = st
	return nil
}

func (b *CoinGeckoBudget) saveLocked() error {
	if b.path == "" {
		return nil
	}
	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(b.path, data, 0644)
}

func (b *CoinGeckoBudget) resetIfNeededLocked(now time.Time) {
	m := monthKey(now)
	if b.state.Month == m {
		if b.state.ByDayUsed == nil {
			b.state.ByDayUsed = make(map[string]int)
		}
		return
	}
	b.state.Month = m
	b.state.Used = 0
	b.state.ByDayUsed = make(map[string]int)
	b.state.UpdatedAt = now.UTC().Format(time.RFC3339)
}

func (b *CoinGeckoBudget) snapshotLocked(now time.Time) BudgetSnapshot {
	dk := dayKey(now)
	remaining := b.monthlyLimit - b.state.Used
	if remaining < 0 {
		remaining = 0
	}
	remainingDays := daysInMonth(now) - now.UTC().Day() + 1
	if remainingDays <= 0 {
		remainingDays = 1
	}
	todayCap := 0
	if b.monthlyLimit > 0 {
		todayCap = int(math.Ceil(float64(remaining) / float64(remainingDays)))
	}
	return BudgetSnapshot{
		Enabled:       b.enabled && b.monthlyLimit > 0,
		Month:         b.state.Month,
		Used:          b.state.Used,
		MonthlyLimit:  b.monthlyLimit,
		Remaining:     remaining,
		TodayUsed:     b.state.ByDayUsed[dk],
		TodayCap:      todayCap,
		RemainingDays: remainingDays,
	}
}

// TryConsume 尝试消费预算。返回 allowed=false 时，上层应跳过该请求。
func (b *CoinGeckoBudget) TryConsume(cost int, now time.Time) (allowed bool, reason string, snap BudgetSnapshot, err error) {
	if cost <= 0 {
		cost = 1
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	b.resetIfNeededLocked(now)

	enabled := b.enabled && b.monthlyLimit > 0
	if !enabled {
		return true, "", b.snapshotLocked(now), nil
	}

	dk := dayKey(now)
	snap = b.snapshotLocked(now)

	if b.state.Used+cost > b.monthlyLimit {
		return false, "monthly_limit", snap, nil
	}
	// 最后一天允许把剩余额度用完，不做日节奏限流
	if snap.RemainingDays > 1 && snap.TodayCap > 0 && b.state.ByDayUsed[dk]+cost > snap.TodayCap {
		return false, "daily_pacing", snap, nil
	}

	b.state.Used += cost
	b.state.ByDayUsed[dk] += cost
	b.state.UpdatedAt = now.UTC().Format(time.RFC3339)
	if err := b.saveLocked(); err != nil {
		return false, "", snap, fmt.Errorf("save budget: %w", err)
	}
	return true, "", b.snapshotLocked(now), nil
}
