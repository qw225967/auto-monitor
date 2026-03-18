package opportunities

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/qw225967/auto-monitor/internal/model"
)

type Handler struct {
	mu       sync.RWMutex
	finder   *Finder
	response *model.OpportunitiesResponse
}

func NewHandler(finder *Finder) *Handler {
	return &Handler{
		finder: finder,
	}
}

func (h *Handler) UpdateResponse(resp *model.OpportunitiesResponse) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.response = resp
}

func (h *Handler) GetOpportunities(c *gin.Context) {
	h.mu.RLock()
	resp := h.response
	h.mu.RUnlock()

	if resp == nil {
		c.JSON(http.StatusOK, &model.OpportunitiesResponse{
			Opportunities: []model.OpportunityItem{},
			FunnelStats:   model.FunnelStats{},
			UpdatedAt:     "",
		})
		return
	}

	c.JSON(http.StatusOK, resp)
}
