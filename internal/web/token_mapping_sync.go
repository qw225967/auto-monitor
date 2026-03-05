package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// GainersAPIResponse API 返回的数据结构
type GainersAPIResponse struct {
	Exchange          string  `json:"exchange"`
	Symbol            string  `json:"symbol"`
	PriceChangePercent float64 `json:"priceChangePercent"`
	QuoteVolume       float64 `json:"quoteVolume"`
	LastPrice         float64 `json:"lastPrice"`
	High24h           float64 `json:"high24h"`
	Low24h            float64 `json:"low24h"`
	Open24h           float64 `json:"open24h"`
	DexInfo           *DexInfo `json:"dexInfo,omitempty"`
}

// DexInfo 链上信息
type DexInfo struct {
	BuybackAmount   string `json:"buybackAmount"`
	Change24h       string `json:"change24h"`
	ContractAddress string `json:"contractAddress"`
	Holders         string `json:"holders"`
	Liquidity       string `json:"liquidity"`
	MarketCap       string `json:"marketCap"`
	Name            string `json:"name"`
	NetFlow         string `json:"netFlow"`
	Price           string `json:"price"`
	SymbolTime      string `json:"symbolTime"`
	Tags            []string `json:"tags"`
	TopHolderInfo   string `json:"topHolderInfo"`
	Transactions    string `json:"transactions"`
	URL             string `json:"url"`
	Volume24h       string `json:"volume24h"`
}

const (
	gainersAPIURL = "http://43.165.183.132:8989/api/gainers/binance/top20"
	syncInterval  = 5 * time.Minute // 每 5 分钟同步一次
)

// startAutoSync 启动自动同步任务
func (d *Dashboard) startAutoSync() {
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()

	// 启动时立即同步一次
	d.syncTokenMappingsFromAPI()

	for {
		select {
		case <-d.syncCtx.Done():
			d.logger.Info("Token 映射自动同步任务已停止")
			return
		case <-ticker.C:
			d.syncTokenMappingsFromAPI()
		}
	}
}

// syncTokenMappingsFromAPI 从 API 同步 token 映射
func (d *Dashboard) syncTokenMappingsFromAPI() {
	d.syncMu.Lock()
	if d.syncInProgress {
		d.syncMu.Unlock()
		d.logger.Debug("Token 映射同步正在进行中，跳过本次同步")
		return
	}
	d.syncInProgress = true
	d.syncMu.Unlock()

	defer func() {
		d.syncMu.Lock()
		d.syncInProgress = false
		d.lastSyncTime = time.Now()
		d.syncMu.Unlock()
	}()

	d.logger.Info("开始同步 Token 映射...")

	// 创建 HTTP 客户端，设置超时
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 发送请求
	resp, err := client.Get(gainersAPIURL)
	if err != nil {
		d.logger.Errorf("请求 Token 映射 API 失败: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		d.logger.Errorf("Token 映射 API 返回错误状态码: %d, 响应: %s", resp.StatusCode, string(body))
		return
	}

	// 解析响应
	var gainers []GainersAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&gainers); err != nil {
		d.logger.Errorf("解析 Token 映射 API 响应失败: %v", err)
		return
	}

	// 转换为 token 映射并添加
	addedCount := 0
	updatedCount := 0
	skippedCount := 0

	for _, gainer := range gainers {
		// 检查是否有 dexInfo 和 contractAddress
		if gainer.DexInfo == nil || gainer.DexInfo.ContractAddress == "" {
			skippedCount++
			continue
		}

		// 从 symbol 中提取基础符号（去掉 USDT 后缀）
		baseSymbol := extractBaseSymbolFromTradingPair(gainer.Symbol)
		if baseSymbol == "" {
			skippedCount++
			continue
		}

		// 检查是否已存在映射（按地址匹配任意链的条目）
		allMappings := d.tokenMappingMgr.GetAllMappings()
		normalizedAddr := normalizeAddressForCheck(gainer.DexInfo.ContractAddress)
		var existingSymbol string
		for _, e := range allMappings {
			if normalizeAddressForCheck(e.Address) == normalizedAddr {
				existingSymbol = e.Symbol
				break
			}
		}

		if existingSymbol != "" {
			// 已存在映射
			if existingSymbol != baseSymbol {
				// 符号不同，更新映射
				if err := d.tokenMappingMgr.AddMapping(gainer.DexInfo.ContractAddress, baseSymbol, ""); err != nil {
					d.logger.Warnf("更新 Token 映射失败: %s -> %s, 错误: %v", gainer.DexInfo.ContractAddress, baseSymbol, err)
					continue
				}
				updatedCount++
				d.logger.Debugf("更新 Token 映射: %s -> %s", gainer.DexInfo.ContractAddress, baseSymbol)
			} else {
				// 符号相同，跳过
				skippedCount++
			}
		} else {
			// 不存在映射，添加新映射
			if err := d.tokenMappingMgr.AddMapping(gainer.DexInfo.ContractAddress, baseSymbol, ""); err != nil {
				d.logger.Warnf("添加 Token 映射失败: %s -> %s, 错误: %v", gainer.DexInfo.ContractAddress, baseSymbol, err)
				continue
			}
			addedCount++
			d.logger.Debugf("添加 Token 映射: %s -> %s", gainer.DexInfo.ContractAddress, baseSymbol)
		}
	}

	// 保存到文件
	if err := d.tokenMappingMgr.SaveToFile(); err != nil {
		d.logger.Errorf("保存 Token 映射到文件失败: %v", err)
		return
	}

	d.logger.Infof("Token 映射同步完成: 新增 %d 个, 更新 %d 个, 跳过 %d 个", addedCount, updatedCount, skippedCount)
}

// extractBaseSymbolFromTradingPair 从交易对中提取基础符号
// 例如: "TRADOORUSDT" -> "TRADOOR", "BTCUSDT" -> "BTC"
func extractBaseSymbolFromTradingPair(tradingPair string) string {
	// 去掉 USDT 后缀（不区分大小写）
	tradingPair = strings.ToUpper(tradingPair)
	suffixes := []string{"USDT", "USDC", "BUSD", "BTC", "ETH"}
	
	for _, suffix := range suffixes {
		if strings.HasSuffix(tradingPair, suffix) {
			baseSymbol := strings.TrimSuffix(tradingPair, suffix)
			if len(baseSymbol) > 0 {
				return baseSymbol
			}
		}
	}
	
	return ""
}

// normalizeAddressForCheck 标准化地址用于检查（统一转为小写，处理 0x 前缀）
func normalizeAddressForCheck(addr string) string {
	if len(addr) == 0 {
		return addr
	}
	// 去除前后空格
	addr = strings.TrimSpace(addr)
	// 统一转为小写
	addr = strings.ToLower(addr)
	// 统一处理 0x 前缀：如果有 0x 前缀则保留，如果没有则添加
	if !strings.HasPrefix(addr, "0x") {
		addr = "0x" + addr
	}
	return addr
}

// handleSyncTokenMappings 处理手动同步 Token 映射的请求
func (d *Dashboard) handleSyncTokenMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 在 goroutine 中执行同步，避免阻塞
	go func() {
		d.syncTokenMappingsFromAPI()
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "sync_started",
		"message": "Token 映射同步已启动",
	})
}

// handleSyncStatus 获取同步状态
func (d *Dashboard) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	d.syncMu.RLock()
	inProgress := d.syncInProgress
	lastSyncTime := d.lastSyncTime
	d.syncMu.RUnlock()

	var lastSyncTimeStr string
	if !lastSyncTime.IsZero() {
		lastSyncTimeStr = lastSyncTime.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"inProgress":   inProgress,
		"lastSyncTime": lastSyncTimeStr,
	})
}

