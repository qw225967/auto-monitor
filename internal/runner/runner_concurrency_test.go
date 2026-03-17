package runner

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

type fakeDetector struct {
	active    int32
	maxActive int32
	sleep     time.Duration
}

func (f *fakeDetector) DetectRoutes(ctx context.Context, symbol, buyExchange, sellExchange string) ([]model.PhysicalPath, error) {
	cur := atomic.AddInt32(&f.active, 1)
	for {
		max := atomic.LoadInt32(&f.maxActive)
		if cur <= max || atomic.CompareAndSwapInt32(&f.maxActive, max, cur) {
			break
		}
	}
	defer atomic.AddInt32(&f.active, -1)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(f.sleep):
	}
	return []model.PhysicalPath{
		{
			PathID:        "P1",
			Hops:          []model.Hop{{FromNode: buyExchange, EdgeDesc: "X", ToNode: sellExchange, Status: "ok"}},
			OverallStatus: "ok",
		},
	}, nil
}

func TestRunDetectRespectsConcurrencyLimit(t *testing.T) {
	fd := &fakeDetector{sleep: 30 * time.Millisecond}
	r := NewWithOptions(fd, 0.1, Options{
		DetectConcurrency: 3,
		DetectTimeout:     2 * time.Second,
	})

	var items []model.SpreadItem
	for i := 0; i < 20; i++ {
		items = append(items, model.SpreadItem{
			Symbol:        fmt.Sprintf("ASSET%dUSDT", i),
			BuyExchange:   "BITGET",
			SellExchange:  "BYBIT",
			SpreadPercent: 1.2,
		})
	}

	resp, err := r.RunDetect(context.Background(), items, nil, nil)
	if err != nil {
		t.Fatalf("RunDetect returned error: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
	if atomic.LoadInt32(&fd.maxActive) > 3 {
		t.Fatalf("expected max concurrency <= 3, got %d", atomic.LoadInt32(&fd.maxActive))
	}
}
