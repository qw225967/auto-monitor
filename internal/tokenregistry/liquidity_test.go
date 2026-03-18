package tokenregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiquidityFetcherAuthHeaderDemo(t *testing.T) {
	var gotDemo string
	var gotPro string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDemo = r.Header.Get("x-cg-demo-api-key")
		gotPro = r.Header.Get("x-cg-pro-api-key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	f := NewLiquidityFetcher("demo-key", false)
	f.baseURL = srv.URL

	_, err := f.FetchReserveUSD(context.Background(), "1", "0x123")
	if err != nil {
		t.Fatalf("FetchReserveUSD returned error: %v", err)
	}
	if gotDemo != "demo-key" {
		t.Fatalf("expected demo header, got %q", gotDemo)
	}
	if gotPro != "" {
		t.Fatalf("expected empty pro header, got %q", gotPro)
	}
}

func TestLiquidityFetcherAuthHeaderPro(t *testing.T) {
	var gotDemo string
	var gotPro string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotDemo = r.Header.Get("x-cg-demo-api-key")
		gotPro = r.Header.Get("x-cg-pro-api-key")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	f := NewLiquidityFetcher("pro-key", true)
	f.baseURL = srv.URL

	_, err := f.FetchReserveUSD(context.Background(), "1", "0x123")
	if err != nil {
		t.Fatalf("FetchReserveUSD returned error: %v", err)
	}
	if gotPro != "pro-key" {
		t.Fatalf("expected pro header, got %q", gotPro)
	}
	if gotDemo != "" {
		t.Fatalf("expected empty demo header, got %q", gotDemo)
	}
}
