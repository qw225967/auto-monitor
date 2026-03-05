package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/onchain/bridge"
	"auto-arbitrage/internal/onchain/bridge/layerzero"
	"auto-arbitrage/internal/onchain/bridge/wormhole"
	"auto-arbitrage/internal/pipeline"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/statistics/monitor"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/trigger"
	"auto-arbitrage/internal/trigger/token_mapping"
	"auto-arbitrage/internal/utils/notify/telegram"
	"auto-arbitrage/internal/version"
)

// ensureQuoteSuffix 若 symbol 无 USDT/USDC/BUSD 后缀则补上 USDT，保证 CEX 收到 HANAUSDT 等形式；链上在 subscribeOnchainForSource、extractBaseSymbol 会剥后缀取 base
func ensureQuoteSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	u := strings.ToUpper(s)
	if strings.HasSuffix(u, "USDT") || strings.HasSuffix(u, "USDC") || strings.HasSuffix(u, "BUSD") {
		return s
	}
	return s + "USDT"
}

// getTriggerFromRequest 从请求中获取 trigger，优先使用 triggerId，否则使用 symbol
func (d *Dashboard) getTriggerFromRequest(r *http.Request) (proto.Trigger, error) {
	if idStr := r.URL.Query().Get("triggerId"); idStr != "" {
		var id uint64
		if _, err := fmt.Sscanf(idStr, "%d", &id); err == nil && id != 0 {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				return tm.GetTriggerByIDAsProto(id)
			}
		}
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		return nil, fmt.Errorf("symbol or triggerId parameter is required")
	}
	return d.triggerManager.GetTrigger(symbol)
}

// handleTokenMappings 处理 Token 映射列表请求（按链返回 address, symbol, chainId）
func (d *Dashboard) handleTokenMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	search := r.URL.Query().Get("search")
	allMappings := d.tokenMappingMgr.GetAllMappings()

	var mappings []map[string]string
	for _, entry := range allMappings {
		if search == "" ||
			strings.Contains(strings.ToLower(entry.Address), strings.ToLower(search)) ||
			strings.Contains(strings.ToLower(entry.Symbol), strings.ToLower(search)) ||
			strings.Contains(strings.ToLower(entry.ChainId), strings.ToLower(search)) {
			mappings = append(mappings, map[string]string{
				"address": entry.Address,
				"symbol":  entry.Symbol,
				"chainId": entry.ChainId,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"mappings": mappings,
	})
}

// handleTokenMapping 处理单个 Token 映射的增删改
func (d *Dashboard) handleTokenMapping(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// 添加或更新映射（支持 chainId）
		var req struct {
			Address string `json:"address"`
			Symbol  string `json:"symbol"`
			ChainId string `json:"chainId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if err := d.tokenMappingMgr.AddMapping(req.Address, req.Symbol, req.ChainId); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := d.tokenMappingMgr.SaveToFile(); err != nil {
			http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	case http.MethodDelete:
		// 删除映射（支持按 address 或 address+chainId）
		address := r.URL.Query().Get("address")
		chainId := r.URL.Query().Get("chainId")
		if address == "" {
			http.Error(w, "address parameter is required", http.StatusBadRequest)
			return
		}

		if err := d.tokenMappingMgr.RemoveMapping(address, chainId); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := d.tokenMappingMgr.SaveToFile(); err != nil {
			http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// parseTraderType 解析类型字符串
// 对于 "onchain:56"，返回 type="onchain", chainId="56"
// 对于 "binance:futures"，返回 type="binance", marketType="futures"
func parseTraderType(traderType string) (traderTypeStr, chainId, marketType string, err error) {
	if traderType == "" {
		return "", "", "", fmt.Errorf("trader type is empty")
	}

	parts := strings.Split(traderType, ":")
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("invalid trader type format: %s (expected format: 'type:value')", traderType)
	}

	traderTypeStr = parts[0]
	value := parts[1]

	if traderTypeStr == "onchain" {
		chainId = value
		return traderTypeStr, chainId, "", nil
	}

	// 交易所类型（如 binance, gate, bybit, bitget 等）
	marketType = value // value 是 marketType（spot 或 futures）
	return traderTypeStr, "", marketType, nil
}

// handleContractMappings 处理合约映射列表请求
func (d *Dashboard) handleContractMappings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	search := r.URL.Query().Get("search")
	allMappings := d.contractMappingMgr.GetAllMappings()

	var mappings []proto.ContractMapping
	for _, mapping := range allMappings {
		if search == "" || strings.Contains(strings.ToLower(mapping.Symbol), strings.ToLower(search)) ||
			strings.Contains(strings.ToLower(mapping.TraderAType), strings.ToLower(search)) ||
			strings.Contains(strings.ToLower(mapping.TraderBType), strings.ToLower(search)) {
			mappings = append(mappings, mapping)
		}
	}

	// 确保 mappings 始终是数组，即使为空
	if mappings == nil {
		mappings = []proto.ContractMapping{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"mappings": mappings,
	})
}

// handleContractMapping 处理单个合约映射的增删改
func (d *Dashboard) handleContractMapping(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// 添加或更新映射
		var req struct {
			Symbol      string `json:"symbol"`
			TraderAType string `json:"traderAType"`
			TraderBType string `json:"traderBType"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		d.logger.Infof("Received contract mapping request: symbol=%s, traderAType=%s, traderBType=%s", req.Symbol, req.TraderAType, req.TraderBType)

		// 验证格式
		if _, _, _, err := parseTraderType(req.TraderAType); err != nil {
			d.logger.Errorf("Invalid traderAType: %v", err)
			http.Error(w, fmt.Sprintf("Invalid traderAType: %v", err), http.StatusBadRequest)
			return
		}
		if _, _, _, err := parseTraderType(req.TraderBType); err != nil {
			d.logger.Errorf("Invalid traderBType: %v", err)
			http.Error(w, fmt.Sprintf("Invalid traderBType: %v", err), http.StatusBadRequest)
			return
		}

		if err := d.contractMappingMgr.AddMapping(req.Symbol, req.TraderAType, req.TraderBType); err != nil {
			d.logger.Errorf("Failed to add mapping: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := d.contractMappingMgr.SaveToFile(); err != nil {
			d.logger.Errorf("Failed to save mapping to file: %v", err)
			http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		d.logger.Infof("Successfully saved contract mapping: symbol=%s, traderAType=%s, traderBType=%s", req.Symbol, req.TraderAType, req.TraderBType)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	case http.MethodDelete:
		// 删除映射
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			http.Error(w, "symbol parameter is required", http.StatusBadRequest)
			return
		}

		if err := d.contractMappingMgr.RemoveMapping(symbol); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := d.contractMappingMgr.SaveToFile(); err != nil {
			http.Error(w, "Failed to save: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetTriggers 返回所有 trigger 的状态（排除搬砖区的trigger）
func (d *Dashboard) handleGetTriggers(w http.ResponseWriter, r *http.Request) {
	triggers := d.triggerManager.GetAllTriggers()
	// 初始化为空数组而不是 nil，确保 JSON 编码为 [] 而不是 null
	result := make([]map[string]interface{}, 0)

	for _, tg := range triggers {
		// 过滤掉搬砖区的trigger，避免在交易量页面显示
		if d.isBrickMovingSymbol(tg.GetSymbol()) {
			continue
		}

		optimalThresholds := tg.GetOptimalThresholds()
		status := tg.GetStatus() // 获取实际状态

		// 获取最小阈值
		minInterval := tg.GetMinThreshold()

		// 获取类型信息
		chainId := tg.GetChainId()
		exchangeType := tg.GetExchangeType()
		traderAType := tg.GetTraderAType()
		traderBType := tg.GetTraderBType()

		result = append(result, map[string]interface{}{
			"id":                          tg.GetID(),
			"symbol":                      tg.GetSymbol(),
			"status":                      status, // 使用实际状态
			"telegramNotificationEnabled": tg.GetTelegramNotificationEnabled(),
			"optimalThresholds":           optimalThresholds,
			"minInterval":                 minInterval,
			"interval":                    minInterval, // 向后兼容
			"chainId":                     chainId,
			"exchangeType":                exchangeType,
			"traderAType":                 traderAType,
			"traderBType":                 traderBType,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleGetTrigger 返回单个 trigger 的详细信息
func (d *Dashboard) handleGetTrigger(w http.ResponseWriter, r *http.Request) {
	tg, err := d.getTriggerFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 获取最小阈值和最大阈值
	minInterval := tg.GetMinThreshold()
	maxInterval := tg.GetMaxThreshold()
	status := tg.GetStatus() // 获取实际状态

	// 获取类型信息
	chainId := tg.GetChainId()
	exchangeType := tg.GetExchangeType()
	traderAType := tg.GetTraderAType()
	traderBType := tg.GetTraderBType()

	result := map[string]interface{}{
		"id":                          tg.GetID(),
		"symbol":                      tg.GetSymbol(),
		"status":                      status, // 返回状态
		"telegramNotificationEnabled": tg.GetTelegramNotificationEnabled(),
		"enabledAB":                   tg.GetDirectionEnabled(0), // 0 表示 DirectionAB
		"enabledBA":                   tg.GetDirectionEnabled(1), // 1 表示 DirectionBA
		"minInterval":                 minInterval,
		"maxInterval":                 maxInterval,
		"interval":                    minInterval, // 向后兼容
		"bundlerEnabled":              tg.IsBundlerEnabled(),
		"onChainSlippage":             tg.GetOnChainSlippage(),
		"gasMultiplier":               tg.GetGasMultiplier(),
		"onChainGasLimit":             tg.GetOnChainGasLimit(),
		"chainId":                     chainId,
		"exchangeType":                exchangeType,
		"traderAType":                 traderAType,
		"traderBType":                 traderBType,
		"fastTriggerConfig":           tg.GetFastTriggerConfig(), // 快速触发优化器配置
	}
	// 搬砖 trigger 返回按方向区分的 size 与阈值、交易状态、触发间隔，供详情页 loadTriggerState 使用
	if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
		result["triggerABSize"] = brickTrigger.GetConfiguredSizeAB()
		result["triggerBASize"] = brickTrigger.GetConfiguredSizeBA()
		result["thresholdAB"] = brickTrigger.GetThresholdAB()
		result["thresholdBA"] = brickTrigger.GetThresholdBA()
		statusABA, statusABB := brickTrigger.GetTradeStatusAB()
		statusBAB, statusBAA := brickTrigger.GetTradeStatusBA()
		result["tradeStatusABA"] = statusABA
		result["tradeStatusABB"] = statusABB
		result["tradeStatusBAB"] = statusBAB
		result["tradeStatusBAA"] = statusBAA
		result["orderLoopMs"] = int64(brickTrigger.GetOrderLoopInterval().Milliseconds())
		result["cooldownSec"] = int64(brickTrigger.GetCooldown().Seconds())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleCreateTrigger 创建新的 trigger
func (d *Dashboard) handleCreateTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 1.解析请求
	var req struct {
		Symbol      string `json:"symbol"`
		TraderAType string `json:"traderAType"` // 如 "onchain:56"
		TraderBType string `json:"traderBType"` // 如 "binance:futures"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	d.logger.Infof("Received create trigger request: symbol=%s, traderAType=%s, traderBType=%s", req.Symbol, req.TraderAType, req.TraderBType)

	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	// 识别后缀：无 USDT/USDC/BUSD 时补 USDT，保证 CEX 下发 HANAUSDT；链上在 subscribeOnchainForSource、extractBaseSymbol 剥后缀取 base
	req.Symbol = ensureQuoteSuffix(req.Symbol)

	// 2.检查是否已存在
	_, err := d.triggerManager.GetTrigger(req.Symbol)
	if err == nil {
		http.Error(w, "Trigger already exists", http.StatusConflict)
		return
	}

	// 3.解析并获取 traderType（从映射表或默认值）
	traderAType, traderBType, err := d.resolveTraderTypes(req.Symbol, req.TraderAType, req.TraderBType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to resolve trader types: %v", err), http.StatusBadRequest)
		return
	}

	d.logger.Infof("Resolved trader types: traderAType=%s, traderBType=%s", traderAType, traderBType)

	// 4.解析 traderType 信息
	aInfo, err := parseTraderTypeInfo(traderAType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid traderAType: %v", err), http.StatusBadRequest)
		return
	}

	bInfo, err := parseTraderTypeInfo(traderBType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid traderBType: %v", err), http.StatusBadRequest)
		return
	}

	// 5.创建 Trader 实例
	sourceA, err := d.createTraderFromType(traderAType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create sourceA: %v", err), http.StatusInternalServerError)
		return
	}

	sourceB, err := d.createTraderFromType(traderBType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create sourceB: %v", err), http.StatusInternalServerError)
		return
	}

	// 6.创建 trigger
	tm, ok := d.triggerManager.(*trigger.TriggerManager)
	if !ok {
		http.Error(w, "TriggerManager type assertion failed", http.StatusInternalServerError)
		return
	}

	tg := tm.NewTriggerWithMode(req.Symbol, sourceA, sourceB, trigger.ModeScheduled)

	// 7.配置 trigger（设置类型和链ID）
	configureTrigger(tg, traderAType, traderBType, aInfo, bInfo)

	// 设置默认配置
	tg = tg.
		SetSlippageOpt(trigger.DefaultSlippageOpt()).
		SetIntervalOpt(trigger.DefaultIntervalOpt()).
		SetOrderOpt(trigger.DefaultOrderOpt())

	// 8.添加 trigger，如果链上订阅失败会返回错误
	if err := d.triggerManager.AddTrigger(req.Symbol, tg); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 9.构建并返回响应
	response := buildTriggerResponse(req.Symbol, traderAType, traderBType, aInfo, bInfo)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleUpdateTrigger 更新 trigger（暂时不需要，配置通过 handleUpdateTriggerConfig）
func (d *Dashboard) handleUpdateTrigger(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not implemented", http.StatusNotImplemented)
}

// handleDeleteTrigger 删除 trigger
func (d *Dashboard) handleDeleteTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tg, err := d.getTriggerFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	symbol := tg.GetSymbol()
	if err := d.triggerManager.RemoveTrigger(symbol); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// 删除 trigger 时，同时删除对应的 contract mapping，避免再次创建时使用旧的配置
	if err := d.contractMappingMgr.RemoveMapping(symbol); err != nil {
		// 如果 mapping 不存在，不报错（可能用户手动删除了 mapping）
		d.logger.Debugf("Failed to remove contract mapping for %s (may not exist): %v", symbol, err)
	} else {
		// 如果成功删除 mapping，保存到文件
		if err := d.contractMappingMgr.SaveToFile(); err != nil {
			d.logger.Warnf("Failed to save contract mapping after deletion: %v", err)
			// 不返回错误，因为 trigger 已经成功删除
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "symbol": symbol})
}

// handleUpdateTriggerConfig 更新 trigger 配置
func (d *Dashboard) handleUpdateTriggerConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Symbol                      string   `json:"symbol"`
		EnableAB                    bool     `json:"enableAB"`
		EnableBA                    bool     `json:"enableBA"`
		MinInterval                 *float64 `json:"minInterval"`                 // 最小阈值（可选）
		MaxInterval                 *float64 `json:"maxInterval"`                 // 最大阈值（可选，0 表示不限制）
		Interval                    float64  `json:"interval"`                    // 向后兼容，等同于 minInterval
		TelegramNotificationEnabled *bool    `json:"telegramNotificationEnabled"` // Telegram 通知开关（可选）
		BundlerEnabled              *bool    `json:"bundlerEnabled"`              // Bundler 开关（可选）
		OnChainSlippage             *string  `json:"onChainSlippage"`             // 链上滑点（可选）
		GasMultiplier               *float64 `json:"gasMultiplier"`               // Gas 乘数（可选，如 1.5 表示增加 50%）
		OnChainGasLimit             *string  `json:"onChainGasLimit"`             // 链上 GasLimit（可选）
		// 快速触发优化器参数
		SpeedWeight               *float64 `json:"speedWeight"`               // 速度权重（可选，0-1）
		QuantileLevel             *float64 `json:"quantileLevel"`             // 分位数水平（可选，0.1-0.5）
		MaxAcceptableDelay        *int64   `json:"maxAcceptableDelay"`        // 最大可接受延迟（可选，毫秒）
		MinValidTriggers          *int     `json:"minValidTriggers"`          // 最小有效触发次数（可选）
		CleanupPriceDiffsInterval *int64   `json:"cleanupPriceDiffsInterval"` // 清理价差数据间隔（可选，秒，0 表示不自动清理）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	tg, err := d.triggerManager.GetTrigger(req.Symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// 更新方向启用状态
	tg.SetDirectionEnabled(0, req.EnableAB) // 0 表示 DirectionAB
	tg.SetDirectionEnabled(1, req.EnableBA) // 1 表示 DirectionBA

	// 更新最小阈值
	if req.MinInterval != nil {
		if err := tg.SetMinThreshold(*req.MinInterval); err != nil {
			http.Error(w, "Failed to update min threshold: "+err.Error(), http.StatusBadRequest)
			return
		}
	} else if req.Interval > 0 {
		// 向后兼容
		if err := tg.SetMinThreshold(req.Interval); err != nil {
			http.Error(w, "Failed to update interval: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// 更新最大阈值
	if req.MaxInterval != nil {
		if err := tg.SetMaxThreshold(*req.MaxInterval); err != nil {
			http.Error(w, "Failed to update max threshold: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// 更新 Telegram 通知开关（如果提供了该字段）
	if req.TelegramNotificationEnabled != nil {
		tg.SetTelegramNotificationEnabled(*req.TelegramNotificationEnabled)
	}

	// 更新 Bundler 开关（如果提供了该字段）
	if req.BundlerEnabled != nil {
		if *req.BundlerEnabled {
			if err := tg.EnableBundler(); err != nil {
				http.Error(w, "Failed to enable bundler: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			if err := tg.DisableBundler(); err != nil {
				http.Error(w, "Failed to disable bundler: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// 更新链上滑点（如果提供了该字段）
	if req.OnChainSlippage != nil {
		if err := tg.SetOnChainSlippage(*req.OnChainSlippage); err != nil {
			http.Error(w, "Failed to update onchain slippage: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 更新 Gas 乘数（如果提供了该字段）
	if req.GasMultiplier != nil {
		if err := tg.SetGasMultiplier(*req.GasMultiplier); err != nil {
			http.Error(w, "Failed to update gas multiplier: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 更新链上 GasLimit（如果提供了该字段）
	if req.OnChainGasLimit != nil {
		if err := tg.SetOnChainGasLimit(*req.OnChainGasLimit); err != nil {
			http.Error(w, "Failed to update onchain gas limit: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 更新快速触发优化器参数
	if req.SpeedWeight != nil {
		if err := tg.SetFastTriggerSpeedWeight(*req.SpeedWeight); err != nil {
			http.Error(w, "Failed to update speed weight: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if req.QuantileLevel != nil {
		if err := tg.SetFastTriggerQuantileLevel(*req.QuantileLevel); err != nil {
			http.Error(w, "Failed to update quantile level: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if req.MaxAcceptableDelay != nil {
		if err := tg.SetFastTriggerMaxAcceptableDelay(*req.MaxAcceptableDelay); err != nil {
			http.Error(w, "Failed to update max acceptable delay: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	if req.MinValidTriggers != nil {
		if err := tg.SetFastTriggerMinValidTriggers(*req.MinValidTriggers); err != nil {
			http.Error(w, "Failed to update min valid triggers: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	// 更新清理价差数据间隔（如果提供了该字段）
	if req.CleanupPriceDiffsInterval != nil {
		interval := time.Duration(*req.CleanupPriceDiffsInterval) * time.Second
		if err := tg.SetCleanupPriceDiffsInterval(interval); err != nil {
			http.Error(w, "Failed to update cleanup price diffs interval: "+err.Error(), http.StatusBadRequest)
			return
		}
		d.logger.Infof("Updated cleanup price diffs interval for %s: %v", req.Symbol, interval)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleStartTrigger 启动指定的 trigger
func (d *Dashboard) handleStartTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tg, err := d.getTriggerFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := d.triggerManager.GetTriggerContext()
	if err := tg.Start(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "symbol": tg.GetSymbol()})
}

// handleStopTrigger 停止指定的 trigger
func (d *Dashboard) handleStopTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tg, err := d.getTriggerFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := tg.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "stopped", "symbol": tg.GetSymbol()})
}

// CreateTriggerInternal 内部方法，供自动化管理器调用
func (d *Dashboard) CreateTriggerInternal(symbol, traderAType, traderBType string) error {
	// 使用与handleCreateTrigger相同的逻辑，但不通过HTTP
	req := struct {
		Symbol      string
		TraderAType string
		TraderBType string
	}{
		Symbol:      symbol,
		TraderAType: traderAType,
		TraderBType: traderBType,
	}

	// 识别后缀
	req.Symbol = ensureQuoteSuffix(req.Symbol)

	// 检查是否已存在
	_, err := d.triggerManager.GetTrigger(req.Symbol)
	if err == nil {
		return fmt.Errorf("trigger already exists for symbol: %s", req.Symbol)
	}

	// 解析并获取 traderType
	resolvedAType, resolvedBType, err := d.resolveTraderTypes(req.Symbol, req.TraderAType, req.TraderBType)
	if err != nil {
		return fmt.Errorf("failed to resolve trader types: %w", err)
	}

	// 解析 traderType 信息
	aInfo, err := parseTraderTypeInfo(resolvedAType)
	if err != nil {
		return fmt.Errorf("invalid traderAType: %w", err)
	}

	bInfo, err := parseTraderTypeInfo(resolvedBType)
	if err != nil {
		return fmt.Errorf("invalid traderBType: %w", err)
	}

	// 创建 Trader 实例
	sourceA, err := d.createTraderFromType(resolvedAType)
	if err != nil {
		return fmt.Errorf("failed to create sourceA: %w", err)
	}

	sourceB, err := d.createTraderFromType(resolvedBType)
	if err != nil {
		return fmt.Errorf("failed to create sourceB: %w", err)
	}

	// 创建 trigger
	tm, ok := d.triggerManager.(*trigger.TriggerManager)
	if !ok {
		return fmt.Errorf("triggerManager type assertion failed")
	}

	tg := tm.NewTriggerWithMode(req.Symbol, sourceA, sourceB, trigger.ModeScheduled)

	// 配置 trigger
	configureTrigger(tg, resolvedAType, resolvedBType, aInfo, bInfo)

	// 设置默认配置
	tg = tg.
		SetSlippageOpt(trigger.DefaultSlippageOpt()).
		SetIntervalOpt(trigger.DefaultIntervalOpt()).
		SetOrderOpt(trigger.DefaultOrderOpt())

	// 添加 trigger
	if err := d.triggerManager.AddTrigger(req.Symbol, tg); err != nil {
		return fmt.Errorf("failed to add trigger: %w", err)
	}

	// 自动启动trigger
	ctx := d.triggerManager.GetTriggerContext()
	if err := tg.Start(ctx); err != nil {
		d.logger.Warnf("Failed to start trigger for %s: %v", req.Symbol, err)
		// 不返回错误，因为trigger已经创建成功
	}

	return nil
}

// StopTriggerInternal 内部方法，供自动化管理器调用
func (d *Dashboard) StopTriggerInternal(symbol string) error {
	tg, err := d.triggerManager.GetTrigger(symbol)
	if err != nil {
		return fmt.Errorf("trigger not found: %w", err)
	}

	if err := tg.Stop(); err != nil {
		return fmt.Errorf("failed to stop trigger: %w", err)
	}

	return nil
}

// handleGetTriggerData 获取 trigger 的实时数据
func (d *Dashboard) handleGetTriggerData(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol parameter is required", http.StatusBadRequest)
		return
	}

	tg, err := d.triggerManager.GetTrigger(symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	slippageData := tg.GetSlippageData()
	optimalThresholds := tg.GetOptimalThresholds()

	result := map[string]interface{}{
		"slippage":          slippageData,
		"optimalThresholds": optimalThresholds,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleGetTriggerPosition 获取 trigger 的持仓信息
func (d *Dashboard) handleGetTriggerPosition(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol parameter is required", http.StatusBadRequest)
		return
	}

	pm := position.GetPositionManager()
	if pm == nil {
		http.Error(w, "PositionManager not initialized", http.StatusInternalServerError)
		return
	}

	positionSummary := pm.GetSymbolPositionSummary(symbol)
	// 如果为 nil，可能是初始化尚未完成或没有数据，返回空对象而不是错误，方便前端处理
	if positionSummary == nil {
		positionSummary = &position.SymbolPositionSummary{
			Symbol:            symbol,
			ExchangePositions: []*position.ExchangePositionDetail{},
			OnchainBalances:   []*position.OnchainBalanceDetail{},
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(positionSummary)
}

// handleUpdateTelegramNotification 更新 Telegram 通知开关
func (d *Dashboard) handleUpdateTelegramNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Symbol  string `json:"symbol"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	tg, err := d.triggerManager.GetTrigger(req.Symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	tg.SetTelegramNotificationEnabled(req.Enabled)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "updated",
		"symbol":  req.Symbol,
		"enabled": req.Enabled,
	})
}

// handleClearPriceDiffs 清空 trigger 的历史价差数据
func (d *Dashboard) handleClearPriceDiffs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol parameter is required", http.StatusBadRequest)
		return
	}

	tg, err := d.triggerManager.GetTrigger(symbol)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := tg.ClearPriceDiffs(); err != nil {
		http.Error(w, "Failed to clear price diffs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared", "symbol": symbol})
}

// handleClearStatistics 清除 symbol 的历史统计数据和监控数据
func (d *Dashboard) handleClearStatistics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol parameter is required", http.StatusBadRequest)
		return
	}

	// 1. 清除统计数据
	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager != nil {
		if err := statisticsManager.ClearSymbolHistory(symbol); err != nil {
			d.logger.Errorf("Failed to clear statistics history for %s: %v", symbol, err)
			http.Error(w, "Failed to clear statistics history: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// 2. 清除监控数据
	monitorInstance := monitor.GetExecutionMonitor()
	if monitorInstance != nil {
		if err := monitorInstance.ClearHistory(symbol); err != nil {
			d.logger.Errorf("Failed to clear monitor history for %s: %v", symbol, err)
			http.Error(w, "Failed to clear monitor history: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "cleared", "symbol": symbol})
}

// handleGetStatistics 获取 symbol 的统计数据
func (d *Dashboard) handleGetStatistics(w http.ResponseWriter, r *http.Request) {
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		http.Error(w, "symbol parameter is required", http.StatusBadRequest)
		return
	}

	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		http.Error(w, "StatisticsManager not initialized", http.StatusInternalServerError)
		return
	}

	response := statisticsManager.GetStatisticsResponse(symbol)
	if response == nil {
		http.Error(w, "Statistics not found for symbol", http.StatusNotFound)
		return
	}

	// 从 trigger 获取 A 和 B 的显示名称，并添加到响应中
	tg, err := d.triggerManager.GetTrigger(symbol)
	if err == nil {
		// 获取 trigger 的 traderAType 和 traderBType，生成显示名称
		var aName, bName string
		if protoTrigger, ok := tg.(proto.Trigger); ok {
			traderAType := protoTrigger.GetTraderAType()
			traderBType := protoTrigger.GetTraderBType()
			aName = getTraderDisplayName(traderAType)
			bName = getTraderDisplayName(traderBType)
		}

		// 将 aName 和 bName 添加到响应中（如果 slippageStats 存在）
		if response.SlippageStats != nil {
			// 可以直接在 response 中添加，但 StatisticsResponse 结构可能没有这个字段
			// 所以我们将其添加到 slippageStats 的顶层
			responseMap := map[string]interface{}{
				"symbol":           response.Symbol,
				"slippageStats":    response.SlippageStats,
				"costStats":        response.CostStats,
				"sizeStats":        response.SizeStats,
				"triggerStats":     response.TriggerStats,
				"tradeRecords":     response.TradeRecords,
				"priceDiffRecords": response.PriceDiffRecords,
				"priceRecords":     response.PriceRecords,
				"aName":            aName,
				"bName":            bName,
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(responseMap)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// getTraderDisplayName 获取 trader 的显示名称
// 例如："binance:futures" -> "Binance"，"onchain:56" -> "Onchain"
func getTraderDisplayName(traderType string) string {
	if traderType == "" {
		return ""
	}

	// 解析 traderType：格式为 "type:value"（如 "binance:futures" 或 "onchain:56"）
	parts := strings.Split(traderType, ":")
	if len(parts) != 2 {
		return traderType // 如果格式不对，直接返回原值
	}

	traderTypeStr := parts[0]
	if traderTypeStr == "onchain" {
		return "Onchain"
	}

	// 交易所类型，首字母大写
	if len(traderTypeStr) > 0 {
		return strings.ToUpper(traderTypeStr[:1]) + traderTypeStr[1:]
	}
	return traderTypeStr
}

// handleConfig 处理系统配置的获取和更新
func (d *Dashboard) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 获取当前配置
		cfg := config.GetSelfConfigWebEncrypted()
		if cfg == nil {
			http.Error(w, "Config not initialized", http.StatusInternalServerError)
			return
		}

		// 将配置转换为 JSON 字符串
		configJSON, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			http.Error(w, "Failed to marshal config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"config": string(configJSON),
		})

	case http.MethodPost:
		// 更新配置
		var req struct {
			Config string `json:"config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.Config == "" {
			http.Error(w, "config is required", http.StatusBadRequest)
			return
		}

		// 验证 JSON 格式
		var testCfg config.SelfConfig
		if err := json.Unmarshal([]byte(req.Config), &testCfg); err != nil {
			http.Error(w, "Invalid config JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		// 设置配置
		if err := config.SetSelfConfigWeb(req.Config); err != nil {
			http.Error(w, "Failed to set config: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 配置更新后，重新初始化相关组件

		// 注意：OkEx 密钥配置不在 Web API 中更新，只能通过配置文件修改
		// 如果需要更新 OkEx 密钥，需要修改配置文件并重启服务，或通过其他方式调用 ClearOkexKeyCache() 和 Reinit()

		// 1. 重新初始化所有 traders（使用新的 API 密钥）
		if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
			if err := tm.ReinitTraders(); err != nil {
				d.logger.Warnf("重新初始化 traders 失败: %v", err)
			} else {
				// 同步更新 WalletManager 中的 traders
				walletManager := position.GetWalletManager()
				if walletManager != nil {
					tradersList := tm.GetAllTraders()
					walletManager.UpdateTraders(tradersList)
					d.logger.Infof("✅ 已同步更新 WalletManager 中的 traders，共 %d 个", len(tradersList))
				}
			}
		}

		// 2. 如果钱包配置已更新，尝试更新 WalletManager 的链上客户端配置
		position.UpdateOnchainClientConfig()

		// 4. 如果tg配置更新，尝试更新tg客户端配置
		telegram.UpdateFromGlobalConfig()

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleGetWallet 处理获取钱包信息的请求
func (d *Dashboard) handleGetWallet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 获取 WalletManager
	walletManager := position.GetWalletManager()
	if walletManager == nil {
		http.Error(w, "WalletManager not initialized", http.StatusInternalServerError)
		return
	}

	// 获取钱包信息
	walletInfo := walletManager.GetWalletInfo()
	if walletInfo == nil {
		http.Error(w, "Wallet info not available", http.StatusInternalServerError)
		return
	}

	// 获取最后刷新时间
	lastRefreshTime := walletManager.GetLastRefreshTime()

	// 返回 JSON 响应
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"walletInfo":      walletInfo,
		"lastRefreshTime": lastRefreshTime,
	})
}

// handleGetWalletHistory 获取钱包历史数据
func (d *Dashboard) handleGetWalletHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 解析时间参数
	var start, end int64

	// 默认最近 7 天
	end = time.Now().Unix()
	start = time.Now().AddDate(0, 0, -7).Unix()

	// 从查询参数获取
	startParam := r.URL.Query().Get("start")
	if startParam != "" {
		fmt.Sscanf(startParam, "%d", &start)
	}
	endParam := r.URL.Query().Get("end")
	if endParam != "" {
		fmt.Sscanf(endParam, "%d", &end)
	}

	statisticsManager := statistics.GetStatisticsManager()
	if statisticsManager == nil {
		http.Error(w, "StatisticsManager not initialized", http.StatusInternalServerError)
		return
	}

	history, err := statisticsManager.GetAssetHistory(start, end)
	if err != nil {
		d.logger.Errorf("Failed to get asset history: %v", err)
		http.Error(w, "Failed to get asset history: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// handleGetVersion 获取版本信息
func (d *Dashboard) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"version": version.GetVersion(),
	})
}

// handleAutomationConfig 处理自动化配置的获取和更新
func (d *Dashboard) handleAutomationConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// 获取当前配置
		cfg := config.GetGlobalConfig()
		if cfg == nil {
			http.Error(w, "Config not initialized", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cfg.Automation)

	case http.MethodPost:
		// 更新配置
		var req model.AutomationConfig
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// 更新全局配置
		cfg := config.GetGlobalConfig()
		cfg.Automation = req

		// 通知自动化管理器更新配置（如果已初始化）
		if d.automationManager != nil {
			d.automationManager.UpdateConfig(req)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleAutomationStatus 获取自动化运行状态
func (d *Dashboard) handleAutomationStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if d.automationManager == nil {
		http.Error(w, "Automation manager not initialized", http.StatusInternalServerError)
		return
	}

	status := d.automationManager.GetStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleAutomationControl 处理自动化启动/停止
func (d *Dashboard) handleAutomationControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if d.automationManager == nil {
		http.Error(w, "Automation manager not initialized", http.StatusInternalServerError)
		return
	}

	var req struct {
		Action string `json:"action"` // "start" 或 "stop"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "start":
		if err := d.automationManager.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "started"})

	case "stop":
		d.automationManager.Stop()
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})

	default:
		http.Error(w, "Invalid action, must be 'start' or 'stop'", http.StatusBadRequest)
	}
}

// handleDeposit 获取充币地址
func (d *Dashboard) handleDeposit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	exchangeType := r.URL.Query().Get("exchange")
	asset := r.URL.Query().Get("asset")
	network := r.URL.Query().Get("network")

	if exchangeType == "" || asset == "" {
		http.Error(w, "exchange and asset parameters are required", http.StatusBadRequest)
		return
	}

	// 获取交易所实例
	ex := d.getExchange(exchangeType)
	if ex == nil {
		http.Error(w, fmt.Sprintf("exchange %s not found", exchangeType), http.StatusNotFound)
		return
	}

	// 检查是否支持充提币
	depositProvider, ok := ex.(exchange.DepositWithdrawProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("exchange %s does not support deposit/withdraw", exchangeType), http.StatusNotImplemented)
		return
	}

	address, err := depositProvider.Deposit(asset, network)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(address)
}

// handleWithdraw 提币
func (d *Dashboard) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Exchange string                `json:"exchange"`
		Request  model.WithdrawRequest `json:"request"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Exchange == "" {
		http.Error(w, "exchange is required", http.StatusBadRequest)
		return
	}

	// 获取交易所实例
	ex := d.getExchange(req.Exchange)
	if ex == nil {
		http.Error(w, fmt.Sprintf("exchange %s not found", req.Exchange), http.StatusNotFound)
		return
	}

	// 检查是否支持充提币
	depositProvider, ok := ex.(exchange.DepositWithdrawProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("exchange %s does not support deposit/withdraw", req.Exchange), http.StatusNotImplemented)
		return
	}

	response, err := depositProvider.Withdraw(&req.Request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleDepositHistory 查询充币记录
func (d *Dashboard) handleDepositHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	exchangeType := r.URL.Query().Get("exchange")
	asset := r.URL.Query().Get("asset")
	limitStr := r.URL.Query().Get("limit")

	if exchangeType == "" {
		http.Error(w, "exchange parameter is required", http.StatusBadRequest)
		return
	}

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// 获取交易所实例
	ex := d.getExchange(exchangeType)
	if ex == nil {
		http.Error(w, fmt.Sprintf("exchange %s not found", exchangeType), http.StatusNotFound)
		return
	}

	// 检查是否支持充提币
	depositProvider, ok := ex.(exchange.DepositWithdrawProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("exchange %s does not support deposit/withdraw", exchangeType), http.StatusNotImplemented)
		return
	}

	records, err := depositProvider.GetDepositHistory(asset, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// handleWithdrawHistory 查询提币记录
func (d *Dashboard) handleWithdrawHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	exchangeType := r.URL.Query().Get("exchange")
	asset := r.URL.Query().Get("asset")
	limitStr := r.URL.Query().Get("limit")

	if exchangeType == "" {
		http.Error(w, "exchange parameter is required", http.StatusBadRequest)
		return
	}

	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// 获取交易所实例
	ex := d.getExchange(exchangeType)
	if ex == nil {
		http.Error(w, fmt.Sprintf("exchange %s not found", exchangeType), http.StatusNotFound)
		return
	}

	// 检查是否支持充提币
	depositProvider, ok := ex.(exchange.DepositWithdrawProvider)
	if !ok {
		http.Error(w, fmt.Sprintf("exchange %s does not support deposit/withdraw", exchangeType), http.StatusNotImplemented)
		return
	}

	records, err := depositProvider.GetWithdrawHistory(asset, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(records)
}

// handleBridgeToken 跨链转账
func (d *Dashboard) handleBridgeToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.BridgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// 获取链上客户端
	onchainClient := d.getOnchainClient()
	if onchainClient == nil {
		http.Error(w, "onchain client not initialized", http.StatusInternalServerError)
		return
	}

	response, err := onchainClient.BridgeToken(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleBridgeStatus 查询跨链状态
func (d *Dashboard) handleBridgeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	txHash := r.URL.Query().Get("txHash")
	fromChain := r.URL.Query().Get("fromChain")
	toChain := r.URL.Query().Get("toChain")

	if txHash == "" || fromChain == "" || toChain == "" {
		http.Error(w, "txHash, fromChain, and toChain parameters are required", http.StatusBadRequest)
		return
	}

	// 获取链上客户端
	onchainClient := d.getOnchainClient()
	if onchainClient == nil {
		http.Error(w, "onchain client not initialized", http.StatusInternalServerError)
		return
	}

	status, err := onchainClient.GetBridgeStatus(txHash, fromChain, toChain)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handleBridgeQuote 获取跨链报价
func (d *Dashboard) handleBridgeQuote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req model.BridgeQuoteRequest
	req.FromChain = r.URL.Query().Get("fromChain")
	req.ToChain = r.URL.Query().Get("toChain")
	req.FromToken = r.URL.Query().Get("fromToken")
	req.ToToken = r.URL.Query().Get("toToken")
	req.Amount = r.URL.Query().Get("amount")

	if req.FromChain == "" || req.ToChain == "" || req.FromToken == "" || req.ToToken == "" || req.Amount == "" {
		http.Error(w, "fromChain, toChain, fromToken, toToken, and amount parameters are required", http.StatusBadRequest)
		return
	}

	// 获取链上客户端
	onchainClient := d.getOnchainClient()
	if onchainClient == nil {
		http.Error(w, "onchain client not initialized", http.StatusInternalServerError)
		return
	}

	quote, err := onchainClient.GetBridgeQuote(&req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(quote)
}

// getExchange 获取交易所实例（辅助函数）
func (d *Dashboard) getExchange(exchangeType string) exchange.Exchange {
	if d.triggerManager == nil {
		return nil
	}

	// 从 triggerManager 获取交易所实例
	// 需要将 exchangeType 字符串转换为 ExchangeType 常量
	var exchangeTypeConst constants.ExchangeType
	switch exchangeType {
	case "binance":
		exchangeTypeConst = constants.ExchangeBinance
	case "bybit":
		exchangeTypeConst = constants.ExchangeByBit
	case "bitget":
		exchangeTypeConst = constants.ExchangeBitGet
	case "gate":
		exchangeTypeConst = constants.ExchangeGate
	case "okex":
		exchangeTypeConst = constants.ExchangeOKEX
	case "hyperliquid":
		exchangeTypeConst = constants.ExchangeHyperliquid
	case "lighter":
		exchangeTypeConst = constants.ExchangeLighter
	case "aster":
		exchangeTypeConst = constants.ExchangeAster
	default:
		return nil
	}

	traderInterface := d.triggerManager.GetExchangeSource(exchangeTypeConst)
	if traderInterface == nil {
		return nil
	}

	if ex, ok := traderInterface.(exchange.Exchange); ok {
		return ex
	}
	if c, ok := traderInterface.(*trader.CexTrader); ok {
		return c.GetExchange()
	}

	return nil
}

// getOnchainClient 获取链上客户端（辅助函数）
// TODO: 实现从全局管理器获取 OnchainClient 的逻辑
func (d *Dashboard) getOnchainClient() onchain.OnchainClient {
	// 暂时返回 nil，后续可以从 WalletManager 或其他全局管理器获取
	// 实际实现应该：
	// 1. 从 WalletManager 获取 OnchainClientConfig
	// 2. 或从某个全局的 OnchainClient 管理器获取
	return nil
}

// ============================================================================
// Pipeline API 处理器
// ============================================================================

// chainIDToNetworkForEdge 将链 ID 转为交易所提币网络名（与 pipeline 执行层一致），用于 ex→ex 边缺 network 时兜底。
func chainIDToNetworkForEdge(chainID string) string {
	m := map[string]string{
		"1": "ERC20", "56": "BEP20", "137": "POLYGON", "42161": "ARBITRUM",
		"10": "OPTIMISM", "43114": "AVAXC", "8453": "BASE", "250": "FTM", "25": "CRO",
	}
	if n, ok := m[chainID]; ok {
		return n
	}
	return ""
}

// normalizeOnchainChainID 将 onchain 节点 type 解析出的 chainID 规范为纯数字链 ID（如 "1-2" -> "1"），供 GetOnchainClient、CCIP 等使用。
func normalizeOnchainChainID(chainID string) string {
	if chainID == "" {
		return chainID
	}
	if idx := strings.Index(chainID, "-"); idx > 0 && idx < len(chainID) {
		first := strings.TrimSpace(chainID[:idx])
		if first != "" {
			allDigits := true
			for _, c := range first {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return first
			}
		}
	}
	return chainID
}

// buildPipelineFromNodeMaps 从 nodes/edges 的 map 列表构建 pipeline.Node 与 EdgeConfig；供 create 与搬砖 apply 共用。
// defaultAsset 在节点未填 asset 时使用（如搬砖场景用 trigger 的 base 币）。
func (d *Dashboard) buildPipelineFromNodeMaps(reqNodes []map[string]interface{}, reqEdges []map[string]interface{}, defaultAsset string) (nodes []pipeline.Node, edges []*pipeline.EdgeConfig, needBridge bool, err error) {
	walletManager := position.GetWalletManager()
	if walletManager == nil {
		return nil, nil, false, fmt.Errorf("wallet manager not initialized")
	}
	globalConfig := config.GetGlobalConfig()
	if globalConfig == nil {
		return nil, nil, false, fmt.Errorf("global config not initialized")
	}

	nodes = make([]pipeline.Node, 0, len(reqNodes))
	for i, nodeConfig := range reqNodes {
		nodeType, ok := nodeConfig["type"].(string)
		if !ok || nodeType == "" {
			return nil, nil, false, fmt.Errorf("node %d: type is required", i)
		}
		asset, _ := nodeConfig["asset"].(string)
		if asset == "" {
			asset = defaultAsset
		}
		if asset == "" {
			return nil, nil, false, fmt.Errorf("node %d: asset is required", i)
		}

		var node pipeline.Node
		if strings.HasPrefix(nodeType, "onchain:") {
			chainID := strings.TrimPrefix(nodeType, "onchain:")
			chainID = normalizeOnchainChainID(chainID) // "1-2" -> "1"，供 GetOnchainClient 与跨链 CCIP 使用
			walletAddress, _ := nodeConfig["walletAddress"].(string)
			if walletAddress == "" {
				walletAddress = globalConfig.Wallet.WalletAddress
			}
			if walletAddress == "" {
				return nil, nil, false, fmt.Errorf("node %d: walletAddress is required for onchain node", i)
			}
			onchainClient := walletManager.GetOnchainClient(chainID)
			if onchainClient == nil {
				return nil, nil, false, fmt.Errorf("node %d: 链 %s 的 onchain 客户端未配置（chain %s not found）。请先在「钱包/链配置」中配置该链的 RPC 与钱包地址；当前默认仅支持 BSC(56)，如需 Ethereum(1) 等需在配置中增加", i, chainID, chainID)
			}
			tokenAddress, _ := nodeConfig["tokenAddress"].(string)
			// 如果 tokenAddress 为空，尝试多种方式获取该链上正确的代币地址
			if tokenAddress == "" && asset != "" {
				// 1. 优先从 OFT 注册表获取（每条链有独立的合约地址，最准确）
				reg := d.getOrCreateBridgeOFTRegistry()
				if reg != nil {
					if t, ok := reg.Get(chainID, asset); ok && t.Address != "" {
						tokenAddress = t.Address
						d.logger.Infof("Got token address from OFT registry for %s on chain %s: %s", asset, chainID, tokenAddress)
					}
				}
				// 2. 回退：从全局配置的 OFT contracts 获取（config.Bridge.LayerZero.OFTContracts）
				if tokenAddress == "" && globalConfig != nil && globalConfig.Bridge.LayerZero.OFTContracts != nil {
					oftKey := chainID + ":" + asset
					if addr, ok := globalConfig.Bridge.LayerZero.OFTContracts[oftKey]; ok && addr != "" {
						tokenAddress = addr
						d.logger.Infof("Got token address from config OFTContracts for %s on chain %s: %s", asset, chainID, tokenAddress)
					}
				}
				// 3. 回退：从 WalletInfo（OKX DEX API）获取（每条链有独立的合约地址，较准确）
				if tokenAddress == "" {
					if wm := position.GetWalletManager(); wm != nil {
						if wi := wm.GetWalletInfo(); wi != nil && wi.OnchainBalances != nil {
							if symbolMap, ok := wi.OnchainBalances[chainID]; ok {
								for sym, asset2 := range symbolMap {
									if strings.EqualFold(sym, asset) && asset2.TokenContractAddress != "" {
										tokenAddress = asset2.TokenContractAddress
										d.logger.Infof("Got token address from WalletInfo (OKX DEX API) for %s on chain %s: %s", asset, chainID, tokenAddress)
										break
									}
								}
							}
						}
					}
				}
				// 4. 回退：从 token mapping 按链获取
				if tokenAddress == "" {
					mappingMgr := token_mapping.GetTokenMappingManager()
					if addr, err := mappingMgr.GetAddressBySymbol(asset, chainID); err == nil && addr != "" {
						tokenAddress = addr
						d.logger.Infof("Got token address from token mapping for %s on chain %s: %s", asset, chainID, tokenAddress)
					}
				}
			}
			nid, _ := nodeConfig["id"].(string)
			nodeName, _ := nodeConfig["name"].(string)
			node = pipeline.NewOnchainNode(pipeline.OnchainNodeConfig{
				ID: nid, Name: nodeName, ChainID: chainID, AssetSymbol: asset, TokenAddress: tokenAddress,
				WalletAddress: walletAddress, Client: onchainClient,
			})
		} else if strings.HasPrefix(nodeType, "bridge:") {
			return nil, nil, false, fmt.Errorf("node %d: 跨链已改为边行为，请使用两个 OnchainNode 并在边配置 BridgeProtocol", i)
		} else {
			network, _ := nodeConfig["network"].(string)
			if network == "" {
				network = "ERC20"
			}
			nid, _ := nodeConfig["id"].(string)
			nodeName, _ := nodeConfig["name"].(string)
			node = pipeline.NewExchangeNode(pipeline.ExchangeNodeConfig{
				ID: nid, Name: nodeName, ExchangeType: nodeType, Asset: asset, DefaultNetwork: network,
			})
		}
		nodes = append(nodes, node)
	}

	edges = make([]*pipeline.EdgeConfig, 0, len(reqEdges))
	for i, edgeConfig := range reqEdges {
		fromIdx, ok1 := edgeConfig["from"].(float64)
		toIdx, ok2 := edgeConfig["to"].(float64)
		if !ok1 || !ok2 {
			return nil, nil, false, fmt.Errorf("edge %d: from and to indices are required", i)
		}
		fi, ti := int(fromIdx), int(toIdx)
		if fi < 0 || fi >= len(nodes) || ti < 0 || ti >= len(nodes) {
			return nil, nil, false, fmt.Errorf("edge %d: invalid node indices", i)
		}
		fromNode, toNode := nodes[fi], nodes[ti]
		amountTypeStr, _ := edgeConfig["amountType"].(string)
		var amountType pipeline.AmountType
		switch amountTypeStr {
		case "fixed":
			amountType = pipeline.AmountTypeFixed
		case "percentage":
			amountType = pipeline.AmountTypePercentage
		default:
			amountType = pipeline.AmountTypeAll
		}
		amount := 0.0
		if amt, ok := edgeConfig["amount"].(float64); ok {
			amount = amt
		}
		network, _ := edgeConfig["network"].(string)
		memo, _ := edgeConfig["memo"].(string)
		bridgeProtocol, _ := edgeConfig["bridgeProtocol"].(string)
		asset, _ := edgeConfig["asset"].(string)
		chainID, _ := edgeConfig["chainId"].(string)
		if chainID == "" {
			chainID, _ = edgeConfig["chainID"].(string)
		}
		// 交易所→交易所边：若未填 network 但有 chainId（提现链），用 chainId 推导 Network，避免 OKX 等 51000 Parameter chainName error。
		// 应用时 edges 来自 cfg.BackwardEdges/ForwardEdges（路由探测→应用保存），含 from/to 索引及可选的 network/chainId，此处补全后写入 EdgeConfig。
		if network == "" && chainID != "" && fromNode.GetType() == pipeline.NodeTypeExchange && toNode.GetType() == pipeline.NodeTypeExchange {
			network = chainIDToNetworkForEdge(chainID)
		}
		maxWaitTime := 30 * time.Minute
		if mwt, ok := edgeConfig["maxWaitTime"].(float64); ok {
			maxWaitTime = time.Duration(mwt) * time.Second
		}
		checkInterval := 10 * time.Second
		if ci, ok := edgeConfig["checkInterval"].(float64); ok {
			checkInterval = time.Duration(ci) * time.Second
		}
		confirmations := 12
		if conf, ok := edgeConfig["confirmations"].(float64); ok {
			confirmations = int(conf)
		}
		edges = append(edges, &pipeline.EdgeConfig{
			FromNodeID: fromNode.GetID(), ToNodeID: toNode.GetID(),
			AmountType: amountType, Amount: amount, Network: network, Memo: memo,
			Asset: asset, ChainID: chainID,
			BridgeProtocol: bridgeProtocol, MaxWaitTime: maxWaitTime, CheckInterval: checkInterval, Confirmations: confirmations,
		})
	}

	for _, edgeConfig := range reqEdges {
		fromIdx, ok1 := edgeConfig["from"].(float64)
		toIdx, ok2 := edgeConfig["to"].(float64)
		if !ok1 || !ok2 {
			continue
		}
		fi, ti := int(fromIdx), int(toIdx)
		if fi < 0 || fi >= len(reqNodes) || ti < 0 || ti >= len(reqNodes) {
			continue
		}
		fromType, _ := reqNodes[fi]["type"].(string)
		toType, _ := reqNodes[ti]["type"].(string)
		if !strings.HasPrefix(fromType, "onchain:") || !strings.HasPrefix(toType, "onchain:") {
			continue
		}
		fromChain := normalizeOnchainChainID(strings.TrimPrefix(fromType, "onchain:"))
		toChain := normalizeOnchainChainID(strings.TrimPrefix(toType, "onchain:"))
		if fromChain != "" && toChain != "" && fromChain != toChain {
			needBridge = true
			break
		}
	}
	return nodes, edges, needBridge, nil
}

// handleCreatePipeline 创建 pipeline
func (d *Dashboard) handleCreatePipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name  string                   `json:"name"`
		Nodes []map[string]interface{} `json:"nodes"` // 节点配置列表
		Edges []map[string]interface{} `json:"edges"` // 边配置列表（可选）
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if len(req.Nodes) == 0 {
		http.Error(w, "at least one node is required", http.StatusBadRequest)
		return
	}

	nodes, edges, needBridge, err := d.buildPipelineFromNodeMaps(req.Nodes, req.Edges, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var bridgeMgr *bridge.Manager
	if needBridge {
		bridgeMgr = bridge.NewManager(true)
		rpcURLs := constants.GetAllDefaultRPCURLs()
		if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.LayerZero.RPCURLs != nil {
			for chainID, url := range cfg.Bridge.LayerZero.RPCURLs {
				if url != "" {
					rpcURLs[chainID] = url
				}
			}
		}
		lz := layerzero.NewLayerZero(rpcURLs, true)
		reg := d.getOrCreateBridgeOFTRegistry()
		lz.SetOFTRegistry(reg)
		applyOFTContractsFromConfig(lz)
		bridgeMgr.RegisterProtocol(lz)
		wh := wormhole.NewWormhole(getWormholeRPCURLs(), getWormholeEnabled())
		applyWormholeTokenContractsFromConfig(wh)
		bridgeMgr.RegisterProtocol(wh)
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.CreatePipeline(req.Name, nodes, edges)
	if err != nil {
		d.logger.Errorf("Failed to create pipeline: %v", err)
		http.Error(w, "Failed to create pipeline: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if needBridge && bridgeMgr != nil {
		p.SetBridgeManager(bridgeMgr)
		d.logger.Infof("Pipeline %s: BridgeManager injected for chain-to-chain edges", p.ID())
	}

	d.logger.Infof("Pipeline created: %s (ID: %s) with %d nodes", p.Name(), p.ID(), len(nodes))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"id":     p.ID(),
		"name":   p.Name(),
		"nodes":  len(nodes),
		"edges":  len(edges),
	})
}

// handleRunPipeline 执行 pipeline
func (d *Dashboard) handleRunPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pipelineID := r.URL.Query().Get("id")
	if pipelineID == "" {
		http.Error(w, "pipeline id is required", http.StatusBadRequest)
		return
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.GetPipeline(pipelineID)
	if err != nil {
		http.Error(w, "pipeline not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// 检查 pipeline 状态
	if p.Status() == pipeline.PipelineStatusRunning {
		http.Error(w, "pipeline is already running", http.StatusBadRequest)
		return
	}

	// 在 goroutine 中异步执行
	go func() {
		d.logger.Infof("Starting pipeline execution: %s (ID: %s)", p.Name(), p.ID())
		if err := p.Run(); err != nil {
			d.logger.Errorf("Pipeline %s execution failed: %v", pipelineID, err)
		} else {
			d.logger.Infof("Pipeline %s execution completed", pipelineID)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "started",
		"id":     pipelineID,
	})
}

// handlePipelineStatus 查询 pipeline 状态
func (d *Dashboard) handlePipelineStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pipelineID := r.URL.Query().Get("id")
	if pipelineID == "" {
		http.Error(w, "pipeline id is required", http.StatusBadRequest)
		return
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.GetPipeline(pipelineID)
	if err != nil {
		http.Error(w, "pipeline not found: "+err.Error(), http.StatusNotFound)
		return
	}

	// 获取节点信息（包括余额）
	nodes := p.Nodes()
	nodeInfos := make([]map[string]interface{}, 0, len(nodes))
	for _, node := range nodes {
		balance, _ := node.CheckBalance()
		availableBalance, _ := node.GetAvailableBalance()

		nodeInfo := map[string]interface{}{
			"id":               node.GetID(),
			"name":             node.GetName(),
			"type":             string(node.GetType()),
			"asset":            node.GetAsset(),
			"balance":          balance,
			"availableBalance": availableBalance,
			"canDeposit":       node.CanDeposit(),
			"canWithdraw":      node.CanWithdraw(),
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}

	// 获取边配置信息（含 step 与 bridgeProtocol，便于前端按步骤显示跨链协议）
	edges := make([]map[string]interface{}, 0)
	for i := 0; i < len(nodes)-1; i++ {
		fromNode := nodes[i]
		toNode := nodes[i+1]
		edge, ok := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
		if ok {
			edgeInfo := map[string]interface{}{
				"from":          fromNode.GetID(),
				"to":            toNode.GetID(),
				"step":          i + 1,
				"amountType":    string(edge.AmountType),
				"amount":        edge.Amount,
				"network":       edge.Network,
				"maxWaitTime":   edge.MaxWaitTime.Seconds(),
				"checkInterval": edge.CheckInterval.Seconds(),
				"confirmations": edge.Confirmations,
			}
			if edge.BridgeProtocol != "" {
				edgeInfo["bridgeProtocol"] = edge.BridgeProtocol
			}
			edges = append(edges, edgeInfo)
		}
	}

	status := map[string]interface{}{
		"id":          p.ID(),
		"name":        p.Name(),
		"status":      string(p.Status()),
		"currentStep": p.CurrentStep(),
		"nodes":       nodeInfos,
		"edges":       edges,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// handlePipelineNodes 管理 pipeline 节点（添加/删除/插入）
func (d *Dashboard) handlePipelineNodes(w http.ResponseWriter, r *http.Request) {
	pipelineID := r.URL.Query().Get("id")
	if pipelineID == "" {
		http.Error(w, "pipeline id is required", http.StatusBadRequest)
		return
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.GetPipeline(pipelineID)
	if err != nil {
		http.Error(w, "pipeline not found: "+err.Error(), http.StatusNotFound)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// 添加或插入节点
		var req struct {
			Action string                 `json:"action"` // "add" 或 "insert"
			Index  int                    `json:"index"`  // 插入位置（仅 insert 时需要）
			Node   map[string]interface{} `json:"node"`   // 节点配置
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// 解析节点配置（简化版本，实际应该复用 handleCreatePipeline 中的逻辑）
		// 这里只做基本验证，实际创建节点需要完整的配置
		nodeType, _ := req.Node["type"].(string)
		if nodeType == "" {
			http.Error(w, "node type is required", http.StatusBadRequest)
			return
		}

		// 注意：这里简化处理，实际应该完整实现节点创建逻辑
		// 为了简化，这里只返回成功，实际应该创建节点并添加到 pipeline
		d.logger.Warnf("Node add/insert not fully implemented, node type: %s", nodeType)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Node add/insert functionality needs full implementation",
		})

	case http.MethodDelete:
		// 删除节点
		nodeID := r.URL.Query().Get("nodeId")
		if nodeID == "" {
			http.Error(w, "nodeId is required", http.StatusBadRequest)
			return
		}

		if err := p.RemoveNode(nodeID); err != nil {
			http.Error(w, "Failed to remove node: "+err.Error(), http.StatusInternalServerError)
			return
		}

		d.logger.Infof("Node %s removed from pipeline %s", nodeID, pipelineID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleCheckAvailability 检查节点可充提币能力
func (d *Dashboard) handleCheckAvailability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeType string `json:"nodeType"` // 节点类型（如 "binance", "onchain:56", "bridge:auto"）
		Asset    string `json:"asset"`    // 资产符号
		Network  string `json:"network"`  // 网络（可选）
		// 其他节点特定配置
		WalletAddress string `json:"walletAddress"` // 链上节点需要
		ChainID       string `json:"chainID"`       // 链上节点需要
		FromChain     string `json:"fromChain"`     // 跨链桥需要
		ToChain       string `json:"toChain"`       // 跨链桥需要
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.NodeType == "" {
		http.Error(w, "nodeType is required", http.StatusBadRequest)
		return
	}

	result := map[string]interface{}{
		"nodeType": req.NodeType,
		"asset":    req.Asset,
		"network":  req.Network,
	}

	// 根据节点类型创建临时节点并检查
	if strings.HasPrefix(req.NodeType, "onchain:") {
		// 链上节点
		chainID := strings.TrimPrefix(req.NodeType, "onchain:")
		if chainID == "" {
			chainID = req.ChainID
		}
		if chainID == "" {
			http.Error(w, "chainID is required for onchain node", http.StatusBadRequest)
			return
		}

		walletAddress := req.WalletAddress
		if walletAddress == "" {
			globalConfig := config.GetGlobalConfig()
			if globalConfig != nil {
				walletAddress = globalConfig.Wallet.WalletAddress
			}
		}
		if walletAddress == "" {
			http.Error(w, "walletAddress is required for onchain node", http.StatusBadRequest)
			return
		}

		walletManager := position.GetWalletManager()
		if walletManager == nil {
			http.Error(w, "wallet manager not initialized", http.StatusInternalServerError)
			return
		}

		onchainClient := walletManager.GetOnchainClient(chainID)
		if onchainClient == nil {
			http.Error(w, fmt.Sprintf("onchain client for chain %s not found", chainID), http.StatusBadRequest)
			return
		}

		node := pipeline.NewOnchainNode(pipeline.OnchainNodeConfig{
			ChainID:       chainID,
			AssetSymbol:   req.Asset,
			WalletAddress: walletAddress,
			Client:        onchainClient,
		})

		canDeposit := node.CanDeposit()
		canWithdraw := node.CanWithdraw()

		depositAvailable := false
		depositErr := ""
		if canDeposit {
			available, err := node.CheckDepositAvailability(req.Network)
			depositAvailable = available
			if err != nil {
				depositErr = err.Error()
			}
		}

		result["canDeposit"] = canDeposit
		result["canWithdraw"] = canWithdraw
		result["depositAvailable"] = depositAvailable
		if depositErr != "" {
			result["depositError"] = depositErr
		}

	} else if strings.HasPrefix(req.NodeType, "bridge:") {
		// 跨链为边上的行为，检查链对是否支持跨链时直接使用 bridge.Manager
		if req.FromChain == "" || req.ToChain == "" {
			http.Error(w, "fromChain and toChain are required for bridge check", http.StatusBadRequest)
			return
		}

		bridgeMgr := bridge.NewManager(true)
		rpcURLs := constants.GetAllDefaultRPCURLs()
		if cfg := config.GetGlobalConfig(); cfg != nil && cfg.Bridge.LayerZero.RPCURLs != nil {
			for chainID, url := range cfg.Bridge.LayerZero.RPCURLs {
				if url != "" {
					rpcURLs[chainID] = url
				}
			}
		}
		lz := layerzero.NewLayerZero(rpcURLs, true)
		reg := d.getOrCreateBridgeOFTRegistry()
		lz.SetOFTRegistry(reg)
		applyOFTContractsFromConfig(lz)
		bridgeMgr.RegisterProtocol(lz)
		wh := wormhole.NewWormhole(getWormholeRPCURLs(), getWormholeEnabled())
		applyWormholeTokenContractsFromConfig(wh)
		bridgeMgr.RegisterProtocol(wh)

		quoteReq := &model.BridgeQuoteRequest{
			FromChain: req.FromChain,
			ToChain:   req.ToChain,
			FromToken: req.Asset,
			ToToken:   req.Asset,
			Amount:    "0",
		}
		quote, err := bridgeMgr.GetBridgeQuote(quoteReq)
		available := false
		if err == nil && quote != nil && len(quote.Protocols) > 0 {
			for _, pq := range quote.Protocols {
				if pq.Supported {
					available = true
					break
				}
			}
		}
		result["available"] = available
		if err != nil {
			result["error"] = err.Error()
		}

	} else {
		// 交易所节点
		if req.Asset == "" {
			http.Error(w, "asset is required for exchange node", http.StatusBadRequest)
			return
		}

		network := req.Network
		if network == "" {
			network = "ERC20" // 默认网络
		}

		node := pipeline.NewExchangeNode(pipeline.ExchangeNodeConfig{
			ExchangeType:   req.NodeType,
			Asset:          req.Asset,
			DefaultNetwork: network,
		})

		canDeposit := node.CanDeposit()
		canWithdraw := node.CanWithdraw()

		depositAvailable := false
		depositErr := ""
		withdrawAvailable := false
		withdrawErr := ""

		if canDeposit {
			available, err := node.CheckDepositAvailability(network)
			depositAvailable = available
			if err != nil {
				depositErr = err.Error()
			}
		}

		if canWithdraw {
			available, err := node.CheckWithdrawAvailability(network)
			withdrawAvailable = available
			if err != nil {
				withdrawErr = err.Error()
			}
		}

		result["canDeposit"] = canDeposit
		result["canWithdraw"] = canWithdraw
		result["depositAvailable"] = depositAvailable
		result["withdrawAvailable"] = withdrawAvailable
		if depositErr != "" {
			result["depositError"] = depositErr
		}
		if withdrawErr != "" {
			result["withdrawError"] = withdrawErr
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleListPipelines 列出所有 pipeline
func (d *Dashboard) handleListPipelines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pm := pipeline.GetPipelineManager()
	pipelines := pm.ListPipelines()

	pipelineInfos := make([]map[string]interface{}, 0, len(pipelines))
	for _, p := range pipelines {
		nodes := p.Nodes()
		pipelineInfo := map[string]interface{}{
			"id":          p.ID(),
			"name":        p.Name(),
			"status":      string(p.Status()),
			"currentStep": p.CurrentStep(),
			"nodeCount":   len(nodes),
		}
		pipelineInfos = append(pipelineInfos, pipelineInfo)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pipelines": pipelineInfos,
		"count":     len(pipelineInfos),
	})
}
