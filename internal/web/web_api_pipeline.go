package web

import (
	"encoding/json"
	"net/http"

	"auto-arbitrage/internal/config"
)

// handleGetPipelineAddress 返回当前配置的钱包地址（用于链上/充提测试展示）
func (d *Dashboard) handleGetPipelineAddress(w http.ResponseWriter, r *http.Request) {
	cfg := config.GetGlobalConfig()
	addr := ""
	if cfg != nil {
		addr = cfg.Wallet.WalletAddress
	}
	if addr == "" {
		http.Error(w, "wallet address not configured", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"walletAddress": addr})
}

