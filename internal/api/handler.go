package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/qw225967/auto-monitor/internal/backtest"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
)

// Handler API 处理器
type Handler struct {
	mu                 sync.RWMutex
	overview           *model.OverviewResponse
	overviewUpdatedAt  time.Time
	lastDetectError    string // 最近一次 RunDetect 失败时的错误
	chainPriceMu       sync.RWMutex
	chainPrices        map[string]float64 // key: "asset:chainID"
	chainPricesUpdatedAt time.Time
	liquidityMu        sync.RWMutex
	liquidity          map[string]float64 // key: "asset:chainID" -> reserve_usd
	liquidityUpdatedAt time.Time
}

// New 创建 Handler
func New() *Handler {
	return &Handler{
		overview:    &model.OverviewResponse{Overview: []model.OverviewRow{}},
		chainPrices: make(map[string]float64),
		liquidity:   make(map[string]float64),
	}
}

// UpdateOverview 更新概览数据（由 Runner 调用），err 为 nil 时清除 lastDetectError
func (h *Handler) UpdateOverview(resp *model.OverviewResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.overview = resp
	h.overviewUpdatedAt = time.Now()
	h.lastDetectError = ""
}

// SetLastDetectError 记录最近一次通路探测失败（RunDetect 返回 error 时调用）
func (h *Handler) SetLastDetectError(err string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastDetectError = err
}

// UpdateChainPrices 更新链上价格缓存（由 ChainPrice Ticker 调用）
func (h *Handler) UpdateChainPrices(prices map[string]float64) {
	h.chainPriceMu.Lock()
	defer h.chainPriceMu.Unlock()
	h.chainPrices = prices
	h.chainPricesUpdatedAt = time.Now()
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

// UpdateLiquidity 更新流动性缓存（由 LiquiditySync 调用）
func (h *Handler) UpdateLiquidity(m map[string]float64) {
	h.liquidityMu.Lock()
	defer h.liquidityMu.Unlock()
	h.liquidityUpdatedAt = time.Now()
	if m == nil {
		h.liquidity = make(map[string]float64)
		return
	}
	h.liquidity = make(map[string]float64, len(m))
	for k, v := range m {
		h.liquidity[k] = v
	}
}

// GetAllLiquidity 获取全部流动性（供 Runner 使用）
func (h *Handler) GetAllLiquidity() map[string]float64 {
	h.liquidityMu.RLock()
	defer h.liquidityMu.RUnlock()
	out := make(map[string]float64, len(h.liquidity))
	for k, v := range h.liquidity {
		out[k] = v
	}
	return out
}

// GetOverview 获取搬砖概览
func (h *Handler) GetOverview(c *gin.Context) {
	h.mu.RLock()
	resp := h.overview
	overviewAt := h.overviewUpdatedAt
	lastErr := h.lastDetectError
	h.mu.RUnlock()
	h.chainPriceMu.RLock()
	chainAt := h.chainPricesUpdatedAt
	h.chainPriceMu.RUnlock()
	h.liquidityMu.RLock()
	liquidityAt := h.liquidityUpdatedAt
	h.liquidityMu.RUnlock()
	if resp == nil {
		c.JSON(http.StatusOK, &model.OverviewResponse{Overview: []model.OverviewRow{}})
		return
	}
	now := time.Now()
	overviewAge := int64(0)
	chainAge := int64(0)
	liquidityAge := int64(0)
	overviewAtStr := ""
	chainAtStr := ""
	liquidityAtStr := ""
	if !overviewAt.IsZero() {
		overviewAge = int64(now.Sub(overviewAt).Seconds())
		overviewAtStr = overviewAt.UTC().Format(time.RFC3339)
	}
	if !chainAt.IsZero() {
		chainAge = int64(now.Sub(chainAt).Seconds())
		chainAtStr = chainAt.UTC().Format(time.RFC3339)
	}
	if !liquidityAt.IsZero() {
		liquidityAge = int64(now.Sub(liquidityAt).Seconds())
		liquidityAtStr = liquidityAt.UTC().Format(time.RFC3339)
	}
	out := &model.OverviewResponse{
		Overview:             resp.Overview,
		LiquidityThreshold:   config.GetLiquidityThreshold(),
		OverviewUpdatedAt:    overviewAtStr,
		ChainPricesUpdatedAt: chainAtStr,
		LiquidityUpdatedAt:   liquidityAtStr,
		OverviewAgeSec:       overviewAge,
		ChainPricesAgeSec:    chainAge,
		LiquidityAgeSec:      liquidityAge,
		LastDetectError:      lastErr,
	}
	c.JSON(http.StatusOK, out)
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

// PostLiquidityThreshold 设置流动性阈值（USDT），仅存内存
// 当某 symbol 在某链上流动性低于该阈值时，不展示该套利机会
func (h *Handler) PostLiquidityThreshold(c *gin.Context) {
	var body struct {
		Threshold float64 `json:"threshold"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 JSON"})
		return
	}
	if body.Threshold < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "阈值不能为负数"})
		return
	}
	config.SetLiquidityThreshold(body.Threshold)
	c.JSON(http.StatusOK, gin.H{"ok": true, "message": "已保存", "threshold": body.Threshold})
}

// PostBacktestRun 回测：symbol + 起止时间（RFC3339），返回价差/价格序列与信号点（Binance 现货+U本位 1m K 线）
func (h *Handler) PostBacktestRun(c *gin.Context) {
	var body struct {
		Symbol string `json:"symbol"`
		From   string `json:"from"`
		To     string `json:"to"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 JSON"})
		return
	}
	body.Symbol = strings.TrimSpace(strings.ToUpper(body.Symbol))
	if body.Symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol 不能为空"})
		return
	}
	from, err := time.Parse(time.RFC3339, body.From)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from 需为 RFC3339 时间"})
		return
	}
	to, err := time.Parse(time.RFC3339, body.To)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to 需为 RFC3339 时间"})
		return
	}

	cfg, err := config.Load()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "配置加载失败"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()

	out, err := backtest.Run(ctx, cfg.Funnel, body.Symbol, from, to, http.DefaultClient)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
