package monitor

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"auto-arbitrage/internal/utils/logger"
	"auto-arbitrage/internal/utils/notify/telegram"
	"auto-arbitrage/internal/web/ws"
)

// ExecutionStatus 定义执行状态
type ExecutionStatus string

const (
	StatusPending       ExecutionStatus = "Pending"       // 待交易/进行中
	StatusSuccess       ExecutionStatus = "Success"       // 成功
	StatusFailed        ExecutionStatus = "Failed"        // 失败
	StatusNotApplicable ExecutionStatus = "N/A"           // 不适用（如纯交易所-交易所交易，不涉及链上）
	StatusFiltered      ExecutionStatus = "Filtered"      // 被过滤（如价格趋势检查未通过）
)

// ExecutionDetail 描述单边（链上或交易所）的具体执行情况
type ExecutionDetail struct {
	Status   ExecutionStatus `json:"status"`             // 状态
	ErrorMsg string          `json:"error_msg"`          // 错误信息（如有）
	Size     float64         `json:"size"`               // 实际成交数量
	Price    float64         `json:"price"`              // 实际成交价格
	TxHash   string          `json:"tx_hash,omitempty"`  // 链上：交易哈希
	OrderID  string          `json:"order_id,omitempty"` // 交易所：订单ID
}

// ExecutionRecord 描述一次完整的套利尝试
type ExecutionRecord struct {
	ID          string    `json:"id"`           // 唯一ID
	StartTime   time.Time `json:"start_time"`   // 开始时间
	EndTime     time.Time `json:"end_time"`     // 结束时间
	Symbol      string    `json:"symbol"`       // 交易对
	Direction   string    `json:"direction"`    // AB 或 BA
	DiffValue   float64   `json:"diff_value"`   // 触发时的价差
	Threshold   float64   `json:"threshold"`    // 触发时的阈值
	PlannedSize float64   `json:"planned_size"` // 计划执行数量
	FilterReason string   `json:"filter_reason,omitempty"` // 过滤原因（如价格趋势检查未通过）

	// 双边执行详情（A 和 B 可能是链上或交易所的任意组合）
	A ExecutionDetail `json:"a"`
	B ExecutionDetail `json:"b"`

	// Telegram 消息 ID (不导出到 JSON)
	TgMessageID int `json:"-"`
}

// WSMessage WebSocket 消息结构
type WSMessage struct {
	Type string           `json:"type"` // "execution_update"
	Data *ExecutionRecord `json:"data"`
}

// FilteredExecutionAggregate 被过滤订单的聚合信息
type FilteredExecutionAggregate struct {
	Symbol       string
	Direction    string
	FilterReason string
	Count        int           // 过滤次数
	MinDiff      float64       // 最小价差
	MaxDiff      float64       // 最大价差
	AvgDiff      float64       // 平均价差
	Threshold    float64       // 阈值
	LastUpdate   time.Time     // 最后更新时间
	HasOutput    bool          // 是否已经输出过（输出后清空，避免重复输出）
}

// ExecutionMonitor 监控管理器
type ExecutionMonitor struct {
	records []*ExecutionRecord
	limit   int
	mu      sync.RWMutex
	wsHub   *ws.Hub            // 添加 wsHub 引用
	logger  *zap.SugaredLogger // 添加 logger
	dataDir string             // 数据持久化目录
	
	// 过滤信息聚合
	filteredAggregates map[string]*FilteredExecutionAggregate // key: symbol+direction+filterReason
	aggregateMu        sync.Mutex
	aggregateTicker    *time.Ticker
	aggregateStopCh    chan struct{}
}

var (
	monitorInstance *ExecutionMonitor
	monitorOnce     sync.Once
)

// GetExecutionMonitor 获取单例
func GetExecutionMonitor() *ExecutionMonitor {
	monitorOnce.Do(func() {
		monitorInstance = &ExecutionMonitor{
			records:            make([]*ExecutionRecord, 0, 100),
			limit:              100,
			logger:              logger.GetLoggerInstance().Named("monitor").Sugar(),
			dataDir:             "data/history/monitor",
			filteredAggregates:  make(map[string]*FilteredExecutionAggregate),
			aggregateTicker:     time.NewTicker(1 * time.Second),
			aggregateStopCh:     make(chan struct{}),
		}
		// 确保目录存在
		_ = os.MkdirAll(monitorInstance.dataDir, 0755)
		// 加载数据
		monitorInstance.LoadData()
		// 启动聚合输出协程
		go monitorInstance.startAggregateOutput()
	})
	return monitorInstance
}

// startAggregateOutput 启动聚合输出协程，每秒输出一次聚合信息
func (m *ExecutionMonitor) startAggregateOutput() {
	for {
		select {
		case <-m.aggregateStopCh:
			return
		case <-m.aggregateTicker.C:
			m.flushAggregates()
		}
	}
}

// flushAggregates 输出并清空聚合信息
// 只输出有新增过滤记录的聚合信息（HasOutput=false），输出后标记为已输出并清空计数
func (m *ExecutionMonitor) flushAggregates() {
	m.aggregateMu.Lock()
	if len(m.filteredAggregates) == 0 {
		m.aggregateMu.Unlock()
		return
	}

	// 找出所有未输出的聚合信息（有新的过滤记录）
	var toOutput []*FilteredExecutionAggregate
	for _, agg := range m.filteredAggregates {
		if agg.Count > 0 && !agg.HasOutput {
			toOutput = append(toOutput, agg)
		}
	}

	if len(toOutput) == 0 {
		m.aggregateMu.Unlock()
		return
	}

	// 输出所有未输出的聚合信息（在锁外输出，避免阻塞）
	m.aggregateMu.Unlock()

	for _, agg := range toOutput {
		// 输出聚合日志
		m.logger.Infof("【过滤聚合】%s %s: %d次被过滤, 原因: %s, 价差范围: [%.6f, %.6f], 平均: %.6f, 阈值: %.6f",
			agg.Symbol, agg.Direction, agg.Count, agg.FilterReason,
			agg.MinDiff, agg.MaxDiff, agg.AvgDiff, agg.Threshold)
		
		// 创建一条聚合记录用于监控系统（只创建一条，代表这1秒内的所有过滤）
		record := &ExecutionRecord{
			ID:          uuid.New().String(),
			StartTime:   agg.LastUpdate,
			EndTime:     time.Now(),
			Symbol:      agg.Symbol,
			Direction:   agg.Direction,
			DiffValue:   agg.AvgDiff, // 使用平均价差
			Threshold:   agg.Threshold,
			PlannedSize: 0,
			FilterReason: fmt.Sprintf("%s (聚合: %d次)", agg.FilterReason, agg.Count),
			A: ExecutionDetail{
				Status:   StatusFiltered,
				ErrorMsg: fmt.Sprintf("%s (聚合: %d次)", agg.FilterReason, agg.Count),
			},
			B: ExecutionDetail{
				Status:   StatusFiltered,
				ErrorMsg: fmt.Sprintf("%s (聚合: %d次)", agg.FilterReason, agg.Count),
			},
		}
		
		// 添加到记录列表
		m.mu.Lock()
		m.records = append([]*ExecutionRecord{record}, m.records...)
		if len(m.records) > m.limit {
			m.records = m.records[:m.limit]
		}
		m.mu.Unlock()
		
		// 广播更新（但不发送 Telegram，减少通知频率）
		go m.broadcastUpdate(record)
	}
	
	// 重新获取锁，标记已输出并清空计数
	m.aggregateMu.Lock()
	for _, agg := range toOutput {
		// 标记为已输出，并清空计数（保留结构，等待新的过滤记录）
		agg.HasOutput = true
		agg.Count = 0
		// 如果后续没有新的过滤记录，这个聚合项会在下次 flush 时被清理
	}
	
	// 清理已输出且没有新记录的聚合项（避免内存泄漏）
	for key, agg := range m.filteredAggregates {
		if agg.HasOutput && agg.Count == 0 {
			// 如果已经输出过且没有新记录，删除这个聚合项
			delete(m.filteredAggregates, key)
		}
	}
	
	m.aggregateMu.Unlock()
	
	// 批量保存数据（避免频繁写入）
	go m.SaveData()
}

// getAggregateKey 生成聚合键
func getAggregateKey(symbol, direction, filterReason string) string {
	return fmt.Sprintf("%s:%s:%s", symbol, direction, filterReason)
}

// LoadData 从文件加载数据
func (m *ExecutionMonitor) LoadData() {
	filename := filepath.Join(m.dataDir, "executions.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return
	}

	data, err := ioutil.ReadFile(filename)
	if err != nil {
		m.logger.Warnf("加载监控数据失败: %v", err)
		return
	}

	var records []*ExecutionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		m.logger.Warnf("解析监控数据失败: %v", err)
		return
	}

	m.mu.Lock()
	m.records = records
	m.mu.Unlock()

	m.logger.Infof("已恢复监控数据 (记录数: %d)", len(records))
}

// SaveData 保存数据到文件
func (m *ExecutionMonitor) SaveData() {
	m.mu.RLock()
	// 只保存最近的 records
	records := m.records
	m.mu.RUnlock()

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		m.logger.Errorf("序列化监控数据失败: %v", err)
		return
	}

	filename := filepath.Join(m.dataDir, "executions.json")
	if err := ioutil.WriteFile(filename, data, 0644); err != nil {
		m.logger.Errorf("保存监控数据失败: %v", err)
	}
}

// ClearHistory 清除历史数据（支持按 symbol 过滤，如果 symbol 为空则清除所有）
func (m *ExecutionMonitor) ClearHistory(symbol string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if symbol == "" {
		// 清除所有
		m.records = make([]*ExecutionRecord, 0, m.limit)
		filename := filepath.Join(m.dataDir, "executions.json")
		if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除监控文件失败: %v", err)
		}
		m.logger.Infof("已清除所有监控历史数据")
	} else {
		// 按 symbol 过滤
		newRecords := make([]*ExecutionRecord, 0, len(m.records))
		for _, r := range m.records {
			if r.Symbol != symbol {
				newRecords = append(newRecords, r)
			}
		}
		m.records = newRecords

		// 保存过滤后的数据（相当于删除了指定 symbol 的数据）
		// 注意：这里不能调用 m.SaveData() 因为已经持有锁，需要手动保存逻辑或者提取 save 逻辑
		data, err := json.MarshalIndent(m.records, "", "  ")
		if err == nil {
			filename := filepath.Join(m.dataDir, "executions.json")
			_ = ioutil.WriteFile(filename, data, 0644)
		}
		m.logger.Infof("已清除 symbol 监控历史数据: %s", symbol)
	}

	// 广播清空事件（让前端刷新）
	// TODO: 前端可能需要处理这种全量刷新的消息，或者简单地让用户刷新页面

	return nil
}

// SetWSHub 设置 WebSocket Hub
func (m *ExecutionMonitor) SetWSHub(hub *ws.Hub) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.wsHub = hub
}

// broadcastUpdate 广播更新
func (m *ExecutionMonitor) broadcastUpdate(record *ExecutionRecord) {
	if m.wsHub == nil {
		return
	}

	msg := WSMessage{
		Type: "execution_update",
		Data: record,
	}

	bytes, err := json.Marshal(msg)
	if err == nil {
		m.wsHub.Broadcast(bytes)
	}
}

// generateTelegramMessage 生成 Telegram 消息内容
func (m *ExecutionMonitor) generateTelegramMessage(record *ExecutionRecord) string {
	statusIcon := "🔄"
	if record.A.Status == StatusFiltered || record.B.Status == StatusFiltered {
		statusIcon = "🚫"
	} else if record.A.Status == StatusSuccess && record.B.Status == StatusSuccess {
		statusIcon = "✅"
	} else if record.A.Status == StatusFailed || record.B.Status == StatusFailed {
		statusIcon = "❌"
	}

	msg := fmt.Sprintf("<b>【套利执行监控】</b> %s\n\n", statusIcon)
	msg += fmt.Sprintf("🆔 <b>ID:</b> %s\n", record.ID[:8])
	msg += fmt.Sprintf("💱 <b>交易对:</b> %s (%s)\n", record.Symbol, record.Direction)
	msg += fmt.Sprintf("📊 <b>价差:</b> %.4f%% (阈值: %.4f%%)\n", record.DiffValue, record.Threshold)
	msg += fmt.Sprintf("📦 <b>计划数量:</b> %.6f\n\n", record.PlannedSize)

	// 如果有过滤原因，显示过滤原因
	if record.FilterReason != "" {
		msg += fmt.Sprintf("🚫 <b>过滤原因:</b> %s\n\n", record.FilterReason)
	}

	// A 状态
	aIcon := "⏳"
	if record.A.Status == StatusSuccess {
		aIcon = "✅"
	} else if record.A.Status == StatusFailed {
		aIcon = "❌"
	} else if record.A.Status == StatusFiltered {
		aIcon = "🚫"
	}
	msg += fmt.Sprintf("%s <b>A 状态:</b> %s\n", aIcon, record.A.Status)
	if record.A.TxHash != "" {
		msg += fmt.Sprintf("   Tx: <code>%s</code>\n", record.A.TxHash)
	}
	if record.A.OrderID != "" {
		msg += fmt.Sprintf("   Order: <code>%s</code>\n", record.A.OrderID)
	}
	if record.A.ErrorMsg != "" {
		msg += fmt.Sprintf("   Err: %s\n", record.A.ErrorMsg)
	}

	// B 状态
	bIcon := "⏳"
	if record.B.Status == StatusSuccess {
		bIcon = "✅"
	} else if record.B.Status == StatusFailed {
		bIcon = "❌"
	} else if record.B.Status == StatusFiltered {
		bIcon = "🚫"
	}
	msg += fmt.Sprintf("%s <b>B 状态:</b> %s\n", bIcon, record.B.Status)
	if record.B.TxHash != "" {
		msg += fmt.Sprintf("   Tx: <code>%s</code>\n", record.B.TxHash)
	}
	if record.B.OrderID != "" {
		msg += fmt.Sprintf("   Order: <code>%s</code>\n", record.B.OrderID)
	}
	if record.B.ErrorMsg != "" {
		msg += fmt.Sprintf("   Err: %s\n", record.B.ErrorMsg)
	}

	msg += fmt.Sprintf("\n⏱ <b>时间:</b> %s", record.StartTime.Format("15:04:05.000"))

	return msg
}

// triggerTelegramNotify 触发 Telegram 通知（异步）
func (m *ExecutionMonitor) triggerTelegramNotify(record *ExecutionRecord) {
	msg := m.generateTelegramMessage(record)
	currentID := record.TgMessageID

	go func(rec *ExecutionRecord, txt string, id int) {
		if telegram.GlobalTgBotClient == nil {
			return
		}

		var err error
		var newID int

		if id == 0 {
			// 发送新消息
			newID, err = telegram.GlobalTgBotClient.SendHTMLMessage(txt)
			if err == nil && newID != 0 {
				m.mu.Lock()
				if rec.TgMessageID == 0 {
					rec.TgMessageID = newID
				}
				m.mu.Unlock()
			}
		} else {
			// 编辑已有消息
			_, err = telegram.GlobalTgBotClient.EditMessageTextWithParseMode(id, txt, "HTML")
			// 如果编辑失败（例如消息太旧或已被删除），可能需要降级为发送新消息？
			// 暂时只记录日志
		}

		if err != nil {
			m.logger.Warnf("Telegram通知失败: %v", err)
		}
	}(record, msg, currentID)
}

// StartExecution 开始一次新的执行记录
// isAOnchain: A 是否是链上 trader
// isBOnchain: B 是否是链上 trader
func (m *ExecutionMonitor) StartExecution(symbol, direction string, diff, threshold, plannedSize float64, isAOnchain, isBOnchain bool) *ExecutionRecord {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 根据 A 和 B 的类型设置初始状态
	// 如果不是对应的类型，状态设为 N/A（不适用）
	aStatus := StatusPending
	if !isAOnchain {
		// A 不是链上 trader，A 是交易所 trader，状态为 Pending（等待交易所下单）
		aStatus = StatusPending
	}
	
	bStatus := StatusPending
	if !isBOnchain {
		// B 不是链上 trader，B 是交易所 trader，状态为 Pending（等待交易所下单）
		bStatus = StatusPending
	}

	record := &ExecutionRecord{
		ID:          uuid.New().String(),
		StartTime:   time.Now(),
		Symbol:      symbol,
		Direction:   direction,
		DiffValue:   diff,
		Threshold:   threshold,
		PlannedSize: plannedSize,
		A: ExecutionDetail{
			Status: aStatus,
		},
		B: ExecutionDetail{
			Status: bStatus,
		},
	}

	// 头部插入
	m.records = append([]*ExecutionRecord{record}, m.records...)
	// 截断
	if len(m.records) > m.limit {
		m.records = m.records[:m.limit]
	}

	// 广播更新
	go m.broadcastUpdate(record)
	// Telegram 通知
	m.triggerTelegramNotify(record)
	// 保存数据
	go m.SaveData()

	return record
}

// UpdateA 更新 A 执行结果
func (m *ExecutionMonitor) UpdateA(record *ExecutionRecord, txHash, orderID string, size, price float64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果 A 状态是 N/A（不适用），则不应该更新
	if record.A.Status == StatusNotApplicable {
		return
	}

	if txHash != "" {
		// 链上交易
		record.A.TxHash = txHash
	} else if orderID != "" {
		// 交易所交易
		record.A.OrderID = orderID
	}

	if err != nil {
		record.A.Status = StatusFailed
		record.A.ErrorMsg = err.Error()
	} else {
		record.A.Status = StatusSuccess
		record.A.Size = size
		if price > 0 {
			record.A.Price = price
		}
	}

	// 广播更新
	go m.broadcastUpdate(record)
	// Telegram 通知
	m.triggerTelegramNotify(record)
	// 保存数据
	go m.SaveData()
}

// UpdateB 更新 B 执行结果
func (m *ExecutionMonitor) UpdateB(record *ExecutionRecord, txHash, orderID string, size, price float64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 如果 B 状态是 N/A（不适用），则不应该更新
	if record.B.Status == StatusNotApplicable {
		return
	}

	if txHash != "" {
		// 链上交易
		record.B.TxHash = txHash
	} else if orderID != "" {
		// 交易所交易
		record.B.OrderID = orderID
	}

	if err != nil {
		record.B.Status = StatusFailed
		record.B.ErrorMsg = err.Error()
	} else {
		record.B.Status = StatusSuccess
		record.B.Size = size
		if price > 0 {
			record.B.Price = price
		}
	}
	record.EndTime = time.Now()

	// 广播更新
	go m.broadcastUpdate(record)
	// Telegram 通知
	m.triggerTelegramNotify(record)
	// 保存数据
	go m.SaveData()
}

// FailExecution 标记整体执行失败（通常是未执行任何动作就失败，或者兜底）
func (m *ExecutionMonitor) FailExecution(record *ExecutionRecord, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if record.A.Status == StatusPending {
		record.A.Status = StatusFailed
		record.A.ErrorMsg = errMsg
	}
	if record.B.Status == StatusPending {
		record.B.Status = StatusFailed
		record.B.ErrorMsg = errMsg
	}
	record.EndTime = time.Now()

	// 广播更新
	go m.broadcastUpdate(record)
	// Telegram 通知
	m.triggerTelegramNotify(record)
	// 保存数据
	go m.SaveData()
}

// RecordFilteredExecution 记录被过滤的订单（如价格趋势检查未通过）
// 使用聚合机制，1秒内的相同过滤原因会聚合为一条记录
// 不立即创建 ExecutionRecord，只在聚合输出时创建一条聚合记录
func (m *ExecutionMonitor) RecordFilteredExecution(symbol, direction string, diff, threshold, plannedSize float64, filterReason string) *ExecutionRecord {
	// 聚合过滤信息（不立即输出）
	key := getAggregateKey(symbol, direction, filterReason)
	m.aggregateMu.Lock()
	agg, exists := m.filteredAggregates[key]
	if !exists {
		agg = &FilteredExecutionAggregate{
			Symbol:       symbol,
			Direction:    direction,
			FilterReason: filterReason,
			Count:        0,
			MinDiff:      diff,
			MaxDiff:      diff,
			AvgDiff:      diff,
			Threshold:    threshold,
			LastUpdate:   time.Now(),
			HasOutput:    false, // 新创建的聚合项未输出过
		}
		m.filteredAggregates[key] = agg
	}
	
	// 如果有新记录进来，重置输出标记（表示有新的过滤记录需要输出）
	if agg.HasOutput {
		agg.HasOutput = false
		// 重置统计信息，开始新的聚合周期
		agg.MinDiff = diff
		agg.MaxDiff = diff
		agg.AvgDiff = diff
		agg.Count = 0
	}
	
	// 更新聚合信息
	agg.Count++
	if diff < agg.MinDiff {
		agg.MinDiff = diff
	}
	if diff > agg.MaxDiff {
		agg.MaxDiff = diff
	}
	// 更新平均价差（使用移动平均）
	agg.AvgDiff = (agg.AvgDiff*float64(agg.Count-1) + diff) / float64(agg.Count)
	agg.LastUpdate = time.Now()
	m.aggregateMu.Unlock()

	// 不立即创建记录，由聚合输出统一处理
	// 返回 nil，因为记录会在聚合输出时创建
	return nil
}

// GetRecentExecutions 获取最近的执行记录
func (m *ExecutionMonitor) GetRecentExecutions(limit int) []*ExecutionRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.records) {
		limit = len(m.records)
	}

	// 返回副本以避免并发问题
	result := make([]*ExecutionRecord, limit)
	copy(result, m.records[:limit])
	return result
}

