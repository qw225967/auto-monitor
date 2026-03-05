package web

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"auto-arbitrage/internal/trigger"
)

//go:embed templates/*.html
var templateFS embed.FS

func mustParseTemplates() *template.Template {
	t, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		panic(err)
	}
	return t
}

// 初始化模板
var templates = mustParseTemplates()

// handleHomePage 返回首页
func (d *Dashboard) handleHomePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "home.html", nil); err != nil {
		d.logger.Errorf("Render home page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleTriggerDetailPage 返回 Trigger 详情页
func (d *Dashboard) handleTriggerDetailPage(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}

	// URL 解码 symbol
	symbol, err := url.PathUnescape(parts[2])
	if err != nil {
		http.Error(w, "Invalid symbol encoding", http.StatusBadRequest)
		return
	}

	// 检查 trigger 是否存在
	_, err = d.triggerManager.GetTrigger(symbol)
	if err != nil {
		http.Error(w, "Trigger not found", http.StatusNotFound)
		return
	}

	data := map[string]string{
		"Symbol": symbol,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "detail.html", data); err != nil {
		d.logger.Errorf("Render detail page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleVolumeScalperPage 返回交易量刷子页
func (d *Dashboard) handleVolumeScalperPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "volume-scalper.html", nil); err != nil {
		d.logger.Errorf("Render volume scalper page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleBrickMovingPage 返回搬砖区页
func (d *Dashboard) handleBrickMovingPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "brick-moving.html", nil); err != nil {
		d.logger.Errorf("Render brick moving page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleBrickMovingDetailPage 返回搬砖详细信息页
// 支持 /brick-moving/{symbol} 或 /brick-moving/{symbol}?triggerId=123（同 symbol 多 trigger 时用 triggerId 区分）
func (d *Dashboard) handleBrickMovingDetailPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/brick-moving/" || r.URL.Path == "/brick-moving" {
		http.Redirect(w, r, "/brick-moving", http.StatusFound)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}

	symbolFromPath, err := url.PathUnescape(parts[2])
	if err != nil {
		http.Error(w, "Invalid symbol encoding", http.StatusBadRequest)
		return
	}

	triggerIDStr := r.URL.Query().Get("triggerId")
	symbol := symbolFromPath
	if triggerIDStr != "" {
		var id uint64
		if _, err := fmt.Sscanf(triggerIDStr, "%d", &id); err == nil && id != 0 {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, err := tm.GetTriggerByIDAsProto(id)
				if err == nil {
					symbol = tg.GetSymbol()
				}
			}
		}
	} else {
		_, err = d.triggerManager.GetTrigger(symbol)
		if err != nil {
			http.Error(w, "Trigger not found", http.StatusNotFound)
			return
		}
	}

	data := map[string]string{
		"Symbol":    symbol,
		"TriggerID": triggerIDStr,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "brick-moving-detail.html", data); err != nil {
		d.logger.Errorf("Render brick moving detail page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleAutomationPage 返回自动化配置页
func (d *Dashboard) handleAutomationPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, "automation.html", nil); err != nil {
		d.logger.Errorf("Render automation page error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
