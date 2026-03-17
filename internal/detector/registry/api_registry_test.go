package registry

import (
	"testing"
	"time"

	"github.com/qw225967/auto-monitor/internal/model"
)

func TestGetNetworksRespectsTTL(t *testing.T) {
	r := NewAPINetworkRegistryWithTTL(1 * time.Second)
	k := r.cacheKey("bitget", "USDT")
	r.mu.Lock()
	r.withdraw[k] = []model.WithdrawNetworkInfo{{Network: "BSC", ChainID: "56", WithdrawEnable: true}}
	r.withdrawUpdatedAt[k] = time.Now().Add(-2 * time.Second)
	r.deposit[k] = []model.WithdrawNetworkInfo{{Network: "ETH", ChainID: "1", WithdrawEnable: true}}
	r.depositUpdatedAt[k] = time.Now()
	r.mu.Unlock()

	if w, _ := r.GetWithdrawNetworks("bitget", "USDT"); len(w) != 0 {
		t.Fatalf("expected stale withdraw cache to be ignored, got %d", len(w))
	}
	if d, _ := r.GetDepositNetworks("bitget", "USDT"); len(d) == 0 {
		t.Fatalf("expected fresh deposit cache to be returned")
	}
}

func TestCleanupStaleLocked(t *testing.T) {
	r := NewAPINetworkRegistryWithTTL(2 * time.Second)
	staleKey := r.cacheKey("gate", "BTC")
	freshKey := r.cacheKey("gate", "ETH")
	now := time.Now()

	r.mu.Lock()
	r.withdraw[staleKey] = []model.WithdrawNetworkInfo{{Network: "ETH", ChainID: "1", WithdrawEnable: true}}
	r.withdrawUpdatedAt[staleKey] = now.Add(-3 * time.Second)
	r.deposit[staleKey] = []model.WithdrawNetworkInfo{{Network: "ETH", ChainID: "1", WithdrawEnable: true}}
	r.depositUpdatedAt[staleKey] = now.Add(-3 * time.Second)

	r.withdraw[freshKey] = []model.WithdrawNetworkInfo{{Network: "BSC", ChainID: "56", WithdrawEnable: true}}
	r.withdrawUpdatedAt[freshKey] = now
	r.deposit[freshKey] = []model.WithdrawNetworkInfo{{Network: "BSC", ChainID: "56", WithdrawEnable: true}}
	r.depositUpdatedAt[freshKey] = now

	r.cleanupStaleLocked(now)
	_, staleWd := r.withdraw[staleKey]
	_, staleDep := r.deposit[staleKey]
	_, freshWd := r.withdraw[freshKey]
	_, freshDep := r.deposit[freshKey]
	r.mu.Unlock()

	if staleWd || staleDep {
		t.Fatalf("expected stale keys to be removed")
	}
	if !freshWd || !freshDep {
		t.Fatalf("expected fresh keys to be kept")
	}
}
