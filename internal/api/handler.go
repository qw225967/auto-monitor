package api

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/qw225967/auto-monitor/internal/model"
)

// Handler API 处理器
type Handler struct {
	mu       sync.RWMutex
	overview *model.OverviewResponse
}

// New 创建 Handler
func New() *Handler {
	return &Handler{
		overview: &model.OverviewResponse{Overview: []model.OverviewRow{}},
	}
}

// UpdateOverview 更新概览数据（由 Runner 调用）
func (h *Handler) UpdateOverview(resp *model.OverviewResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.overview = resp
}

// GetOverview 获取搬砖概览
func (h *Handler) GetOverview(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c.JSON(http.StatusOK, h.overview)
}
