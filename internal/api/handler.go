package api

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
)

// Handler API 处理器
type Handler struct {
	mu            sync.RWMutex
	overview      *model.OverviewResponse
	chainPriceMu  sync.RWMutex
	chainPrices   map[string]float64 // key: "asset:chainID"
}

// New 创建 Handler
func New() *Handler {
	return &Handler{
		overview:    &model.OverviewResponse{Overview: []model.OverviewRow{}},
		chainPrices: make(map[string]float64),
	}
}

// UpdateOverview 更新概览数据（由 Runner 调用）
func (h *Handler) UpdateOverview(resp *model.OverviewResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.overview = resp
}

// UpdateChainPrices 更新链上价格缓存（由 ChainPrice Ticker 调用）
func (h *Handler) UpdateChainPrices(prices map[string]float64) {
	h.chainPriceMu.Lock()
	defer h.chainPriceMu.Unlock()
	h.chainPrices = prices
}

// GetChainPrice 获取某资产在某链上的价格（供 Phase 10 使用）
func (h *Handler) GetChainPrice(asset, chainID string) (float64, bool) {
	h.chainPriceMu.RLock()
	defer h.chainPriceMu.RUnlock()
	p, ok := h.chainPrices[asset+":"+chainID]
	return p, ok
}

// GetAllChainPrices 获取全部链上价格（供 Runner 使用）
func (h *Handler) GetAllChainPrices() map[string]float64 {
	h.chainPriceMu.RLock()
	defer h.chainPriceMu.RUnlock()
	out := make(map[string]float64, len(h.chainPrices))
	for k, v := range h.chainPrices {
		out[k] = v
	}
	return out
}

// GetOverview 获取搬砖概览
func (h *Handler) GetOverview(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c.JSON(http.StatusOK, h.overview)
}

// PostExchangeKeys 接收交易所密钥 JSON（仅存内存，不落盘，避免泄露）
func (h *Handler) PostExchangeKeys(c *gin.Context) {
	var body struct {
		Keys string `json:"keys"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 JSON"})
		return
	}
	if body.Keys == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keys 不能为空"})
		return
	}
	if err := config.SetExchangeKeysFromJSON(body.Keys); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "JSON 格式错误: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "已保存，仅存内存"})
}
