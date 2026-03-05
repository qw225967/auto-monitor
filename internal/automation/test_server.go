package automation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"go.uber.org/zap"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
)

// TestServer 测试服务器，用于模拟套利机会列表API
type TestServer struct {
	mu           sync.RWMutex
	opportunities map[string][]model.ArbitrageOpportunity // key: endpoint type, value: opportunities
	server       *http.Server
	logger       *zap.SugaredLogger
	port         int
}

// NewTestServer 创建新的测试服务器
func NewTestServer(port int) *TestServer {
	return &TestServer{
		opportunities: make(map[string][]model.ArbitrageOpportunity),
		logger:        logger.GetLoggerInstance().Named("AutomationTestServer").Sugar(),
		port:          port,
	}
}

// Start 启动测试服务器
func (ts *TestServer) Start() error {
	mux := http.NewServeMux()

	// 获取所有机会列表（按类型分类的map）
	mux.HandleFunc("/api/opportunities/getall", ts.handleGetAllOpportunities)

	// 设置所有机会列表（按类型分类的map）
	mux.HandleFunc("/api/opportunities/setall", ts.handleSetAllOpportunities)

	// 清空所有机会列表
	mux.HandleFunc("/api/opportunities/clear", ts.handleClearOpportunities)

	// 健康检查
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	ts.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", ts.port),
		Handler: mux,
	}

	ts.logger.Infof("测试服务器启动在端口 %d", ts.port)
	ts.logger.Infof("API端点:")
	ts.logger.Infof("  GET  /api/opportunities/getall - 获取所有机会列表（返回按类型分类的map）")
	ts.logger.Infof("  POST /api/opportunities/setall - 设置所有机会列表（Body: 按类型分类的map）")
	ts.logger.Infof("  POST /api/opportunities/clear - 清空所有机会列表")

	return ts.server.ListenAndServe()
}

// Stop 停止测试服务器
func (ts *TestServer) Stop() error {
	if ts.server != nil {
		return ts.server.Close()
	}
	return nil
}

// handleGetAllOpportunities 获取所有机会列表（按类型分类的map）
func (ts *TestServer) handleGetAllOpportunities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ts.mu.RLock()
	allOpportunities := make(map[string][]model.ArbitrageOpportunity)
	for k, v := range ts.opportunities {
		allOpportunities[k] = v
	}
	ts.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allOpportunities)
}

// handleSetAllOpportunities 设置所有机会列表（按类型分类的map）
// Body: JSON对象，key为端点类型，value为机会列表数组
func (ts *TestServer) handleSetAllOpportunities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var allOpportunities map[string][]model.ArbitrageOpportunity
	if err := json.NewDecoder(r.Body).Decode(&allOpportunities); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	ts.mu.Lock()
	ts.opportunities = allOpportunities
	ts.mu.Unlock()

	totalCount := 0
	for _, opps := range allOpportunities {
		totalCount += len(opps)
	}
	ts.logger.Infof("设置所有机会列表，共 %d 个类型，%d 个机会", len(allOpportunities), totalCount)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// handleClearOpportunities 清空所有机会列表
func (ts *TestServer) handleClearOpportunities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ts.mu.Lock()
	ts.opportunities = make(map[string][]model.ArbitrageOpportunity)
	ts.mu.Unlock()

	ts.logger.Info("已清空所有机会列表")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

// GetURL 获取统一的API URL（所有类型都使用同一个URL）
func (ts *TestServer) GetURL() string {
	return fmt.Sprintf("http://localhost:%d/api/opportunities/getall", ts.port)
}
