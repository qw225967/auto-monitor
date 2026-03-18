package tokenregistry

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBudgetTryConsumeMonthlyLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	b := GetCoinGeckoBudget(path, true, 2)
	// 选择当月最后一天，避免日节奏限制干扰月度上限验证
	now := time.Date(2026, 3, 31, 10, 0, 0, 0, time.UTC)

	ok, _, _, err := b.TryConsume(1, now)
	if err != nil || !ok {
		t.Fatalf("first consume should pass, err=%v ok=%v", err, ok)
	}
	ok, _, _, err = b.TryConsume(1, now)
	if err != nil || !ok {
		t.Fatalf("second consume should pass, err=%v ok=%v", err, ok)
	}
	ok, reason, _, err := b.TryConsume(1, now)
	if err != nil {
		t.Fatalf("third consume unexpected err: %v", err)
	}
	if ok || reason != "monthly_limit" {
		t.Fatalf("expected monthly_limit deny, got ok=%v reason=%q", ok, reason)
	}
}

func TestBudgetTryConsumeDailyPacing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	b := GetCoinGeckoBudget(path, true, 31) // 31 天，每天约 1
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	ok, _, snap, err := b.TryConsume(1, now)
	if err != nil || !ok {
		t.Fatalf("first consume should pass, err=%v ok=%v", err, ok)
	}
	if snap.TodayCap < 1 {
		t.Fatalf("unexpected today cap: %d", snap.TodayCap)
	}

	ok, reason, _, err := b.TryConsume(1, now)
	if err != nil {
		t.Fatalf("second consume unexpected err: %v", err)
	}
	if ok || reason != "daily_pacing" {
		t.Fatalf("expected daily_pacing deny, got ok=%v reason=%q", ok, reason)
	}
}

func TestBudgetResetOnNewMonth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	b := GetCoinGeckoBudget(path, true, 100)

	ok, _, _, err := b.TryConsume(10, time.Date(2026, 3, 31, 23, 0, 0, 0, time.UTC))
	if err != nil || !ok {
		t.Fatalf("march consume should pass, err=%v ok=%v", err, ok)
	}
	ok, _, snap, err := b.TryConsume(1, time.Date(2026, 4, 1, 0, 10, 0, 0, time.UTC))
	if err != nil || !ok {
		t.Fatalf("april consume should pass, err=%v ok=%v", err, ok)
	}
	if snap.Month != "2026-04" || snap.Used != 1 {
		t.Fatalf("expected month reset, got month=%s used=%d", snap.Month, snap.Used)
	}
}
