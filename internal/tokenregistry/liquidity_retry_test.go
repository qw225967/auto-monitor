package tokenregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShouldRetryLiquidityError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"429", errString("status 429"), true},
		{"500", errString("status 500"), true},
		{"timeout", errString("i/o timeout"), true},
		{"not found", errString("status 404"), false},
		{"canceled", context.Canceled, false},
	}
	for _, tc := range cases {
		if got := shouldRetryLiquidityError(tc.err); got != tc.want {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

func TestCalcBackoffDelayRange(t *testing.T) {
	base := 100 * time.Millisecond
	max := 800 * time.Millisecond
	d0 := calcBackoffDelay(0, base, max, 0)
	d1 := calcBackoffDelay(1, base, max, 0)
	d2 := calcBackoffDelay(2, base, max, 0)
	d10 := calcBackoffDelay(10, base, max, 0)
	if d0 != 100*time.Millisecond || d1 != 200*time.Millisecond || d2 != 400*time.Millisecond {
		t.Fatalf("unexpected backoff sequence: %v %v %v", d0, d1, d2)
	}
	if d10 != max {
		t.Fatalf("expected capped backoff %v, got %v", max, d10)
	}
}

func TestFetchReserveWithRetrySucceedsAfter429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"attributes":{"reserve_usd":"123.45"}}]}`))
	}))
	defer srv.Close()

	f := NewLiquidityFetcher("", false)
	f.baseURL = srv.URL

	reserve, err := fetchReserveWithRetry(context.Background(), f, "1", "0x123", LiquiditySyncConfig{
		MaxRetries:    2,
		BackoffBase:   1 * time.Millisecond,
		BackoffMax:    5 * time.Millisecond,
		BackoffJitter: 0,
	})
	if err != nil {
		t.Fatalf("expected retry success, got error: %v", err)
	}
	if reserve <= 0 {
		t.Fatalf("expected positive reserve, got %v", reserve)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 calls, got %d", calls)
	}
}

func TestShouldSkipByNegativeCache(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	info := TokenChainInfo{
		LiquidityNegativeUntil: now.Add(1 * time.Hour).Format(time.RFC3339),
	}
	if !shouldSkipByNegativeCache(info, now) {
		t.Fatalf("expected skip when negative cache still valid")
	}
	info.LiquidityNegativeUntil = now.Add(-1 * time.Hour).Format(time.RFC3339)
	if shouldSkipByNegativeCache(info, now) {
		t.Fatalf("expected no skip when negative cache expired")
	}
}

func TestClassifyNegativeReason(t *testing.T) {
	if reason, ok := classifyNegativeReason(errString("status 404"), 0); !ok || reason != "status_404" {
		t.Fatalf("expected status_404 cacheable, got reason=%q ok=%v", reason, ok)
	}
	if reason, ok := classifyNegativeReason(nil, 0); !ok || reason != "no_pool" {
		t.Fatalf("expected no_pool cacheable, got reason=%q ok=%v", reason, ok)
	}
	if reason, ok := classifyNegativeReason(errString("status 500"), 0); ok || reason != "" {
		t.Fatalf("expected non-cacheable for status 500, got reason=%q ok=%v", reason, ok)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
