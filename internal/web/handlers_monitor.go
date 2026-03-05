package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"auto-arbitrage/internal/statistics/monitor"
)

// handleGetExecutionLogs 获取执行日志
func (d *Dashboard) handleGetExecutionLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50 // 默认返回 50 条
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	records := monitor.GetExecutionMonitor().GetRecentExecutions(limit)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		d.logger.Errorf("Failed to encode execution logs: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
