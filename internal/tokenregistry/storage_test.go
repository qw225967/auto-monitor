package tokenregistry

import (
	"testing"
	"time"
)

func TestNeedRefreshAssetByTTL(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-2 * time.Hour).Format(time.RFC3339)
	stale := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)

	rd := &RegistryData{
		Assets: map[string]map[string]TokenChainInfo{
			"BTC": {
				"1": {Address: "0x1", UpdatedAt: fresh},
			},
			"ETH": {
				"1": {Address: "0x2", UpdatedAt: stale},
			},
		},
	}

	if NeedRefreshAssetByTTL(rd, "BTC", 7*24*time.Hour, now) {
		t.Fatalf("expected BTC to be fresh")
	}
	if !NeedRefreshAssetByTTL(rd, "ETH", 7*24*time.Hour, now) {
		t.Fatalf("expected ETH to need refresh due to stale timestamp")
	}
	if !NeedRefreshAssetByTTL(rd, "SOL", 7*24*time.Hour, now) {
		t.Fatalf("expected SOL to need refresh because it is missing")
	}
}

func TestMergeIncrementalRefreshesUpdatedAt(t *testing.T) {
	store := NewStorage("unused")
	rd := &RegistryData{
		Assets: map[string]map[string]TokenChainInfo{
			"BTC": {
				"1": {
					Address:   "0xabc",
					Decimals:  8,
					Symbol:    "btc",
					UpdatedAt: "2020-01-01T00:00:00Z",
					ReserveUSD: 1000,
				},
			},
		},
	}

	updated := store.MergeIncremental(rd, []TokenInfo{
		{Asset: "BTC", ChainID: "1", Address: "0xabc", Decimals: 8, Symbol: "btc"},
	})
	if updated == 0 {
		t.Fatalf("expected merge to touch existing entry and update timestamp")
	}
	got := rd.Assets["BTC"]["1"]
	if got.UpdatedAt == "2020-01-01T00:00:00Z" || got.UpdatedAt == "" {
		t.Fatalf("expected updated_at refreshed, got %q", got.UpdatedAt)
	}
	if got.ReserveUSD != 1000 {
		t.Fatalf("expected reserve_usd preserved, got %v", got.ReserveUSD)
	}
}
