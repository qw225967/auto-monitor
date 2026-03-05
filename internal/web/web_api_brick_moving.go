package web

import (
	"errors"
	"auto-arbitrage/constants"
	"auto-arbitrage/internal/analytics"
	"auto-arbitrage/internal/config"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain"
	"auto-arbitrage/internal/onchain/bridge"
	"auto-arbitrage/internal/onchain/bridge/ccip"
	"auto-arbitrage/internal/onchain/bridge/layerzero"
	"auto-arbitrage/internal/onchain/bridge/wormhole"
	"auto-arbitrage/internal/pipeline"
	"auto-arbitrage/internal/position"
	"auto-arbitrage/internal/proto"
	"auto-arbitrage/internal/statistics"
	"auto-arbitrage/internal/trader"
	"auto-arbitrage/internal/trigger"
	"auto-arbitrage/internal/trigger/chain_token_registry"
	"auto-arbitrage/internal/trigger/token_mapping"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func debugLogBrickMoving(location, message string, data map[string]interface{}, hypothesisId string) {}

const (
	brickMovingPipelinesFile = "data/brick_moving_pipelines.json"
	brickMovingHedgingFile   = "data/brick_moving_hedging.json"
	brickMovingSymbolsFile   = "data/brick_moving_symbols.json"
)

// brickMovingPersistData holds all brick-moving state for serialization
type brickMovingPersistData struct {
	Pipelines map[string]*brickMovingPipelineConfig `json:"pipelines"`
}

type brickMovingHedgingPersistData struct {
	Configs map[string]*brickMovingHedgingConfig `json:"configs"`
}

func (d *Dashboard) saveBrickMovingPipelines() {
	d.brickMovingPipelineMu.RLock()
	data := brickMovingPersistData{Pipelines: d.brickMovingPipelines}
	d.brickMovingPipelineMu.RUnlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		d.logger.Warnf("Failed to marshal brick-moving pipelines: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(brickMovingPipelinesFile), 0755); err != nil {
		d.logger.Warnf("Failed to create data dir: %v", err)
		return
	}
	if err := os.WriteFile(brickMovingPipelinesFile, b, 0644); err != nil {
		d.logger.Warnf("Failed to save brick-moving pipelines: %v", err)
	}
}

func (d *Dashboard) loadBrickMovingPipelines() {
	b, err := os.ReadFile(brickMovingPipelinesFile)
	if err != nil {
		return
	}
	var data brickMovingPersistData
	if err := json.Unmarshal(b, &data); err != nil {
		d.logger.Warnf("Failed to parse brick-moving pipelines file: %v", err)
		return
	}
	if data.Pipelines != nil {
		d.brickMovingPipelineMu.Lock()
		d.brickMovingPipelines = data.Pipelines
		d.brickMovingPipelineMu.Unlock()
	}
}

func (d *Dashboard) saveBrickMovingHedging() {
	d.brickMovingHedgingMu.RLock()
	data := brickMovingHedgingPersistData{Configs: d.brickMovingHedgingCfgs}
	d.brickMovingHedgingMu.RUnlock()

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		d.logger.Warnf("Failed to marshal brick-moving hedging: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(brickMovingHedgingFile), 0755); err != nil {
		d.logger.Warnf("Failed to create data dir: %v", err)
		return
	}
	if err := os.WriteFile(brickMovingHedgingFile, b, 0644); err != nil {
		d.logger.Warnf("Failed to save brick-moving hedging: %v", err)
	}
}

func (d *Dashboard) loadBrickMovingHedging() {
	b, err := os.ReadFile(brickMovingHedgingFile)
	if err != nil {
		return
	}
	var data brickMovingHedgingPersistData
	if err := json.Unmarshal(b, &data); err != nil {
		d.logger.Warnf("Failed to parse brick-moving hedging file: %v", err)
		return
	}
	if data.Configs != nil {
		d.brickMovingHedgingMu.Lock()
		d.brickMovingHedgingCfgs = data.Configs
		d.brickMovingHedgingMu.Unlock()
	}
}

func (d *Dashboard) saveBrickMovingSymbols() {
	d.brickMovingMu.RLock()
	symbols := make([]string, 0, len(d.brickMovingSymbols))
	for s := range d.brickMovingSymbols {
		symbols = append(symbols, s)
	}
	d.brickMovingMu.RUnlock()

	b, err := json.MarshalIndent(symbols, "", "  ")
	if err != nil {
		d.logger.Warnf("Failed to marshal brick-moving symbols: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(brickMovingSymbolsFile), 0755); err != nil {
		d.logger.Warnf("Failed to create data dir: %v", err)
		return
	}
	if err := os.WriteFile(brickMovingSymbolsFile, b, 0644); err != nil {
		d.logger.Warnf("Failed to save brick-moving symbols: %v", err)
	}
}

func (d *Dashboard) loadBrickMovingSymbols() {
	b, err := os.ReadFile(brickMovingSymbolsFile)
	if err != nil {
		return
	}
	var symbols []string
	if err := json.Unmarshal(b, &symbols); err != nil {
		d.logger.Warnf("Failed to parse brick-moving symbols file: %v", err)
		return
	}
	d.brickMovingMu.Lock()
	for _, s := range symbols {
		d.brickMovingSymbols[s] = struct{}{}
	}
	d.brickMovingMu.Unlock()
}

// loadBrickMovingState loads all persisted brick-moving config on startup
func (d *Dashboard) loadBrickMovingState() {
	d.loadBrickMovingSymbols()
	d.loadBrickMovingPipelines()
	d.loadBrickMovingHedging()
	d.logger.Infof("Loaded brick-moving state: %d symbols, %d pipeline configs, %d hedging configs",
		len(d.brickMovingSymbols), len(d.brickMovingPipelines), len(d.brickMovingHedgingCfgs))
}

// 供 runBrickMovingPipelineInternal 与 HTTP 层区分的错误
var (
	errNoActivePipeline = errors.New("no active pipeline for this trigger and direction; save and apply a pipeline first")
	errPipelineNotFound = errors.New("pipeline not found")
)

// errPipelineCannotRun 表示 pipeline 当前不可运行（冷却或已运行），Reason 为 CanRun 返回的说明
type errPipelineCannotRun struct{ Reason string }

func (e *errPipelineCannotRun) Error() string { return e.Reason }

// lastFlipResult 智能翻转刚结束时的两条 pipeline 状态，供提币历史短时间返回 flipPipelines
type lastFlipResult struct {
	TokenStatus string    // "completed" | "failed"
	UsdtStatus  string    // "completed" | "failed"
	EndTime     time.Time // 超过 60s 后不再返回
}

// brickMovingPipelineConfig 按 triggerSymbol 存储的搬砖 Pipeline 配置（内存）
type brickMovingPipelineConfig struct {
	ForwardNodes             []map[string]interface{} `json:"forwardNodes"`
	ForwardEdges             []map[string]interface{} `json:"forwardEdges"`
	BackwardNodes            []map[string]interface{} `json:"backwardNodes"`
	BackwardEdges            []map[string]interface{} `json:"backwardEdges"`
	ActiveForwardPipelineId  string                   `json:"activeForwardPipelineId"`
	ActiveBackwardPipelineId string                   `json:"activeBackwardPipelineId"`
	// 四条 pipeline 各自的可达性：正常充提 A→B / B→A 由保存或 route-probe 写入；智能翻转两条由应用后镜像探测写入
	ForwardReachable          bool   `json:"forwardReachable"`          // 正常充提 A→B 是否可达
	BackwardReachable         bool   `json:"backwardReachable"`         // 正常充提 B→A 是否可达
	MirrorForwardReachable    bool   `json:"mirrorForwardReachable"`    // 智能翻转 B-A 币（forward 的镜像）是否可达，应用 A→B 后探测写入
	MirrorBackwardReachable   bool   `json:"mirrorBackwardReachable"`   // 智能翻转 A-B USDT（backward 的镜像）是否可达，应用 B→A 后探测写入
	ForwardReachableReason    string `json:"forwardReachableReason"`    // 正常充提 A→B 不可达原因
	BackwardReachableReason   string `json:"backwardReachableReason"`   // 正常充提 B→A 不可达原因
	MirrorForwardReachableReason  string `json:"mirrorForwardReachableReason"`  // 智能翻转 B-A 币 不可达原因
	MirrorBackwardReachableReason string `json:"mirrorBackwardReachableReason"` // 智能翻转 A-B USDT 不可达原因
}

// brickMovingHedgingConfig 按 symbol 存储的套保配置（内存）
type brickMovingHedgingConfig struct {
	ExchangeType        string  `json:"exchangeType"`
	HedgingSymbol       string  `json:"hedgingSymbol"`
	Size                float64 `json:"size"`                  // 套保所合约单次开仓大小（做空数量）
	PositionOpenSize float64 `json:"positionOpenSize"` // 套保开仓专用数量（A 端链上/现货 + 与 Trigger 日常 Size 独立），若 >0 开仓时优先使用
	Position         float64 `json:"position"`         // 总仓位大小（用于限制开仓和自动充提）
	Enabled             bool    `json:"enabled"`
	OpenPositionSide    string  `json:"openPositionSide"`      // 开仓买入源："A" | "B"，默认 "A"

	// 自动充提配置
	AutoWithdrawEnabled    bool    `json:"autoWithdrawEnabled"`    // 是否启用自动充提
	AutoWithdrawUseFixed   bool    `json:"autoWithdrawUseFixed"`   // true=固定size, false=仓位百分比
	AutoWithdrawFixedSize  float64 `json:"autoWithdrawFixedSize"`  // 固定提币大小（当 autoWithdrawUseFixed=true 时使用）
	AutoWithdrawPercentage float64 `json:"autoWithdrawPercentage"` // 仓位百分比（0-1，当 autoWithdrawUseFixed=false 时使用）

	// B→A 方向默认计价/提现资产（如 USDT、USDC）；空或未设置时按 "USDT" 处理
	QuoteAsset string `json:"quoteAsset"`

	// 智能翻转充提
	SmartFlipEnabled    bool    `json:"smartFlipEnabled"`    // 是否启用智能翻转充提
	SmartFlipThreshold  float64 `json:"smartFlipThreshold"`  // 翻转触发阈值（%），默认 1.5
}

// pipelineConfigKey 同 symbol 多 trigger 时按 triggerId 区分 pipeline 配置，避免提币记录/状态串数据
func pipelineConfigKey(triggerSymbol, triggerIDStr string) string {
	if triggerIDStr != "" {
		return triggerSymbol + "_" + triggerIDStr
	}
	return triggerSymbol
}

// getAnyPipelineConfigForSymbol 当未传 triggerId 时，返回该 symbol 下任意一个已有配置（用于提币记录兜底展示）
func (d *Dashboard) getAnyPipelineConfigForSymbol(triggerSymbol string) (cfg *brickMovingPipelineConfig, resolvedKey string) {
	prefix := triggerSymbol + "_"
	d.brickMovingPipelineMu.RLock()
	defer d.brickMovingPipelineMu.RUnlock()
	for k, c := range d.brickMovingPipelines {
		if c == nil {
			continue
		}
		if k == triggerSymbol || strings.HasPrefix(k, prefix) {
			if len(c.ForwardNodes) > 0 || len(c.BackwardNodes) > 0 {
				return c, k
			}
		}
	}
	return nil, ""
}

// getQuoteAssetForTrigger 返回该 trigger 的 B→A 计价/提现资产（配置的 QuoteAsset），空则 "USDT"
// triggerIDStr 非空时按 trigger 取套保配置，同 symbol 多 trigger 时避免串配置
func (d *Dashboard) getQuoteAssetForTrigger(triggerSymbol, triggerIDStr string) string {
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[key]
	d.brickMovingHedgingMu.RUnlock()
	if hedging != nil && strings.TrimSpace(hedging.QuoteAsset) != "" {
		return strings.TrimSpace(hedging.QuoteAsset)
	}
	return "USDT"
}

// applyAutoWithdrawConfigToPipeline 将最新的 hedging 自动充提配置应用到 pipeline 的所有边上。
// 每次运行 pipeline 前都应调用此函数，确保使用的是最新的 size/percentage 配置。
// triggerIDStr 非空时按 trigger 区分配置（与 pipeline 的 key 一致）。
// 仅改写 AmountType、Amount、Asset；不覆盖 edge.Network/ChainID，避免破坏路由探测写入的提现链（ex→ex 需正确 Network 避免 OKX 51000）。
func (d *Dashboard) applyAutoWithdrawConfigToPipeline(p *pipeline.Pipeline, triggerSymbol, triggerIDStr string) {
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[key]
	d.brickMovingHedgingMu.RUnlock()

	// #region agent log
	{
		hedgingNil := hedging == nil
		var autoEnabled bool
		var autoUseFixed bool
		var autoFixedSize, hedgingSize float64
		if hedging != nil {
			autoEnabled = hedging.AutoWithdrawEnabled
			autoUseFixed = hedging.AutoWithdrawUseFixed
			autoFixedSize = hedging.AutoWithdrawFixedSize
			hedgingSize = hedging.Size
		}
		debugLogBrickMoving("applyAutoWithdraw:entry", "called with hedging state", map[string]interface{}{
			"pipelineName": p.Name(), "pipelineID": p.ID(),
			"triggerSymbol": triggerSymbol, "hedgingNil": hedgingNil,
			"autoEnabled": autoEnabled, "autoUseFixed": autoUseFixed,
			"autoFixedSize": autoFixedSize, "hedgingSize": hedgingSize,
		}, "H7")
	}
	// #endregion

	if hedging == nil || !hedging.AutoWithdrawEnabled {
		return
	}

	// 检查是否为 backward pipeline（B→A 反向）：边金额按计价资产（QuoteAsset）计算，并写入边 Asset 供执行层使用
	isBackward := strings.Contains(strings.ToLower(p.Name()), "backward") ||
		strings.Contains(strings.ToLower(p.ID()), "backward")
	quoteAsset := d.getQuoteAssetForTrigger(triggerSymbol, triggerIDStr)

	nodes := p.Nodes()
	for i := 0; i < len(nodes)-1; i++ {
		fromNode := nodes[i]
		toNode := nodes[i+1]
		edge, hasEdge := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID())
		if !hasEdge {
			edge = &pipeline.EdgeConfig{
				AmountType:    pipeline.AmountTypeAll,
				MaxWaitTime:   30 * time.Minute,
				CheckInterval: 10 * time.Second,
			}
			p.SetEdgeConfig(fromNode.GetID(), toNode.GetID(), edge)
		}

		if hedging.AutoWithdrawUseFixed {
			fixedSize := hedging.AutoWithdrawFixedSize
			if fixedSize <= 0 {
				fixedSize = hedging.Size
			}

			if isBackward {
				// backward 链路：边金额按计价资产（QuoteAsset）计算，执行层按边 Asset 提币
				coinAsset := strings.TrimSuffix(triggerSymbol, "USDT")
				if coinAsset == triggerSymbol {
					coinAsset = strings.TrimSuffix(triggerSymbol, "USDC")
				}
				baseSymbol := coinAsset + quoteAsset

				var quoteAmount float64
				pm := position.GetPositionManager()
				if pm != nil {
					priceData := pm.GetLatestPrice(baseSymbol)
					if priceData != nil && priceData.BidPrice > 0 {
						quoteAmount = fixedSize * priceData.BidPrice
						quoteAmount = float64(int(quoteAmount))
					}
				}

			// #region agent log
			debugLogBrickMoving("applyAutoWithdraw:backwardPrice", "price lookup result", map[string]interface{}{
				"baseSymbol": baseSymbol, "coinAsset": coinAsset, "fixedSize": fixedSize,
				"quoteAmount": quoteAmount, "quoteAsset": quoteAsset, "pmNil": pm == nil,
			}, "H1")
			// #endregion

			if quoteAmount > 0 {
				edge.AmountType = pipeline.AmountTypeFixed
				edge.Amount = quoteAmount
				edge.PositionSize = 0
				edge.Asset = quoteAsset
				price := 0.0
				if fixedSize > 0 {
					price = quoteAmount / fixedSize
				}
				d.logger.Infof("Backward pipeline: edge amount set to %s: coinSize=%.4f, price=%.8f, amount=%.2f %s",
					quoteAsset, fixedSize, price, quoteAmount, quoteAsset)
			} else {
				d.logger.Warnf("Backward pipeline: cannot get price for %s, falling back to coin size=%.4f", baseSymbol, fixedSize)
				edge.AmountType = pipeline.AmountTypeFixed
				edge.Amount = fixedSize
				edge.PositionSize = 0
				edge.Asset = quoteAsset
			}
			} else {
				// forward 链路：edge.Amount 用币数量
				edge.AmountType = pipeline.AmountTypeFixed
				edge.Amount = fixedSize
				edge.PositionSize = 0
			}
		} else {
			edge.AmountType = pipeline.AmountTypePercentage
			edge.Amount = hedging.AutoWithdrawPercentage
			edge.PositionSize = hedging.Position
		}
		p.SetEdgeConfig(fromNode.GetID(), toNode.GetID(), edge)
	}
	d.logger.Infof("Applied auto-withdraw config to pipeline %s (backward=%v): useFixed=%v, fixedSize=%.4f, percentage=%.4f, positionSize=%.4f",
		p.Name(), isBackward, hedging.AutoWithdrawUseFixed, hedging.AutoWithdrawFixedSize, hedging.AutoWithdrawPercentage, hedging.Position)
}

// getMergedRPCURLs returns RPC URLs merged from constants and global config
func getMergedRPCURLs() map[string]string {
	rpcURLs := constants.GetAllDefaultRPCURLs()
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil && globalConfig.Bridge.LayerZero.RPCURLs != nil {
		for chainID, url := range globalConfig.Bridge.LayerZero.RPCURLs {
			if url != "" {
				rpcURLs[chainID] = url
			}
		}
	}
	return rpcURLs
}

// ensureRPCURLsForChains fills in missing RPC URLs for the given chain IDs
func (d *Dashboard) ensureRPCURLsForChains(rpcURLs map[string]string, chainIDs map[string]bool) {
	for chainID := range chainIDs {
		if _, exists := rpcURLs[chainID]; !exists {
			if defaultURL := constants.GetDefaultRPCURL(chainID); defaultURL != "" {
				rpcURLs[chainID] = defaultURL
				d.logger.Infof("Using default RPC URL for chain %s: %s", chainID, defaultURL)
			} else {
				d.logger.Warnf("No RPC URL configured for chain %s, bridge may fail", chainID)
			}
		}
	}
}

// chainIDsFromNodes extracts chain IDs from pipeline nodes
func chainIDsFromNodes(nodes []pipeline.Node) map[string]bool {
	chainIDs := make(map[string]bool)
	for _, node := range nodes {
		if cid := pipeline.ChainIDFromNodeID(node.GetID()); cid != "" {
			chainIDs[cid] = true
		}
	}
	return chainIDs
}

// buildBridgeManager creates and configures a bridge.Manager with all protocols
func (d *Dashboard) buildBridgeManager(triggerSymbol string, nodes []pipeline.Node) *bridge.Manager {
	rpcURLs := getMergedRPCURLs()
	chainIDs := chainIDsFromNodes(nodes)
	d.ensureRPCURLsForChains(rpcURLs, chainIDs)

	bridgeMgr := bridge.NewManager(true)

	lz := layerzero.NewLayerZero(rpcURLs, true)
	reg := d.getOrCreateBridgeOFTRegistry()
	lz.SetOFTRegistry(reg)
	d.refreshBridgeAddressesForSymbol(triggerSymbol)

	chainIDList := make([]string, 0, len(chainIDs))
	for cid := range chainIDs {
		chainIDList = append(chainIDList, cid)
	}
	d.autoDiscoverOFTFromTokenMapping(lz, triggerSymbol, chainIDList)
	d.discoverOFTFromWalletInfo(lz, triggerSymbol, chainIDList)
	applyOFTContractsFromConfig(lz)
	bridgeMgr.RegisterProtocol(lz)

	wh := wormhole.NewWormhole(getWormholeRPCURLs(), getWormholeEnabled())
	applyWormholeTokenContractsFromConfig(wh)
	bridgeMgr.RegisterProtocol(wh)

	// CCIP 与 route probe 保持一致：始终注册，避免边指定 bridgeProtocol=ccip 时出现 "protocol ccip not found"。
	// route probe 会通过 DiscoverToken 动态发现 token（如 POWER），若 buildBridgeManager 不注册 CCIP，
	// 用户应用探测结果后 pipeline run 会因协议未注册而失败。
	// 边上的 token 使用 base symbol（如 POWER），非 triggerSymbol（POWERUSDT），故用 extractBaseSymbol 做发现。
	ccipProtocol := ccip.NewCCIP(rpcURLs, true)
	// 为 CCIP 设置完整 RPC 列表（GetDefaultRPCURLs），供 verifyBroadcast 重广播使用
	for cid := range chainIDs {
		if urls := constants.GetDefaultRPCURLs(cid); len(urls) > 0 {
			ccipProtocol.SetRPCURLsForChain(cid, urls)
		}
	}
	applyCCIPTokenPoolsFromConfig(ccipProtocol)
	baseSymbol := extractBaseSymbol(triggerSymbol)
	if baseSymbol == "" {
		baseSymbol = triggerSymbol
	}
	if baseSymbol != "" && len(chainIDList) > 0 {
		knownAddrs := collectKnownTokenAddresses(baseSymbol, chainIDList)
		if disc, ok := interface{}(ccipProtocol).(bridge.TokenDiscoverer); ok && len(knownAddrs) > 0 {
			if found, err := disc.DiscoverToken(baseSymbol, knownAddrs, chainIDList); err == nil && len(found) > 0 {
				d.logger.Infof("buildBridgeManager: CCIP discovered %d token addresses for %s", len(found), baseSymbol)
			}
		}
	}
	bridgeMgr.RegisterProtocol(ccipProtocol)

	return bridgeMgr
}

// writeJSONError 返回 JSON 格式错误，便于前端显示具体原因
func writeJSONError(w http.ResponseWriter, statusCode int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// markBrickMovingSymbol 在 Dashboard 中标记一个 symbol 属于「搬砖区」
func (d *Dashboard) markBrickMovingSymbol(symbol string) {
	if symbol == "" {
		return
	}
	d.brickMovingMu.Lock()
	d.brickMovingSymbols[symbol] = struct{}{}
	d.brickMovingMu.Unlock()
}

// cleanupBrickMovingState 清理搬砖区关联状态：只删除该 trigger 的 pipeline 与套保配置；仅当该 symbol 下再无其他 trigger 时才移除 symbol 标记。
// 调用方需在 RemoveTrigger 之后调用，以便正确判断是否还有同 symbol trigger。
func (d *Dashboard) cleanupBrickMovingState(symbol, triggerIDStr string) {
	if symbol == "" {
		return
	}
	key := pipelineConfigKey(symbol, triggerIDStr)

	d.brickMovingPipelineMu.Lock()
	delete(d.brickMovingPipelines, key)
	d.brickMovingPipelineMu.Unlock()

	d.brickMovingHedgingMu.Lock()
	delete(d.brickMovingHedgingCfgs, key)
	d.brickMovingHedgingMu.Unlock()

	// 仅当该 symbol 下再无其他 trigger 时，才从搬砖区移除 symbol
	hasOther := false
	for _, tg := range d.triggerManager.GetAllTriggers() {
		if tg.GetSymbol() == symbol {
			hasOther = true
			break
		}
	}
	if !hasOther {
		d.brickMovingMu.Lock()
		delete(d.brickMovingSymbols, symbol)
		d.brickMovingMu.Unlock()
	}

	d.logger.Infof("Cleaned up brick-moving state for symbol %s (triggerId=%s)", symbol, triggerIDStr)
}

// isBrickMovingSymbol 判断某个 symbol 是否属于「搬砖区」
func (d *Dashboard) isBrickMovingSymbol(symbol string) bool {
	if symbol == "" {
		return false
	}
	d.brickMovingMu.RLock()
	defer d.brickMovingMu.RUnlock()
	_, ok := d.brickMovingSymbols[symbol]
	return ok
}

// handleGetBrickMovingTriggers 返回搬砖区专用的 Trigger 列表
// 注意：底层仍然复用同一个 TriggerManager，但这里只展示通过搬砖区创建的 Trigger（在内存中打了标记）
func (d *Dashboard) handleGetBrickMovingTriggers(w http.ResponseWriter, r *http.Request) {
	triggers := d.triggerManager.GetAllTriggers()

	// 只保留被标记为「搬砖」的 Trigger
	filtered := make([]proto.Trigger, 0, len(triggers))
	for _, tg := range triggers {
		if d.isBrickMovingSymbol(tg.GetSymbol()) {
			filtered = append(filtered, tg)
		}
	}

	// 与 handleGetTriggers 保持完全一致的返回结构
	result := make([]map[string]interface{}, 0, len(filtered))
	for _, tg := range filtered {
		optimalThresholds := tg.GetOptimalThresholds()
		status := tg.GetStatus()

		minInterval := tg.GetMinThreshold()
		maxInterval := tg.GetMaxThreshold()

		chainId := tg.GetChainId()
		exchangeType := tg.GetExchangeType()
		traderAType := tg.GetTraderAType()
		traderBType := tg.GetTraderBType()

		// 从 optimalThresholds 中提取 thresholdAB 和 thresholdBA（如果是 BrickMovingTrigger）
		var thresholdAB, thresholdBA interface{}
		if optimalThresholds != nil {
			if val, ok := optimalThresholds["thresholdAB"]; ok {
				thresholdAB = val
			}
			if val, ok := optimalThresholds["thresholdBA"]; ok {
				thresholdBA = val
			}
		}

		resultItem := map[string]interface{}{
			"id":                          strconv.FormatUint(tg.GetID(), 10), // 字符串避免 JS 大数精度丢失
			"symbol":                      tg.GetSymbol(),
			"status":                      status,
			"telegramNotificationEnabled": tg.GetTelegramNotificationEnabled(),
			"optimalThresholds":           optimalThresholds,
			"minInterval":                 minInterval,
			"maxInterval":                 maxInterval,
			"thresholdAB":                 thresholdAB, // BrickMovingTrigger 专用
			"thresholdBA":                 thresholdBA, // BrickMovingTrigger 专用
			"interval":                    minInterval, // 向后兼容
			"chainId":                     chainId,
			"exchangeType":                exchangeType,
			"traderAType":                 traderAType,
			"traderBType":                 traderBType,
		}

		// BrickMovingTrigger：若有链上端，返回滑点与 gas 配置；并返回 +A-B/-A+B 各自的交易状态与触发间隔供详情页展示
		if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
			statusABA, statusABB := brickTrigger.GetTradeStatusAB()
			statusBAB, statusBAA := brickTrigger.GetTradeStatusBA()
			resultItem["tradeStatusABA"] = statusABA
			resultItem["tradeStatusABB"] = statusABB
			resultItem["tradeStatusBAB"] = statusBAB
			resultItem["tradeStatusBAA"] = statusBAA
			resultItem["orderLoopMs"] = int64(brickTrigger.GetOrderLoopInterval().Milliseconds())
			resultItem["cooldownSec"] = int64(brickTrigger.GetCooldown().Seconds())
			if pipeline.IsOnchainNodeID(traderAType) {
				resultItem["onChainSlippageA"] = brickTrigger.GetOnChainSlippageA()
				resultItem["gasMultiplierA"] = brickTrigger.GetGasMultiplierA()
				resultItem["onChainGasLimitA"] = brickTrigger.GetOnChainGasLimitA()
			}
			if pipeline.IsOnchainNodeID(traderBType) {
				resultItem["onChainSlippageB"] = brickTrigger.GetOnChainSlippageB()
				resultItem["gasMultiplierB"] = brickTrigger.GetGasMultiplierB()
				resultItem["onChainGasLimitB"] = brickTrigger.GetOnChainGasLimitB()
			}
		}

		result = append(result, resultItem)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleGetBrickMovingArbitrageList 返回搬砖区币列表（表格用）：symbol、方向、交易所 A/B、价格 A/B、差价、状态、操作
// 复用 Trigger 数据源，补充 exchangeA/exchangeB（即 traderAType/traderBType），priceA/priceB/diff 暂无数据源时返回 0
func (d *Dashboard) handleGetBrickMovingArbitrageList(w http.ResponseWriter, r *http.Request) {
	triggers := d.triggerManager.GetAllTriggers()
	filtered := make([]proto.Trigger, 0, len(triggers))
	for _, tg := range triggers {
		if d.isBrickMovingSymbol(tg.GetSymbol()) {
			filtered = append(filtered, tg)
		}
	}
	result := make([]map[string]interface{}, 0, len(filtered))
	for _, tg := range filtered {
		traderAType := tg.GetTraderAType()
		traderBType := tg.GetTraderBType()
		symbol := tg.GetSymbol()

		// 获取套保配置以检查是否启用了自动充提（按 trigger 区分，同 symbol 多 trigger 互不串数据）
		autoWithdrawEnabled := false
		triggerIDStr := strconv.FormatUint(tg.GetID(), 10)
		key := pipelineConfigKey(symbol, triggerIDStr)
		d.brickMovingHedgingMu.RLock()
		if hedging, exists := d.brickMovingHedgingCfgs[key]; exists && hedging != nil {
			autoWithdrawEnabled = hedging.AutoWithdrawEnabled
		}
		d.brickMovingHedgingMu.RUnlock()

		var diffAB, diffBA float64
		if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
			diffAB, diffBA, _ = brickTrigger.GetLatestPriceDiff()
		}
		if diffAB == 0 && diffBA == 0 {
			sm := statistics.GetStatisticsManager()
			if sm != nil {
				diffAB, diffBA, _ = sm.GetLatestPriceDiff(symbol)
			}
		}
		// BrickMovingTrigger 优先用本 trigger 的价差，同 symbol 多 trigger 时列表各显各的

		row := map[string]interface{}{
			"symbol":              symbol,
			"direction":           "A→B",
			"exchangeA":           traderAType,
			"exchangeB":           traderBType,
			"diffAB":              diffAB,
			"diffBA":              diffBA,
			"status":              tg.GetStatus(),
			"id":                  strconv.FormatUint(tg.GetID(), 10), // 字符串避免 JS Number 精度丢失（>2^53 会丢精度导致 trigger not found）
			"traderAType":         traderAType,
			"traderBType":         traderBType,
			"autoWithdrawEnabled": autoWithdrawEnabled,
			"enabledAB":           tg.GetDirectionEnabled(0),
			"enabledBA":           tg.GetDirectionEnabled(1),
		}
		if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
			statusABA, statusABB := brickTrigger.GetTradeStatusAB()
			statusBAB, statusBAA := brickTrigger.GetTradeStatusBA()
			row["tradeStatusABA"] = statusABA
			row["tradeStatusABB"] = statusABB
			row["tradeStatusBAB"] = statusBAB
			row["tradeStatusBAA"] = statusBAA
			row["orderLoopMs"] = int64(brickTrigger.GetOrderLoopInterval().Milliseconds())
			row["cooldownSec"] = int64(brickTrigger.GetCooldown().Seconds())
		}
		result = append(result, row)
	}
	// 按 trigger ID 升序排序，保证列表顺序稳定（先创建的在前、序号与创建顺序一致），避免 sync.Map 迭代顺序随机导致“第一个添加的被压到第二行、数据串行”
	sort.Slice(result, func(i, j int) bool {
		sI, _ := result[i]["id"].(string)
		sJ, _ := result[j]["id"].(string)
		idI, _ := strconv.ParseUint(sI, 10, 64)
		idJ, _ := strconv.ParseUint(sJ, 10, 64)
		return idI < idJ
	})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleCreateBrickMovingTrigger 创建搬砖 trigger（使用 BrickMovingTrigger）
func (d *Dashboard) handleCreateBrickMovingTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. 解析请求
	var req struct {
		Symbol      string   `json:"symbol"`
		TraderAType string   `json:"traderAType"`
		TraderBType string   `json:"traderBType"`
		ThresholdAB    *float64 `json:"thresholdAB"`    // 可选，默认 0.2
		ThresholdBA    *float64 `json:"thresholdBA"`    // 可选，默认 0
		Size            *float64 `json:"size"`          // 可选，兼容：同时设置 AB/BA
		TriggerABSize  *float64 `json:"triggerABSize"`  // +A-B 方向 size
		TriggerBASize  *float64 `json:"triggerBASize"`  // -A+B 方向 size
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	d.logger.Infof("Received create brick-moving trigger request: symbol=%s, traderAType=%s, traderBType=%s", req.Symbol, req.TraderAType, req.TraderBType)

	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	// 识别后缀：无 USDT/USDC/BUSD 时补 USDT
	req.Symbol = ensureQuoteSuffix(req.Symbol)

	// 标记为搬砖区 symbol
	d.markBrickMovingSymbol(req.Symbol)

	// 2. 允许同一 symbol 多个 trigger（不同方向等），不再检查已存在

	// 3. 解析并获取 traderType（从映射表或默认值）
	traderAType, traderBType, err := d.resolveTraderTypes(req.Symbol, req.TraderAType, req.TraderBType)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to resolve trader types: %v", err), http.StatusBadRequest)
		return
	}

	d.logger.Infof("Resolved trader types: traderAType=%s, traderBType=%s", traderAType, traderBType)

	// 4. 解析 traderType 信息
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

	// 5. 创建 Trader 实例
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

	// 5.1. 如果是链上类型，需要创建 OnchainTrader 实例
	if sourceA == nil && pipeline.IsOnchainNodeID(traderAType) {
		okOnChainClient := onchain.NewOkdex()
		if err := okOnChainClient.Init(); err != nil {
			http.Error(w, fmt.Sprintf("Failed to init onchain client for sourceA: %v", err), http.StatusInternalServerError)
			return
		}
		sourceA = trader.NewOnchainTrader(okOnChainClient, traderAType)
		d.logger.Infof("Created OnchainTrader for sourceA: %s", traderAType)
	}

	if sourceB == nil && pipeline.IsOnchainNodeID(traderBType) {
		okOnChainClient := onchain.NewOkdex()
		if err := okOnChainClient.Init(); err != nil {
			http.Error(w, fmt.Sprintf("Failed to init onchain client for sourceB: %v", err), http.StatusInternalServerError)
			return
		}
		sourceB = trader.NewOnchainTrader(okOnChainClient, traderBType)
		d.logger.Infof("Created OnchainTrader for sourceB: %s", traderBType)
	}

	// 6. 获取 TriggerManager 以生成 ID
	tm, ok := d.triggerManager.(*trigger.TriggerManager)
	if !ok {
		http.Error(w, "TriggerManager type assertion failed", http.StatusInternalServerError)
		return
	}

	// 7. 生成 ID
	triggerID := tm.GetNextID()

	// 8. 设置默认阈值
	thresholdAB := 0.2 // 默认 +A-B 阈值
	thresholdBA := 0.0 // 默认 -A+B 阈值
	if req.ThresholdAB != nil {
		thresholdAB = *req.ThresholdAB
	}
	if req.ThresholdBA != nil {
		thresholdBA = *req.ThresholdBA
	}

	// 8.1. 默认 size（优先 triggerABSize/triggerBASize，兼容 size 同时设两边）
	sizeAB, sizeBA := 1000.0, 1000.0
	if req.TriggerABSize != nil && *req.TriggerABSize > 0 {
		sizeAB = *req.TriggerABSize
	} else if req.Size != nil && *req.Size > 0 {
		sizeAB = *req.Size
	}
	if req.TriggerBASize != nil && *req.TriggerBASize > 0 {
		sizeBA = *req.TriggerBASize
	} else if req.Size != nil && *req.Size > 0 {
		sizeBA = *req.Size
	}

	// 9. 创建 BrickMovingTrigger
	brickTrigger := trigger.NewBrickMovingTrigger(
		triggerID,
		req.Symbol,
		sourceA,
		sourceB,
		traderAType,
		traderBType,
		thresholdAB,
		thresholdBA,
	)

	// 9.1. 设置按方向区分的 size（初始状态即生效，用于 StartSwap 的 swapInfo.Amount）
	brickTrigger.SetConfiguredSizeAB(sizeAB)
	brickTrigger.SetConfiguredSizeBA(sizeBA)
	// 9.2. 设置 size 到 PositionManager，使 updateSwapInfoAmount 只使用 Web 固定 size
	positionManager := position.GetPositionManager()
	if positionManager != nil {
		positionManager.SetConfiguredSizeForSymbol(req.Symbol, sizeAB, sizeBA)
		analyticsInstance := analytics.NewAnalytics(thresholdAB, sizeAB, req.Symbol)

		// 注册 Analytics 到 PositionManager
		positionManager.RegisterAnalytics(req.Symbol, analyticsInstance)
		d.logger.Infof("Registered Analytics with default size %.2f for symbol %s", sizeAB, req.Symbol)

		// 设置初始 maxSize（通过调用 CalculateSlippage 来设置，但需要实际的 trader）
		// 如果 sourceB 是交易所类型，可以使用它来设置 maxSize
		if sourceB != nil {
			// 尝试计算滑点来设置 maxSize
			// 使用一个默认的 amount 值
			defaultAmount := sizeAB
			if defaultAmount <= 0 {
				defaultAmount = 1000.0
			}
			// 计算买入和卖出的滑点来设置 maxSize
			// 注意：这里使用 futures=true，因为大多数交易对使用合约
			analyticsInstance.CalculateSlippage(sourceB, req.Symbol, defaultAmount, true, model.OrderSideBuy, 0.5)
			analyticsInstance.CalculateSlippage(sourceB, req.Symbol, defaultAmount, true, model.OrderSideSell, 0.5)
			d.logger.Debugf("Set initial maxSize for symbol %s: %.2f", req.Symbol, sizeAB)
		} else {
			// 如果没有 sourceB，暂时先注册，maxSize 会在后续的滑点计算中设置
			d.logger.Debugf("sourceB is nil, maxSize will be set later for symbol %s", req.Symbol)
		}
	}

	// 10. 配置 trigger（设置类型和链ID）
	// 注意：BrickMovingTrigger 没有 configureTrigger 方法，需要手动设置
	// 但我们可以通过 proto.Trigger 接口设置一些基本配置

	// 10.1 注册交易完成回调：交易成功后自动触发 Pipeline 联动（按本 trigger 的 triggerId 查 hedging 与 pipeline）
	triggerIDStr := strconv.FormatUint(brickTrigger.GetID(), 10)
	brickTrigger.SetOnTradeComplete(func(symbol string, direction string) {
		key := pipelineConfigKey(symbol, triggerIDStr)
		d.brickMovingHedgingMu.RLock()
		hedging := d.brickMovingHedgingCfgs[key]
		d.brickMovingHedgingMu.RUnlock()
		if hedging == nil || !hedging.AutoWithdrawEnabled {
			return
		}
		pipelineDir := "forward"
		if direction == "BA" {
			pipelineDir = "backward"
		}
		d.logger.Infof("Trade completed for %s direction=%s, auto-triggering pipeline %s (triggerId=%s)", symbol, direction, pipelineDir, triggerIDStr)
		go func() {
			if _, err := d.runBrickMovingPipelineInternal(symbol, pipelineDir, triggerIDStr, false); err != nil {
				d.logger.Warnf("Auto pipeline run failed for %s %s (triggerId=%s): %v", symbol, pipelineDir, triggerIDStr, err)
				if msg, marshalErr := json.Marshal(map[string]interface{}{
					"type":    "pipeline_auto_run_failed",
					"symbol":  symbol,
					"dir":     pipelineDir,
					"message": err.Error(),
				}); marshalErr == nil {
					d.wsHub.Broadcast(msg)
				}
			}
		}()
	})

	brickTrigger.SetOnSpreadUpdate(func(symbol string, triggerID uint64, diffAB, diffBA float64) {
		d.trySmartFlipWithdraw(symbol, strconv.FormatUint(triggerID, 10), diffBA)
	})

	// 11. 添加 trigger
	if err := d.triggerManager.AddTrigger(req.Symbol, brickTrigger); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 11.1 从 LayerZero 等拉取该 trigger 对应资产的 OFT 地址，供后续跨链使用
	go d.refreshBridgeAddressesForSymbol(req.Symbol)

	// 11.2 异步扫链：收集 base symbol 在所有链上的合约地址
	go d.scanTokenForRegistry(req.Symbol)

	// 12. 同步启动 trigger 以开始价差计算（AddTrigger 之后 trigger 已在 manager 中可查，无需延迟）
	ctx := tm.GetTriggerContext()
	if err := brickTrigger.Start(ctx); err != nil {
		d.logger.Warnf("Failed to auto-start brick-moving trigger %s: %v", req.Symbol, err)
	} else {
		d.logger.Infof("Auto-started brick-moving trigger %s for price diff calculation", req.Symbol)
	}

	// 13. 构建并返回响应（含 id 供列表/详情用 triggerId 区分同 symbol 多 trigger；id 用字符串避免 JS 大数精度丢失）
	response := buildTriggerResponse(req.Symbol, traderAType, traderBType, aInfo, bInfo)
	response["id"] = triggerIDStr
	d.logger.Infof("Created brick-moving trigger: symbol=%s, triggerId=%s (frontend 应带此 id 做保存/应用/自动充提)", req.Symbol, triggerIDStr)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handleUpdateBrickMovingTriggerConfig 更新搬砖 trigger 配置
// 支持普通 Trigger 和 BrickMovingTrigger 两种类型
func (d *Dashboard) handleUpdateBrickMovingTriggerConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Symbol        string   `json:"symbol"`
		Size          *float64 `json:"size"`          // 兼容：同时更新 AB/BA
		TriggerABSize *float64 `json:"triggerABSize"` // +A-B 方向 size
		TriggerBASize *float64 `json:"triggerBASize"` // -A+B 方向 size
		MinThreshold  *float64 `json:"minThreshold"`  // 普通 Trigger 使用，或 BrickMovingTrigger 的 thresholdAB
		MaxThreshold *float64 `json:"maxThreshold"` // 普通 Trigger 使用，或 BrickMovingTrigger 的 thresholdBA
		ThresholdAB  *float64 `json:"thresholdAB"`  // BrickMovingTrigger 专用：+A-B 方向阈值
		ThresholdBA  *float64 `json:"thresholdBA"`  // BrickMovingTrigger 专用：-A+B 方向阈值
		EnabledAB    *bool    `json:"enabledAB"`
		EnabledBA    *bool    `json:"enabledBA"`
		// 搬砖触发间隔（影响触发交易频率）
		OrderLoopMs *int64 `json:"orderLoopMs"` // 尝试交易循环间隔（毫秒），如 500
		CooldownSec *int64 `json:"cooldownSec"` // 同方向冷却时间（秒），如 2
		// 链上配置（搬砖 trigger 有链上端时）
		OnChainSlippageA *string  `json:"onChainSlippageA"`
		OnChainSlippageB *string  `json:"onChainSlippageB"`
		GasMultiplierA   *float64 `json:"gasMultiplierA"`
		GasMultiplierB   *float64 `json:"gasMultiplierB"`
		OnChainGasLimitA *string  `json:"onChainGasLimitA"`
		OnChainGasLimitB *string  `json:"onChainGasLimitB"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// #region agent log
	enabledABVal, enabledBAVal := interface{}(nil), interface{}(nil)
	if req.EnabledAB != nil {
		enabledABVal = *req.EnabledAB
	}
	if req.EnabledBA != nil {
		enabledBAVal = *req.EnabledBA
	}
	debugLogBrickMoving("web_api_brick_moving.go:handleUpdateBrickMovingTriggerConfig", "request decoded", map[string]interface{}{
		"symbol": req.Symbol, "enabledAB": enabledABVal, "enabledBA": enabledBAVal,
		"hasEnabledAB": req.EnabledAB != nil, "hasEnabledBA": req.EnabledBA != nil,
	}, "H-start-stop")
	// #endregion

	// 优先按 triggerId 定位（同 symbol 多 trigger 时）；否则按 symbol
	var tg proto.Trigger
	var err error
	if triggerIDStr := r.URL.Query().Get("triggerId"); triggerIDStr != "" {
		var id uint64
		if _, parseErr := fmt.Sscanf(triggerIDStr, "%d", &id); parseErr == nil && id != 0 {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, err = tm.GetTriggerByIDAsProto(id)
			}
		}
	}
	if tg == nil && err == nil {
		if req.Symbol == "" {
			http.Error(w, "symbol or triggerId is required", http.StatusBadRequest)
			return
		}
		tg, err = d.triggerManager.GetTrigger(req.Symbol)
	}
	// #region agent log
	debugLogBrickMoving("web_api_brick_moving.go:handleUpdateBrickMovingTriggerConfig", "GetTrigger result", map[string]interface{}{
		"symbol": req.Symbol, "getTriggerErr": err != nil, "errMsg": func() string { if err != nil { return err.Error() }; return "" }(), "tgNil": tg == nil,
	}, "H-start-stop")
	// #endregion
	if err != nil || tg == nil {
		http.Error(w, "trigger not found", http.StatusNotFound)
		return
	}
	symbolForUpdate := req.Symbol
	if symbolForUpdate == "" {
		symbolForUpdate = tg.GetSymbol()
	}

	// 更新方向启用状态
	if req.EnabledAB != nil {
		// #region agent log
		debugLogBrickMoving("web_api_brick_moving.go:handleUpdateBrickMovingTriggerConfig", "SetDirectionEnabled AB", map[string]interface{}{"direction": 0, "enabled": *req.EnabledAB}, "H-start-stop")
		// #endregion
		tg.SetDirectionEnabled(0, *req.EnabledAB)
	}
	if req.EnabledBA != nil {
		// #region agent log
		debugLogBrickMoving("web_api_brick_moving.go:handleUpdateBrickMovingTriggerConfig", "SetDirectionEnabled BA", map[string]interface{}{"direction": 1, "enabled": *req.EnabledBA}, "H-start-stop")
		// #endregion
		tg.SetDirectionEnabled(1, *req.EnabledBA)
	}

	// 更新 size：同步到 Analytics 的 configured size，使 swapInfo.Amount 与 Web 一致且不被订单簿结果覆盖
	sizeABFromReq, sizeBAFromReq := 0.0, 0.0
	if req.TriggerABSize != nil && *req.TriggerABSize > 0 {
		sizeABFromReq = *req.TriggerABSize
	} else if req.Size != nil && *req.Size > 0 {
		sizeABFromReq = *req.Size
	}
	if req.TriggerBASize != nil && *req.TriggerBASize > 0 {
		sizeBAFromReq = *req.TriggerBASize
	} else if req.Size != nil && *req.Size > 0 {
		sizeBAFromReq = *req.Size
	}
	if sizeABFromReq > 0 || sizeBAFromReq > 0 {
		positionManager := position.GetPositionManager()
		if positionManager != nil {
			positionManager.SetConfiguredSizeForSymbol(symbolForUpdate, sizeABFromReq, sizeBAFromReq)
			positionManager.TriggerImmediateUpdate(symbolForUpdate)
			d.logger.Infof("Updated configured size for symbol %s: AB=%.2f BA=%.2f, triggered swap amount update", symbolForUpdate, sizeABFromReq, sizeBAFromReq)
			// #region agent log
			debugLogBrickMoving("web_api_brick_moving.go:handleUpdateTriggerConfig", "trigger size updated, TriggerImmediateUpdate called", map[string]interface{}{"symbol": symbolForUpdate, "triggerABSize": sizeABFromReq, "triggerBASize": sizeBAFromReq}, "H1,H2")
			// #endregion
		}
	}

	// 检查是否是 BrickMovingTrigger
	if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
		if req.TriggerABSize != nil && *req.TriggerABSize > 0 {
			brickTrigger.SetConfiguredSizeAB(*req.TriggerABSize)
		}
		if req.TriggerBASize != nil && *req.TriggerBASize > 0 {
			brickTrigger.SetConfiguredSizeBA(*req.TriggerBASize)
		}
		if req.Size != nil && *req.Size > 0 && (req.TriggerABSize == nil || req.TriggerBASize == nil) {
			if req.TriggerABSize == nil {
				brickTrigger.SetConfiguredSizeAB(*req.Size)
			}
			if req.TriggerBASize == nil {
				brickTrigger.SetConfiguredSizeBA(*req.Size)
			}
		}
		if req.OrderLoopMs != nil && *req.OrderLoopMs > 0 {
			brickTrigger.SetOrderLoopInterval(time.Duration(*req.OrderLoopMs) * time.Millisecond)
			d.logger.Infof("Updated brick trigger %s orderLoop: %d ms", symbolForUpdate, *req.OrderLoopMs)
		}
		if req.CooldownSec != nil && *req.CooldownSec >= 0 {
			brickTrigger.SetCooldown(time.Duration(*req.CooldownSec) * time.Second)
			d.logger.Infof("Updated brick trigger %s cooldown: %d s", symbolForUpdate, *req.CooldownSec)
		}
		// BrickMovingTrigger：使用 thresholdAB 和 thresholdBA
		if req.ThresholdAB != nil {
			brickTrigger.SetThresholdAB(*req.ThresholdAB)
		} else if req.MinThreshold != nil {
			// 兼容：如果没有 thresholdAB，使用 minThreshold
			brickTrigger.SetThresholdAB(*req.MinThreshold)
		}
		if req.ThresholdBA != nil {
			brickTrigger.SetThresholdBA(*req.ThresholdBA)
		} else if req.MaxThreshold != nil {
			// 兼容：如果没有 thresholdBA，使用 maxThreshold
			brickTrigger.SetThresholdBA(*req.MaxThreshold)
		}
		// 链上滑点、Gas 乘数、Gas Limit
		if req.OnChainSlippageA != nil {
			_ = brickTrigger.SetOnChainSlippageA(*req.OnChainSlippageA)
		}
		if req.OnChainSlippageB != nil {
			_ = brickTrigger.SetOnChainSlippageB(*req.OnChainSlippageB)
		}
		if req.GasMultiplierA != nil {
			_ = brickTrigger.SetGasMultiplierA(*req.GasMultiplierA)
		}
		if req.GasMultiplierB != nil {
			_ = brickTrigger.SetGasMultiplierB(*req.GasMultiplierB)
		}
		if req.OnChainGasLimitA != nil {
			_ = brickTrigger.SetOnChainGasLimitA(*req.OnChainGasLimitA)
		}
		if req.OnChainGasLimitB != nil {
			_ = brickTrigger.SetOnChainGasLimitB(*req.OnChainGasLimitB)
		}
	} else {
		// 普通 Trigger：使用 minThreshold 和 maxThreshold
		if req.MinThreshold != nil {
			if err := tg.SetMinThreshold(*req.MinThreshold); err != nil {
				http.Error(w, "Failed to update min threshold: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		if req.MaxThreshold != nil {
			if err := tg.SetMaxThreshold(*req.MaxThreshold); err != nil {
				http.Error(w, "Failed to update max threshold: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	// 每次配置更新后重新设置链上 SwapInfo，确保 OK 等收到最新的 Amount/Slippage/GasLimit
	if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
		brickTrigger.RefreshSwapInfo()
	}

	// 当用户点击「启动+A-B」或「启动-A+B」时仅会发 enabledAB/enabledBA，不会调 trigger.Start()；
	// setupOnchainSubscription() 只在 Start() -> startConsumePriceMsg() 里执行。若 trigger 未运行则先 Start，保证链上订阅会建立。
	if _, ok := tg.(*trigger.BrickMovingTrigger); ok {
		enableAB := req.EnabledAB != nil && *req.EnabledAB
		enableBA := req.EnabledBA != nil && *req.EnabledBA
		if (enableAB || enableBA) && tg.GetStatus() != "running" {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				ctx := tm.GetTriggerContext()
				if err := tg.Start(ctx); err != nil {
					d.logger.Warnf("Start brick-moving trigger %s on enable direction: %v", symbolForUpdate, err)
				} else {
					d.logger.Infof("Started brick-moving trigger %s so setupOnchainSubscription runs", symbolForUpdate)
				}
			}
		}
		go d.refreshBridgeAddressesForSymbol(req.Symbol)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleBrickMovingTriggerHedging 套保配置：GET 获取 / POST 更新
func (d *Dashboard) handleBrickMovingTriggerHedging(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		d.handleGetBrickMovingTriggerHedging(w, r)
		return
	}
	if r.Method == http.MethodPost {
		d.handleUpdateBrickMovingTriggerHedging(w, r)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// handleGetBrickMovingTriggerHedging 获取套保配置（支持 triggerId 区分同 symbol 多 trigger）
func (d *Dashboard) handleGetBrickMovingTriggerHedging(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	if triggerSymbol == "" {
		triggerSymbol = r.URL.Query().Get("symbol")
	}
	triggerIDStr := r.URL.Query().Get("triggerId")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingHedgingMu.RLock()
	cfg := d.brickMovingHedgingCfgs[key]
	d.brickMovingHedgingMu.RUnlock()
	out := map[string]interface{}{
		"symbol":                 triggerSymbol,
		"enabled":                false,
		"exchangeType":           "binance",
		"hedgingSymbol":          triggerSymbol,
		"size":                   float64(1000),
		"positionOpenSize":       float64(0),
		"position": float64(0),
		"openPositionSide":       "A",
		"autoWithdrawEnabled":    false,
		"autoWithdrawUseFixed":   true,
		"autoWithdrawFixedSize":  float64(0),
		"autoWithdrawPercentage": float64(0),
	}
	if cfg != nil {
		out["enabled"] = cfg.Enabled
		out["exchangeType"] = cfg.ExchangeType
		if cfg.HedgingSymbol != "" {
			out["hedgingSymbol"] = cfg.HedgingSymbol
		}
		out["size"] = cfg.Size
		out["positionOpenSize"] = cfg.PositionOpenSize
		out["position"] = cfg.Position
		if cfg.OpenPositionSide == "A" || cfg.OpenPositionSide == "B" {
			out["openPositionSide"] = cfg.OpenPositionSide
		} else {
			out["openPositionSide"] = "A"
		}
		out["autoWithdrawEnabled"] = cfg.AutoWithdrawEnabled
		out["autoWithdrawUseFixed"] = cfg.AutoWithdrawUseFixed
		out["autoWithdrawFixedSize"] = cfg.AutoWithdrawFixedSize
		out["autoWithdrawPercentage"] = cfg.AutoWithdrawPercentage
		if cfg.QuoteAsset != "" {
			out["quoteAsset"] = cfg.QuoteAsset
		} else {
			out["quoteAsset"] = "USDT"
		}
		out["smartFlipEnabled"] = cfg.SmartFlipEnabled
		th := cfg.SmartFlipThreshold
		if th <= 0 {
			th = 1.5
		}
		out["smartFlipThreshold"] = th
	} else {
		out["smartFlipEnabled"] = false
		out["smartFlipThreshold"] = 1.5
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// handleUpdateBrickMovingTriggerHedging 更新搬砖 trigger 的对冲配置
func (d *Dashboard) handleUpdateBrickMovingTriggerHedging(w http.ResponseWriter, r *http.Request) {

	var req struct {
		Symbol                 string  `json:"symbol"`
		TriggerID              string  `json:"triggerId"` // 同 symbol 多 trigger 时区分
		Enabled                bool    `json:"enabled"`
		ExchangeType           string  `json:"exchangeType"`
		HedgingSymbol          string  `json:"hedgingSymbol"`
		Size                   float64 `json:"size"`
		PositionOpenSize       float64 `json:"positionOpenSize"`
		Position               float64 `json:"position"`
		OpenPositionSide       string  `json:"openPositionSide"` // "A" | "B"，默认 "A"
		AutoWithdrawEnabled    bool    `json:"autoWithdrawEnabled"`
		AutoWithdrawUseFixed   bool    `json:"autoWithdrawUseFixed"`
		AutoWithdrawFixedSize  float64 `json:"autoWithdrawFixedSize"`
		AutoWithdrawPercentage float64 `json:"autoWithdrawPercentage"`
		QuoteAsset             string  `json:"quoteAsset"` // B→A 计价/提现资产，默认 USDT
		SmartFlipEnabled       bool    `json:"smartFlipEnabled"`
		SmartFlipThreshold     float64 `json:"smartFlipThreshold"` // 翻转阈值 %，默认 1.5
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(req.Symbol, req.TriggerID)
	d.brickMovingHedgingMu.Lock()
	existing := d.brickMovingHedgingCfgs[key]
	if existing == nil {
		existing = &brickMovingHedgingConfig{}
	}
	quoteAsset := strings.TrimSpace(req.QuoteAsset)
	if quoteAsset == "" {
		quoteAsset = existing.QuoteAsset
	}
	if quoteAsset == "" {
		quoteAsset = "USDT"
	}
	smartFlipThreshold := req.SmartFlipThreshold
	if smartFlipThreshold <= 0 && existing != nil && existing.SmartFlipThreshold > 0 {
		smartFlipThreshold = existing.SmartFlipThreshold
	}
	if smartFlipThreshold <= 0 {
		smartFlipThreshold = 1.5
	}
	smartFlipEnabled := req.SmartFlipEnabled
	if !smartFlipEnabled && existing != nil && existing.SmartFlipEnabled {
		smartFlipEnabled = existing.SmartFlipEnabled
	}
	openPositionSide := strings.TrimSpace(strings.ToUpper(req.OpenPositionSide))
	if openPositionSide != "A" && openPositionSide != "B" {
		if existing != nil && (existing.OpenPositionSide == "A" || existing.OpenPositionSide == "B") {
			openPositionSide = existing.OpenPositionSide
		} else {
			openPositionSide = "A"
		}
	}
	d.brickMovingHedgingCfgs[key] = &brickMovingHedgingConfig{
		ExchangeType:           req.ExchangeType,
		HedgingSymbol:          req.HedgingSymbol,
		Size:                   req.Size,
		PositionOpenSize:       req.PositionOpenSize,
		Position:               req.Position,
		Enabled:                req.Enabled,
		OpenPositionSide:       openPositionSide,
		AutoWithdrawEnabled:    req.AutoWithdrawEnabled,
		AutoWithdrawUseFixed:   req.AutoWithdrawUseFixed,
		AutoWithdrawFixedSize:  req.AutoWithdrawFixedSize,
		AutoWithdrawPercentage: req.AutoWithdrawPercentage,
		QuoteAsset:             quoteAsset,
		SmartFlipEnabled:       smartFlipEnabled,
		SmartFlipThreshold:     smartFlipThreshold,
	}
	d.brickMovingHedgingMu.Unlock()

	d.saveBrickMovingHedging() // 持久化到 data/brick_moving_hedging.json，确保自动充提按钮状态重启后保留

	// 保存后必须更新链上交易 amount：让该 trigger 的 OnchainTrader 使用 positionOpenSize，避免开仓时用到 Trigger 日常 Size
	d.updateTriggerSwapAmountForHedging(req.Symbol, req.PositionOpenSize, req.Size)

	// 选交易所即订阅套保合约价，供价差展示与平仓使用
	if req.ExchangeType != "" {
		if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
			tm.SubscribeHedgingPrice(req.Symbol, req.TriggerID, req.ExchangeType)
		}
	}

	d.logger.Infof("Update hedging (自动充提): symbol=%s, triggerId=%s, configKey=%s, enabled=%v, autoWithdrawEnabled=%v, autoWithdrawFixedSize=%.6f",
		req.Symbol, req.TriggerID, key, req.Enabled, req.AutoWithdrawEnabled, req.AutoWithdrawFixedSize)
	if req.TriggerID == "" && req.AutoWithdrawEnabled {
		d.logger.Warnf("自动充提已开启但 triggerId 为空，configKey=%s；中间节点调度器从 pipeline 名解析的 triggerId 若也为空才能命中，否则会 skipped", key)
	}

	// 仅触发中间节点调度器立即检测（3+ 节点余额转账）。不在此处调用 runAutoWithdrawTick，
	// 避免「仅保存套保配置」就立刻触发首节点提币；首节点提币由 runAutoWithdrawLoop 每 5 秒轮询触发。
	if req.AutoWithdrawEnabled {
		pipeline.GetMiddleNodeScheduler().TriggerImmediateCheck()
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// handleUpdateBrickMovingTriggerSmartFlip 仅更新智能翻转充提配置（开关、阈值），供智能翻转卡片失焦保存等使用
func (d *Dashboard) handleUpdateBrickMovingTriggerSmartFlip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Symbol             string  `json:"symbol"`
		TriggerID          string  `json:"triggerId"`
		SmartFlipEnabled   bool    `json:"smartFlipEnabled"`
		SmartFlipThreshold float64 `json:"smartFlipThreshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	th := req.SmartFlipThreshold
	if th <= 0 {
		th = 1.5
	}
	if th < 0.1 || th > 20 {
		http.Error(w, "smartFlipThreshold must be between 0.1 and 20", http.StatusBadRequest)
		return
	}
	key := pipelineConfigKey(req.Symbol, req.TriggerID)
	d.brickMovingHedgingMu.Lock()
	cfg := d.brickMovingHedgingCfgs[key]
	if cfg == nil {
		cfg = &brickMovingHedgingConfig{}
		d.brickMovingHedgingCfgs[key] = cfg
	}
	cfg.SmartFlipEnabled = req.SmartFlipEnabled
	cfg.SmartFlipThreshold = th
	d.brickMovingHedgingMu.Unlock()
	d.saveBrickMovingHedging()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// updateTriggerSwapAmountForHedging 在保存套保配置后更新该 trigger 的链上 SwapInfo.Amount，使开仓使用 positionOpenSize（或 size），不与 Trigger 日常 Size 混用
func (d *Dashboard) updateTriggerSwapAmountForHedging(triggerSymbol string, positionOpenSize, size float64) {
	tg, err := d.triggerManager.GetTrigger(triggerSymbol)
	if err != nil {
		return
	}
	bmt, ok := tg.(*trigger.BrickMovingTrigger)
	if !ok {
		return
	}
	amount := positionOpenSize
	if amount <= 0 {
		amount = size
	}
	if amount <= 0 {
		return
	}
	amountStr := strconv.FormatFloat(amount, 'f', 4, 64)
	bmt.UpdateSwapAmountForOpen(amountStr)
}

// handleUpdateBrickMovingTriggerAutoTransfer 更新搬砖 trigger 的自动转账配置
func (d *Dashboard) handleUpdateBrickMovingTriggerAutoTransfer(w http.ResponseWriter, r *http.Request) {
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

	d.logger.Infof("Update auto transfer config for %s: enabled=%v (not implemented)", req.Symbol, req.Enabled)

	writeJSONError(w, http.StatusNotImplemented, "自动转账配置功能尚未实现")
}

// handleBrickMovingTriggerStart 启动搬砖 trigger（与普通 trigger 相同）
func (d *Dashboard) handleBrickMovingTriggerStart(w http.ResponseWriter, r *http.Request) {
	d.handleStartTrigger(w, r)
}

// handleBrickMovingTriggerStop 停止搬砖 trigger（与普通 trigger 相同）
func (d *Dashboard) handleBrickMovingTriggerStop(w http.ResponseWriter, r *http.Request) {
	d.handleStopTrigger(w, r)
}

// handleBrickMovingTriggerDelete 删除搬砖 trigger，并清理搬砖区关联状态
func (d *Dashboard) handleBrickMovingTriggerDelete(w http.ResponseWriter, r *http.Request) {
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
	tm, ok := d.triggerManager.(*trigger.TriggerManager)
	if !ok {
		http.Error(w, "trigger manager type assertion failed", http.StatusInternalServerError)
		return
	}
	if err := tm.RemoveTriggerByID(tg.GetID()); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	d.cleanupBrickMovingState(symbol, strconv.FormatUint(tg.GetID(), 10))

	if err := d.contractMappingMgr.RemoveMapping(symbol); err != nil {
		d.logger.Debugf("Failed to remove contract mapping for %s (may not exist): %v", symbol, err)
	} else {
		if err := d.contractMappingMgr.SaveToFile(); err != nil {
			d.logger.Warnf("Failed to save contract mapping after deletion: %v", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "symbol": symbol})
}

// handleGetBrickMovingPipelines 获取搬砖 trigger 的 pipeline 配置（支持 triggerId 区分同 symbol 多 trigger）
func (d *Dashboard) handleGetBrickMovingPipelines(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol parameter is required", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()

	result := map[string]interface{}{
		"forward": map[string]interface{}{
			"nodes": []interface{}{},
			"edges": []interface{}{},
		},
		"backward": map[string]interface{}{
			"nodes": []interface{}{},
			"edges": []interface{}{},
		},
		"mirrorForward":  map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}},
		"mirrorBackward": map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}},
		"activeForwardPipelineId":  "",
		"activeBackwardPipelineId": "",
		"forwardReachable":                true,
		"backwardReachable":               true,
		"forwardReachableReason":         "",
		"backwardReachableReason":         "",
		"mirrorForwardReachable":          true,
		"mirrorBackwardReachable":         true,
		"mirrorForwardReachableReason":    "",
		"mirrorBackwardReachableReason":   "",
	}
	if cfg != nil {
		if cfg.ForwardNodes != nil {
			result["forward"].(map[string]interface{})["nodes"] = sliceMapToInterface(cfg.ForwardNodes)
		}
		if cfg.ForwardEdges != nil {
			result["forward"].(map[string]interface{})["edges"] = sliceMapToInterface(cfg.ForwardEdges)
		}
		if cfg.BackwardNodes != nil {
			result["backward"].(map[string]interface{})["nodes"] = sliceMapToInterface(cfg.BackwardNodes)
		}
		if cfg.BackwardEdges != nil {
			result["backward"].(map[string]interface{})["edges"] = sliceMapToInterface(cfg.BackwardEdges)
		}
		if len(cfg.ForwardNodes) > 0 {
			mn, me := mirrorPipelineNodeMaps(cfg.ForwardNodes, cfg.ForwardEdges)
			if mn != nil {
				result["mirrorForward"].(map[string]interface{})["nodes"] = sliceMapToInterface(mn)
				result["mirrorForward"].(map[string]interface{})["edges"] = sliceMapToInterface(me)
			}
		}
		if len(cfg.BackwardNodes) > 0 {
			mn, me := mirrorPipelineNodeMaps(cfg.BackwardNodes, cfg.BackwardEdges)
			if mn != nil {
				result["mirrorBackward"].(map[string]interface{})["nodes"] = sliceMapToInterface(mn)
				result["mirrorBackward"].(map[string]interface{})["edges"] = sliceMapToInterface(me)
			}
		}
		result["activeForwardPipelineId"] = cfg.ActiveForwardPipelineId
		result["activeBackwardPipelineId"] = cfg.ActiveBackwardPipelineId
		result["forwardReachable"] = cfg.ForwardReachable
		result["backwardReachable"] = cfg.BackwardReachable
		result["forwardReachableReason"] = cfg.ForwardReachableReason
		result["backwardReachableReason"] = cfg.BackwardReachableReason
		result["mirrorForwardReachable"] = cfg.MirrorForwardReachable
		result["mirrorBackwardReachable"] = cfg.MirrorBackwardReachable
		result["mirrorForwardReachableReason"] = cfg.MirrorForwardReachableReason
		result["mirrorBackwardReachableReason"] = cfg.MirrorBackwardReachableReason
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func sliceMapToInterface(s []map[string]interface{}) []interface{} {
	out := make([]interface{}, len(s))
	for i := range s {
		out[i] = s[i]
	}
	return out
}

// mirrorPipelineNodeMaps 根据正向 pipeline 的 nodes/edges 生成镜像（节点顺序反转、边方向反转），不落库，按需生成
func mirrorPipelineNodeMaps(nodes []map[string]interface{}, edges []map[string]interface{}) (mirrorNodes []map[string]interface{}, mirrorEdges []map[string]interface{}) {
	if len(nodes) == 0 {
		return nil, nil
	}
	n := len(nodes)
	mirrorNodes = make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		mirrorNodes[i] = nodes[n-1-i]
	}
	mirrorEdges = make([]map[string]interface{}, 0, len(edges))
	for _, e := range edges {
		fromIdx, ok1 := e["from"].(float64)
		toIdx, ok2 := e["to"].(float64)
		if !ok1 || !ok2 {
			continue
		}
		fi, ti := int(fromIdx), int(toIdx)
		if fi < 0 || fi >= n || ti < 0 || ti >= n {
			continue
		}
		newFrom := n - 1 - ti
		newTo := n - 1 - fi
		me := make(map[string]interface{})
		for k, v := range e {
			me[k] = v
		}
		me["from"] = float64(newFrom)
		me["to"] = float64(newTo)
		mirrorEdges = append(mirrorEdges, me)
	}
	return mirrorNodes, mirrorEdges
}

// handleSaveBrickMovingPipeline 保存搬砖 trigger 的 pipeline 配置（nodes + edges，不创建 PipelineManager 中的 Pipeline）
func (d *Dashboard) handleSaveBrickMovingPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TriggerSymbol     string                   `json:"triggerSymbol"`
		TriggerID         string                   `json:"triggerId"` // 同 symbol 多 trigger 时区分
		Direction         string                   `json:"direction"` // "forward" or "backward"
		Nodes             []map[string]interface{} `json:"nodes"`
		Edges             []map[string]interface{} `json:"edges"`
		Reachable         *bool                    `json:"reachable"`         // 本方向是否可达（应用探测时由 segment.available 推导）
		ReachableReason   string                   `json:"reachableReason"`   // 不可达原因（可选）
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TriggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}

	if req.Direction != "forward" && req.Direction != "backward" {
		http.Error(w, "direction must be 'forward' or 'backward'", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(req.TriggerSymbol, req.TriggerID)
	d.brickMovingPipelineMu.Lock()
	existing := d.brickMovingPipelines[key]
	var otherDirLen int
	if existing != nil {
		if req.Direction == "forward" {
			otherDirLen = len(existing.BackwardNodes)
		} else {
			otherDirLen = len(existing.ForwardNodes)
		}
	}
	if d.brickMovingPipelines[key] == nil {
		d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
	}
	cfg := d.brickMovingPipelines[key]
	// 只更新当前方向的 nodes/edges；可达性仅在前端显式传 reachable 时更新，未传则保留原值（避免覆盖应用后镜像探测写入的不可达结果）
	if req.Direction == "forward" {
		cfg.ForwardNodes = req.Nodes
		cfg.ForwardEdges = req.Edges
		if req.Reachable != nil {
			cfg.ForwardReachable = *req.Reachable
			cfg.ForwardReachableReason = req.ReachableReason
		}
	} else {
		cfg.BackwardNodes = req.Nodes
		cfg.BackwardEdges = req.Edges
		if req.Reachable != nil {
			cfg.BackwardReachable = *req.Reachable
			cfg.BackwardReachableReason = req.ReachableReason
		}
	}
	d.brickMovingPipelineMu.Unlock()

	reachableLog := "nil(默认可达)"
	if req.Reachable != nil {
		if *req.Reachable {
			reachableLog = "true"
		} else {
			reachableLog = "false"
			if req.ReachableReason != "" {
				reachableLog += " reason=" + req.ReachableReason
			}
		}
	}
	d.logger.Infof("Save pipeline config: triggerSymbol=%s, triggerId=%s, configKey=%s, direction=%s, nodes=%d, edges=%d, reachable=%s, otherDirectionNodes=%d",
		req.TriggerSymbol, req.TriggerID, key, req.Direction, len(req.Nodes), len(req.Edges), reachableLog, otherDirLen)
	if otherDirLen == 0 && existing != nil {
		d.logger.Warnf("Save: 当前 configKey=%s 下对侧方向节点数为 0；若之前已保存过另一侧，请确认本次请求带与当时一致的 triggerId，否则会落在不同 key 导致应用后「只显示一侧」", key)
	}

	d.saveBrickMovingPipelines()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// handleApplyBrickMovingPipeline 将已存储的 pipeline 配置「应用」为当前生效的 Pipeline（创建/更新到 PipelineManager）
func (d *Dashboard) handleApplyBrickMovingPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TriggerSymbol string `json:"triggerSymbol"`
		TriggerID     string `json:"triggerId"` // 同 symbol 多 trigger 时区分
		Direction     string `json:"direction"` // "forward" or "backward"
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.TriggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}

	if req.Direction != "forward" && req.Direction != "backward" {
		http.Error(w, "direction must be 'forward' or 'backward'", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(req.TriggerSymbol, req.TriggerID)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()

	var nodes []map[string]interface{}
	var edges []map[string]interface{}
	if req.Direction == "forward" {
		if cfg == nil || len(cfg.ForwardNodes) == 0 {
			http.Error(w, "no forward pipeline config saved for this trigger", http.StatusBadRequest)
			return
		}
		nodes = cfg.ForwardNodes
		edges = cfg.ForwardEdges
	} else {
		if cfg == nil || len(cfg.BackwardNodes) == 0 {
			http.Error(w, "no backward pipeline config saved for this trigger", http.StatusBadRequest)
			return
		}
		nodes = cfg.BackwardNodes
		edges = cfg.BackwardEdges
	}
	if edges == nil {
		edges = []map[string]interface{}{}
	}

	defaultAsset := extractBaseSymbol(req.TriggerSymbol)

	builtNodes, builtEdges, needBridge, err := d.buildPipelineFromNodeMaps(nodes, edges, defaultAsset)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "build pipeline: " + err.Error()})
		return
	}

	// 提前将 pipeline 涉及的所有链注册到钱包余额查询列表，并强制刷新
	// 这样后续 autoDiscoverOFT 时 WalletInfo 已有中间链的代币数据（包括正确的合约地址）
	{
		var pipelineChainIDs []string
		for _, n := range builtNodes {
			cid := chainIDFromNodeID(n.GetID())
			if cid != "" {
				pipelineChainIDs = append(pipelineChainIDs, cid)
			}
		}
		if len(pipelineChainIDs) > 0 {
			position.AddChainsGlobal(pipelineChainIDs...)
			// 强制刷新，确保 OKX DEX API 已查询所有链的余额（包含 TokenContractAddress）
			if wm := position.GetWalletManager(); wm != nil {
				_ = wm.ForceRefresh()
			}
			d.logger.Infof("已将 pipeline 涉及的链 %v 注册到钱包余额查询并强制刷新", pipelineChainIDs)
		}
	}

	pipelineName := "brick-" + req.TriggerSymbol + "-" + req.Direction
	if req.TriggerID != "" {
		pipelineName = "brick-" + req.TriggerSymbol + "-" + req.TriggerID + "-" + req.Direction
	}
	if req.TriggerID == "" {
		d.logger.Warnf("Apply pipeline: triggerId 为空，pipeline 名称将无 triggerId，中间节点调度器会按 symbol 查套保 key 导致 auto-withdraw 可能不生效；请确保前端传入 triggerId")
	}
	d.logger.Infof("Apply pipeline: triggerSymbol=%s, triggerId=%s, direction=%s, pipelineName=%s",
		req.TriggerSymbol, req.TriggerID, req.Direction, pipelineName)
	pm := pipeline.GetPipelineManager()

	// 若已存在同名 pipeline 先删除再创建
	_ = pm.DeletePipeline(pipelineName)
	// 带 triggerId 应用时，删除同 symbol 同方向的旧版「无 triggerId」pipeline，避免调度器仍轮询到并报 auto-withdraw disabled
	if req.TriggerID != "" {
		_ = pm.DeletePipeline("brick-" + req.TriggerSymbol + "-" + req.Direction)
	}

	var bridgeMgr *bridge.Manager
	if needBridge {
		bridgeMgr = d.buildBridgeManager(req.TriggerSymbol, builtNodes)
	}

	p, err := pm.CreatePipeline(pipelineName, builtNodes, builtEdges)
	if err != nil {
		d.logger.Errorf("Apply brick-moving pipeline failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "create pipeline: " + err.Error()})
		return
	}

	if needBridge && bridgeMgr != nil {
		p.SetBridgeManager(bridgeMgr)
	}

	// 用 OFT 注册表中的正确地址更新 pipeline 节点的 TokenAddress
	// （因为 buildPipelineFromNodeMaps 时 OFT 注册表可能还没有正确地址，
	//  discoverOFTFromWalletInfo 之后注册表已更新，需要回写到节点）
	// 同时，如果 OFT 条目缺少 UnderlyingTokenAddress，尝试链上查询 token() 补全
	{
		reg := d.getOrCreateBridgeOFTRegistry()
		// 从持久化文件恢复 UnderlyingTokenAddress（OFT Adapter 底层 ERC-20 地址）
		applyDiscoveredOFTsToRegistry(reg)

		// 如果存在 OFT 条目但缺少 UnderlyingTokenAddress，尝试链上查询 token() 补全
		if needBridge {
			cfg := config.GetGlobalConfig()
			rpcURLs2 := constants.GetAllDefaultRPCURLs()
			if cfg != nil && cfg.Bridge.LayerZero.RPCURLs != nil {
				for cid, url := range cfg.Bridge.LayerZero.RPCURLs {
					if url != "" {
						rpcURLs2[cid] = url
					}
				}
			}
			lz2 := layerzero.NewLayerZero(rpcURLs2, true)
			for _, node := range p.Nodes() {
				if node.GetType() != pipeline.NodeTypeOnchain {
					continue
				}
				onNode, ok := node.(*pipeline.OnchainNode)
				if !ok {
					continue
				}
				chainID := onNode.GetChainID()
				asset := extractBaseSymbol(req.TriggerSymbol)
				if t, found := reg.Get(chainID, asset); found && t.Address != "" && t.UnderlyingTokenAddress == "" {
					underlyingToken, _ := lz2.QueryUnderlyingToken(chainID, t.Address)
					if underlyingToken != "" {
						t.UnderlyingTokenAddress = underlyingToken
						reg.Upsert(*t)
						persistDiscoveredOFT(chainID, asset+":erc20", underlyingToken)
						d.logger.Infof("Discovered underlying ERC-20 for %s on chain %s: oft=%s, erc20=%s", asset, chainID, t.Address, underlyingToken)
					}
				}
			}
		}

		for _, node := range p.Nodes() {
			if node.GetType() != pipeline.NodeTypeOnchain {
				continue
			}
			onNode, ok := node.(*pipeline.OnchainNode)
			if !ok {
				continue
			}
			chainID := onNode.GetChainID()
			asset := extractBaseSymbol(req.TriggerSymbol)
			if t, found := reg.Get(chainID, asset); found && t.Address != "" {
				// 使用 GetERC20Address：如果是 OFT Adapter 则返回底层 ERC-20 地址，
				// 否则返回 OFT 地址本身（纯 OFT 同时实现 ERC-20）
				erc20Addr := t.GetERC20Address()
				oldAddr := onNode.GetTokenAddress()
				// #region agent log
				debugLogBrickMoving("handleApply:updateNodeTokenAddr", "pipeline node token address update check", map[string]interface{}{
					"nodeID": node.GetID(), "chainID": chainID, "asset": asset,
					"oldAddr": oldAddr, "erc20Addr": erc20Addr,
					"oftAddr": t.Address, "underlyingAddr": t.UnderlyingTokenAddress,
					"willUpdate": !strings.EqualFold(oldAddr, erc20Addr),
				}, "H2")
				// #endregion
				if !strings.EqualFold(oldAddr, erc20Addr) {
					onNode.SetTokenAddress(erc20Addr)
					d.logger.Infof("Updated pipeline node %s tokenAddress: %s -> %s (erc20, from OFT registry chain %s, oft=%s, underlying=%s)",
						node.GetID(), oldAddr, erc20Addr, chainID, t.Address, t.UnderlyingTokenAddress)
				}
			}
		}
	}

	// 应用自动充提配置到 pipeline edges
	d.applyAutoWithdrawConfigToPipeline(p, req.TriggerSymbol, req.TriggerID)

	// 审计日志：探测币种与首边资产（backward 首步提现为 edge.Asset=USDT，与充提链一致）
	if nodes := p.Nodes(); len(nodes) >= 2 {
		if edge, has := p.GetEdgeConfig(nodes[0].GetID(), nodes[1].GetID()); has && edge != nil {
			d.logger.Infow("Brick-moving pipeline applied", "triggerSymbol", req.TriggerSymbol, "direction", req.Direction, "firstEdgeAsset", edge.Asset, "firstEdgeNetwork", edge.Network)
		}
	}

	d.brickMovingPipelineMu.Lock()
	cfgBefore := d.brickMovingPipelines[key]
	var nFwd, nBwd int
	var idFwd, idBwd string
	if cfgBefore != nil {
		nFwd, nBwd = len(cfgBefore.ForwardNodes), len(cfgBefore.BackwardNodes)
		idFwd, idBwd = cfgBefore.ActiveForwardPipelineId, cfgBefore.ActiveBackwardPipelineId
	}
	if d.brickMovingPipelines[key] == nil {
		d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
	}
	if req.Direction == "forward" {
		d.brickMovingPipelines[key].ActiveForwardPipelineId = p.ID()
	} else {
		d.brickMovingPipelines[key].ActiveBackwardPipelineId = p.ID()
	}
	d.brickMovingPipelineMu.Unlock()

	// 诊断：若当前 config 下对侧方向节点数为 0，说明本次 key 与之前应用另一侧时用的 key 不一致（例如未传 triggerId 导致 key=symbol，另一侧在 symbol_id 下）
	d.logger.Infof("Apply pipeline: configKey=%s triggerId=%q direction=%s pipelineId=%s | config before: forwardNodes=%d backwardNodes=%d activeFwd=%q activeBwd=%q",
		key, req.TriggerID, req.Direction, p.ID(), nFwd, nBwd, idFwd, idBwd)
	if req.Direction == "backward" && nFwd == 0 {
		d.logger.Warnf("Apply backward: 当前 config 下 forward 无节点，A→B 状态会「看起来被覆盖」；若之前已应用过 A→B，请确认本次请求带与当时一致的 triggerId")
	}
	if req.Direction == "forward" && nBwd == 0 {
		d.logger.Warnf("Apply forward: 当前 config 下 backward 无节点；若之后应用 B→A 时未带相同 triggerId，会落在另一 configKey，两侧显示会分离")
	}

	d.logger.Infof("Applied brick-moving pipeline: %s (%s) -> %s", req.TriggerSymbol, req.Direction, p.ID())

	// 应用后异步对镜像方向做一次探测并更新可达性，避免阻塞 Apply 响应（探测可能需 10s+，RPC/跨链报价）
	go d.probeMirrorDirectionAndUpdateReachability(req.TriggerSymbol, req.TriggerID, req.Direction, builtNodes, bridgeMgr)

	// 应用后立即触发一次余额检测，实现实时并行检测（不等待下次定时 tick）
	pipeline.GetMiddleNodeScheduler().TriggerImmediateCheck()

	// #region agent log
	nodeIDs := make([]string, 0, len(builtNodes))
	for _, n := range builtNodes {
		nodeIDs = append(nodeIDs, n.GetID())
	}
	debugLogBrickMoving("handleApply:flowSuccess", "apply pipeline created", map[string]interface{}{
		"triggerSymbol": req.TriggerSymbol, "direction": req.Direction,
		"pipelineId": p.ID(), "name": p.Name(), "needBridge": needBridge,
		"nodeCount": len(builtNodes), "nodeIDs": nodeIDs,
	}, "H-flow-apply")
	// #endregion
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "success",
		"pipelineId": p.ID(),
		"name":       p.Name(),
	})
}

// handleGetBrickMovingPipelineCurrent 返回当前生效的 pipeline 信息（供黄框「当前使用的 pipeline」展示）
func (d *Dashboard) handleGetBrickMovingPipelineCurrent(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	direction := r.URL.Query().Get("direction")
	if triggerSymbol == "" || direction == "" {
		http.Error(w, "triggerSymbol and direction are required", http.StatusBadRequest)
		return
	}
	if direction != "forward" && direction != "backward" {
		http.Error(w, "direction must be forward or backward", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	var activeID string
	if cfg != nil {
		if direction == "forward" {
			activeID = cfg.ActiveForwardPipelineId
		} else {
			activeID = cfg.ActiveBackwardPipelineId
		}
	}
	d.brickMovingPipelineMu.RUnlock()

	if activeID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "", "name": "", "nodes": []interface{}{}, "edges": []interface{}{}, "status": "",
		})
		return
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.GetPipeline(activeID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": activeID, "name": "", "nodes": []interface{}{}, "edges": []interface{}{}, "status": "not_found",
		})
		return
	}

	nodeList := p.Nodes()
	nodeSummaries := make([]map[string]interface{}, 0, len(nodeList))
	for _, n := range nodeList {
		nodeSummaries = append(nodeSummaries, map[string]interface{}{
			"id": n.GetID(), "name": n.GetName(), "asset": n.GetAsset(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":     p.ID(),
		"name":   p.Name(),
		"nodes":  nodeSummaries,
		"edges":  []interface{}{},
		"status": string(p.Status()),
	})
}

// runPipelineForTrigger 封装 runBrickMovingPipelineInternal，供交易完成后自动联动调用（无 triggerId 时用 symbol 作 key）
func (d *Dashboard) runPipelineForTrigger(triggerSymbol, direction string) error {
	_, err := d.runBrickMovingPipelineInternal(triggerSymbol, direction, "", false)
	return err
}

// runBrickMovingPipelineInternal 按当前生效的 pipeline 执行一次 run，供 HTTP 与后端自动充提轮询复用。
// useAllBalance 为 true 时（如 B-A 反向 USDT 达 70% 不足 100% 时）首边按「全提走」执行，忽略配置的固定数量。
func (d *Dashboard) runBrickMovingPipelineInternal(triggerSymbol, direction, triggerIDStr string, useAllBalance bool) (pipelineID string, err error) {
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	var activeID string
	if cfg != nil {
		if direction == "forward" {
			activeID = cfg.ActiveForwardPipelineId
		} else {
			activeID = cfg.ActiveBackwardPipelineId
		}
	}
	d.brickMovingPipelineMu.RUnlock()

	if activeID == "" {
		// 尝试先 apply 再取
		d.brickMovingPipelineMu.RLock()
		cfg2 := d.brickMovingPipelines[key]
		d.brickMovingPipelineMu.RUnlock()
		var nodes []map[string]interface{}
		var edges []map[string]interface{}
		if direction == "forward" && cfg2 != nil && len(cfg2.ForwardNodes) > 0 {
			nodes = cfg2.ForwardNodes
			edges = cfg2.ForwardEdges
		} else if direction == "backward" && cfg2 != nil && len(cfg2.BackwardNodes) > 0 {
			nodes = cfg2.BackwardNodes
			edges = cfg2.BackwardEdges
		}
		if len(nodes) > 0 {
			if edges == nil {
				edges = []map[string]interface{}{}
			}
			defaultAsset := extractBaseSymbol(triggerSymbol)
			builtNodes, builtEdges, needBridge, err := d.buildPipelineFromNodeMaps(nodes, edges, defaultAsset)
			if err == nil {
				pipelineName := "brick-" + triggerSymbol + "-" + direction
				if triggerIDStr != "" {
					pipelineName = "brick-" + triggerSymbol + "-" + triggerIDStr + "-" + direction
				}
				pm := pipeline.GetPipelineManager()
				_ = pm.DeletePipeline(pipelineName)
				var bridgeMgr *bridge.Manager
				if needBridge {
					bridgeMgr = d.buildBridgeManager(triggerSymbol, builtNodes)
				}
				p, createErr := pm.CreatePipeline(pipelineName, builtNodes, builtEdges)
				if createErr == nil {
					if needBridge && bridgeMgr != nil {
						p.SetBridgeManager(bridgeMgr)
					}

					// 应用自动充提配置到 pipeline edges
					d.applyAutoWithdrawConfigToPipeline(p, triggerSymbol, triggerIDStr)

					d.brickMovingPipelineMu.Lock()
					if d.brickMovingPipelines[key] == nil {
						d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
					}
					if direction == "forward" {
						d.brickMovingPipelines[key].ActiveForwardPipelineId = p.ID()
					} else {
						d.brickMovingPipelines[key].ActiveBackwardPipelineId = p.ID()
					}
					d.brickMovingPipelineMu.Unlock()
					activeID = p.ID()
				}
			}
		}
	}

	if activeID == "" {
		return "", errNoActivePipeline
	}

	pm := pipeline.GetPipelineManager()
	p, getErr := pm.GetPipeline(activeID)
	if getErr != nil {
		return "", fmt.Errorf("%w: %v", errPipelineNotFound, getErr)
	}

	if canRun, reason := p.CanRun(15 * time.Second); !canRun {
		return "", &errPipelineCannotRun{Reason: reason}
	}

	d.applyAutoWithdrawConfigToPipeline(p, triggerSymbol, triggerIDStr)
	if useAllBalance {
		nodes := p.Nodes()
		if len(nodes) >= 2 {
			edge, has := p.GetEdgeConfig(nodes[0].GetID(), nodes[1].GetID())
			if has && edge != nil {
				edge.AmountType = pipeline.AmountTypeAll
				edge.Amount = 0
				_ = p.SetEdgeConfig(nodes[0].GetID(), nodes[1].GetID(), edge)
				d.logger.Infof("Brick-moving pipeline %s (%s): useAllBalance=true, first edge set to AmountTypeAll", triggerSymbol, direction)
			}
		}
	}

	go func() {
		d.logger.Infof("Brick-moving pipeline run: %s (%s) id=%s", triggerSymbol, direction, activeID)
		if nodes := p.Nodes(); len(nodes) >= 2 {
			if edge, has := p.GetEdgeConfig(nodes[0].GetID(), nodes[1].GetID()); has && edge != nil {
				d.logger.Infow("Brick-moving pipeline run starting", "triggerSymbol", triggerSymbol, "direction", direction, "firstEdgeAsset", edge.Asset, "firstEdgeNetwork", edge.Network)
			}
		}
		if runErr := p.Run(); runErr != nil {
			errStr := runErr.Error()
			isInsufficientBalance := strings.Contains(errStr, "insufficient") && strings.Contains(errStr, "balance")
			if isInsufficientBalance {
				d.logger.Warnf("Brick-moving pipeline %s skipped (insufficient balance, will retry next tick): %v", activeID, runErr)
				if d.wsHub != nil {
					if msg, e := json.Marshal(map[string]interface{}{
						"type":          "pipeline_skipped",
						"pipelineId":    activeID,
						"triggerSymbol": triggerSymbol,
						"direction":     direction,
						"reason":        "insufficient_balance",
						"message":       errStr,
					}); e == nil {
						d.wsHub.Broadcast(msg)
					}
				}
			} else {
				d.logger.Errorf("Brick-moving pipeline %s run failed: %v", activeID, runErr)
				if d.wsHub != nil {
					if msg, e := json.Marshal(map[string]interface{}{
						"type":          "pipeline_error",
						"pipelineId":    activeID,
						"triggerSymbol": triggerSymbol,
						"direction":     direction,
						"error":         errStr,
					}); e == nil {
						d.wsHub.Broadcast(msg)
					}
				}
			}
		} else {
			d.logger.Infof("Brick-moving pipeline %s run completed", activeID)
			if d.wsHub != nil {
				if msg, e := json.Marshal(map[string]interface{}{
					"type":          "pipeline_completed",
					"pipelineId":    activeID,
					"triggerSymbol": triggerSymbol,
					"direction":     direction,
				}); e == nil {
					d.wsHub.Broadcast(msg)
				}
			}
		}
	}()

	return activeID, nil
}

// isForwardWithdrawInProgress 检查该 trigger 的正向充提（forward 或 backward）是否有任一步在执行
func (d *Dashboard) isForwardWithdrawInProgress(symbol, triggerIDStr string) bool {
	pm := pipeline.GetPipelineManager()
	for _, p := range pm.ListPipelines() {
		name := p.Name()
		s, tid, dir, ok := pipeline.ParseBrickPipelineName(name)
		if !ok || s != symbol || tid != triggerIDStr {
			continue
		}
		if dir != "forward" && dir != "backward" {
			continue
		}
		if p.Status() == pipeline.PipelineStatusRunning {
			return true
		}
	}
	return false
}

// trySmartFlipWithdraw 智能翻转充提决策：当 SmartFlipEnabled 且 diffBA>=阈值 且 无正向充提中 且 余额满足时触发翻转
func (d *Dashboard) trySmartFlipWithdraw(symbol, triggerIDStr string, diffBA float64) {
	key := pipelineConfigKey(symbol, triggerIDStr)
	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[key]
	d.brickMovingHedgingMu.RUnlock()
	if hedging == nil || !hedging.SmartFlipEnabled {
		return
	}
	th := hedging.SmartFlipThreshold
	if th <= 0 {
		th = 1.5
	}
	if diffBA < th {
		return
	}
	d.brickMovingInFlipModeMu.RLock()
	inFlip := d.brickMovingInFlipMode[key]
	d.brickMovingInFlipModeMu.RUnlock()
	if inFlip {
		return
	}
	if d.isForwardWithdrawInProgress(symbol, triggerIDStr) {
		return
	}
	if !d.checkFlipFeasibility(symbol, triggerIDStr, hedging) {
		return
	}
	go d.runFlipWithdrawInternal(symbol, triggerIDStr, hedging)
}

// checkFlipFeasibility 检查翻转充提可行性：B-A 币起点、A-B USDT 起点余额均 >= 正向充提 size；且反向路径可达
func (d *Dashboard) checkFlipFeasibility(symbol, triggerIDStr string, hedging *brickMovingHedgingConfig) bool {
	key := pipelineConfigKey(symbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	if cfg == nil || len(cfg.ForwardNodes) == 0 || len(cfg.BackwardNodes) == 0 {
		return false
	}
	// 智能翻转需执行 B-A 币 + A-B USDT 两条镜像路径，均需可达（用镜像探测结果，不用正常充提的可达性）
	if !cfg.MirrorForwardReachable {
		d.logger.Debugf("checkFlipFeasibility: skip flip, mirror B-A 币 not reachable (reason: %s)", cfg.MirrorForwardReachableReason)
		return false
	}
	if !cfg.MirrorBackwardReachable {
		d.logger.Debugf("checkFlipFeasibility: skip flip, mirror A-B USDT not reachable (reason: %s)", cfg.MirrorBackwardReachableReason)
		return false
	}
	size := hedging.AutoWithdrawFixedSize
	if size <= 0 {
		size = hedging.Size
	}
	if size <= 0 {
		return false
	}
	mirrorFwdNodes, mirrorFwdEdges := mirrorPipelineNodeMaps(cfg.ForwardNodes, cfg.ForwardEdges)
	mirrorBwdNodes, mirrorBwdEdges := mirrorPipelineNodeMaps(cfg.BackwardNodes, cfg.BackwardEdges)
	if len(mirrorFwdNodes) == 0 || len(mirrorBwdNodes) == 0 {
		return false
	}
	defaultAsset := extractBaseSymbol(symbol)
	builtToken, edgesToken, needBridgeToken, errToken := d.buildPipelineFromNodeMaps(mirrorFwdNodes, mirrorFwdEdges, defaultAsset)
	if errToken != nil || len(builtToken) == 0 {
		return false
	}
	quoteAsset := hedging.QuoteAsset
	if quoteAsset == "" {
		quoteAsset = "USDT"
	}
	builtUsdt, edgesUsdt, needBridgeUsdt, errUsdt := d.buildPipelineFromNodeMaps(mirrorBwdNodes, mirrorBwdEdges, quoteAsset)
	if errUsdt != nil || len(builtUsdt) == 0 {
		return false
	}
	pipeNameToken := "brick-flip-tmp-" + symbol + "-" + triggerIDStr + "-token"
	pipeNameUsdt := "brick-flip-tmp-" + symbol + "-" + triggerIDStr + "-usdt"
	pm := pipeline.GetPipelineManager()
	_ = pm.DeletePipeline(pipeNameToken)
	_ = pm.DeletePipeline(pipeNameUsdt)
	var bridgeMgrToken, bridgeMgrUsdt *bridge.Manager
	if needBridgeToken {
		bridgeMgrToken = d.buildBridgeManager(symbol, builtToken)
	}
	if needBridgeUsdt {
		bridgeMgrUsdt = d.buildBridgeManager(symbol, builtUsdt)
	}
	pToken, err := pm.CreatePipeline(pipeNameToken, builtToken, edgesToken)
	if err != nil {
		return false
	}
	defer pm.DeletePipeline(pipeNameToken)
	if needBridgeToken && bridgeMgrToken != nil {
		pToken.SetBridgeManager(bridgeMgrToken)
	}
	pUsdt, err := pm.CreatePipeline(pipeNameUsdt, builtUsdt, edgesUsdt)
	if err != nil {
		return false
	}
	defer pm.DeletePipeline(pipeNameUsdt)
	if needBridgeUsdt && bridgeMgrUsdt != nil {
		pUsdt.SetBridgeManager(bridgeMgrUsdt)
	}
	applyFlipHedgingToPipeline(pToken, hedging, size, defaultAsset)
	applyFlipHedgingToPipeline(pUsdt, hedging, size, quoteAsset)
	resToken, errToken := d.getBalanceAndThresholdFromPipeline(pToken, symbol, triggerIDStr, "backward", hedging)
	resUsdt, errUsdt := d.getBalanceAndThresholdFromPipeline(pUsdt, symbol, triggerIDStr, "backward", hedging)
	if errToken != nil || errUsdt != nil {
		return false
	}
	thresholdToken := size
	thresholdUsdt := resUsdt.Threshold
	if thresholdUsdt <= 0 {
		thresholdUsdt = size
	}
	return resToken.Balance >= thresholdToken*0.95 && resUsdt.Balance >= thresholdUsdt*0.95
}

// applyFlipHedgingToPipeline 将 hedging 的 size 应用到 pipeline 的首边（用于翻转充提）
func applyFlipHedgingToPipeline(p *pipeline.Pipeline, hedging *brickMovingHedgingConfig, size float64, asset string) {
	nodes := p.Nodes()
	if len(nodes) < 2 {
		return
	}
	edge, has := p.GetEdgeConfig(nodes[0].GetID(), nodes[1].GetID())
	if !has || edge == nil {
		return
	}
	edge.AmountType = pipeline.AmountTypeFixed
	edge.Amount = size
	edge.Asset = asset
	_ = p.SetEdgeConfig(nodes[0].GetID(), nodes[1].GetID(), edge)
}

// runFlipWithdrawInternal 执行一次翻转充提：B-A 币 + A-B USDT 并发，完成后清除 InFlipMode
func (d *Dashboard) runFlipWithdrawInternal(symbol, triggerIDStr string, hedging *brickMovingHedgingConfig) {
	key := pipelineConfigKey(symbol, triggerIDStr)
	d.brickMovingInFlipModeMu.Lock()
	if d.brickMovingInFlipMode[key] {
		d.brickMovingInFlipModeMu.Unlock()
		return
	}
	d.brickMovingInFlipMode[key] = true
	d.brickMovingInFlipModeMu.Unlock()
	defer func() {
		d.brickMovingInFlipModeMu.Lock()
		delete(d.brickMovingInFlipMode, key)
		d.brickMovingInFlipModeMu.Unlock()
	}()

	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	if cfg == nil || len(cfg.ForwardNodes) == 0 || len(cfg.BackwardNodes) == 0 {
		d.logger.Warnf("Smart flip: no pipeline config for %s (triggerId=%s)", symbol, triggerIDStr)
		return
	}
	size := hedging.AutoWithdrawFixedSize
	if size <= 0 {
		size = hedging.Size
	}
	quoteAsset := hedging.QuoteAsset
	if quoteAsset == "" {
		quoteAsset = "USDT"
	}
	defaultAsset := extractBaseSymbol(symbol)
	mirrorFwdNodes, mirrorFwdEdges := mirrorPipelineNodeMaps(cfg.ForwardNodes, cfg.ForwardEdges)
	mirrorBwdNodes, mirrorBwdEdges := mirrorPipelineNodeMaps(cfg.BackwardNodes, cfg.BackwardEdges)
	builtToken, edgesToken, needBridgeToken, errToken := d.buildPipelineFromNodeMaps(mirrorFwdNodes, mirrorFwdEdges, defaultAsset)
	if errToken != nil {
		d.logger.Warnf("Smart flip build token pipeline failed: %v", errToken)
		return
	}
	builtUsdt, edgesUsdt, needBridgeUsdt, errUsdt := d.buildPipelineFromNodeMaps(mirrorBwdNodes, mirrorBwdEdges, quoteAsset)
	if errUsdt != nil {
		d.logger.Warnf("Smart flip build usdt pipeline failed: %v", errUsdt)
		return
	}
	pipeNameToken := "brick-flip-" + symbol + "-" + triggerIDStr + "-token"
	pipeNameUsdt := "brick-flip-" + symbol + "-" + triggerIDStr + "-usdt"
	pm := pipeline.GetPipelineManager()
	_ = pm.DeletePipeline(pipeNameToken)
	_ = pm.DeletePipeline(pipeNameUsdt)
	var bridgeMgrToken, bridgeMgrUsdt *bridge.Manager
	if needBridgeToken {
		bridgeMgrToken = d.buildBridgeManager(symbol, builtToken)
	}
	if needBridgeUsdt {
		bridgeMgrUsdt = d.buildBridgeManager(symbol, builtUsdt)
	}
	pToken, err := pm.CreatePipeline(pipeNameToken, builtToken, edgesToken)
	if err != nil {
		d.logger.Warnf("Smart flip create token pipeline failed: %v", err)
		return
	}
	defer pm.DeletePipeline(pipeNameToken)
	if needBridgeToken && bridgeMgrToken != nil {
		pToken.SetBridgeManager(bridgeMgrToken)
	}
	pUsdt, err := pm.CreatePipeline(pipeNameUsdt, builtUsdt, edgesUsdt)
	if err != nil {
		d.logger.Warnf("Smart flip create usdt pipeline failed: %v", err)
		return
	}
	defer pm.DeletePipeline(pipeNameUsdt)
	if needBridgeUsdt && bridgeMgrUsdt != nil {
		pUsdt.SetBridgeManager(bridgeMgrUsdt)
	}
	applyFlipHedgingToPipeline(pToken, hedging, size, defaultAsset)
	applyFlipHedgingToPipeline(pUsdt, hedging, size, quoteAsset)

	d.logger.Infof("Smart flip withdraw starting: %s (triggerId=%s) B-A token + A-B USDT concurrent", symbol, triggerIDStr)
	var wg sync.WaitGroup
	var tokenErr, usdtErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		tokenErr = pToken.Run()
		if tokenErr != nil {
			d.logger.Warnf("Smart flip token pipeline failed: %v", tokenErr)
		} else {
			d.logger.Infof("Smart flip token pipeline completed")
		}
	}()
	go func() {
		defer wg.Done()
		usdtErr = pUsdt.Run()
		if usdtErr != nil {
			d.logger.Warnf("Smart flip usdt pipeline failed: %v", usdtErr)
		} else {
			d.logger.Infof("Smart flip usdt pipeline completed")
		}
	}()
	wg.Wait()
	tokenStatus := "completed"
	if tokenErr != nil {
		tokenStatus = "failed"
	}
	usdtStatus := "completed"
	if usdtErr != nil {
		usdtStatus = "failed"
	}
	d.lastFlipPipelineResultMu.Lock()
	d.lastFlipPipelineResult[key] = lastFlipResult{TokenStatus: tokenStatus, UsdtStatus: usdtStatus, EndTime: time.Now()}
	d.lastFlipPipelineResultMu.Unlock()
	d.logger.Infof("Smart flip withdraw completed for %s (triggerId=%s), will re-check on next spread update", symbol, triggerIDStr)
}

// handleRunBrickMovingPipeline 按当前生效的 pipeline 执行「自动检测并提币」
func (d *Dashboard) handleRunBrickMovingPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	direction := r.URL.Query().Get("direction")
	if triggerSymbol == "" || direction == "" {
		http.Error(w, "triggerSymbol and direction are required", http.StatusBadRequest)
		return
	}
	if direction != "forward" && direction != "backward" {
		http.Error(w, "direction must be forward or backward", http.StatusBadRequest)
		return
	}

	pipelineID, err := d.runBrickMovingPipelineInternal(triggerSymbol, direction, triggerIDStr, false)
	if err != nil {
		var cannotRun *errPipelineCannotRun
		switch {
		case errors.Is(err, errNoActivePipeline):
			writeJSONError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errPipelineNotFound):
			writeJSONError(w, http.StatusNotFound, err.Error())
		case errors.As(err, &cannotRun):
			writeJSONError(w, http.StatusConflict, cannotRun.Reason)
		default:
			writeJSONError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "started",
		"pipelineId": pipelineID,
	})
}

// handleLastRunBrickMovingPipeline 返回当前生效 pipeline 的最近执行状态（供「最近一次提币」展示）
func (d *Dashboard) handleLastRunBrickMovingPipeline(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	direction := r.URL.Query().Get("direction")
	if triggerSymbol == "" || direction == "" {
		http.Error(w, "triggerSymbol and direction are required", http.StatusBadRequest)
		return
	}

	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	var activeID string
	if cfg != nil {
		if direction == "forward" {
			activeID = cfg.ActiveForwardPipelineId
		} else {
			activeID = cfg.ActiveBackwardPipelineId
		}
	}
	d.brickMovingPipelineMu.RUnlock()

	if activeID == "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pipelineId": "", "currentStep": 0, "status": "", "lastStepAmount": "", "lastStepTime": "",
			"nodes": []interface{}{}, "edges": []interface{}{},
		})
		return
	}

	pm := pipeline.GetPipelineManager()
	p, err := pm.GetPipeline(activeID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pipelineId": activeID, "currentStep": 0, "status": "not_found", "lastStepAmount": "", "lastStepTime": "",
			"nodes": []interface{}{}, "edges": []interface{}{},
		})
		return
	}

	// 获取节点信息
	nodes := p.Nodes()
	nodeInfos := make([]map[string]interface{}, 0, len(nodes))
	for i, node := range nodes {
		balance, _ := node.CheckBalance()
		nodeInfo := map[string]interface{}{
			"id":      node.GetID(),
			"name":    node.GetName(),
			"type":    string(node.GetType()),
			"asset":   node.GetAsset(),
			"balance": balance,
			"index":   i,
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}

	// 获取边信息（包括执行状态）
	edgeInfos := make([]map[string]interface{}, 0)
	currentStep := p.CurrentStep()
	status := p.Status()
	for i := 0; i < len(nodes)-1; i++ {
		fromNode := nodes[i]
		toNode := nodes[i+1]
		stepNum := i + 1

		// 判断边的状态
		edgeStatus := "pending" // pending, running, success, failed
		if status == pipeline.PipelineStatusRunning {
			if stepNum == currentStep {
				edgeStatus = "running"
			} else if stepNum < currentStep {
				edgeStatus = "success"
			}
		} else if status == pipeline.PipelineStatusCompleted {
			edgeStatus = "success"
		} else if status == pipeline.PipelineStatusFailed {
			if stepNum < currentStep {
				edgeStatus = "success"
			} else if stepNum == currentStep {
				edgeStatus = "failed"
			}
		}

		edgeInfo := map[string]interface{}{
			"from":     fromNode.GetID(),
			"to":       toNode.GetID(),
			"fromName": fromNode.GetName(),
			"toName":   toNode.GetName(),
			"step":     stepNum,
			"status":   edgeStatus,
		}
		if edgeCfg, has := p.GetEdgeConfig(fromNode.GetID(), toNode.GetID()); has && edgeCfg != nil && edgeCfg.BridgeProtocol != "" {
			edgeInfo["bridgeProtocol"] = edgeCfg.BridgeProtocol
		}
		edgeInfos = append(edgeInfos, edgeInfo)
	}

	lastError := p.GetLastError()
	lastErrorIsWarning := strings.HasPrefix(lastError, "[WARN]")
	btStatus, btStep, btFrom, btTo, btErr, btEnd := p.GetBalanceTransferState()
	balanceTransfer := map[string]interface{}{
		"status":    btStatus,
		"step":      btStep,
		"fromName":  btFrom,
		"toName":    btTo,
		"lastError": btErr,
		"lastEnd":   btEnd.UnixMilli(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pipelineId":         activeID,
		"currentStep":        currentStep,
		"status":             string(status),
		"lastStepAmount":     "",
		"lastStepTime":       "",
		"lastError":          lastError,
		"lastErrorIsWarning": lastErrorIsWarning,
		"nodes":           nodeInfos,
		"edges":           edgeInfos,
		"balanceTransfer": balanceTransfer,
	})
}

// routeGraphExchangeTypes 参与路由图构建的交易所类型（与 getExchange 支持且具备充提网络查询的保持一致）
var routeGraphExchangeTypes = []string{"binance", "bybit", "bitget", "gate", "okex"}

// buildRouteGraph 用实时 API 构建路由图：对 symbol 查询各交易所的提现/充币网络，得到邻接表。
// 边：交易所→链（提现）、链→交易所（充币）、链→链（跨链，由探测阶段校验）。整条路径固定同一 symbol。
// 返回 (邻接表 adj, 图中出现的链网络数量, error)
func (d *Dashboard) buildRouteGraph(symbol string) (adj map[string][]string, networkCount int, err error) {
	adj = make(map[string][]string)
	addEdge := func(from, to string) {
		if from == "" || to == "" {
			return
		}
		adj[from] = append(adj[from], to)
	}

	chainIDSet := make(map[string]bool)

	for _, exType := range routeGraphExchangeTypes {
		ex := d.getExchange(exType)
		if ex == nil && position.GetWalletManager() != nil {
			ex = position.GetWalletManager().GetExchange(exType)
		}
		if ex == nil {
			continue
		}

		// 交易所 → 链（提现）
		if wd, ok := ex.(exchange.WithdrawNetworkLister); ok {
			networks, e := wd.GetWithdrawNetworks(symbol)
			if e != nil {
				d.logger.Warnf("route-graph: %s GetWithdrawNetworks(%s): %v", exType, symbol, e)
				continue
			}
			for _, n := range networks {
				if n.ChainID == "" || !isNumericChainID(n.ChainID) {
					continue
				}
				chainIDSet[n.ChainID] = true
				addEdge(exType, pipeline.OnchainNodeID(n.ChainID))
			}
		}

		// 链 → 交易所（充币）
		if dep, ok := ex.(exchange.DepositNetworkLister); ok {
			networks, e := dep.GetDepositNetworks(symbol)
			if e != nil {
				d.logger.Warnf("route-graph: %s GetDepositNetworks(%s): %v", exType, symbol, e)
				continue
			}
			for _, n := range networks {
				if n.ChainID == "" || !isNumericChainID(n.ChainID) {
					continue
				}
				chainIDSet[n.ChainID] = true
				addEdge(pipeline.OnchainNodeID(n.ChainID), exType)
			}
		} else if wd, ok := ex.(exchange.WithdrawNetworkLister); ok {
			// 无 DepositNetworkLister 时用提现网络作为充币的近似
			networks, e := wd.GetWithdrawNetworks(symbol)
			if e != nil {
				continue
			}
			for _, n := range networks {
				if n.ChainID == "" || !isNumericChainID(n.ChainID) {
					continue
				}
				chainIDSet[n.ChainID] = true
				addEdge(pipeline.OnchainNodeID(n.ChainID), exType)
			}
		}
	}

	// 链→链：图中出现的链两两可达（跨链由探测时实时 GetBridgeQuote 校验）
	chainIDs := make([]string, 0, len(chainIDSet))
	for c := range chainIDSet {
		chainIDs = append(chainIDs, c)
	}
	for _, a := range chainIDs {
		for _, b := range chainIDs {
			if a == b {
				continue
			}
			addEdge(pipeline.OnchainNodeID(a), pipeline.OnchainNodeID(b))
		}
	}

	// 交易所→交易所：若存在链 C 使 exA 可提现到 C 且 exB 可从 C 充币，则加逻辑边 exA→exB（探测时展开为 exA→C→exB）
	for _, exA := range routeGraphExchangeTypes {
		for _, exB := range routeGraphExchangeTypes {
			if exA == exB {
				continue
			}
			for _, chainID := range chainIDs {
				chainNode := pipeline.OnchainNodeID(chainID)
				if hasEdge(adj, exA, chainNode) && hasEdge(adj, chainNode, exB) {
					addEdge(exA, exB)
					break
				}
			}
		}
	}

	networkCount = len(chainIDSet)
	d.logger.Infof("route-graph: symbol=%s, chains=%d, edges built from real-time withdraw/deposit APIs", symbol, networkCount)
	return adj, networkCount, nil
}

// hasEdge 判断邻接表 adj 中是否存在 from→to 的边
func hasEdge(adj map[string][]string, from, to string) bool {
	for _, n := range adj[from] {
		if n == to {
			return true
		}
	}
	return false
}

// injectEndpointChainsIntoGraph 将 source/dest 中的链 ID 注入路由图：
// 确保即使交易所 API 未返回这些链，图中仍包含对应节点和 chain↔chain 桥接边。
// 桥接边的实际可用性由探测阶段的 GetBridgeQuote 校验。
func injectEndpointChainsIntoGraph(adj map[string][]string, networkCount int, sourceNode, destNode string) (map[string][]string, int) {
	existingChains := make(map[string]bool)
	// 从 adj 的 key 和 value（邻居）中收集链 ID：GetDepositNetworks 失败时可能只有 ex→chain 边，adj 中无 chain 节点
	for nodeID, neighbors := range adj {
		if cid := pipeline.ChainIDFromNodeID(nodeID); cid != "" {
			existingChains[cid] = true
		}
		for _, nb := range neighbors {
			if cid := pipeline.ChainIDFromNodeID(nb); cid != "" {
				existingChains[cid] = true
			}
		}
	}

	// 1. 注入 source/dest 的链节点（onchain 端点）
	extraChainIDs := make([]string, 0, 2)
	if cid := pipeline.ChainIDFromNodeID(sourceNode); cid != "" {
		extraChainIDs = append(extraChainIDs, cid)
	}
	if cid := pipeline.ChainIDFromNodeID(destNode); cid != "" {
		extraChainIDs = append(extraChainIDs, cid)
	}

	for _, cid := range extraChainIDs {
		if existingChains[cid] {
			continue
		}
		existingChains[cid] = true
		nodeID := pipeline.OnchainNodeID(cid)
		for otherCID := range existingChains {
			if otherCID == cid {
				continue
			}
			otherNodeID := pipeline.OnchainNodeID(otherCID)
			if !hasEdge(adj, nodeID, otherNodeID) {
				adj[nodeID] = append(adj[nodeID], otherNodeID)
			}
			if !hasEdge(adj, otherNodeID, nodeID) {
				adj[otherNodeID] = append(adj[otherNodeID], nodeID)
			}
		}
		networkCount++
	}
	for _, a := range extraChainIDs {
		for _, b := range extraChainIDs {
			if a == b {
				continue
			}
			aNode := pipeline.OnchainNodeID(a)
			bNode := pipeline.OnchainNodeID(b)
			if !hasEdge(adj, aNode, bNode) {
				adj[aNode] = append(adj[aNode], bNode)
			}
		}
	}

	// 2. 注入 source/dest 的交易所节点：当交易所 API 失败（如 404）时，
	// 该交易所在图中无边。注入它与所有链节点的双向边（withdraw/deposit），
	// 实际可用性由探测阶段的 RouteProbe 校验。
	for _, exNode := range []string{sourceNode, destNode} {
		if exNode == "" || pipeline.IsOnchainNodeID(exNode) {
			continue
		}
		if _, hasEdges := adj[exNode]; hasEdges {
			continue
		}
		for cid := range existingChains {
			chainNodeID := pipeline.OnchainNodeID(cid)
			if !hasEdge(adj, exNode, chainNodeID) {
				adj[exNode] = append(adj[exNode], chainNodeID)
			}
			if !hasEdge(adj, chainNodeID, exNode) {
				adj[chainNodeID] = append(adj[chainNodeID], exNode)
			}
		}
	}

	return adj, networkCount
}

// normalizeRouteNodeID 将源/目标规范为图节点 ID（使用 pipeline 约定，不写死前缀）；交易所保持类型小写。
func normalizeRouteNodeID(node string) string {
	if node == "" {
		return node
	}
	if strings.HasPrefix(node, pipeline.NodeIDPrefixOnchain) {
		return node
	}
	if isNumericChainID(node) {
		return pipeline.OnchainNodeID(node)
	}
	// 交易所可能带后缀如 "binance:spot"，图里用纯类型
	if idx := strings.Index(node, ":"); idx > 0 {
		return strings.ToLower(node[:idx])
	}
	return strings.ToLower(node)
}

// resolveCandidatePathsForRouteProbe 按「全图枚举」解析候选路径：用实时 API 建图后 BFS 枚举 source→dest 所有路径（最多 4 跳）。
// sourceChainID 为「源链」链 ID（如 "56" 表示 BSC）时，仅保留从该链出发的路径；空表示不限制。
// bridgeMgr 仅用于 handleRouteProbe 内单路径探测，此处不再用于建图。
func (d *Dashboard) resolveCandidatePathsForRouteProbe(source, destination, symbol, sourceChainID string, bridgeMgr *bridge.Manager) ([][]string, int, error) {
	sourceNode := normalizeRouteNodeID(source)
	destNode := normalizeRouteNodeID(destination)
	// #region agent log
	debugLogBrickMoving("resolveCandidatePathsForRouteProbe:entry", "direction", map[string]interface{}{"source": source, "destination": destination, "symbol": symbol, "sourceNode": sourceNode, "destNode": destNode, "sourceChainID": sourceChainID}, "H2")
	// #endregion

	adj, networkCount, err := d.buildRouteGraph(symbol)
	if err != nil {
		d.logger.Warnf("route-probe: buildRouteGraph failed: %v, fallback to single path", err)
		return [][]string{{sourceNode, destNode}}, 0, nil
	}

	// 将 source/dest 的链 ID 注入图中，确保即使交易所 API 未返回这些链，
	// 图中仍包含它们的节点和 chain↔chain 桥接边（可达性由探测阶段校验）。
	adj, networkCount = injectEndpointChainsIntoGraph(adj, networkCount, sourceNode, destNode)

	if len(adj) == 0 {
		return [][]string{{sourceNode, destNode}}, networkCount, nil
	}

	const maxHops = 2 // 最多 3 个节点（2 跳）
	// 链节点使用 pipeline 约定；源为指定链时只保留从该链出发的路径；裸 "onchain" 且无 sourceChainID 才展开为图中所有链节点。
	sourceStarts := []string{sourceNode}
	if sourceNode == string(pipeline.NodeTypeOnchain) {
		if sourceChainID != "" && isNumericChainID(sourceChainID) {
			sourceStarts = []string{pipeline.OnchainNodeID(sourceChainID)}
		} else {
			for k := range adj {
				if pipeline.IsOnchainNodeID(k) {
					sourceStarts = append(sourceStarts, k)
				}
			}
			sourceStarts = sourceStarts[1:]
			if len(sourceStarts) == 0 {
				sourceStarts = []string{string(pipeline.NodeTypeOnchain)}
			}
		}
	}
	destEnds := []string{destNode}
	if destNode == string(pipeline.NodeTypeOnchain) {
		for k := range adj {
			if pipeline.IsOnchainNodeID(k) {
				destEnds = append(destEnds, k)
			}
		}
		destEnds = destEnds[1:]
		if len(destEnds) == 0 {
			destEnds = []string{string(pipeline.NodeTypeOnchain)}
		}
	}

	var candidates [][]string
	seen := make(map[string]bool)
	for _, src := range sourceStarts {
		for _, dst := range destEnds {
			if src == dst {
				continue
			}
			routes := pipeline.FindAllRoutes(src, dst, adj, maxHops)
			for _, r := range routes {
				// 2 节点且均为交易所：展开为 交易所→链→交易所，便于探测与合并段并得到 WithdrawNetworkChainID
				if len(r) == 2 && !pipeline.IsOnchainNodeID(r[0]) && !pipeline.IsOnchainNodeID(r[1]) {
					exA, exB := r[0], r[1]
					var mids []string
					for _, mid := range adj[exA] {
						if !pipeline.IsOnchainNodeID(mid) {
							continue
						}
						if !hasEdge(adj, mid, exB) {
							continue
						}
						mids = append(mids, mid)
						expanded := []string{exA, mid, exB}
						key := fmt.Sprintf("%v", expanded)
						if !seen[key] {
							seen[key] = true
							candidates = append(candidates, expanded)
						}
					}
					// #region agent log
					debugLogBrickMoving("resolveCandidatePathsForRouteProbe:expand2ex", "expand 2-node ex-ex", map[string]interface{}{
						"exA": exA, "exB": exB, "midChainsCount": len(mids), "midChains": mids, "expandedPathsAdded": len(mids),
					}, "H-ex2ex-expand")
					// #endregion
					continue
				}
				// 3 节点且最后两节点为交易所（链→交易所→交易所）：展开为 链→交易所→链→交易所，以便 merge 后 WithdrawNetworkChainID 有值、边上可展示提现链
				if len(r) == 3 && !pipeline.IsOnchainNodeID(r[1]) && !pipeline.IsOnchainNodeID(r[2]) {
					exA, exB := r[1], r[2]
					var mids []string
					for _, mid := range adj[exA] {
						if !pipeline.IsOnchainNodeID(mid) {
							continue
						}
						if !hasEdge(adj, mid, exB) {
							continue
						}
						mids = append(mids, mid)
						expanded := []string{r[0], exA, mid, exB}
						key := fmt.Sprintf("%v", expanded)
						if !seen[key] {
							seen[key] = true
							candidates = append(candidates, expanded)
						}
					}
					// #region agent log
					debugLogBrickMoving("resolveCandidatePathsForRouteProbe:expand3ex", "expand 3-node chain-ex-ex", map[string]interface{}{
						"firstNode": r[0], "exA": exA, "exB": exB, "midChainsCount": len(mids), "midChains": mids, "expandedPathsAdded": len(mids),
					}, "H-ex2ex-expand")
					// #endregion
					continue
				}
				// 3 节点且前两节点为交易所、最后一节点为链（交易所→交易所→链）：展开为 交易所→链→交易所→链，以便 merge 后 WithdrawNetworkChainID 有值、边上可展示提现链
				if len(r) == 3 && !pipeline.IsOnchainNodeID(r[0]) && !pipeline.IsOnchainNodeID(r[1]) && pipeline.IsOnchainNodeID(r[2]) {
					exA, exB, destChain := r[0], r[1], r[2]
					var mids []string
					for _, mid := range adj[exA] {
						if !pipeline.IsOnchainNodeID(mid) {
							continue
						}
						if !hasEdge(adj, mid, exB) {
							continue
						}
						mids = append(mids, mid)
						expanded := []string{exA, mid, exB, destChain}
						key := fmt.Sprintf("%v", expanded)
						if !seen[key] {
							seen[key] = true
							candidates = append(candidates, expanded)
						}
					}
					debugLogBrickMoving("resolveCandidatePathsForRouteProbe:expand3ex", "expand 3-node ex-ex-chain", map[string]interface{}{
						"exA": exA, "exB": exB, "destChain": destChain, "midChainsCount": len(mids), "midChains": mids, "expandedPathsAdded": len(mids),
					}, "H-ex2ex-expand")
					continue
				}
				key := fmt.Sprintf("%v", r)
				if !seen[key] {
					seen[key] = true
					candidates = append(candidates, r)
				}
			}
		}
	}
	if len(candidates) == 0 {
		// 诊断：为何 FindAllRoutes 未找到路径（CCIP 等桥接发现成功时，图应有 bitget→chain1→chain56）
		adjKeys := make([]string, 0, len(adj))
		for k := range adj {
			adjKeys = append(adjKeys, k)
		}
		sort.Strings(adjKeys)
		srcNeighbors := adj[sourceNode]
		d.logger.Warnf("route-probe: no path from %s to %s within %d hops (symbol=%s) | adjNodes=%v | adj[%s]=%v | sourceStarts=%v destEnds=%v",
			sourceNode, destNode, maxHops, symbol, adjKeys, sourceNode, srcNeighbors, sourceStarts, destEnds)
		// 图枚举未找到路径时，回退为直接路径 [source, dest]，供探测阶段校验
		candidates = [][]string{{sourceNode, destNode}}
	}
	// #region agent log
	var candStrs []string
	ex2exCandidateCount := 0
	for _, c := range candidates {
		candStrs = append(candStrs, fmt.Sprintf("%v", c))
		// 4 节点 [链, ex, 链, ex]（chain→ex→chain→ex 展开）
		if len(c) == 4 && !pipeline.IsOnchainNodeID(c[1]) && pipeline.IsOnchainNodeID(c[2]) && !pipeline.IsOnchainNodeID(c[3]) {
			ex2exCandidateCount++
		}
		// 4 节点 [ex, 链, ex, 链]（ex→ex→chain 展开为 ex→mid→ex→chain）
		if len(c) == 4 && !pipeline.IsOnchainNodeID(c[0]) && pipeline.IsOnchainNodeID(c[1]) && !pipeline.IsOnchainNodeID(c[2]) && pipeline.IsOnchainNodeID(c[3]) {
			ex2exCandidateCount++
		}
		// 3 节点 [ex, 链, ex]（2 节点 ex-ex 展开）
		if len(c) == 3 && !pipeline.IsOnchainNodeID(c[0]) && pipeline.IsOnchainNodeID(c[1]) && !pipeline.IsOnchainNodeID(c[2]) {
			ex2exCandidateCount++
		}
	}
	debugLogBrickMoving("resolveCandidatePathsForRouteProbe:graph", "candidates", map[string]interface{}{"source": sourceNode, "dest": destNode, "symbol": symbol, "networkCount": networkCount, "candidates": candStrs, "sourceStarts": sourceStarts, "destEnds": destEnds}, "H3")
	debugLogBrickMoving("resolveCandidatePathsForRouteProbe:ex2exCandidates", "candidates containing ex-ex", map[string]interface{}{
		"totalCandidates": len(candidates), "ex2exCandidateCount": ex2exCandidateCount, "samplePaths": candStrs,
	}, "H-ex2ex-expand")
	// #endregion
	d.logger.Infof("route-probe: resolved %d candidate paths (full-graph, maxHops=%d, networks=%d)", len(candidates), maxHops, networkCount)
	return candidates, networkCount, nil
}

func isNumericChainID(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// collectKnownTokenAddresses 收集 token 在各链上的 ERC-20 合约地址。
// 优先使用 ChainTokenRegistry（已扫链验证），其次 WalletInfo（per-chain），最后 token_mapping（仅一条链兜底）。
func collectKnownTokenAddresses(symbol string, chainIDs []string) map[string]string {
	addrs := make(map[string]string)

	// 来源 1: ChainTokenRegistry（已扫链、交叉验证的 per-chain 地址，最可信）
	reg := chain_token_registry.GetRegistry()
	if reg != nil {
		for chainID, info := range reg.GetAllChains(symbol) {
			if info != nil && info.Address != "" {
				addrs[chainID] = info.Address
			}
		}
	}

	// 来源 2: WalletInfo（OKX DEX API per-chain 地址，补充 registry 未覆盖的链）
	if wm := position.GetWalletManager(); wm != nil {
		if wi := wm.GetWalletInfo(); wi != nil && wi.OnchainBalances != nil {
			for _, chainID := range chainIDs {
				if _, ok := addrs[chainID]; ok {
					continue
				}
				symbolMap, ok := wi.OnchainBalances[chainID]
				if !ok {
					continue
				}
				for sym, asset := range symbolMap {
					if strings.EqualFold(sym, symbol) && asset.TokenContractAddress != "" {
						addrs[chainID] = asset.TokenContractAddress
						break
					}
				}
			}
		}
	}

	// 来源 3: token_mapping 按链兜底（WalletInfo + Registry 都为空时）
	if len(addrs) == 0 {
		mappingMgr := token_mapping.GetTokenMappingManager()
		if mappingMgr != nil {
			for _, chainID := range chainIDs {
				if addr, err := mappingMgr.GetAddressBySymbol(symbol, chainID); err == nil && addr != "" {
					addrs[chainID] = addr
				} else if addr, err := mappingMgr.GetAddressBySymbol(symbol, ""); err == nil && addr != "" {
					addrs[chainID] = addr // 旧格式不区分链兜底
				}
			}
		}
	}

	return addrs
}

// scanTokenForRegistry 异步扫链：收集指定 triggerSymbol 的 base token 在所有支持链上的合约地址，
// 通过交易所 API + WalletInfo + RPC ERC-20 验证，交叉比对后存入 ChainTokenRegistry。
func (d *Dashboard) scanTokenForRegistry(triggerSymbol string) {
	base := strings.TrimSuffix(triggerSymbol, "USDT")
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "USDC")
	}
	if base == "" {
		return
	}

	reg := chain_token_registry.GetRegistry()

	// 目标链列表
	targetChainIDs := []string{"1", "56", "137", "42161", "10", "43114", "8453", "250"}

	// 收集交易所实例
	var exchanges []exchange.Exchange
	for _, exType := range []string{"binance", "bybit", "bitget", "gate", "okex"} {
		ex := d.getExchange(exType)
		if ex == nil && position.GetWalletManager() != nil {
			ex = position.GetWalletManager().GetExchange(exType)
		}
		if ex != nil {
			exchanges = append(exchanges, ex)
		}
	}

	// WalletInfo per-chain 地址
	walletAddrs := make(map[string]string)
	if wm := position.GetWalletManager(); wm != nil {
		if wi := wm.GetWalletInfo(); wi != nil && wi.OnchainBalances != nil {
			for _, chainID := range targetChainIDs {
				if symbolMap, ok := wi.OnchainBalances[chainID]; ok {
					for sym, asset := range symbolMap {
						if strings.EqualFold(sym, base) && asset.TokenContractAddress != "" {
							walletAddrs[chainID] = asset.TokenContractAddress
							break
						}
					}
				}
			}
		}
	}

	results := chain_token_registry.ScanTokenOnChains(base, targetChainIDs, exchanges, walletAddrs, nil)
	count := chain_token_registry.ApplyResults(reg, base, results)
	d.logger.Infof("scanTokenForRegistry: %s scanned %d chains, registered %d addresses", base, len(targetChainIDs), count)

	// 将扫描结果写入 token_mapping（按链），便于 BrickMovingTrigger 等按 chainId 取地址
	tokenMappingMgr := token_mapping.GetTokenMappingManager()
	for _, r := range results {
		if r.Address != "" && r.Address != "native" {
			if err := tokenMappingMgr.AddMapping(r.Address, base, r.ChainID); err != nil {
				d.logger.Warnf("scanTokenForRegistry: token_mapping AddMapping %s @ %s err: %v", base, r.ChainID, err)
			}
		}
	}
	if count > 0 {
		if err := tokenMappingMgr.SaveToFile(); err != nil {
			d.logger.Warnf("scanTokenForRegistry: token_mapping SaveToFile err: %v", err)
		}
	}

	// 同时扫 quote asset（USDT）
	quoteResults := chain_token_registry.ScanTokenOnChains("USDT", targetChainIDs, exchanges, nil, nil)
	quoteCount := chain_token_registry.ApplyResults(reg, "USDT", quoteResults)
	if quoteCount > 0 {
		d.logger.Infof("scanTokenForRegistry: USDT registered %d addresses", quoteCount)
		for _, r := range quoteResults {
			if r.Address != "" && r.Address != "native" {
				_ = tokenMappingMgr.AddMapping(r.Address, "USDT", r.ChainID)
			}
		}
		_ = tokenMappingMgr.SaveToFile()
	}
}

// handleScanTokenChains Web API 入口：手动触发某 token 的全链扫描
func (d *Dashboard) handleScanTokenChains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		var req struct{ Symbol string `json:"symbol"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.Symbol != "" {
			symbol = req.Symbol
		}
	}
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}
	symbol = strings.ToUpper(symbol)

	go d.scanTokenForRegistry(symbol + "USDT")

	reg := chain_token_registry.GetRegistry()
	existing := reg.GetAllChains(symbol)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "scan_started",
		"symbol":   symbol,
		"existing": existing,
	})
}

// handleGetTokenChains Web API 入口：查询某 token 在各链上的已知地址
func (d *Dashboard) handleGetTokenChains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	symbol := strings.ToUpper(r.URL.Query().Get("symbol"))
	if symbol == "" {
		http.Error(w, "symbol is required", http.StatusBadRequest)
		return
	}

	reg := chain_token_registry.GetRegistry()
	chains := reg.GetAllChains(symbol)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"symbol": symbol,
		"chains": chains,
	})
}

// routeProbeFirstBridgeSegment 返回 path 中第一段跨链的 fromChain、toChain（节点 ID 解析为链 ID）；若无跨链段则返回 "",""
func routeProbeFirstBridgeSegment(path []string) (fromChain, toChain string) {
	for i := 0; i < len(path)-1; i++ {
		fc := chainIDFromNodeID(path[i])
		tc := chainIDFromNodeID(path[i+1])
		if fc != "" && tc != "" && fc != tc {
			return fc, tc
		}
	}
	return "", ""
}

// chainIDFromNodeID 使用 pipeline 的节点 ID 约定解析链 ID，避免写死前缀。
func chainIDFromNodeID(nodeID string) string {
	return pipeline.ChainIDFromNodeID(nodeID)
}

// getOrCreateBridgeOFTRegistry 返回 Dashboard 的全局 OFT 注册表，不存在则创建（供 LayerZero 查询 OFT 地址）；具体 OFT 由 refreshBridgeAddressesForSymbol 按 trigger 配置加载
func (d *Dashboard) getOrCreateBridgeOFTRegistry() *bridge.OFTRegistry {
	d.bridgeRegistryMu.Lock()
	defer d.bridgeRegistryMu.Unlock()
	if d.bridgeOFTRegistry == nil {
		d.bridgeOFTRegistry = bridge.NewOFTRegistry()
	}
	return d.bridgeOFTRegistry
}

// refreshBridgeAddressesForSymbol 根据 trigger 的 symbol 从 LayerZero API 拉取 OFT 地址并合并到全局注册表；配置好 trigger 后调用（串行执行，避免并发写注册表）
func (d *Dashboard) refreshBridgeAddressesForSymbol(triggerSymbol string) {
	if triggerSymbol == "" {
		return
	}
	d.bridgeRefreshMu.Lock()
	defer d.bridgeRefreshMu.Unlock()
	base := strings.TrimSuffix(triggerSymbol, "USDT")
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "USDC")
	}
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "BUSD")
	}
	if base == triggerSymbol {
		base = triggerSymbol
	}
	symbolsToLoad := base
	if base != "USDT" {
		symbolsToLoad = base + ",USDT"
	}
	reg := d.getOrCreateBridgeOFTRegistry()
	if reg == nil {
		return
	}
	if err := reg.LoadFromLayerZeroAPI(context.Background(), "", &bridge.LayerZeroAPIListOpts{Symbols: symbolsToLoad}, nil); err != nil {
		d.logger.Warnf("refresh bridge OFT for symbol %s failed: %v", triggerSymbol, err)
		return
	}

	// #region agent log
	{
		allTokens := reg.List()
		tokenSummary := make([]map[string]interface{}, 0, len(allTokens))
		for _, t := range allTokens {
			tokenSummary = append(tokenSummary, map[string]interface{}{"chainID": t.ChainID, "symbol": t.Symbol, "address": t.Address, "source": t.Source})
		}
		debugLogBrickMoving("web_api_brick_moving.go:refreshBridgeAddressesForSymbol:afterLoad", "Registry contents after API load",
			map[string]interface{}{"triggerSymbol": triggerSymbol, "base": base, "symbolsToLoad": symbolsToLoad, "totalTokens": len(allTokens), "tokens": tokenSummary}, "H2,H4")
	}
	// #endregion

	// 验证加载结果：检查 base token 是否真的拿到了至少一条 OFT 记录
	found := false
	for _, t := range reg.List() {
		if strings.EqualFold(t.Symbol, base) {
			found = true
			break
		}
	}
	if found {
		d.logger.Infof("Refreshed bridge OFT registry for symbol %s (loaded: %s)", triggerSymbol, symbolsToLoad)
	} else {
		d.logger.Warnf("Refreshed bridge OFT registry for symbol %s, but token %s was NOT found in LayerZero OFT list. "+
			"This token may not be a registered LayerZero OFT. Cross-chain bridge for this token will fail unless "+
			"manually configured in config: bridge.layerZero.oftContracts (e.g. \"56:%s\" = \"<oft_contract_address>\")",
			triggerSymbol, base, base)
	}
}

// autoDiscoverOFTFromTokenMapping 当 LayerZero API 未返回某 token 的 OFT 数据时，
// 从 token_mapping 获取该 token 的合约地址，在各链上通过链上调用 VerifyOFTContract 检测是否为 OFT 合约，
// 验证通过后自动注册到 OFTRegistry。
// 这样即使 token 没有在 LayerZero 官方 OFT 列表中注册，也能自动发现并使用。
func (d *Dashboard) autoDiscoverOFTFromTokenMapping(lz *layerzero.LayerZero, triggerSymbol string, chainIDs []string) {
	if lz == nil || triggerSymbol == "" {
		return
	}

	// 提取 base symbol（如 ZAMAUSDT -> ZAMA, USDT -> USDT）
	base := strings.TrimSuffix(triggerSymbol, "USDT")
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "USDC")
	}
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "BUSD")
	}
	if base == "" {
		base = triggerSymbol // 当 triggerSymbol 本身就是 "USDT" 时，TrimSuffix 返回空字符串，此时 base 应该是 "USDT"
	}
	if base == triggerSymbol {
		base = triggerSymbol
	}

	reg := d.getOrCreateBridgeOFTRegistry()
	if reg == nil {
		return
	}

	// 如果外部没传入 chainIDs，从当前 trigger 的 pipeline 配置中解析
	if len(chainIDs) == 0 {
		d.brickMovingPipelineMu.RLock()
		cfg := d.brickMovingPipelines[triggerSymbol]
		d.brickMovingPipelineMu.RUnlock()
		if cfg != nil {
			allNodes := append(cfg.ForwardNodes, cfg.BackwardNodes...)
			seen := make(map[string]bool)
			// #region agent log
			nodeMapIDs := make([]string, 0, len(allNodes))
			nodeMapKeys := make([][]string, 0, len(allNodes))
			// #endregion
			for _, nodeMap := range allNodes {
				// #region agent log
				idRaw, _ := nodeMap["id"].(string)
				nodeMapIDs = append(nodeMapIDs, idRaw)
				keys := make([]string, 0, len(nodeMap))
				for k := range nodeMap {
					keys = append(keys, k)
				}
				nodeMapKeys = append(nodeMapKeys, keys)
				// #endregion

				// 前端节点 map 没有 "id" 字段，chainID 存在 "type" 字段中（使用 pipeline 约定）
				typeRaw, _ := nodeMap["type"].(string)
				if strings.HasPrefix(typeRaw, pipeline.NodeIDPrefixOnchain) {
					chainID := strings.TrimPrefix(typeRaw, pipeline.NodeIDPrefixOnchain)
					if chainID != "" && !seen[chainID] {
						chainIDs = append(chainIDs, chainID)
						seen[chainID] = true
					}
				}
			}
			// #region agent log
			debugLogBrickMoving("web_api_brick_moving.go:autoDiscoverOFT:fallbackParse", "Fallback parsing ForwardNodes/BackwardNodes",
				map[string]interface{}{"nodeCount": len(allNodes), "nodeMapIDs": nodeMapIDs, "nodeMapKeys": nodeMapKeys, "parsedChainIDs": chainIDs, "cfgHasForward": len(cfg.ForwardNodes) > 0, "cfgHasBackward": len(cfg.BackwardNodes) > 0}, "H8,H10")
			// #endregion
		} else {
			// #region agent log
			debugLogBrickMoving("web_api_brick_moving.go:autoDiscoverOFT:fallbackNoCfg", "No pipeline config found for fallback",
				map[string]interface{}{"triggerSymbol": triggerSymbol}, "H10")
			// #endregion
		}
	}

	// #region agent log
	debugLogBrickMoving("web_api_brick_moving.go:autoDiscoverOFT:entry", "autoDiscoverOFT called",
		map[string]interface{}{"triggerSymbol": triggerSymbol, "base": base, "chainIDs": chainIDs, "lzNil": lz == nil}, "H6-entry")
	// #endregion

	if len(chainIDs) == 0 {
		d.logger.Debugf("autoDiscoverOFT: no chainIDs to check for %s, skipping", base)
		return
	}

	// 检查是否所有需要的链都已经有 OFT 地址
	allFound := true
	for _, chainID := range chainIDs {
		if _, ok := reg.Get(chainID, base); !ok {
			allFound = false
			break
		}
	}
	if allFound {
		return // 全部已有，无需自动发现
	}

	// 从 token_mapping 按链获取合约地址并在各链上验证 OFT
	mappingMgr := token_mapping.GetTokenMappingManager()
	if mappingMgr == nil {
		return
	}

	for _, chainID := range chainIDs {
		if _, ok := reg.Get(chainID, base); ok {
			continue // 该链已有
		}

		tokenAddress, err := mappingMgr.GetAddressBySymbol(base, chainID)
		if err != nil || tokenAddress == "" {
			if tokenAddress, err = mappingMgr.GetAddressBySymbol(base, ""); err != nil || tokenAddress == "" {
				continue // 该链无映射，跳过
			}
		}

		isOFT, verifyErr := lz.VerifyOFTContract(chainID, tokenAddress)
		if verifyErr != nil {
			d.logger.Warnf("autoDiscoverOFT: verify %s on chain %s failed: %v", base, chainID, verifyErr)
			continue
		}

		// #region agent log
		debugLogBrickMoving("web_api_brick_moving.go:autoDiscoverOFT:verify", "OFT verification result",
			map[string]interface{}{"chainID": chainID, "symbol": base, "address": tokenAddress, "isOFT": isOFT}, "H2-fix")
		// #endregion

		if isOFT {
			reg.Upsert(bridge.OFTToken{
				ChainID:  chainID,
				Symbol:   base,
				Address:  tokenAddress,
				Enabled:  true,
				Source:   "auto-discover-token-mapping",
			})
			// 同时注册到 LayerZero 实例的本地 map（双重保险）
			lz.SetOFTContract(chainID, base, tokenAddress)
			// 持久化到 config 内存 + JSON 文件，确保后续路由探测和重启后可用
			persistDiscoveredOFT(chainID, base, tokenAddress)
			d.logger.Infof("autoDiscoverOFT: ✅ verified %s on chain %s as OFT, address=%s (auto-registered & persisted)", base, chainID, tokenAddress)
		} else {
			d.logger.Warnf("autoDiscoverOFT: %s on chain %s is NOT an OFT contract (address=%s)", base, chainID, tokenAddress)
		}
	}

	// Peer discovery：对于已确认的 OFT 合约，查询其 peers() 获取其他链的 OFT 地址。
	// 这解决了 token_mapping 只有一个地址（如 BSC），但其他链（如 ETH）有不同合约地址的问题。
	// 找到第一个已确认的 OFT 链作为查询源
	sourceChainID := ""
	sourceAddr := ""
	for _, chainID := range chainIDs {
		if t, ok := reg.Get(chainID, base); ok && t.Address != "" {
			sourceChainID = chainID
			sourceAddr = t.Address
			break
		}
	}
	if sourceChainID != "" && sourceAddr != "" {
		for _, targetChainID := range chainIDs {
			if targetChainID == sourceChainID {
				continue
			}
			// 已有 OFT 地址的链跳过
			if _, ok := reg.Get(targetChainID, base); ok {
				continue
			}
			peerAddr, peerErr := lz.QueryPeerAddress(sourceChainID, sourceAddr, targetChainID)
			// #region agent log
			debugLogBrickMoving("autoDiscoverOFT:peerQuery", "OFT peer query result", map[string]interface{}{
				"sourceChain": sourceChainID, "sourceAddr": sourceAddr,
				"targetChain": targetChainID, "peerAddr": peerAddr,
				"err": fmt.Sprintf("%v", peerErr), "symbol": base,
			}, "FIX3")
			// #endregion
			if peerErr != nil {
				d.logger.Warnf("autoDiscoverOFT: peer query %s->%s failed: %v", sourceChainID, targetChainID, peerErr)
				continue
			}
			if peerAddr == "" {
				continue
			}
			// 验证 peer 地址确实是 OFT
			isPeerOFT, verifyErr := lz.VerifyOFTContract(targetChainID, peerAddr)
			if verifyErr != nil {
				d.logger.Warnf("autoDiscoverOFT: peer verify %s on chain %s failed: %v", base, targetChainID, verifyErr)
				continue
			}
			if isPeerOFT {
				// 检查是否为 OFT Adapter：查询 token() 获取底层 ERC-20 地址
				underlyingToken, utErr := lz.QueryUnderlyingToken(targetChainID, peerAddr)
				// #region agent log
				debugLogBrickMoving("autoDiscoverOFT:underlyingToken", "QueryUnderlyingToken result for peer", map[string]interface{}{
					"targetChain": targetChainID, "peerAddr": peerAddr, "symbol": base,
					"underlyingToken": underlyingToken, "err": fmt.Sprintf("%v", utErr),
				}, "H1")
				// #endregion
				oftToken := bridge.OFTToken{
					ChainID:                targetChainID,
					Symbol:                 base,
					Address:                peerAddr,
					UnderlyingTokenAddress: underlyingToken,
					Enabled:                true,
					Source:                 "auto-discover-peer",
				}
				reg.Upsert(oftToken)
				lz.SetOFTContract(targetChainID, base, peerAddr)
				persistDiscoveredOFT(targetChainID, base, peerAddr)
				if underlyingToken != "" {
					// 同时持久化底层 ERC-20 地址（带 :erc20 后缀）
					persistDiscoveredOFT(targetChainID, base+":erc20", underlyingToken)
					d.logger.Infof("autoDiscoverOFT: ✅ discovered %s on chain %s via peer (OFT Adapter), oft=%s, erc20=%s (persisted)", base, targetChainID, peerAddr, underlyingToken)
				} else {
					d.logger.Infof("autoDiscoverOFT: ✅ discovered %s on chain %s via peer query from chain %s, address=%s (pure OFT, persisted)", base, targetChainID, sourceChainID, peerAddr)
				}
			}
		}
	}
}

// discoverOFTFromWalletInfo 从 WalletInfo（OKX DEX API 数据）中提取代币的合约地址，
// 验证其是否为 OFT 合约，若是则注册到 OFT 注册表。
// 这是 autoDiscoverOFTFromTokenMapping 的补充：当 token_mapping 的全局地址在某链上无效时，
// 利用 OKX DEX API 返回的链上真实合约地址作为候选。
func (d *Dashboard) discoverOFTFromWalletInfo(lz *layerzero.LayerZero, triggerSymbol string, chainIDs []string) {
	if lz == nil || triggerSymbol == "" || len(chainIDs) == 0 {
		return
	}

	base := strings.TrimSuffix(triggerSymbol, "USDT")
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "USDC")
	}
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "BUSD")
	}
	if base == "" {
		base = triggerSymbol // 当 triggerSymbol 本身就是 "USDT" 时
	}
	if base == triggerSymbol {
		base = triggerSymbol
	}

	reg := d.getOrCreateBridgeOFTRegistry()
	if reg == nil {
		return
	}

	// 如果 LayerZero API 已返回该 symbol 的 OFT 数据，说明 API 权威地知道哪些链支持此 token，
	// 不应再从 WalletInfo 添加额外的链（避免把普通 ERC-20 地址误注册为 OFT）。
	// WalletInfo 发现仅用于 API 完全不认识的 token（如 ZAMA）。
	apiHasSymbol := false
	for _, t := range reg.List() {
		if strings.EqualFold(t.Symbol, base) && t.Source == "layerzero-api" {
			apiHasSymbol = true
			break
		}
	}
	// #region agent log
	debugLogBrickMoving("discoverOFTFromWalletInfo:H11:apiCheck", "apiHasSymbol check", map[string]interface{}{
		"base": base, "apiHasSymbol": apiHasSymbol, "triggerSymbol": triggerSymbol,
	}, "H11")
	// #endregion
	if apiHasSymbol {
		d.logger.Debugf("discoverOFTFromWalletInfo: skip %s — LayerZero API already has authoritative OFT data for this symbol", base)
		return
	}

	wm := position.GetWalletManager()
	if wm == nil {
		return
	}
	wi := wm.GetWalletInfo()
	// #region agent log
	{
		wiChains := []string{}
		if wi != nil && wi.OnchainBalances != nil {
			for k := range wi.OnchainBalances { wiChains = append(wiChains, k) }
		}
		debugLogBrickMoving("discoverOFTFromWalletInfo:H14:walletInfo", "WalletInfo state", map[string]interface{}{
			"base": base, "wiNil": wi == nil, "onchainBalancesNil": wi == nil || wi.OnchainBalances == nil,
			"wiChains": wiChains,
		}, "H14")
	}
	// #endregion
	if wi == nil || wi.OnchainBalances == nil {
		return
	}

	for _, chainID := range chainIDs {
		// 已有 OFT 地址的链跳过
		if _, ok := reg.Get(chainID, base); ok {
			continue
		}

		// 从 WalletInfo 中查找该链上的代币合约地址
		symbolMap, ok := wi.OnchainBalances[chainID]
		if !ok {
			continue
		}
		candidateAddr := ""
		for sym, asset := range symbolMap {
			if strings.EqualFold(sym, base) && asset.TokenContractAddress != "" {
				candidateAddr = asset.TokenContractAddress
				break
			}
		}
		// #region agent log
		{
			symsInMap := []string{}
			for sym, asset := range symbolMap {
				addr := ""
				if asset.TokenContractAddress != "" { addr = asset.TokenContractAddress[:8] + "..." }
				symsInMap = append(symsInMap, sym+"="+addr)
			}
			debugLogBrickMoving("discoverOFTFromWalletInfo:H12:chainLookup", "WalletInfo chain lookup", map[string]interface{}{
				"chainID": chainID, "base": base, "candidateAddr": candidateAddr,
				"symbolMapFound": ok, "symsInMap": symsInMap,
			}, "H12")
		}
		// #endregion
		if candidateAddr == "" {
			continue
		}

		// 验证该地址是否为 OFT 合约
		isOFT, verifyErr := lz.VerifyOFTContract(chainID, candidateAddr)
		if verifyErr != nil {
			d.logger.Warnf("discoverOFTFromWalletInfo: verify %s on chain %s (addr=%s) failed: %v", base, chainID, candidateAddr, verifyErr)
			continue
		}
		if isOFT {
			reg.Upsert(bridge.OFTToken{
				ChainID: chainID,
				Symbol:  base,
				Address: candidateAddr,
				Enabled: true,
				Source:  "auto-discover-walletinfo",
			})
			lz.SetOFTContract(chainID, base, candidateAddr)
			// 持久化到 config 内存 + JSON 文件，确保后续路由探测和重启后可用
			persistDiscoveredOFT(chainID, base, candidateAddr)
			d.logger.Infof("discoverOFTFromWalletInfo: ✅ discovered %s on chain %s as OFT via WalletInfo, address=%s (persisted)", base, chainID, candidateAddr)
		} else {
			d.logger.Warnf("discoverOFTFromWalletInfo: %s on chain %s (addr=%s from WalletInfo) is NOT an OFT contract", base, chainID, candidateAddr)
		}
	}
}

// hasCCIPReadyConfig 检查 CCIP 是否有可用配置（至少配置了 TokenPool），
// 未配置时不注册 CCIP 协议，避免空壳实现被 Manager 自动选中导致运行时失败。
func hasCCIPReadyConfig() bool {
	cfg := config.GetGlobalConfig()
	return cfg != nil && len(cfg.Bridge.CCIP.TokenPools) > 0
}

// applyCCIPTokenPoolsFromConfig 将 config.Bridge.CCIP.TokenPools 应用到 CCIP 实例（key 为 "chainID:symbol"，value 为 Token Pool 合约地址）
func applyCCIPTokenPoolsFromConfig(c *ccip.CCIP) {
	if c == nil {
		return
	}
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Bridge.CCIP.TokenPools == nil {
		return
	}
	for key, addr := range cfg.Bridge.CCIP.TokenPools {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 {
			chainID := strings.TrimSpace(parts[0])
			symbol := strings.TrimSpace(parts[1])
			if chainID != "" && symbol != "" {
				c.SetTokenPool(chainID, symbol, addr)
			}
		}
	}
}

// applyOFTContractsFromConfig 将 config.Bridge.LayerZero.OFTContracts 应用到 LayerZero 实例（用于支持 56:ZAMA 等未在 LayerZero API 收录的 OFT）
func applyOFTContractsFromConfig(lz *layerzero.LayerZero) {
	if lz == nil {
		return
	}
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Bridge.LayerZero.OFTContracts == nil {
		return
	}
	for key, addr := range cfg.Bridge.LayerZero.OFTContracts {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 {
			chainID := strings.TrimSpace(parts[0])
			symbol := strings.TrimSpace(parts[1])
			if chainID != "" && symbol != "" {
				lz.SetOFTContract(chainID, symbol, addr)
			}
		}
	}
}

// applyWormholeTokenContractsFromConfig 将 config.Bridge.Wormhole.TokenContracts 应用到 Wormhole 实例
func applyWormholeTokenContractsFromConfig(wh *wormhole.Wormhole) {
	if wh == nil {
		return
	}
	cfg := config.GetGlobalConfig()
	if cfg == nil || cfg.Bridge.Wormhole.TokenContracts == nil {
		return
	}
	for key, addr := range cfg.Bridge.Wormhole.TokenContracts {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 {
			chainID := strings.TrimSpace(parts[0])
			symbol := strings.TrimSpace(parts[1])
			if chainID != "" && symbol != "" {
				wh.SetTokenContract(chainID, symbol, addr)
			}
		}
	}
}

// getWormholeEnabled 返回 Wormhole 是否启用（默认 true）
func getWormholeEnabled() bool {
	if cfg := config.GetGlobalConfig(); cfg != nil {
		return cfg.Bridge.Wormhole.Enabled
	}
	return true
}

// getWormholeRPCURLs 构建 Wormhole 使用的 RPC URLs：constants 打底，LayerZero.RPCURLs 覆盖，Wormhole.RPCURLs 最优先
func getWormholeRPCURLs() map[string]string {
	rpcURLs := constants.GetAllDefaultRPCURLs()
	cfg := config.GetGlobalConfig()
	if cfg == nil {
		return rpcURLs
	}
	if cfg.Bridge.LayerZero.RPCURLs != nil {
		for chainID, url := range cfg.Bridge.LayerZero.RPCURLs {
			if url != "" {
				rpcURLs[chainID] = url
			}
		}
	}
	if cfg.Bridge.Wormhole.RPCURLs != nil {
		for chainID, url := range cfg.Bridge.Wormhole.RPCURLs {
			if url != "" {
				rpcURLs[chainID] = url
			}
		}
	}
	return rpcURLs
}

// discoveredOFTCacheFile 持久化发现的 OFT 地址（JSON 文件），保证重启后不丢失
const discoveredOFTCacheFile = "discovered_ofts.json"

// persistDiscoveredOFT 将发现的 OFT 地址持久化到内存 config + JSON 文件
// 内存 config 确保同一会话后续路由探测能读取到；
// JSON 文件确保重启后也能恢复。
func persistDiscoveredOFT(chainID, symbol, address string) {
	if chainID == "" || symbol == "" || address == "" {
		return
	}
	key := chainID + ":" + symbol

	// 1) 写入全局 config 内存
	cfg := config.GetGlobalConfig()
	if cfg != nil {
		if cfg.Bridge.LayerZero.OFTContracts == nil {
			cfg.Bridge.LayerZero.OFTContracts = make(map[string]string)
		}
		cfg.Bridge.LayerZero.OFTContracts[key] = address
	}

	// 2) 写入 JSON 文件（与可执行文件同目录）
	cache := loadDiscoveredOFTsFromFile()
	if cache == nil {
		cache = make(map[string]string)
	}
	cache[key] = address
	data, err := json.MarshalIndent(cache, "", "  ")
	if err == nil {
		_ = os.WriteFile(discoveredOFTCacheFile, data, 0644)
	}
}

// loadDiscoveredOFTsFromFile 从 JSON 文件加载之前发现的 OFT 地址
func loadDiscoveredOFTsFromFile() map[string]string {
	data, err := os.ReadFile(discoveredOFTCacheFile)
	if err != nil {
		return nil
	}
	var cache map[string]string
	if json.Unmarshal(data, &cache) != nil {
		return nil
	}
	return cache
}

// applyDiscoveredOFTsToLayerZero 从文件+config 加载持久化的 OFT 地址并应用到 LayerZero 实例
func applyDiscoveredOFTsToLayerZero(lz *layerzero.LayerZero) {
	if lz == nil {
		return
	}
	cache := loadDiscoveredOFTsFromFile()
	for key, addr := range cache {
		if addr == "" {
			continue
		}
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 {
			chainID := strings.TrimSpace(parts[0])
			symbol := strings.TrimSpace(parts[1])
			// 跳过 :erc20 后缀条目（只给 registry 用，不给 LayerZero OFT contract map）
			if strings.HasSuffix(symbol, ":erc20") {
				continue
			}
			if chainID != "" && symbol != "" {
				lz.SetOFTContract(chainID, symbol, addr)
			}
		}
	}
}

// applyDiscoveredOFTsToRegistry 从持久化文件恢复 OFT registry 中的 UnderlyingTokenAddress
func applyDiscoveredOFTsToRegistry(reg *bridge.OFTRegistry) {
	if reg == nil {
		return
	}
	cache := loadDiscoveredOFTsFromFile()
	// #region agent log
	debugLogBrickMoving("applyDiscoveredOFTsToRegistry:start", "restoring UnderlyingTokenAddress from cache", map[string]interface{}{
		"cacheSize": len(cache), "cacheEntries": cache,
	}, "H3")
	// #endregion
	// 查找 :erc20 后缀条目，将底层 ERC-20 地址回写到 registry
	for key, addr := range cache {
		if addr == "" {
			continue
		}
		parts := strings.SplitN(key, ":", 3) // chainID:SYMBOL:erc20
		if len(parts) == 3 && parts[2] == "erc20" {
			chainID := strings.TrimSpace(parts[0])
			symbol := strings.TrimSpace(parts[1])
			if chainID == "" || symbol == "" {
				continue
			}
			if t, found := reg.Get(chainID, symbol); found {
				if t.UnderlyingTokenAddress == "" {
					t.UnderlyingTokenAddress = addr
					reg.Upsert(*t)
					// #region agent log
					debugLogBrickMoving("applyDiscoveredOFTsToRegistry:updated", "set UnderlyingTokenAddress on existing entry", map[string]interface{}{
						"chainID": chainID, "symbol": symbol, "oftAddr": t.Address, "erc20Addr": addr,
					}, "H3")
					// #endregion
				}
			} else {
				// OFT 主条目还没恢复，先用 erc20 地址对应的 OFT 地址创建
				oftAddr := cache[chainID+":"+symbol]
				if oftAddr != "" {
					reg.Upsert(bridge.OFTToken{
						ChainID:                chainID,
						Symbol:                 symbol,
						Address:                oftAddr,
						UnderlyingTokenAddress: addr,
						Enabled:                true,
						Source:                 "persisted-cache",
					})
					// #region agent log
					debugLogBrickMoving("applyDiscoveredOFTsToRegistry:created", "created entry with OFT+ERC20 from cache", map[string]interface{}{
						"chainID": chainID, "symbol": symbol, "oftAddr": oftAddr, "erc20Addr": addr,
					}, "H3")
					// #endregion
				}
			}
		}
	}
}

// nodeMapToPathID 从 pipeline 节点 map 转为路由探测用的节点 ID（如 binance, onchain:56）
func nodeMapToPathID(node map[string]interface{}) string {
	if node == nil {
		return ""
	}
	if t, ok := node["type"].(string); ok && t != "" {
		if strings.HasPrefix(t, pipeline.NodeIDPrefixOnchain) {
			return t
		}
		if t == string(pipeline.NodeTypeOnchain) {
			if n, ok := node["network"].(string); ok && n != "" && isNumericChainID(n) {
				return pipeline.OnchainNodeID(n)
			}
			if c, ok := node["chainID"].(string); ok && c != "" && isNumericChainID(c) {
				return pipeline.OnchainNodeID(c)
			}
			if c, ok := node["chainId"].(string); ok && c != "" && isNumericChainID(c) {
				return pipeline.OnchainNodeID(c)
			}
		}
		if t != string(pipeline.NodeTypeExchange) && t != "chain" {
			return t
		}
	}
	if ex, ok := node["exchangeType"].(string); ok && ex != "" {
		return ex
	}
	if name, ok := node["name"].(string); ok && name != "" {
		return name
	}
	if t, ok := node["type"].(string); ok && t != "" {
		return t
	}
	return ""
}

// chainIDFromNodeMap 从 pipeline 节点 map 中解析链 ID（仅链上节点有效），用于探测时限定「源链」；使用 pipeline 约定。
func chainIDFromNodeMap(node map[string]interface{}) string {
	if node == nil {
		return ""
	}
	if t, ok := node["type"].(string); ok && strings.HasPrefix(t, pipeline.NodeIDPrefixOnchain) {
		return strings.TrimPrefix(t, pipeline.NodeIDPrefixOnchain)
	}
	if t, ok := node["type"].(string); ok && t == string(pipeline.NodeTypeOnchain) {
		if n, ok := node["network"].(string); ok && isNumericChainID(n) {
			return n
		}
		if c, ok := node["chainID"].(string); ok && isNumericChainID(c) {
			return c
		}
		if c, ok := node["chainId"].(string); ok && isNumericChainID(c) {
			return c
		}
	}
	return ""
}

// resolveRouteProbeParamsFromTrigger 根据 trigger 与方向解析 route-probe 的 source、dest、symbol；
// sourceChainID 为 pipeline 首节点的链 ID（仅当首节点为 onchain 时有值），用于探测时只保留从该链出发的路径。
// triggerIDStr 非空时按 trigger 区分配置，同 symbol 多 trigger 时取指定 trigger 的 pipeline 与 trader。
func (d *Dashboard) resolveRouteProbeParamsFromTrigger(triggerSymbol, triggerIDStr, direction string) (source, dest, symbol, sourceChainID string, err error) {
	// #region agent log
	debugLogBrickMoving("resolveRouteProbeParamsFromTrigger:entry", "resolve entry", map[string]interface{}{
		"triggerSymbol": triggerSymbol, "triggerIDStr": triggerIDStr, "direction": direction,
	}, "H1-H2")
	// #endregion
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	var nodes []map[string]interface{}
	if direction == "forward" && cfg != nil && len(cfg.ForwardNodes) > 0 {
		nodes = cfg.ForwardNodes
	} else if direction == "backward" && cfg != nil && len(cfg.BackwardNodes) > 0 {
		nodes = cfg.BackwardNodes
	}
	if len(nodes) >= 2 {
		source = nodeMapToPathID(nodes[0])
		dest = nodeMapToPathID(nodes[len(nodes)-1])
		sourceChainID = chainIDFromNodeMap(nodes[0])
		// #region agent log
		debugLogBrickMoving("resolveRouteProbeParamsFromTrigger:fromConfig", "using pipeline config nodes", map[string]interface{}{
			"direction": direction, "source": source, "dest": dest, "sourceChainID": sourceChainID, "nodesLen": len(nodes),
		}, "H1")
		// #endregion
	} else {
		var tg proto.Trigger
		if triggerIDStr != "" {
			if id, parseErr := strconv.ParseUint(triggerIDStr, 10, 64); parseErr == nil {
				if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
					tg, _ = tm.GetTriggerByIDAsProto(id)
				}
			}
		}
		if tg == nil {
			var getErr error
			tg, getErr = d.triggerManager.GetTrigger(triggerSymbol)
			if getErr != nil {
				return "", "", "", "", fmt.Errorf("trigger not found: %w", getErr)
			}
		}
		traderA := tg.GetTraderAType()
		traderB := tg.GetTraderBType()
		if idx := strings.Index(traderA, ":"); idx > 0 {
			traderA = traderA[:idx]
		}
		if idx := strings.Index(traderB, ":"); idx > 0 {
			traderB = traderB[:idx]
		}
		if direction == "forward" {
			source = traderA
			dest = traderB
			if dest != "" && isNumericChainID(dest) && !pipeline.IsOnchainNodeID(dest) {
				dest = pipeline.OnchainNodeID(dest)
			}
			if isNumericChainID(source) {
				sourceChainID = source
				source = pipeline.OnchainNodeID(source)
			}
		} else {
			source = traderB
			dest = traderA
			if source != "" && isNumericChainID(source) && !pipeline.IsOnchainNodeID(source) {
				source = pipeline.OnchainNodeID(source)
			}
			if isNumericChainID(source) {
				sourceChainID = source
			}
			// #region agent log
			debugLogBrickMoving("resolveRouteProbeParamsFromTrigger:fromTrigger", "fallback to trigger traderA/B", map[string]interface{}{
				"direction": direction, "traderA": traderA, "traderB": traderB, "source": source, "dest": dest, "sourceChainID": sourceChainID,
			}, "H2")
			// #endregion
		}
	}
	base := strings.TrimSuffix(triggerSymbol, "USDT")
	if base == triggerSymbol {
		base = strings.TrimSuffix(triggerSymbol, "USDC")
	}
	if base == "" {
		base = "USDT"
	}
	if direction == "forward" {
		symbol = base
	} else {
		symbol = d.getQuoteAssetForTrigger(triggerSymbol, triggerIDStr)
	}
	// #region agent log
	debugLogBrickMoving("resolveRouteProbeParamsFromTrigger:exit", "resolved params", map[string]interface{}{
		"direction": direction, "source": source, "dest": dest, "symbol": symbol, "sourceChainID": sourceChainID,
	}, "H1-H2-H4")
	// #endregion
	return source, dest, symbol, sourceChainID, nil
}

// handleRouteProbe 路由探测（搜索充提链）：最多 4 跳，返回路径、每段费用/耗时/可用性；
// 当 Path 为空且提供 Source/Destination 时，自动解析候选路径（交易所提现网络 + 跨链），探测多条并返回最佳与备选。
// 若提供 TriggerSymbol + Direction，则从 Trigger 的 pipeline 配置解析 Source/Destination/Symbol，实现 A-B/B-A 统一抽象。
func (d *Dashboard) handleRouteProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	r = r.WithContext(ctx)

	var req model.RouteProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	d.logger.Infof("route-probe: request triggerSymbol=%s triggerId=%s direction=%s source=%s destination=%s symbol=%s pathLen=%d",
		req.TriggerSymbol, req.TriggerID, req.Direction, req.Source, req.Destination, req.Symbol, len(req.Path))
	// #region agent log
	debugLogBrickMoving("handleRouteProbe:entry", "route probe request parsed",
		map[string]interface{}{
			"triggerSymbol": req.TriggerSymbol, "direction": req.Direction,
			"source": req.Source, "destination": req.Destination, "pathLen": len(req.Path),
			"symbol": req.Symbol, "probeAmount": req.ProbeAmount,
		}, "H-probe-entry")
	// #endregion

	// 若传入 TriggerSymbol + Direction，从配置解析 source/dest/symbol，统一 A-B 与 B-A 探测
	var sourceChainID string
	if req.TriggerSymbol != "" && (req.Direction == "forward" || req.Direction == "backward") {
		originalSource := req.Source
		originalDest := req.Destination
		source, dest, sym, srcChain, resolveErr := d.resolveRouteProbeParamsFromTrigger(req.TriggerSymbol, req.TriggerID, req.Direction)
		if resolveErr != nil {
			http.Error(w, "resolve route-probe params: "+resolveErr.Error(), http.StatusBadRequest)
			return
		}
		req.Source = source
		req.Destination = dest
		sourceChainID = srcChain
		// 若 pipeline 存的是裸 "onchain" 无链 ID，导致 sourceChainID 为空，用请求体中的 Source 补全（如前端传 onchain:56 表示从 BSC 探测）
		if sourceChainID == "" && source == string(pipeline.NodeTypeOnchain) && originalSource != "" {
			if chainFromReq := pipeline.ChainIDFromNodeID(originalSource); chainFromReq != "" {
				sourceChainID = chainFromReq
				req.Source = pipeline.OnchainNodeID(chainFromReq)
			}
		}
		// 反向探测时 resolve 可能得到 dest="onchain"（trigger 未存具体链），用请求体中的 Destination 补全为具体链（如 onchain:56）
		if req.Destination == string(pipeline.NodeTypeOnchain) && originalDest != "" {
			if chainFromReq := pipeline.ChainIDFromNodeID(originalDest); chainFromReq != "" {
				req.Destination = pipeline.OnchainNodeID(chainFromReq)
			}
		}
		if sym != "" {
			req.Symbol = sym
		}
		d.logger.Infof("route-probe: resolved from trigger source=%s dest=%s symbol=%s sourceChainID=%s",
			req.Source, req.Destination, req.Symbol, sourceChainID)
		// #region agent log
		debugLogBrickMoving("handleRouteProbe:afterResolve", "resolved from trigger", map[string]interface{}{
			"direction": req.Direction, "source": req.Source, "dest": req.Destination, "sourceChainID": sourceChainID, "symbol": sym, "originalSource": originalSource, "originalDest": originalDest,
		}, "H1-H4")
		// #endregion
	}

	// 整条 pipeline 同方向固定同一币种：A-B 用 trigger 的 base（如 ZAMA），B-A 用 quoteAsset（如 USDT）；何时触发提现的换算在 Web 层后续处理。
	symbol := req.Symbol
	if symbol == "" {
		symbol = "USDT"
	}
	if req.ProbeAmount == "" {
		req.ProbeAmount = "100"
	}

	// 使用全局 OFT 注册表（trigger 配置时已加载），并确保当前探测的 symbol 已加载
	reg := d.getOrCreateBridgeOFTRegistry()
	triggerSym := symbol
	if symbol != "" && !strings.HasSuffix(strings.ToUpper(symbol), "USDT") && !strings.HasSuffix(strings.ToUpper(symbol), "USDC") {
		triggerSym = symbol + "USDT"
	}
	// 同步刷新 OFT 注册表，确保报价查询时 OFT 地址已就绪（避免竞态导致误判）
	d.refreshBridgeAddressesForSymbol(triggerSym)
	// 路由探测需要 RPC URLs 以验证 OFT 合约（autoDiscoverOFT / discoverOFTFromWalletInfo）
	rpcURLs := constants.GetAllDefaultRPCURLs()
	globalConfig := config.GetGlobalConfig()
	if globalConfig != nil && globalConfig.Bridge.LayerZero.RPCURLs != nil {
		for chainID, url := range globalConfig.Bridge.LayerZero.RPCURLs {
			if url != "" {
				rpcURLs[chainID] = url
			}
		}
	}
	lz := layerzero.NewLayerZero(rpcURLs, true)
	lz.SetOFTRegistry(reg)
	applyOFTContractsFromConfig(lz)
	// 加载之前发现并持久化的 OFT 地址（确保重启后或 API 不稳定时仍可用）
	applyDiscoveredOFTsToLayerZero(lz)
	// 同时恢复 registry 中的 UnderlyingTokenAddress（OFT Adapter 底层 ERC-20 地址）
	applyDiscoveredOFTsToRegistry(reg)
	// #region agent log
	{
		cache := loadDiscoveredOFTsFromFile()
		debugLogBrickMoving("handleRouteProbe:persistedOFTs", "loaded persisted OFT cache", map[string]interface{}{
			"cacheSize": len(cache), "cacheEntries": cache,
		}, "FIX2")
	}
	// #endregion
	// 路由探测也需要发现 OFT 地址（与 pipeline apply 相同），否则非 API 注册的 token（如 ZAMA）无法识别
	// 收集已知链 ID 用于 OFT 发现
	routeProbeChainIDs := make([]string, 0, len(rpcURLs))
	for chainID := range rpcURLs {
		routeProbeChainIDs = append(routeProbeChainIDs, chainID)
	}
	// #region agent log
	{
		rpcKeys := make([]string, 0, len(rpcURLs))
		for k := range rpcURLs { rpcKeys = append(rpcKeys, k) }
		debugLogBrickMoving("handleRouteProbe:H1:rpcURLs", "rpcURLs populated", map[string]interface{}{
			"rpcChainIDs": rpcKeys, "rpcCount": len(rpcURLs), "symbol": symbol, "triggerSym": triggerSym,
			"routeProbeChainIDs": routeProbeChainIDs,
		}, "H1")
	}
	// #endregion
	// 确保 WalletManager 知道所有候选链（重启后链列表可能丢失）
	position.AddChainsGlobal(routeProbeChainIDs...)
	// 条件刷新：仅当 WalletInfo 缺少某些链的数据时才 ForceRefresh，
	// 避免在数据已完整的情况下刷新导致 OKX API 丢失小众 token（如 ZAMA）。
	if wm := position.GetWalletManager(); wm != nil {
		wi := wm.GetWalletInfo()
		needsRefresh := false
		if wi == nil || wi.OnchainBalances == nil {
			needsRefresh = true
		} else {
			for _, cid := range routeProbeChainIDs {
				if _, ok := wi.OnchainBalances[cid]; !ok {
					needsRefresh = true
					break
				}
			}
		}
		// #region agent log
		debugLogBrickMoving("handleRouteProbe:conditionalRefresh", "conditional ForceRefresh check", map[string]interface{}{
			"needsRefresh": needsRefresh, "wiNil": wi == nil,
			"routeProbeChainIDs": routeProbeChainIDs,
		}, "FIX1")
		// #endregion
		if needsRefresh {
			d.logger.Infof("handleRouteProbe: WalletInfo 缺少部分链数据，执行 ForceRefresh...")
			_ = wm.ForceRefresh()
		}
	}
	d.autoDiscoverOFTFromTokenMapping(lz, triggerSym, routeProbeChainIDs)
	d.discoverOFTFromWalletInfo(lz, triggerSym, routeProbeChainIDs)

	// 收集 token 在各链上的已知 ERC-20 地址（统一数据源，供所有协议的自动发现使用）
	knownTokenAddresses := collectKnownTokenAddresses(symbol, routeProbeChainIDs)
	d.logger.Infof("handleRouteProbe: collected %d known addresses for %s across chains", len(knownTokenAddresses), symbol)

	bridgeMgr := bridge.NewManager(true)
	bridgeMgr.RegisterProtocol(lz)

	// LayerZero 统一发现：用 per-chain 地址弥补 autoDiscoverOFT 的单地址假设
	if disc, ok := interface{}(lz).(bridge.TokenDiscoverer); ok {
		if found, err := disc.DiscoverToken(symbol, knownTokenAddresses, routeProbeChainIDs); err == nil && len(found) > 0 {
			d.logger.Infof("handleRouteProbe: LayerZero discovered %d new OFT addresses for %s", len(found), symbol)
		}
	}

	wh := wormhole.NewWormhole(getWormholeRPCURLs(), getWormholeEnabled())
	applyWormholeTokenContractsFromConfig(wh)
	if disc, ok := interface{}(wh).(bridge.TokenDiscoverer); ok {
		if found, err := disc.DiscoverToken(symbol, knownTokenAddresses, routeProbeChainIDs); err == nil && len(found) > 0 {
			d.logger.Infof("handleRouteProbe: Wormhole discovered %d token addresses for %s", len(found), symbol)
		}
	}
	bridgeMgr.RegisterProtocol(wh)

	ccipProtocol := ccip.NewCCIP(rpcURLs, true)
	for _, cid := range routeProbeChainIDs {
		if urls := constants.GetDefaultRPCURLs(cid); len(urls) > 0 {
			ccipProtocol.SetRPCURLsForChain(cid, urls)
		}
	}
	applyCCIPTokenPoolsFromConfig(ccipProtocol)
	if disc, ok := interface{}(ccipProtocol).(bridge.TokenDiscoverer); ok {
		if found, err := disc.DiscoverToken(symbol, knownTokenAddresses, routeProbeChainIDs); err == nil && len(found) > 0 {
			d.logger.Infof("handleRouteProbe: CCIP discovered %d token addresses for %s", len(found), symbol)
		}
	}
	bridgeMgr.RegisterProtocol(ccipProtocol)

	// 自动解析候选路径：Path 为空且 Source+Destination 有值时
	if len(req.Path) == 0 && req.Source != "" && req.Destination != "" {
		candidates, networkCount, err := d.resolveCandidatePathsForRouteProbe(req.Source, req.Destination, symbol, sourceChainID, bridgeMgr)
		if err != nil {
			http.Error(w, "resolve candidate paths: "+err.Error(), http.StatusBadRequest)
			return
		}
		// #region agent log
		debugLogBrickMoving("handleRouteProbe:candidates", "candidate paths resolved", map[string]interface{}{
			"totalCandidates": len(candidates), "networkCount": networkCount,
			"symbol": symbol, "source": req.Source, "destination": req.Destination,
		}, "H13")
		// #endregion
		req.Symbol = symbol
		results := make([]*model.RouteProbeResult, 0, len(candidates)*4) // 每条路径可能展开为多协议
		// #region agent log
		var pathProbeErrors []string // 收集每条路径探测失败原因，便于 debug
		// #endregion
		for _, path := range candidates {
			if len(path) < 2 {
				continue
			}
		// 找出路径中第一段跨链的 fromChain/toChain，用于按协议展开
			firstBridgeFrom, firstBridgeTo := routeProbeFirstBridgeSegment(path)
			// #region agent log
			debugLogBrickMoving("handleRouteProbe:H5:bridgeSegment", "bridge segment for path", map[string]interface{}{
				"path": path, "firstBridgeFrom": firstBridgeFrom, "firstBridgeTo": firstBridgeTo,
				"symbol": symbol,
			}, "H5")
			// #endregion
			if firstBridgeFrom != "" && firstBridgeTo != "" {
				// 路径包含跨链段：必须有至少一个支持的协议才保留
				quoteReq := &model.BridgeQuoteRequest{
					FromChain: firstBridgeFrom,
					ToChain:   firstBridgeTo,
					FromToken: symbol,
					ToToken:   symbol,
					Amount:    req.ProbeAmount,
				}
				quote, err := bridgeMgr.GetBridgeQuote(quoteReq)
				expanded := false
				// #region agent log
				{
					protocolSummary := make([]map[string]interface{}, 0)
					if quote != nil {
						for _, pq := range quote.Protocols {
							protocolSummary = append(protocolSummary, map[string]interface{}{
								"protocol": pq.Protocol, "supported": pq.Supported,
							})
						}
					}
					debugLogBrickMoving("handleRouteProbe:bridgeQuote", "bridge quote result for path", map[string]interface{}{
						"path": path, "from": firstBridgeFrom, "to": firstBridgeTo,
						"err": fmt.Sprintf("%v", err), "protocolCount": len(protocolSummary),
						"protocols": protocolSummary,
					}, "H13,H15")
				}
				// #endregion
				if err == nil && quote != nil && len(quote.Protocols) > 0 {
					// 按协议展开：仅展开 Supported=true 的协议，跳过未实现/未配置的协议
					for _, pq := range quote.Protocols {
						if !pq.Supported {
							continue
						}
						probeReq := model.RouteProbeRequest{
							Path:            path,
							Symbol:          symbol,
							ProbeAmount:     req.ProbeAmount,
							BridgeProtocol:  pq.Protocol,
						}
						res, probeErr := pipeline.RouteProbe(&probeReq, bridgeMgr)
						if probeErr != nil {
							d.logger.Warnf("route-probe path %v protocol %s failed: %v", path, pq.Protocol, probeErr)
							// #region agent log
							errStr := fmt.Sprintf("path=%v protocol=%s err=%v", path, pq.Protocol, probeErr)
							pathProbeErrors = append(pathProbeErrors, errStr)
							debugLogBrickMoving("handleRouteProbe:pathProbeFailed", "bridge path probe failed", map[string]interface{}{
								"path": path, "protocol": pq.Protocol, "error": probeErr.Error(),
							}, "H-probe-fail")
							// #endregion
							continue
						}
						results = append(results, res)
						expanded = true
					}
				}
				if !expanded {
					// 跨链段无可用协议 → 跳过此路径（不展示不可达的跨链路由）
					d.logger.Infof("route-probe: no supported bridge protocol for path %v (%s -> %s), skipping", path, firstBridgeFrom, firstBridgeTo)
					// #region agent log
					pathProbeErrors = append(pathProbeErrors, fmt.Sprintf("path=%v no supported bridge protocol %s->%s", path, firstBridgeFrom, firstBridgeTo))
					// #endregion
				}
				continue // 跨链路径已处理完毕
			}
			// 无跨链段（直连路径）：按原逻辑探测一次
			probeReq := model.RouteProbeRequest{
				Path:            path,
				Symbol:          symbol,
				ProbeAmount:     req.ProbeAmount,
				BridgeProtocol:  req.BridgeProtocol,
			}
			res, err := pipeline.RouteProbe(&probeReq, bridgeMgr)
			if err != nil {
				d.logger.Warnf("route-probe path %v failed: %v", path, err)
				// #region agent log
				errStr := fmt.Sprintf("path=%v err=%v", path, err)
				pathProbeErrors = append(pathProbeErrors, errStr)
				debugLogBrickMoving("handleRouteProbe:pathProbeFailed", "direct path probe failed", map[string]interface{}{
					"path": path, "error": err.Error(),
				}, "H-probe-fail")
				// #endregion
				continue
			}
			results = append(results, res)
		}
		if len(results) == 0 {
			// #region agent log
			debugLogBrickMoving("handleRouteProbe:noResult", "no candidate path could be probed", map[string]interface{}{
				"totalCandidates": len(candidates), "resultsCount": len(results),
				"pathProbeErrors": pathProbeErrors, "pathProbeErrorCount": len(pathProbeErrors),
			}, "H-probe-noResult")
			// #endregion
			// 返回 JSON 错误体，避免前端将纯文本当 JSON 解析导致 "Unexpected token 'o'" 等报错
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no candidate path could be probed",
				"details": pathProbeErrors,
			})
			return
		}
		// 只保留源节点等于当前探测源的路径
		probeSourceNode := normalizeRouteNodeID(req.Source)
		probeDestNode := normalizeRouteNodeID(req.Destination)
		// #region agent log
		firstPath0, firstPathLast := "", ""
		if len(results) > 0 && len(results[0].Path) > 0 {
			firstPath0 = results[0].Path[0]
			firstPathLast = results[0].Path[len(results[0].Path)-1]
		}
		debugLogBrickMoving("handleRouteProbe:beforeSourceFilter", "source filter inputs", map[string]interface{}{
			"probeSourceNode": probeSourceNode, "probeDestNode": probeDestNode, "reqSource": req.Source, "reqDest": req.Destination,
			"resultsBeforeFilter": len(results), "firstPath0": firstPath0, "firstPathLast": firstPathLast,
		}, "H3-H5")
		// #endregion
		if probeSourceNode != "" && probeSourceNode != string(pipeline.NodeTypeOnchain) {
			filtered := results[:0]
			for _, r := range results {
				if len(r.Path) > 0 && r.Path[0] == probeSourceNode {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}
		// 只保留终点等于当前探测目标的路径（与源过滤对称）
		if probeDestNode != "" && probeDestNode != string(pipeline.NodeTypeOnchain) {
			filtered := results[:0]
			for _, r := range results {
				if len(r.Path) > 0 && r.Path[len(r.Path)-1] == probeDestNode {
					filtered = append(filtered, r)
				}
			}
			results = filtered
		}
		// #region agent log
		destMismatch := 0
		for _, r := range results {
			if len(r.Path) > 0 && probeDestNode != "" && r.Path[len(r.Path)-1] != probeDestNode {
				destMismatch++
			}
		}
		debugLogBrickMoving("handleRouteProbe:afterSourceFilter", "after source and dest filter", map[string]interface{}{
			"resultsAfterFilter": len(results), "probeDestNode": probeDestNode, "destMismatchCount": destMismatch,
		}, "H3-H5")
		// #endregion
		if len(results) == 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "no path from the given source",
				"details": []string{"源节点不符合当前探测源，已全部过滤"},
			})
			return
		}
		// 校验 deposit/withdraw 段可用性（RouteProbe 默认 Available=true，需用交易所 API 二次校验）
		d.logger.Infof("route-probe: verify start paths=%d symbol=%s (before verify each path has segments with Available=true by default)",
			len(results), symbol)
		for idx, res := range results {
			segTypes := make([]string, 0, len(res.Segments))
			for _, s := range res.Segments {
				segTypes = append(segTypes, string(s.Type)+":"+s.FromNodeID+"->"+s.ToNodeID)
			}
			d.logger.Infof("route-probe: path[%d] path=%v segment_count=%d segment_types=%v",
				idx, res.Path, len(res.Segments), segTypes)
		}
		d.verifyDepositWithdrawSegments(results, symbol)
		// 排序：全段可用优先，其次总费用低，其次耗时短
		// 不过滤不可达路径——前端通过红/绿箭头展示可达性
		sortRouteProbeResults(results)
		// 日志：探测结果与可达性，便于排查「不可达却显示可达」问题
		reachableCount := 0
		for _, r := range results {
			if allSegmentsAvailable(r) {
				reachableCount++
			}
		}
		bestReachable := len(results) > 0 && allSegmentsAvailable(results[0])
		d.logger.Infof("route-probe: symbol=%s direction=%s candidates=%d paths_returned=%d reachable_paths=%d best_reachable=%v",
			symbol, req.Direction, len(candidates), len(results), reachableCount, bestReachable)
		// 每条路径的 segment 可用性摘要，便于排查哪段被标为不可达
		for idx, r := range results {
			segStatus := ""
			for i, s := range r.Segments {
				av := "ok"
				if !s.Available {
					reason := ""
					if s.RawInfo != nil {
						if r0, _ := s.RawInfo["reason"].(string); r0 != "" {
							reason = r0
						}
					}
					av = "UNREACHABLE"
					if reason != "" {
						av = "UNREACHABLE:" + reason
					}
				}
				if segStatus != "" {
					segStatus += " "
				}
				segStatus += string(s.Type) + "(" + s.FromNodeID + "->" + s.ToNodeID + ")==" + av
				_ = i
			}
			d.logger.Infof("route-probe: path[%d] path=%v segments=%d %s", idx, r.Path, len(r.Segments), segStatus)
		}

		for _, r := range results {
			fillSegmentEdgeLabels(r.Segments)
		}

		// 统计可达数量用于 ResolveSummary（与上面日志一致，此处不再重复计算 reachableCount）

		best := results[0]
		alternatives := make([]model.PathProbeSummary, 0, len(results)-1)
		for i := 1; i < len(results); i++ {
			r := results[i]
			alternatives = append(alternatives, model.PathProbeSummary{
				Path:                  r.Path,
				Segments:              r.Segments,
				TotalEstimatedTimeSec: r.TotalEstimatedTimeSec,
				TotalFee:              r.TotalFee,
				RecommendedMinAmount:  r.RecommendedMinAmount,
				ProbeMinAmountHint:    r.ProbeMinAmountHint,
			})
		}
		best.AlternativePaths = alternatives
		best.VerifyWithdrawHint = "提现已通过无消耗接口(GetWithdrawNetworks)确认；小成本确认需在对应链买入 pipeline数量×5 USDT 现货用于测试提现"
		best.ResolveSummary = "可提现网络 " + strconv.Itoa(networkCount) + " 个，可达路径 " + strconv.Itoa(reachableCount) + " 条（共 " + strconv.Itoa(len(results)) + " 条）"
		// #region agent log
		segSummary := make([]map[string]interface{}, 0, len(best.Segments))
		var ex2exSegs []map[string]interface{}
		for _, s := range best.Segments {
			segSummary = append(segSummary, map[string]interface{}{
				"type": s.Type, "from": s.FromNodeID, "to": s.ToNodeID,
				"withdrawNetworkChainID": s.WithdrawNetworkChainID, "bridgeProtocol": s.BridgeProtocol, "edgeLabel": s.EdgeLabel,
			})
			if s.Type == model.SegmentTypeExchangeToExchange {
				ex2exSegs = append(ex2exSegs, map[string]interface{}{
					"from": s.FromNodeID, "to": s.ToNodeID, "withdrawNetworkChainID": s.WithdrawNetworkChainID, "edgeLabel": s.EdgeLabel,
				})
			}
		}
		ex2exCountAll := 0
		for _, r := range results {
			for _, s := range r.Segments {
				if s.Type == model.SegmentTypeExchangeToExchange {
					ex2exCountAll++
				}
			}
		}
		debugLogBrickMoving("handleRouteProbe:beforeSuccessResponse", "best path and segments", map[string]interface{}{
			"bestPath": best.Path, "segmentCount": len(best.Segments), "segments": segSummary,
			"totalResults": len(results), "reachableCount": reachableCount,
		}, "H-flow-success")
		debugLogBrickMoving("handleRouteProbe:ex2exSummary", "exchange_to_exchange segments in response", map[string]interface{}{
			"bestEx2exSegments": ex2exSegs, "bestEx2exCount": len(ex2exSegs), "totalEx2exSegmentsAcrossAllResults": ex2exCountAll, "totalPathsReturned": len(results),
		}, "H-ex2ex-label")
		// #endregion
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(best)
		return
	}

	// 显式 Path 或单跳补全
	if len(req.Path) == 0 && req.Source != "" && req.Destination != "" {
		req.Path = []string{req.Source, req.Destination}
	}
	if len(req.Path) < 2 {
		http.Error(w, "path (or source+destination) with at least 2 nodes is required", http.StatusBadRequest)
		return
	}

	req.Symbol = symbol
	result, err := pipeline.RouteProbe(&req, bridgeMgr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": "route probe failed: " + err.Error()})
		return
	}
	d.verifyDepositWithdrawSegments([]*model.RouteProbeResult{result}, symbol)
	fillSegmentEdgeLabels(result.Segments)
	d.logger.Infof("route-probe: single-path path=%v symbol=%s reachable=%v",
		req.Path, symbol, allSegmentsAvailable(result))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// verifyDepositWithdrawSegments 用交易所 API 校验 deposit/withdraw 段可用性，修正 RouteProbe 默认 Available=true 的误判
func (d *Dashboard) verifyDepositWithdrawSegments(results []*model.RouteProbeResult, symbol string) {
	for _, r := range results {
		for i := range r.Segments {
			seg := &r.Segments[i]
			switch seg.Type {
			case model.SegmentTypeDeposit:
				chainID := pipeline.ChainIDFromNodeID(seg.FromNodeID)
				exType := extractExchangeTypeFromNodeID(seg.ToNodeID)
				if chainID == "" || exType == "" {
					d.logger.Debugf("route-probe verify: skip deposit segment path=%v from=%s to=%s (chainID=%s exType=%s)", r.Path, seg.FromNodeID, seg.ToNodeID, chainID, exType)
				} else if !d.chainInDepositNetworks(exType, symbol, chainID) {
						seg.Available = false
						if seg.RawInfo == nil {
							seg.RawInfo = make(map[string]interface{})
						}
						seg.RawInfo["reason"] = "充币不可达：该交易所不支持该链充币"
						d.logger.Warnf("route-probe verify: segment UNREACHABLE path=%v symbol=%s type=deposit from=%s to=%s reason=充币不可达",
							r.Path, symbol, seg.FromNodeID, seg.ToNodeID)
					}
			case model.SegmentTypeWithdraw:
				exType := extractExchangeTypeFromNodeID(seg.FromNodeID)
				chainID := pipeline.ChainIDFromNodeID(seg.ToNodeID)
				if exType == "" || chainID == "" {
					d.logger.Debugf("route-probe verify: skip withdraw segment path=%v from=%s to=%s (exType=%s chainID=%s)", r.Path, seg.FromNodeID, seg.ToNodeID, exType, chainID)
				} else if !d.chainInWithdrawNetworks(exType, symbol, chainID) {
						seg.Available = false
						if seg.RawInfo == nil {
							seg.RawInfo = make(map[string]interface{})
						}
						seg.RawInfo["reason"] = "提币不可达：该交易所不支持提现到该链"
						d.logger.Warnf("route-probe verify: segment UNREACHABLE path=%v symbol=%s type=withdraw from=%s to=%s reason=提币不可达",
							r.Path, symbol, seg.FromNodeID, seg.ToNodeID)
					}
			case model.SegmentTypeExchangeToExchange:
				chainID := seg.WithdrawNetworkChainID
				if chainID == "" {
					chainID = pipeline.ChainIDFromNodeID(seg.ToNodeID)
				}
				exA := extractExchangeTypeFromNodeID(seg.FromNodeID)
				exB := extractExchangeTypeFromNodeID(seg.ToNodeID)
				if exA == "" || exB == "" || chainID == "" {
					d.logger.Debugf("route-probe verify: skip exchange_to_exchange segment path=%v from=%s to=%s (exA=%s exB=%s chainID=%s)", r.Path, seg.FromNodeID, seg.ToNodeID, exA, exB, chainID)
				} else {
					wdOk := d.chainInWithdrawNetworks(exA, symbol, chainID)
					depOk := d.chainInDepositNetworks(exB, symbol, chainID)
					d.logger.Debugf("route-probe verify: ex2ex path=%v %s->%s chainID=%s withdraw_ok=%v deposit_ok=%v", r.Path, seg.FromNodeID, seg.ToNodeID, chainID, wdOk, depOk)
					if !wdOk || !depOk {
						seg.Available = false
						if seg.RawInfo == nil {
							seg.RawInfo = make(map[string]interface{})
						}
						if !wdOk && !depOk {
							seg.RawInfo["reason"] = "交易所间不可达：提现/充币链均不支持"
						} else if !wdOk {
							seg.RawInfo["reason"] = "交易所间不可达：源交易所不支持提现到该链"
						} else {
							seg.RawInfo["reason"] = "交易所间不可达：目标交易所不支持该链充币"
						}
						d.logger.Warnf("route-probe verify: segment UNREACHABLE path=%v symbol=%s type=exchange_to_exchange from=%s to=%s chainID=%s reason=%v",
							r.Path, symbol, seg.FromNodeID, seg.ToNodeID, chainID, seg.RawInfo["reason"])
					}
				}
			}
		}
	}
}

func extractExchangeTypeFromNodeID(nodeID string) string {
	if nodeID == "" || pipeline.IsOnchainNodeID(nodeID) {
		return ""
	}
	if idx := strings.Index(nodeID, ":"); idx > 0 {
		return nodeID[:idx]
	}
	// 自动生成的交易所节点 ID 格式为 "exchangeType-Asset"（如 bitget-POWER），取前半作为交易所类型以便 getExchange 能命中
	if idx := strings.LastIndex(nodeID, "-"); idx > 0 {
		return nodeID[:idx]
	}
	return nodeID
}

// depositer 用于充币段二次校验：调用交易所「获取充币地址」API，若已停用会返回错误
type depositer interface {
	Deposit(asset string, network string) (*model.DepositAddress, error)
}

// chainInDepositNetworks 检查该链是否在交易所充币网络列表中，并二次调用 Deposit(asset, network) 确认实际可充
func (d *Dashboard) chainInDepositNetworks(exType, symbol, chainID string) bool {
	ex := d.getExchange(exType)
	if ex == nil && position.GetWalletManager() != nil {
		ex = position.GetWalletManager().GetExchange(exType)
	}
	if ex == nil {
		d.logger.Infof("route-probe chainInDepositNetworks: ex=%s 未找到交易所实例，充币段视为不可达", exType)
		return false
	}
	var networks []model.WithdrawNetworkInfo
	var err error
	if dep, ok := ex.(exchange.DepositNetworkLister); ok {
		networks, err = dep.GetDepositNetworks(symbol)
	} else if wd, ok := ex.(exchange.WithdrawNetworkLister); ok {
		networks, err = wd.GetWithdrawNetworks(symbol)
	} else {
		return true
	}
	if err != nil {
		d.logger.Infof("route-probe chainInDepositNetworks: %s GetDepositNetworks(%s) err=%v", exType, symbol, err)
		return false
	}
	norm := pipeline.NormalizeChainID(chainID)
	var matched *model.WithdrawNetworkInfo
	for i := range networks {
		if pipeline.NormalizeChainID(networks[i].ChainID) == norm {
			matched = &networks[i]
			break
		}
	}
	if matched == nil {
		d.logger.Infof("route-probe chainInDepositNetworks: %s symbol=%s chainID=%s 不在充币网络列表（共%d个网络），充币不可达", exType, symbol, chainID, len(networks))
		return false
	}
	// 二次校验：调用交易所「获取充币地址」API，若该网络已停充会返回错误
	if depEx, ok := ex.(depositer); ok && matched.Network != "" {
		_, checkErr := depEx.Deposit(symbol, matched.Network)
		if checkErr != nil {
			d.logger.Warnf("route-probe chainInDepositNetworks: %s deposit %s/%s failed (likely disabled): %v", exType, symbol, matched.Network, checkErr)
			return false
		}
	}
	return true
}

func (d *Dashboard) chainInWithdrawNetworks(exType, symbol, chainID string) bool {
	ex := d.getExchange(exType)
	if ex == nil && position.GetWalletManager() != nil {
		ex = position.GetWalletManager().GetExchange(exType)
	}
	if ex == nil {
		d.logger.Infof("route-probe chainInWithdrawNetworks: ex=%s 未找到交易所实例，提币段视为不可达", exType)
		return false
	}
	wd, ok := ex.(exchange.WithdrawNetworkLister)
	if !ok {
		return true
	}
	networks, err := wd.GetWithdrawNetworks(symbol)
	if err != nil {
		d.logger.Infof("route-probe chainInWithdrawNetworks: %s GetWithdrawNetworks(%s) err=%v", exType, symbol, err)
		return false
	}
	norm := pipeline.NormalizeChainID(chainID)
	for _, n := range networks {
		if pipeline.NormalizeChainID(n.ChainID) == norm {
			return true
		}
	}
	d.logger.Infof("route-probe chainInWithdrawNetworks: %s symbol=%s chainID=%s 不在提币网络列表（共%d个网络），提币不可达", exType, symbol, chainID, len(networks))
	return false
}

// sortRouteProbeResults 按「全段可用优先、总费用低、耗时短」排序
func sortRouteProbeResults(results []*model.RouteProbeResult) {
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			ri, rj := results[i], results[j]
			ai := allSegmentsAvailable(ri)
			aj := allSegmentsAvailable(rj)
			if ai != aj {
				if !ai && aj {
					results[i], results[j] = results[j], results[i]
				}
				continue
			}
			fi, _ := strconv.ParseFloat(ri.TotalFee, 64)
			fj, _ := strconv.ParseFloat(rj.TotalFee, 64)
			if fi != fj {
				if fi > fj {
					results[i], results[j] = results[j], results[i]
				}
				continue
			}
			if ri.TotalEstimatedTimeSec > rj.TotalEstimatedTimeSec {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func allSegmentsAvailable(r *model.RouteProbeResult) bool {
	for _, s := range r.Segments {
		if !s.Available {
			return false
		}
	}
	return true
}

// probeMirrorDirectionAndUpdateReachability 在应用某一方向后，用当前应用路径的逆向作为「智能翻转生成的」镜像路径做一次路由探测并更新其可达性配置
// - 应用 A→B（forward，币）：智能翻转生成 B→A 币链，镜像 path=[onchain:56, onchain:1, bitget]，symbol=base(POWER)
// - 应用 B→A（backward，币）：智能翻转生成 A→B 计价币链，镜像 path=[bitget, onchain:1, onchain:56]，symbol=quote(USDT)
func (d *Dashboard) probeMirrorDirectionAndUpdateReachability(triggerSymbol, triggerID, appliedDirection string, appliedBuiltNodes []pipeline.Node, appliedBridgeMgr *bridge.Manager) {
	mirrorDir := "backward"
	if appliedDirection == "backward" {
		mirrorDir = "forward"
	}
	if len(appliedBuiltNodes) < 2 {
		d.logger.Infof("Apply: 镜像方向 %s 路径节点不足，跳过探测", mirrorDir)
		return
	}
	// 镜像路径 = 当前应用路径的节点顺序取反（如 [bitget, onchain:1, onchain:56] -> [onchain:56, onchain:1, bitget]）
	mirrorPath := make([]string, len(appliedBuiltNodes))
	for i := range appliedBuiltNodes {
		mirrorPath[len(appliedBuiltNodes)-1-i] = appliedBuiltNodes[i].GetID()
	}
	// 探测的是智能翻转生成的那条：forward 生成 B→A 币 → 用 base；backward 生成 A→B USDT → 用 quote
	var symbol string
	if appliedDirection == "forward" {
		symbol = extractBaseSymbol(triggerSymbol)
	} else {
		symbol = d.getQuoteAssetForTrigger(triggerSymbol, triggerID)
	}
	probeReq := model.RouteProbeRequest{Path: mirrorPath, Symbol: symbol}
	result, err := pipeline.RouteProbe(&probeReq, appliedBridgeMgr)
	if err != nil {
		d.logger.Warnf("Apply: 镜像方向 %s 路由探测失败: %v", mirrorDir, err)
		return
	}
	d.verifyDepositWithdrawSegments([]*model.RouteProbeResult{result}, symbol)
	reachable := allSegmentsAvailable(result)
	reason := ""
	if !reachable {
		for _, s := range result.Segments {
			if !s.Available && s.RawInfo != nil {
				if r, ok := s.RawInfo["reason"].(string); ok && r != "" {
					reason = r
					break
				}
			}
		}
		if reason == "" {
			reason = "存在不可达段"
		}
	}
	key := pipelineConfigKey(triggerSymbol, triggerID)
	d.brickMovingPipelineMu.Lock()
	if d.brickMovingPipelines[key] == nil {
		d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
	}
	cfg2 := d.brickMovingPipelines[key]
	if cfg2 != nil {
		// 镜像探测结果写入「智能翻转」专用字段，不覆盖正常充提 A→B/B→A 的可达性
		if mirrorDir == "backward" {
			cfg2.MirrorForwardReachable = reachable
			cfg2.MirrorForwardReachableReason = reason
		} else {
			cfg2.MirrorBackwardReachable = reachable
			cfg2.MirrorBackwardReachableReason = reason
		}
	}
	d.brickMovingPipelineMu.Unlock()
	// 持久化必须在锁外调用：saveBrickMovingPipelines 内部会取 RLock，持 Lock 时调用会导致死锁
	if cfg2 != nil {
		d.saveBrickMovingPipelines()
	}
	d.logger.Infof("Apply: 镜像方向 %s 探测完成 path=%v reachable=%v reason=%q", mirrorDir, mirrorPath, reachable, reason)
}

// handleOpenPosition 套保开仓：同步执行「交易所 A 买现货」+「套保所做空」
// 逻辑：每次点击开仓，在 A 交易所买入 size 的现货，同时在套保配置选择的交易所做空 size 的合约，以对冲搬砖过程中的价格波动
func (d *Dashboard) handleOpenPosition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TriggerSymbol string `json:"triggerSymbol"`
		TriggerID     string `json:"triggerId"` // 同 symbol 多 trigger 时区分
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.TriggerSymbol == "" {
		writeJSONError(w, http.StatusBadRequest, "triggerSymbol is required")
		return
	}
	var tg proto.Trigger
	var err error
	if req.TriggerID != "" {
		if id, parseErr := strconv.ParseUint(req.TriggerID, 10, 64); parseErr == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, err = tm.GetTriggerByIDAsProto(id)
				if err == nil && tg != nil && tg.GetSymbol() != req.TriggerSymbol {
					tg = nil // ID 与 symbol 不一致则忽略
				}
			}
		}
	}
	if tg == nil {
		tg, err = d.triggerManager.GetTrigger(req.TriggerSymbol)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	hedgingKey := pipelineConfigKey(req.TriggerSymbol, req.TriggerID)
	// 在锁内拷贝套保配置的关键字段，避免释放锁后被并发修改
	var hedgingExchangeType, hedgingSymbol, openPositionSide string
	var hedgingPosition, hedgingPositionOpenSize, hedgingSize float64
	d.brickMovingHedgingMu.Lock()
	hedging := d.brickMovingHedgingCfgs[hedgingKey]
	if hedging == nil {
		hedging = &brickMovingHedgingConfig{
			ExchangeType:     "binance",
			HedgingSymbol:    tg.GetSymbol(),
			Size:             0,
			PositionOpenSize: 0,
			Enabled:          false,
			QuoteAsset:       "USDT",
			OpenPositionSide: "A",
		}
		traderB := tg.GetTraderBType()
		if idx := strings.Index(traderB, ":"); idx > 0 {
			hedging.ExchangeType = traderB[:idx]
		} else if traderB != "" && !pipeline.IsOnchainNodeID(traderB) {
			hedging.ExchangeType = traderB
		}
		d.brickMovingHedgingCfgs[hedgingKey] = hedging
	}
	hedgingExchangeType = hedging.ExchangeType
	hedgingSymbol = hedging.HedgingSymbol
	hedgingPosition = hedging.Position
	hedgingPositionOpenSize = hedging.PositionOpenSize
	hedgingSize = hedging.Size
	openPositionSide = hedging.OpenPositionSide
	if openPositionSide != "A" && openPositionSide != "B" {
		openPositionSide = "A"
	}
	d.brickMovingHedgingMu.Unlock()

	if hedgingPosition <= 0 {
		writeJSONError(w, http.StatusBadRequest, "总仓位大小未配置或为0，请先在套保配置区设置总仓位大小（Position）并点击保存配置")
		return
	}

	openSize := hedgingPositionOpenSize
	if openSize <= 0 {
		openSize = hedgingSize
	}
	if openSize <= 0 {
		writeJSONError(w, http.StatusBadRequest, "请先配置套保：在套保配置区填写 Size 或 开仓数量(positionOpenSize) 并点击更新")
		return
	}
	// 开仓使用同一 size：A 端买现货/链上与套保端做空数量必须一致，避免不均衡
	size := openSize
	symbolA := tg.GetSymbol()
	symbolHedging := hedgingSymbol
	if symbolHedging == "" {
		symbolHedging = symbolA
	}
	traderAType := tg.GetTraderAType()
	exchangeAType := traderAType
	if idx := strings.Index(traderAType, ":"); idx > 0 {
		exchangeAType = traderAType[:idx]
	}
	isAOnchain := exchangeAType == string(pipeline.NodeTypeOnchain) || pipeline.IsOnchainNodeID(traderAType)

	var exA exchange.Exchange
	if !isAOnchain {
		exA = d.getExchange(exchangeAType)
		if exA == nil && position.GetWalletManager() != nil {
			exA = position.GetWalletManager().GetExchange(exchangeAType)
		}
		if exA == nil {
			writeJSONError(w, http.StatusBadRequest, "交易所 A 未找到: "+exchangeAType+"，请检查 Trigger 配置")
			return
		}
	}

	exHedging := d.getExchange(hedgingExchangeType)
	if exHedging == nil && position.GetWalletManager() != nil {
		exHedging = position.GetWalletManager().GetExchange(hedgingExchangeType)
	}
	if exHedging == nil {
		writeJSONError(w, http.StatusBadRequest, "套保交易所未找到: "+hedgingExchangeType)
		return
	}

	// 套保所：做空合约（对冲买入端多头的价格风险），市价单不传 Price，Quantity 与买入端一致
	reqShortHedging := &model.PlaceOrderRequest{
		Symbol:       symbolHedging,
		Side:         model.OrderSideSell,
		Type:         model.OrderTypeMarket,
		Quantity:     size,
		MarketType:   model.MarketTypeFutures,
		PositionSide: model.PositionSideBoth,
	}

	// 开仓买入源为 B：B 端买入（现货或链上）+ 套保所做空
	if openPositionSide == "B" {
		traderBType := tg.GetTraderBType()
		exchangeBType := traderBType
		if idx := strings.Index(traderBType, ":"); idx > 0 {
			exchangeBType = traderBType[:idx]
		}
		isBOnchain := exchangeBType == string(pipeline.NodeTypeOnchain) || pipeline.IsOnchainNodeID(traderBType)
		if isBOnchain {
			bmt, ok := tg.(*trigger.BrickMovingTrigger)
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "当前 Trigger 不是搬砖 Trigger，无法执行链上开仓")
				return
			}
			amountStr := strconv.FormatFloat(size, 'f', 4, 64)
			if _, err := bmt.ExecuteSwapAndBroadcast(amountStr, "buy", "B"); err != nil {
				d.logger.Errorf("Open position (B): onchain B buy failed: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "B 端链上买入失败: "+err.Error())
				return
			}
			_, err = exHedging.PlaceOrder(reqShortHedging)
			if err != nil {
				d.logger.Errorf("[CRITICAL] Open position (B): B 端链上买入已成功 (%s USDT), 但套保做空失败: %v -- 尝试反向卖出 B 端", amountStr, err)
				rollbackHash, rollbackErr := bmt.ExecuteSwapAndBroadcast(amountStr, "sell", "B")
				if rollbackErr != nil {
					d.logger.Errorf("[CRITICAL] B 端链上反向卖出也失败: %v -- 当前存在无对冲裸多头！txHash=%s", rollbackErr, rollbackHash)
					writeJSONError(w, http.StatusInternalServerError,
						"[危险] B 端链上买入已成功，套保做空失败: "+err.Error()+"；B 端反向卖出也失败: "+rollbackErr.Error()+"。请立即手动处理！")
					return
				}
				d.logger.Warnf("Open position (B): 套保做空失败后成功链上反向卖出 B 端 %s, txHash=%s", amountStr, rollbackHash)
				writeJSONError(w, http.StatusInternalServerError,
					"套保所做空失败: "+err.Error()+"。已自动链上反向卖出 B 端持仓进行回滚，本次开仓取消。")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok",
				"msg":    "开仓已提交：B 端链上买入 " + amountStr + " USDT 代币，套保所做空 " + strconv.FormatFloat(size, 'f', -1, 64) + " 合约",
			})
			return
		}
		exB := d.getExchange(exchangeBType)
		if exB == nil && position.GetWalletManager() != nil {
			exB = position.GetWalletManager().GetExchange(exchangeBType)
		}
		if exB == nil {
			writeJSONError(w, http.StatusBadRequest, "交易所 B 未找到: "+exchangeBType+"，请检查 Trigger 配置")
			return
		}
		reqSpotB := &model.PlaceOrderRequest{
			Symbol:     symbolA,
			Side:       model.OrderSideBuy,
			Type:       model.OrderTypeMarket,
			Quantity:   size,
			MarketType: model.MarketTypeSpot,
		}
		_, err = exB.PlaceOrder(reqSpotB)
		if err != nil {
			d.logger.Errorf("Open position (B): exchange B spot buy failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "B 交易所现货买入失败: "+err.Error())
			return
		}
		_, err = exHedging.PlaceOrder(reqShortHedging)
		if err != nil {
			d.logger.Errorf("[CRITICAL] Open position (B): B 端现货买入已成功 (%.4f %s), 但套保做空失败: %v -- 尝试反向卖出 B 端", size, symbolA, err)
			rollbackReq := &model.PlaceOrderRequest{
				Symbol:     symbolA,
				Side:       model.OrderSideSell,
				Type:       model.OrderTypeMarket,
				Quantity:   size,
				MarketType: model.MarketTypeSpot,
			}
			if _, rollbackErr := exB.PlaceOrder(rollbackReq); rollbackErr != nil {
				d.logger.Errorf("[CRITICAL] B 端反向卖出也失败: %v", rollbackErr)
				writeJSONError(w, http.StatusInternalServerError,
					"[危险] B 端买入已成功，套保做空失败: "+err.Error()+"；B 端反向卖出也失败: "+rollbackErr.Error()+"。请立即手动处理！")
				return
			}
			d.logger.Warnf("Open position (B): 套保做空失败后成功反向卖出 B 端 %.4f %s", size, symbolA)
			writeJSONError(w, http.StatusInternalServerError,
				"套保所做空失败: "+err.Error()+"。已自动反向卖出 B 端持仓进行回滚，本次开仓取消。")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"msg":    "开仓已提交：B 交易所买入 " + strconv.FormatFloat(size, 'f', -1, 64) + " 现货，套保所做空 " + strconv.FormatFloat(size, 'f', -1, 64) + " 合约",
		})
		return
	}

	// 开仓买入源为 A：A 端买入（现货或链上）+ 套保所做空
	if isAOnchain {
		// A 端为链上：执行链上买入（USDT -> 代币），再套保所做空
		bmt, ok := tg.(*trigger.BrickMovingTrigger)
		if !ok {
			writeJSONError(w, http.StatusBadRequest, "当前 Trigger 不是搬砖 Trigger，无法执行链上开仓")
			return
		}
		amountStr := strconv.FormatFloat(size, 'f', 4, 64)
		// #region agent log
		writeFillsDebug("web_api_brick_moving.go:handleOpenPosition", "onchain open amount", "H1", map[string]interface{}{
			"triggerSymbol": req.TriggerSymbol, "hedgingSize": size, "amountStr": amountStr, "symbolA": symbolA,
		})
		// #endregion
		if err := bmt.OpenPositionOnchainA(amountStr); err != nil {
			d.logger.Errorf("Open position: onchain A buy failed: %v", err)
			writeJSONError(w, http.StatusInternalServerError, "A 端链上买入失败: "+err.Error())
			return
		}
		_, err = exHedging.PlaceOrder(reqShortHedging)
		if err != nil {
			d.logger.Errorf("[CRITICAL] Open position: A 端链上买入已成功 (%s USDT), 但套保做空失败: %v -- 尝试反向卖出链上持仓", amountStr, err)
			rollbackHash, rollbackErr := bmt.ExecuteSwapAndBroadcast(amountStr, "sell", "A")
			if rollbackErr != nil {
				d.logger.Errorf("[CRITICAL] 链上反向卖出也失败: %v -- 当前存在无对冲裸多头！txHash=%s", rollbackErr, rollbackHash)
				if d.wsHub != nil {
					if msg, e := json.Marshal(map[string]interface{}{
						"type":          "hedge_rollback_failed",
						"triggerSymbol": req.TriggerSymbol,
						"amount":        amountStr,
						"hedgeError":    err.Error(),
						"rollbackError": rollbackErr.Error(),
					}); e == nil {
						d.wsHub.Broadcast(msg)
					}
				}
				writeJSONError(w, http.StatusInternalServerError,
					"[危险] A 端链上买入已成功，套保做空失败: "+err.Error()+"；链上反向卖出也失败: "+rollbackErr.Error()+"。请立即手动处理！")
				return
			}
			d.logger.Warnf("Open position: 套保做空失败后成功链上反向卖出 A 端 %s, txHash=%s", amountStr, rollbackHash)
			writeJSONError(w, http.StatusInternalServerError,
				"套保所做空失败: "+err.Error()+"。已自动链上反向卖出 A 端持仓进行回滚，本次开仓取消。")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"msg":    "开仓已提交：A 端链上买入 " + amountStr + " USDT 代币，套保所做空 " + strconv.FormatFloat(size, 'f', -1, 64) + " 合约",
		})
		return
	}

	// A 端为交易所：现货买入 + 套保所做空
	reqSpotA := &model.PlaceOrderRequest{
		Symbol:     symbolA,
		Side:       model.OrderSideBuy,
		Type:       model.OrderTypeMarket,
		Quantity:   size,
		MarketType: model.MarketTypeSpot,
	}
	_, err = exA.PlaceOrder(reqSpotA)
	if err != nil {
		d.logger.Errorf("Open position: exchange A spot buy failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "A 交易所现货买入失败: "+err.Error())
		return
	}
	_, err = exHedging.PlaceOrder(reqShortHedging)
	if err != nil {
		// A 端现货买入已成功但套保失败，尝试反向卖出 A 端
		d.logger.Errorf("[CRITICAL] Open position: A 端现货买入已成功 (%.4f %s), 但套保做空失败: %v -- 尝试反向卖出 A 端", size, symbolA, err)
		rollbackReq := &model.PlaceOrderRequest{
			Symbol:     symbolA,
			Side:       model.OrderSideSell,
			Type:       model.OrderTypeMarket,
			Quantity:   size,
			MarketType: model.MarketTypeSpot,
		}
		if _, rollbackErr := exA.PlaceOrder(rollbackReq); rollbackErr != nil {
			d.logger.Errorf("[CRITICAL] 反向卖出也失败: %v -- 当前存在无对冲裸多头！", rollbackErr)
			writeJSONError(w, http.StatusInternalServerError,
				"[危险] A 端买入已成功，套保做空失败: "+err.Error()+"；反向卖出也失败: "+rollbackErr.Error()+"。请立即手动处理！")
			return
		}
		d.logger.Warnf("Open position: 套保做空失败后成功反向卖出 A 端 %.4f %s", size, symbolA)
		writeJSONError(w, http.StatusInternalServerError,
			"套保所做空失败: "+err.Error()+"。已自动反向卖出 A 端持仓进行回滚，本次开仓取消。")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"msg":    "开仓已提交：A 交易所买入 " + strconv.FormatFloat(size, 'f', -1, 64) + " 现货，套保所做空 " + strconv.FormatFloat(size, 'f', -1, 64) + " 合约",
	})
}

// handleClosePosition 套保平仓：先套保所市价买入（平空），再 A 端市价卖出；A 端失败不自动回滚、仅报错
func (d *Dashboard) handleClosePosition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TriggerSymbol string  `json:"triggerSymbol"`
		TriggerID     string  `json:"triggerId"`
		Size          float64 `json:"size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.TriggerSymbol == "" {
		writeJSONError(w, http.StatusBadRequest, "triggerSymbol is required")
		return
	}
	if req.Size <= 0 {
		writeJSONError(w, http.StatusBadRequest, "size is required and must be > 0")
		return
	}
	var tg proto.Trigger
	if req.TriggerID != "" {
		if id, parseErr := strconv.ParseUint(req.TriggerID, 10, 64); parseErr == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, _ = tm.GetTriggerByIDAsProto(id)
				if tg != nil && tg.GetSymbol() != req.TriggerSymbol {
					tg = nil
				}
			}
		}
	}
	if tg == nil {
		var err error
		tg, err = d.triggerManager.GetTrigger(req.TriggerSymbol)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	hedgingKey := pipelineConfigKey(req.TriggerSymbol, req.TriggerID)
	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[hedgingKey]
	d.brickMovingHedgingMu.RUnlock()
	if hedging == nil || hedging.ExchangeType == "" || hedging.HedgingSymbol == "" {
		writeJSONError(w, http.StatusBadRequest, "请先配置套保交易所与 hedgingSymbol")
		return
	}
	symbolHedging := hedging.HedgingSymbol
	if symbolHedging == "" {
		symbolHedging = tg.GetSymbol()
	}
	traderAType := tg.GetTraderAType()
	exchangeAType := traderAType
	if idx := strings.Index(traderAType, ":"); idx > 0 {
		exchangeAType = traderAType[:idx]
	}
	isAOnchain := exchangeAType == string(pipeline.NodeTypeOnchain) || pipeline.IsOnchainNodeID(traderAType)

	exHedging := d.getExchange(hedging.ExchangeType)
	if exHedging == nil && position.GetWalletManager() != nil {
		exHedging = position.GetWalletManager().GetExchange(hedging.ExchangeType)
	}
	if exHedging == nil {
		writeJSONError(w, http.StatusBadRequest, "套保交易所未找到: "+hedging.ExchangeType)
		return
	}

	// 1. 套保所：市价买入（平空）
	reqBuyHedging := &model.PlaceOrderRequest{
		Symbol:       symbolHedging,
		Side:         model.OrderSideBuy,
		Type:         model.OrderTypeMarket,
		Quantity:     req.Size,
		MarketType:   model.MarketTypeFutures,
		PositionSide: model.PositionSideBoth,
	}
	_, err := exHedging.PlaceOrder(reqBuyHedging)
	if err != nil {
		d.logger.Errorf("Close position: hedging buy (close short) failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "套保所平空失败: "+err.Error())
		return
	}

	// 2. A 端：市价卖出（现货或链上）；失败不自动回滚、仅记录并返回错误
	symbolA := tg.GetSymbol()
	if isAOnchain {
		bmt, ok := tg.(*trigger.BrickMovingTrigger)
		if !ok {
			d.logger.Errorf("Close position: trigger is not BrickMovingTrigger")
			writeJSONError(w, http.StatusInternalServerError, "当前 Trigger 不是搬砖 Trigger")
			return
		}
		amountStr := strconv.FormatFloat(req.Size, 'f', 4, 64)
		if _, errA := bmt.ExecuteSwapAndBroadcast(amountStr, "sell", "A"); errA != nil {
			d.logger.Errorf("Close position: A 端链上卖出失败: %v（套保所已平空）", errA)
			writeJSONError(w, http.StatusInternalServerError, "套保所平空已成功，但 A 端链上卖出失败: "+errA.Error())
			return
		}
	} else {
		exA := d.getExchange(exchangeAType)
		if exA == nil && position.GetWalletManager() != nil {
			exA = position.GetWalletManager().GetExchange(exchangeAType)
		}
		if exA == nil {
			d.logger.Errorf("Close position: exchange A not found: %s", exchangeAType)
			writeJSONError(w, http.StatusInternalServerError, "套保所平空已成功，但 A 交易所未找到: "+exchangeAType)
			return
		}
		reqSellA := &model.PlaceOrderRequest{
			Symbol:     symbolA,
			Side:       model.OrderSideSell,
			Type:       model.OrderTypeMarket,
			Quantity:   req.Size,
			MarketType: model.MarketTypeSpot,
		}
		if _, errA := exA.PlaceOrder(reqSellA); errA != nil {
			d.logger.Errorf("Close position: A 端现货卖出失败: %v（套保所已平空）", errA)
			writeJSONError(w, http.StatusInternalServerError, "套保所平空已成功，但 A 端现货卖出失败: "+errA.Error())
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"msg":    "平仓已提交：套保所平空 " + strconv.FormatFloat(req.Size, 'f', -1, 64) + "，A 端卖出 " + strconv.FormatFloat(req.Size, 'f', -1, 64),
	})
}

// handleBrickMovingTriggerSwap 直接执行链上 swap 并广播（不经过触发逻辑）
func (d *Dashboard) handleBrickMovingTriggerSwap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TriggerSymbol string `json:"triggerSymbol"`
		TriggerID     string `json:"triggerId"` // 同 symbol 多 trigger 时区分
		Amount        string `json:"amount"`
		Direction     string `json:"direction"` // "buy" | "sell"
		Source        string `json:"source"`    // "A" | "B"，默认 "A"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.TriggerSymbol == "" {
		writeJSONError(w, http.StatusBadRequest, "triggerSymbol is required")
		return
	}
	if req.Amount == "" {
		writeJSONError(w, http.StatusBadRequest, "amount is required")
		return
	}
	var tg proto.Trigger
	if req.TriggerID != "" {
		if id, err := strconv.ParseUint(req.TriggerID, 10, 64); err == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, err = tm.GetTriggerByIDAsProto(id)
				if err == nil && tg != nil && tg.GetSymbol() != req.TriggerSymbol {
					tg = nil
				}
			}
		}
	}
	if tg == nil {
		var err error
		tg, err = d.triggerManager.GetTrigger(req.TriggerSymbol)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	bmt, ok := tg.(*trigger.BrickMovingTrigger)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "当前 Trigger 不是搬砖 Trigger")
		return
	}
	direction := req.Direction
	if direction == "" {
		direction = "buy"
	}
	source := req.Source
	if source == "" {
		source = "A"
	}
	txHash, err := bmt.ExecuteSwapAndBroadcast(req.Amount, direction, source)
	if err != nil {
		d.logger.Errorf("Trigger swap failed: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "链上 Swap 失败: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"txHash": txHash,
		"msg":    "已提交链上 Swap 并广播，txHash: " + txHash,
	})
}

func toFloat64(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

// extractBaseSymbol 从交易对提取基础币种（如 SOMIUSDT -> SOMI）
func extractBaseSymbol(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return ""
	}
	upper := strings.ToUpper(symbol)
	for _, suffix := range []string{"USDT", "USDC", "BUSD", "BTC", "ETH"} {
		if strings.HasSuffix(upper, suffix) && len(symbol) > len(suffix) {
			return symbol[:len(symbol)-len(suffix)]
		}
	}
	return symbol
}

// getSpotBalancesFromWallet 从 WalletInfo 获取某交易所的现货余额（币数量、USDT数量）
func getSpotBalancesFromWallet(walletInfo *model.WalletDetailInfo, exchangeKey, baseSymbol string) (coinTotal, usdtTotal float64) {
	if walletInfo == nil || walletInfo.ExchangeWallets == nil {
		return 0, 0
	}
	var balances map[string]*model.Balance
	for k, w := range walletInfo.ExchangeWallets {
		if w == nil {
			continue
		}
		if k == exchangeKey || strings.HasPrefix(k, exchangeKey+":") {
			if w.SpotBalances != nil {
				balances = w.SpotBalances
			} else if w.AccountBalances != nil {
				balances = w.AccountBalances
			}
			break
		}
	}
	if balances == nil {
		return 0, 0
	}
	if b := balances[baseSymbol]; b != nil {
		coinTotal = b.Total
		if coinTotal <= 0 {
			coinTotal = b.Available
		}
	}
	if b := balances["USDT"]; b != nil {
		usdtTotal = b.Total
		if usdtTotal <= 0 {
			usdtTotal = b.Available
		}
	}
	return coinTotal, usdtTotal
}

// chainIDToGasToken 常见链的 Gas 代币
var chainIDToGasToken = map[string]string{
	"1":        "ETH",   // Ethereum
	"56":       "BNB",   // BSC
	"137":      "MATIC", // Polygon (POL 也常用，兼容 MATIC)
	"43114":    "AVAX",  // Avalanche
	"42161":    "ETH",   // Arbitrum
	"10":       "ETH",   // Optimism
	"8453":     "ETH",   // Base
	"25":       "CRO",   // Cronos
	"250":      "FTM",   // Fantom
	"324":      "ETH",   // zkSync Era
	"42170":    "ETH",   // Arbitrum Nova
	"11155111": "ETH",   // Sepolia testnet
}

// chainIDToName 链 ID 到人类可读名称的映射
var chainIDToName = map[string]string{
	"1":     "ETH",
	"56":    "BSC",
	"137":   "Polygon",
	"43114": "AVAX",
	"42161": "Arbitrum",
	"10":    "Optimism",
	"8453":  "Base",
	"324":   "zkSync",
}

// fillSegmentEdgeLabels 根据段类型、BridgeProtocol、WithdrawNetworkChainID 与 FromNodeID/ToNodeID 为每段填充 EdgeLabel，供前端在边上括号内展示。
func fillSegmentEdgeLabels(segments []model.SegmentProbeResult) {
	for i := range segments {
		seg := &segments[i]
		switch seg.Type {
		case model.SegmentTypeBridge:
			seg.EdgeLabel = bridgeProtocolDisplayName(seg.BridgeProtocol)
		case model.SegmentTypeDeposit:
			// 链→交易所：充值的链名称
			chainID := pipeline.ChainIDFromNodeID(seg.FromNodeID)
			seg.EdgeLabel = chainIDToName[chainID]
			if seg.EdgeLabel == "" && chainID != "" {
				seg.EdgeLabel = chainID
			}
		case model.SegmentTypeWithdraw:
			// 交易所→链：提币的链名称
			chainID := pipeline.ChainIDFromNodeID(seg.ToNodeID)
			seg.EdgeLabel = chainIDToName[chainID]
			if seg.EdgeLabel == "" && chainID != "" {
				seg.EdgeLabel = chainID
			}
		case model.SegmentTypeExchangeToExchange:
			// 交易所→交易所：提现/提充的链名称（由 merge 写入 WithdrawNetworkChainID；若为空则兜底展示）
			chainID := seg.WithdrawNetworkChainID
			seg.EdgeLabel = chainIDToName[chainID]
			if seg.EdgeLabel == "" && chainID != "" {
				seg.EdgeLabel = chainID
			}
			if seg.EdgeLabel == "" {
				seg.EdgeLabel = "提现链"
			}
			// #region agent log
			debugLogBrickMoving("fillSegmentEdgeLabels:ex2ex", "exchange_to_exchange edge label", map[string]interface{}{
				"segmentIndex": i, "fromNodeID": seg.FromNodeID, "toNodeID": seg.ToNodeID,
				"withdrawNetworkChainID": chainID, "chainNameFromMap": chainIDToName[chainID], "finalEdgeLabel": seg.EdgeLabel,
			}, "H-ex2ex-label")
			// #endregion
		default:
			if seg.EdgeLabel == "" && seg.BridgeProtocol != "" {
				seg.EdgeLabel = bridgeProtocolDisplayName(seg.BridgeProtocol)
			}
		}
	}
}

// bridgeProtocolDisplayName 将协议 ID 转为展示名（如 layerzero → LayerZero）
func bridgeProtocolDisplayName(protocol string) string {
	if protocol == "" {
		return ""
	}
	return strings.ToUpper(protocol[:1]) + strings.ToLower(protocol[1:])
}

// chainIDAliases 链 ID 别名，用于兼容 API 返回的不同格式
var chainIDAliases = map[string][]string{
	"56":  {"56", "bsc"},
	"1":   {"1", "eth", "ethereum"},
	"137": {"137", "polygon", "matic"},
}

// chainMatches 判断 iterChain 是否匹配 targetChainID（含别名）
func chainMatches(iterChain, targetChainID string) bool {
	if iterChain == targetChainID || strings.EqualFold(iterChain, targetChainID) {
		return true
	}
	tryKeys := []string{targetChainID}
	if aliases, ok := chainIDAliases[targetChainID]; ok {
		tryKeys = append(tryKeys, aliases...)
	}
	for _, k := range tryKeys {
		if iterChain == k || strings.EqualFold(iterChain, k) {
			return true
		}
	}
	return false
}

// getOnchainGasBalance 从 WalletInfo 获取指定链的 Gas 代币余额，返回 (代币符号, 余额)
// 优先从 WalletInfo 缓存获取，如果缓存中余额为 0，回退通过 RPC eth_getBalance 查询原生 Gas 余额
func getOnchainGasBalance(walletInfo *model.WalletDetailInfo, chainID string) (token string, balance float64) {
	token = chainIDToGasToken[chainID]
	if token == "" {
		token = "ETH"
	}
	if chainID == "" {
		return token, 0
	}

	// 1. 优先从 WalletInfo 缓存获取
	if walletInfo != nil && walletInfo.OnchainBalances != nil {
		tryKeys := []string{chainID}
		if aliases, ok := chainIDAliases[chainID]; ok && len(aliases) > 1 {
			tryKeys = append(tryKeys, aliases[1:]...)
		}
		for iterChain, symbolMap := range walletInfo.OnchainBalances {
			if symbolMap == nil {
				continue
			}
			matched := iterChain == chainID
			if !matched {
				for _, k := range tryKeys {
					if iterChain == k || strings.EqualFold(iterChain, k) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
			for _, sym := range []string{token, "BNB", "POL", "MATIC", "WBNB", "WETH", "AVAX", "FTM"} {
				if a, ok := symbolMap[sym]; ok && a.Balance != "" {
					if b, err := strconv.ParseFloat(a.Balance, 64); err == nil && b > 0 {
						if sym == "WBNB" {
							return "BNB", b
						}
						if sym == "WETH" {
							return "ETH", b
						}
						return sym, b
					}
				}
			}
		}
	}

	// 2. 缓存中余额为 0 或未找到，回退通过 RPC eth_getBalance 查询原生 Gas 余额
	rpcBalance := queryNativeGasBalanceViaRPC(chainID)
	if rpcBalance > 0 {
		return token, rpcBalance
	}
	return token, 0
}

// queryNativeGasBalanceViaRPC 通过 RPC 查询指定链的原生 Gas 余额（ETH/BNB 等）
func queryNativeGasBalanceViaRPC(chainID string) float64 {
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil || globalCfg.Wallet.WalletAddress == "" {
		return 0
	}
	walletAddr := globalCfg.Wallet.WalletAddress

	// 获取 RPC URL
	var rpcURL string
	if globalCfg.Bridge.LayerZero.RPCURLs != nil {
		if url, ok := globalCfg.Bridge.LayerZero.RPCURLs[chainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(chainID)
	}
	if rpcURL == "" {
		return 0
	}

	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return 0
	}
	defer client.Close()

	addr := common.HexToAddress(walletAddr)
	bal, err := client.BalanceAt(context.Background(), addr, nil)
	if err != nil || bal == nil {
		return 0
	}
	// 将 wei 转换为 ETH/BNB 等（除以 10^18）
	fbal := new(big.Float).SetInt(bal)
	divisor := new(big.Float).SetFloat64(1e18)
	fbal.Quo(fbal, divisor)
	result, _ := fbal.Float64()
	return result
}

// getOnchainUsdtBalance 从 WalletInfo 获取指定链的 USDT 余额
func getOnchainUsdtBalance(walletInfo *model.WalletDetailInfo, chainID string) float64 {
	if walletInfo == nil || walletInfo.OnchainBalances == nil || chainID == "" {
		return 0
	}
	// 尝试 chainID 及别名匹配
	tryKeys := []string{chainID}
	if aliases, ok := chainIDAliases[chainID]; ok && len(aliases) > 1 {
		tryKeys = append(tryKeys, aliases[1:]...)
	}
	// 遍历所有链，匹配 chainID
	for iterChain, symbolMap := range walletInfo.OnchainBalances {
		if symbolMap == nil {
			continue
		}
		matched := iterChain == chainID
		if !matched {
			for _, k := range tryKeys {
				if iterChain == k || strings.EqualFold(iterChain, k) {
					matched = true
					break
				}
			}
		}
		if !matched {
			continue
		}
		// 查找 USDT 余额
		if asset, ok := symbolMap["USDT"]; ok && asset.Balance != "" {
			if balance, err := strconv.ParseFloat(asset.Balance, 64); err == nil && balance >= 0 {
				return balance
			}
		}
		return 0
	}
	return 0
}

// getOnchainTokenBalance 从 WalletInfo 获取指定链上指定代币的余额
// 优先从 WalletInfo 缓存获取，如果余额为 0，回退通过 RPC balanceOf 查询 ERC20 余额
func getOnchainTokenBalance(walletInfo *model.WalletDetailInfo, chainID, tokenSymbol string) float64 {
	if chainID == "" || tokenSymbol == "" {
		return 0
	}

	// 1. 优先从 WalletInfo 缓存获取
	if walletInfo != nil && walletInfo.OnchainBalances != nil {
		tryKeys := []string{chainID}
		if aliases, ok := chainIDAliases[chainID]; ok && len(aliases) > 1 {
			tryKeys = append(tryKeys, aliases[1:]...)
		}
		for iterChain, symbolMap := range walletInfo.OnchainBalances {
			if symbolMap == nil {
				continue
			}
			matched := iterChain == chainID
			if !matched {
				for _, k := range tryKeys {
					if iterChain == k || strings.EqualFold(iterChain, k) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
			for sym, asset := range symbolMap {
				if strings.EqualFold(sym, tokenSymbol) {
					if balance, err := strconv.ParseFloat(asset.Balance, 64); err == nil && balance > 0 {
						return balance
					}
				}
			}
		}
	}

	// 2. 未找到余额，返回 0（调用方可用 OFT registry + queryERC20BalanceViaRPCWithAddress 回退）
	return 0
}

// rpcTimeout is the default timeout for on-chain RPC calls in HTTP handlers
const rpcTimeout = 15 * time.Second

// queryERC20BalanceViaRPCWithAddress 通过 RPC 查询指定链上指定合约地址的 ERC20 余额
func queryERC20BalanceViaRPCWithAddress(chainID, tokenAddress string) float64 {
	globalCfg := config.GetGlobalConfig()
	if globalCfg == nil || globalCfg.Wallet.WalletAddress == "" || tokenAddress == "" {
		return 0
	}
	walletAddr := globalCfg.Wallet.WalletAddress

	var rpcURL string
	if globalCfg.Bridge.LayerZero.RPCURLs != nil {
		if url, ok := globalCfg.Bridge.LayerZero.RPCURLs[chainID]; ok && url != "" {
			rpcURL = url
		}
	}
	if rpcURL == "" {
		rpcURL = constants.GetDefaultRPCURL(chainID)
	}
	if rpcURL == "" {
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return 0
	}
	defer client.Close()

	contractAddr := common.HexToAddress(tokenAddress)
	ownerAddr := common.HexToAddress(walletAddr)

	code, codeCheckErr := client.CodeAt(ctx, contractAddr, nil)
	if codeCheckErr == nil && len(code) == 0 {
		return 0
	}

	decimals := 18
	decimalsData := common.FromHex("0x313ce567")
	decimalsResult, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &contractAddr,
		Data: decimalsData,
	}, nil)
	if err == nil && len(decimalsResult) >= 32 {
		decimals = int(new(big.Int).SetBytes(decimalsResult[24:32]).Uint64())
	}

	balanceOfABI, err := abi.JSON(strings.NewReader(`[{"constant":true,"inputs":[{"name":"_owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"balance","type":"uint256"}],"type":"function"}]`))
	if err != nil {
		return 0
	}
	balanceData, err := balanceOfABI.Pack("balanceOf", ownerAddr)
	if err != nil {
		return 0
	}
	balanceResult, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &contractAddr,
		Data: balanceData,
	}, nil)
	if err != nil || len(balanceResult) < 32 {
		return 0
	}

	rawBalance := new(big.Int).SetBytes(balanceResult)
	if rawBalance.Sign() <= 0 {
		return 0
	}

	// 转换为人类可读格式
	divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	fBalance := new(big.Float).SetInt(rawBalance)
	fDivisor := new(big.Float).SetInt(divisor)
	fBalance.Quo(fBalance, fDivisor)
	result, _ := fBalance.Float64()
	return result
}

// getFuturesUsdtFromWallet 从 WalletInfo 获取某交易所合约账户的 USDT 余额
// 注意：只从 FuturesBalances 获取，不回退到 AccountBalances（避免获取到现货余额）
func getFuturesUsdtFromWallet(walletInfo *model.WalletDetailInfo, exchangeKey string) float64 {
	if walletInfo == nil || walletInfo.ExchangeWallets == nil {
		return 0
	}
	for k, w := range walletInfo.ExchangeWallets {
		if w == nil {
			continue
		}
		if k != exchangeKey && !strings.HasPrefix(k, exchangeKey+":") {
			continue
		}
		// 只使用 FuturesBalances，不回退到 AccountBalances（AccountBalances 可能包含现货余额）
		if w.FuturesBalances != nil {
			if b := w.FuturesBalances["USDT"]; b != nil {
				if b.Total > 0 {
					return b.Total
				}
				return b.Available
			}
		}
		break
	}
	return 0
}

// handleGetHedgingPosition 返回套保仓位详情
// 支持 triggerId：同 symbol 多 trigger 时按 triggerId 取该 trigger 的套保配置与仓位，避免串数据
func (d *Dashboard) handleGetHedgingPosition(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}
	baseSymbol := extractBaseSymbol(triggerSymbol)
	emptyResp := func() {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"triggerSymbol":         triggerSymbol,
			"margin":                "",
			"maxProfit":             "",
			"riskParams":            map[string]interface{}{},
			"positions":             []interface{}{},
			"hedgingPosition":       float64(0),
			"hedgingPositionUsdt":   float64(0),
			"aPositionCoin":         float64(0),
			"aPositionUsdt":         float64(0),
			"aPositionGasToken":     "",
			"aPositionGasBalance":   float64(0),
			"bPositionCoin":         float64(0),
			"bPositionUsdt":         float64(0),
			"bPositionGasToken":     "",
			"bPositionGasBalance":   float64(0),
			"onchainGasChain":       "",
			"onchainGasToken":       "",
			"onchainGasBalance":     float64(0),
			"intermediatePositions": []interface{}{},
			"spreadOpenPct":         nil,
			"spreadClosePct":        nil,
		})
	}
	pm := position.GetPositionManager()
	if pm == nil {
		emptyResp()
		return
	}
	walletInfo := position.GetWalletManager()
	var wi *model.WalletDetailInfo
	if walletInfo != nil {
		wi = walletInfo.GetWalletInfo()
	}
	summary := pm.GetSymbolPositionSummary(triggerSymbol)
	out := map[string]interface{}{
		"triggerSymbol":       triggerSymbol,
		"margin":              "",
		"maxProfit":           "",
		"riskParams":          map[string]interface{}{},
		"positions":           []interface{}{},
		"onchain":             []interface{}{},
		"hedgingPosition":     float64(0),
		"hedgingPositionUsdt": float64(0),
		"aPositionCoin":       float64(0),
		"aPositionUsdt":       float64(0),
		"aPositionGasToken":   "",
		"aPositionGasBalance": float64(0),
		"bPositionCoin":       float64(0),
		"bPositionUsdt":       float64(0),
		"bPositionGasToken":   "",
		"bPositionGasBalance": float64(0),
		"onchainGasChain":     "",
		"onchainGasToken":     "",
		"onchainGasBalance":   float64(0),
	}
	if summary != nil {
		out["positions"] = summary.ExchangePositions
		out["onchain"] = summary.OnchainBalances
	}
	var tg proto.Trigger
	if triggerIDStr != "" {
		if id, err := strconv.ParseUint(triggerIDStr, 10, 64); err == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, _ = tm.GetTriggerByIDAsProto(id)
			}
		}
	}
	if tg == nil {
		tg, _ = d.triggerManager.GetTrigger(triggerSymbol)
	}
	hedgingKey := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[hedgingKey]
	d.brickMovingHedgingMu.RUnlock()
	exA := ""
	exB := ""
	exHedging := ""
	chainA := ""
	chainB := ""
	if tg != nil {
		traderA := tg.GetTraderAType()
		traderB := tg.GetTraderBType()
		if idx := strings.Index(traderA, ":"); idx > 0 {
			if pipeline.IsOnchainNodeID(traderA) {
				exA = string(pipeline.NodeTypeOnchain)
				chainA = traderA[idx+1:]
			} else {
				exA = traderA[:idx]
			}
		} else if traderA != "" && !pipeline.IsOnchainNodeID(traderA) {
			exA = traderA
		}
		if idx := strings.Index(traderB, ":"); idx > 0 {
			if pipeline.IsOnchainNodeID(traderB) {
				exB = string(pipeline.NodeTypeOnchain)
				chainB = traderB[idx+1:]
			} else {
				exB = traderB[:idx]
			}
		} else if traderB != "" {
			if pipeline.IsOnchainNodeID(traderB) {
				exB = string(pipeline.NodeTypeOnchain)
				if idx := strings.Index(traderB, ":"); idx > 0 {
					chainB = traderB[idx+1:]
				}
			} else {
				exB = traderB
			}
		}
	}
	if hedging != nil && hedging.ExchangeType != "" {
		exHedging = hedging.ExchangeType
	}
	// 当 trader 类型为 onchain 但未带链 ID 时，用 GetChainId() 补充（用户可能只配置了 chain:56）
	chainFromTrigger := ""
	if tg != nil {
		chainFromTrigger = tg.GetChainId()
	}
	if exA == string(pipeline.NodeTypeOnchain) && chainA == "" && chainFromTrigger != "" {
		chainA = chainFromTrigger
	}
	if exB == string(pipeline.NodeTypeOnchain) && chainB == "" && chainFromTrigger != "" {
		chainB = chainFromTrigger
	}
	// 套保持仓：合约开仓数量（支持 LONG 和 SHORT）+ 合约账户 USDT 余额
	if summary != nil {
		// 如果有 hedging 配置，优先显示配置的交易所持仓
		if exHedging != "" {
			for _, p := range summary.ExchangePositions {
				et := p.ExchangeType
				if idx := strings.Index(et, ":"); idx > 0 {
					et = et[:idx]
				}
				if et == exHedging {
					// 获取持仓数量（Long 为正，Short 为负，但显示时取绝对值）
					size := p.Size
					if size < 0 {
						size = -size
					}
					// 支持 LONG 和 SHORT 两种方向
					if p.Side == "LONG" || p.Side == "SHORT" {
						out["hedgingPosition"] = size
						// 如果找到匹配的持仓，继续查找 USDT 余额
						break
					}
				}
			}
		} else {
			// 如果没有 hedging 配置，按交易所名称排序后取第一个合约持仓，避免 map 遍历顺序随机导致 7000/300 来回闪
			positions := make([]*position.ExchangePositionDetail, 0, len(summary.ExchangePositions))
			for _, p := range summary.ExchangePositions {
				if p != nil && (p.Side == "LONG" || p.Side == "SHORT") {
					positions = append(positions, p)
				}
			}
			if len(positions) > 0 {
				sort.Slice(positions, func(i, j int) bool {
					eti := positions[i].ExchangeType
					if idx := strings.Index(eti, ":"); idx > 0 {
						eti = eti[:idx]
					}
					etj := positions[j].ExchangeType
					if idx := strings.Index(etj, ":"); idx > 0 {
						etj = etj[:idx]
					}
					return eti < etj
				})
				p := positions[0]
				size := p.Size
				if size < 0 {
					size = -size
				}
				out["hedgingPosition"] = size
				et := p.ExchangeType
				if idx := strings.Index(et, ":"); idx > 0 {
					et = et[:idx]
				}
				if et != "" && wi != nil {
					out["hedgingPositionUsdt"] = getFuturesUsdtFromWallet(wi, et)
				}
			}
		}
	}
	// 获取合约账户的 USDT 余额（如果有 hedging 配置，显示配置的交易所余额）
	if exHedging != "" && wi != nil {
		// 如果还没有设置 USDT 余额，尝试获取
		if out["hedgingPositionUsdt"] == nil || toFloat64(out["hedgingPositionUsdt"]) == 0 {
			out["hedgingPositionUsdt"] = getFuturesUsdtFromWallet(wi, exHedging)
		}
	}
	// A持仓、B持仓：现货 币数量 + USDT数量；链上时额外展示 Gas 代币
	// 注意：summary.OnchainBalances 仅包含有 baseSymbol 的链；链 ID 需用 chainMatches 做别名匹配（如 56 vs bsc）
	if exA == string(pipeline.NodeTypeOnchain) && summary != nil {
		var totalCoin, totalValue float64
		for _, o := range summary.OnchainBalances {
			if chainA != "" && !chainMatches(o.ChainIndex, chainA) {
				continue // 只统计 A 链的余额
			}
			totalCoin += o.Balance
			totalValue += o.Value
		}
		out["aPositionCoin"] = totalCoin
		// 对于链上节点，显示基础币种的价值（已转换为 USDT）
		out["aPositionUsdt"] = totalValue
		// 同时获取该链上的 USDT 余额（如果有）
		if chainA != "" && wi != nil {
			usdtBalance := getOnchainUsdtBalance(wi, chainA)
			if usdtBalance > 0 {
				// 如果有 USDT 余额，添加到显示值中（或者单独显示，这里合并显示）
				out["aPositionUsdt"] = totalValue + usdtBalance
			}
			gasToken, gasBal := getOnchainGasBalance(wi, chainA)
			out["aPositionGasToken"] = gasToken
			out["aPositionGasBalance"] = gasBal
		}
	} else if exA != "" && wi != nil {
		coin, usdt := getSpotBalancesFromWallet(wi, exA, baseSymbol)
		out["aPositionCoin"] = coin
		out["aPositionUsdt"] = usdt
	}
	// B 为 onchain 时：优先从 summary 取（若该链在 summary 中），否则直接从 walletInfo 取，避免「链有 USDT 无 base 币」时显示 0
	if exB == string(pipeline.NodeTypeOnchain) {
		var totalCoin, totalValue float64
		if summary != nil {
			for _, o := range summary.OnchainBalances {
				if chainB != "" && !chainMatches(o.ChainIndex, chainB) {
					continue // 只统计 B 链的余额
				}
				totalCoin += o.Balance
				totalValue += o.Value
			}
		}
		out["bPositionCoin"] = totalCoin
		// 直接从 walletInfo 取 USDT，不依赖 summary（summary 仅含 base 币，无 base 时链会缺失）
		if chainB != "" && wi != nil {
			usdtBalance := getOnchainUsdtBalance(wi, chainB)
			out["bPositionUsdt"] = usdtBalance
			// 若 summary 中该链无 base 币记录，则 base 币也从 walletInfo 补充（如仅配了链、钱包有币但 summary 未收录）
			if totalCoin == 0 && baseSymbol != "" {
				out["bPositionCoin"] = getOnchainTokenBalance(wi, chainB, baseSymbol)
			}
			gasToken, gasBal := getOnchainGasBalance(wi, chainB)
			out["bPositionGasToken"] = gasToken
			out["bPositionGasBalance"] = gasBal
		} else {
			out["bPositionUsdt"] = totalValue
		}
	} else if exB != "" && wi != nil {
		coin, usdt := getSpotBalancesFromWallet(wi, exB, baseSymbol)
		out["bPositionCoin"] = coin
		out["bPositionUsdt"] = usdt
	}
	// 当 Trigger 配置了 chainId 且 A/B 均未展示该链 Gas 时，单独展示（如 pipeline 用到的链）
	if chainFromTrigger != "" && wi != nil {
		aHasGas := (out["aPositionGasToken"] != nil && out["aPositionGasToken"] != "") || (out["aPositionGasBalance"] != nil && toFloat64(out["aPositionGasBalance"]) > 0)
		bHasGas := (out["bPositionGasToken"] != nil && out["bPositionGasToken"] != "") || (out["bPositionGasBalance"] != nil && toFloat64(out["bPositionGasBalance"]) > 0)
		if !aHasGas && !bHasGas {
			gasToken, gasBal := getOnchainGasBalance(wi, chainFromTrigger)
			out["onchainGasChain"] = chainFromTrigger
			out["onchainGasToken"] = gasToken
			if gasToken == "" {
				out["onchainGasToken"] = chainIDToGasToken[chainFromTrigger]
				if out["onchainGasToken"] == "" {
					out["onchainGasToken"] = "ETH"
				}
			}
			out["onchainGasBalance"] = gasBal
		}
	}
	// ===== 中间链持仓：从实际 Pipeline 对象中提取中间 onchain 节点，查询其代币余额 =====
	// 优先从 PipelineManager 获取实际的 Pipeline（比内存配置更可靠）
	intermediatePositions := make([]map[string]interface{}, 0)
	if wi != nil {
		addedChains := make(map[string]bool)
		// 标记 A/B 链，避免重复展示
		if chainA != "" {
			addedChains[chainA] = true
		}
		if chainB != "" {
			addedChains[chainB] = true
		}
		pm := pipeline.GetPipelineManager()
		d.brickMovingPipelineMu.RLock()
		pipeCfg := d.brickMovingPipelines[hedgingKey] // 按 trigger 区分，与 apply/save 使用的 key 一致
		d.brickMovingPipelineMu.RUnlock()
		// 收集所有可能的 pipeline ID（forward + backward）
		var pipelineIDs []string
		if pipeCfg != nil {
			if pipeCfg.ActiveForwardPipelineId != "" {
				pipelineIDs = append(pipelineIDs, pipeCfg.ActiveForwardPipelineId)
			}
			if pipeCfg.ActiveBackwardPipelineId != "" {
				pipelineIDs = append(pipelineIDs, pipeCfg.ActiveBackwardPipelineId)
			}
		}
		for _, pid := range pipelineIDs {
			p, pErr := pm.GetPipeline(pid)
			if pErr != nil {
				continue
			}
			nodes := p.Nodes()
			if len(nodes) <= 2 {
				continue // 只有两个节点，没有中间链
			}
			for i := 1; i < len(nodes)-1; i++ { // 跳过首尾节点
				node := nodes[i]
				if node.GetType() != pipeline.NodeTypeOnchain {
					continue
				}
				midChainID := pipeline.ChainIDFromNodeID(node.GetID())
				if midChainID == "" || addedChains[midChainID] {
					continue
				}
				addedChains[midChainID] = true
				coin := getOnchainTokenBalance(wi, midChainID, baseSymbol)
				// 如果缓存余额为 0，直接从 Pipeline 节点的 TokenAddress 通过 RPC 查询
				if coin <= 0 {
					onNode, castOk := node.(*pipeline.OnchainNode)
					tokenAddr := ""
					if castOk {
						tokenAddr = onNode.GetTokenAddress()
					}
					if castOk && tokenAddr != "" {
						coin = queryERC20BalanceViaRPCWithAddress(midChainID, tokenAddr)
					}
				}
				// 仍然为 0，尝试 OFT 注册表
				if coin <= 0 {
					reg := d.getOrCreateBridgeOFTRegistry()
					if reg != nil {
						if t, ok := reg.Get(midChainID, baseSymbol); ok && t.Address != "" {
							coin = queryERC20BalanceViaRPCWithAddress(midChainID, t.Address)
						}
					}
				}
				gasToken, gasBal := getOnchainGasBalance(wi, midChainID)
				chainName := chainIDToName[midChainID]
				if chainName == "" {
					chainName = midChainID
				}
				intermediatePositions = append(intermediatePositions, map[string]interface{}{
					"chainId":    midChainID,
					"chainName":  chainName,
					"coin":       coin,
					"gasToken":   gasToken,
					"gasBalance": gasBal,
				})
			}
		}
		// 回退：如果没有活跃的 pipeline，尝试从内存配置的节点列表提取
		if len(intermediatePositions) == 0 && pipeCfg != nil {
			for _, nodeList := range [][]map[string]interface{}{pipeCfg.ForwardNodes, pipeCfg.BackwardNodes} {
				if len(nodeList) <= 2 {
					continue
				}
				for i := 1; i < len(nodeList)-1; i++ {
					nodeID, _ := nodeList[i]["id"].(string)
					if nodeID == "" {
						continue
					}
					midChainID := pipeline.ChainIDFromNodeID(nodeID)
					if midChainID == "" || addedChains[midChainID] {
						continue
					}
					addedChains[midChainID] = true
					coin := getOnchainTokenBalance(wi, midChainID, baseSymbol)
					if coin <= 0 {
						reg := d.getOrCreateBridgeOFTRegistry()
						if reg != nil {
							if t, ok := reg.Get(midChainID, baseSymbol); ok && t.Address != "" {
								coin = queryERC20BalanceViaRPCWithAddress(midChainID, t.Address)
							}
						}
					}
					gasToken, gasBal := getOnchainGasBalance(wi, midChainID)
					chainName := chainIDToName[midChainID]
					if chainName == "" {
						chainName = midChainID
					}
					intermediatePositions = append(intermediatePositions, map[string]interface{}{
						"chainId":    midChainID,
						"chainName":  chainName,
						"coin":       coin,
						"gasToken":   gasToken,
						"gasBalance": gasBal,
					})
				}
			}
		}
	}
	out["intermediatePositions"] = intermediatePositions

	// 开仓价差、平仓价差：直接复用 trigger 的 directionAB/directionBA，套保价从 TriggerManager 取
	var spreadOpenPct, spreadClosePct interface{} = nil, nil
	if bmt, ok := tg.(*trigger.BrickMovingTrigger); ok && hedging != nil && exHedging != "" {
		ab, ba := bmt.GetPriceData()
		if ab != nil && ba != nil {
			aAsk := ab.PriceData.AskPrice
			aBid := ba.PriceData.BidPrice
			bAsk := ba.PriceData.AskPrice
			// bBid := ab.PriceData.BidPrice
			hedgeBid, hedgeAsk := float64(0), float64(0)
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				hedgeBid, hedgeAsk = tm.GetHedgingPrice(triggerSymbol, triggerIDStr, exHedging)
			}
			openSide := "A"
			if hedging.OpenPositionSide == "B" {
				openSide = "B"
			}
			var buySourceAsk float64
			if openSide == "A" {
				buySourceAsk = aAsk
			} else {
				buySourceAsk = bAsk
			}
			if buySourceAsk > 0 && hedgeBid > 0 {
				spreadOpenPct = (hedgeBid - buySourceAsk) / buySourceAsk * 100
			}
			if aBid > 0 && hedgeAsk > 0 {
				spreadClosePct = (aBid - hedgeAsk) / aBid * 100
			}
		}
	}
	out["spreadOpenPct"] = spreadOpenPct
	out["spreadClosePct"] = spreadClosePct

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func writeFillsDebug(location, message, hypothesisId string, data map[string]interface{}) {}

// handleGetTriggerFills 返回 Trigger 最近成交记录（从 StatisticsManager 读取，供详情页展示）
func (d *Dashboard) handleGetTriggerFills(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	fills := make([]map[string]interface{}, 0)
	sm := statistics.GetStatisticsManager()
	for _, t := range sm.GetRecentTradeRecords(triggerSymbol, limit) {
		fills = append(fills, map[string]interface{}{
			"timestamp":    t.Timestamp.Unix(),
			"direction":    t.Direction,
			"size":         t.Size,
			"sizeUSDT":     t.SizeUSDT,
			"price":        t.Price,
			"diffValue":    t.DiffValue,
			"profit":       t.Profit,
			"costInCoin":   t.CostInCoin,
			"costPercent":  t.CostPercent,
			"filledQtyA":   t.FilledQtyA,
			"filledQtyB":   t.FilledQtyB,
			"filledPriceA": t.FilledPriceA,
			"filledPriceB": t.FilledPriceB,
			"feeA":         t.FeeA,
			"feeB":         t.FeeB,
			"gasA":         t.GasA,
			"gasB":         t.GasB,
			"revenueA":     t.RevenueA,
			"costA":        t.CostA,
			"revenueB":     t.RevenueB,
			"costB":        t.CostB,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"triggerSymbol": triggerSymbol,
		"fills":         fills,
		"limit":         limit,
	})
}

// handleBrickMovingWithdrawHistory 返回搬砖相关的提币记录（从 pipeline 交易所聚合）
// 支持 triggerId：同 symbol 多 trigger 时按 triggerId 返回该 trigger 的 pipeline 与记录，互不串数据
func (d *Dashboard) handleBrickMovingWithdrawHistory(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	limitStr := r.URL.Query().Get("limit")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}
	limit := 20
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	baseAsset := extractBaseSymbol(triggerSymbol)
	if baseAsset == "" {
		baseAsset = "USDT"
	}
	// 从 trigger 或 pipeline 获取交易所（有 triggerId 时按 ID 取 trigger）
	exchangeType := ""
	var tg proto.Trigger
	if triggerIDStr != "" {
		if id, err := strconv.ParseUint(triggerIDStr, 10, 64); err == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				tg, _ = tm.GetTriggerByIDAsProto(id)
			}
		}
	}
	if tg == nil {
		tg, _ = d.triggerManager.GetTrigger(triggerSymbol)
	}
	if tg != nil {
		traderA := tg.GetTraderAType()
		traderB := tg.GetTraderBType()
		for _, t := range []string{traderA, traderB} {
			if t == "" || pipeline.IsOnchainNodeID(t) {
				continue
			}
			if idx := strings.Index(t, ":"); idx > 0 {
				exchangeType = t[:idx]
			} else {
				exchangeType = t
			}
			break
		}
	}
	if exchangeType == "" {
		key := pipelineConfigKey(triggerSymbol, triggerIDStr)
		d.brickMovingPipelineMu.RLock()
		cfg := d.brickMovingPipelines[key]
		d.brickMovingPipelineMu.RUnlock()
		if cfg != nil && len(cfg.ForwardNodes) > 0 {
			for _, n := range cfg.ForwardNodes {
				if id, ok := n["id"].(string); ok && id != "" && !pipeline.IsOnchainNodeID(id) {
					if idx := strings.Index(id, ":"); idx > 0 {
						exchangeType = id[:idx]
					} else {
						exchangeType = id
					}
					break
				}
			}
		}
	}
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingInFlipModeMu.RLock()
	inFlipMode := d.brickMovingInFlipMode[key]
	d.brickMovingInFlipModeMu.RUnlock()

	// 始终返回四条 pipeline 各自可达性：forward/backward（正常充提）+ mirrorForward/mirrorBackward（智能翻转）
	emptyResp := func(records []map[string]interface{}) {
		pipelines, pipelineInfo := d.buildWithdrawHistoryPipelines(triggerSymbol, triggerIDStr)
		mirrorFwd, mirrorBwd := d.buildWithdrawHistoryMirrors(triggerSymbol, triggerIDStr)
		flipPipelines := d.buildFlipPipelines(triggerSymbol, triggerIDStr)
		fwdReach, bwdReach, fwdReason, bwdReason, mFwdReach, mBwdReach, mFwdReason, mBwdReason := d.getPipelineReachability(triggerSymbol, triggerIDStr)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"records":                         records,
			"pipeline":                        pipelineInfo,
			"pipelines":                       pipelines,
			"mirrorForward":                   mirrorFwd,
			"mirrorBackward":                  mirrorBwd,
			"inFlipMode":                      inFlipMode,
			"flipPipelines":                   flipPipelines,
			"forwardReachable":                fwdReach,
			"backwardReachable":               bwdReach,
			"forwardReachableReason":          fwdReason,
			"backwardReachableReason":         bwdReason,
			"mirrorForwardReachable":          mFwdReach,
			"mirrorBackwardReachable":         mBwdReach,
			"mirrorForwardReachableReason":    mFwdReason,
			"mirrorBackwardReachableReason":   mBwdReason,
		})
	}
	if exchangeType == "" {
		emptyResp([]map[string]interface{}{})
		return
	}
	ex := d.getExchange(exchangeType)
	if ex == nil && position.GetWalletManager() != nil {
		ex = position.GetWalletManager().GetExchange(exchangeType)
	}
	if ex == nil {
		emptyResp([]map[string]interface{}{})
		return
	}
	depositProvider, ok := ex.(exchange.DepositWithdrawProvider)
	if !ok {
		emptyResp([]map[string]interface{}{})
		return
	}
	records, err := depositProvider.GetWithdrawHistory(baseAsset, limit)
	// #region agent log
	debugLogBrickMoving("handleBrickMovingWithdrawHistory:H6H7H8", "withdraw history query", map[string]interface{}{
		"triggerSymbol": triggerSymbol, "baseAsset": baseAsset, "exchangeType": exchangeType,
		"recordCount": len(records), "err": fmt.Sprintf("%v", err),
	}, "H6,H7,H8")
	// #endregion
	if err != nil {
		emptyResp([]map[string]interface{}{})
		return
	}
	out := make([]map[string]interface{}, 0, len(records))
	for _, r := range records {
		out = append(out, map[string]interface{}{
			"withdrawId": r.WithdrawID,
			"txHash":     r.TxHash,
			"asset":      r.Asset,
			"amount":     r.Amount,
			"network":    r.Network,
			"address":    r.Address,
			"status":     r.Status,
			"createTime": r.CreateTime.Format("2006-01-02 15:04:05"),
		})
	}

	pipelines, pipelineInfo := d.buildWithdrawHistoryPipelines(triggerSymbol, triggerIDStr)
	mirrorFwd, mirrorBwd := d.buildWithdrawHistoryMirrors(triggerSymbol, triggerIDStr)
	flipPipelines := d.buildFlipPipelines(triggerSymbol, triggerIDStr)

	// #region agent log
	{
		pipelineDirs := make([]string, 0, len(pipelines))
		pipelineStatuses := make(map[string]string)
		for dir, p := range pipelines {
			pipelineDirs = append(pipelineDirs, dir)
			if pi, ok := p.(map[string]interface{}); ok {
				if s, ok := pi["status"].(string); ok { pipelineStatuses[dir] = s }
			}
		}
		debugLogBrickMoving("handleBrickMovingWithdrawHistory:H9H10:response", "withdraw history response", map[string]interface{}{
			"recordCount": len(out), "pipelineDirs": pipelineDirs, "pipelineStatuses": pipelineStatuses,
			"hasPipelineInfo": pipelineInfo != nil,
		}, "H9,H10")
	}
	// #endregion
	fwdReach, bwdReach, fwdReason, bwdReason, mFwdReach, mBwdReach, mFwdReason, mBwdReason := d.getPipelineReachability(triggerSymbol, triggerIDStr)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"records":                         out,
		"pipeline":                        pipelineInfo,
		"pipelines":                       pipelines,
		"mirrorForward":                   mirrorFwd,
		"mirrorBackward":                  mirrorBwd,
		"inFlipMode":                      inFlipMode,
		"flipPipelines":                   flipPipelines,
		"forwardReachable":                fwdReach,
		"backwardReachable":               bwdReach,
		"forwardReachableReason":          fwdReason,
		"backwardReachableReason":         bwdReason,
		"mirrorForwardReachable":          mFwdReach,
		"mirrorBackwardReachable":         mBwdReach,
		"mirrorForwardReachableReason":    mFwdReason,
		"mirrorBackwardReachableReason":   mBwdReason,
	})
}

// buildWithdrawHistoryPipelines 构建提币历史所需的 pipeline 信息（forward/backward）
// 优先从 PipelineManager 取已应用的 pipeline；若未找到则用已保存的 config 节点/边兜底，保证「正常充提」A→B/B→A 能显示路径而非未配置
func (d *Dashboard) buildWithdrawHistoryPipelines(triggerSymbol, triggerIDStr string) (pipelines map[string]interface{}, pipelineInfo map[string]interface{}) {
	pipelines = make(map[string]interface{})
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	var forwardID, backwardID string
	if cfg != nil {
		forwardID = cfg.ActiveForwardPipelineId
		backwardID = cfg.ActiveBackwardPipelineId
	}
	d.brickMovingPipelineMu.RUnlock()
	pm := pipeline.GetPipelineManager()
	suffixForward := "-forward"
	suffixBackward := "-backward"
	if triggerIDStr != "" {
		suffixForward = "-" + triggerIDStr + "-forward"
		suffixBackward = "-" + triggerIDStr + "-backward"
	}
	if forwardID == "" {
		forwardID = "brick-" + triggerSymbol + suffixForward
	}
	if backwardID == "" {
		backwardID = "brick-" + triggerSymbol + suffixBackward
	}
	if forwardID != "" {
		if p, err := pm.GetPipeline(forwardID); err == nil {
			if pi := d.buildPipelineInfo(p, forwardID, "forward"); pi != nil {
				pipelines["forward"] = pi
			}
			d.brickMovingPipelineMu.Lock()
			if d.brickMovingPipelines[key] == nil {
				d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
			}
			d.brickMovingPipelines[key].ActiveForwardPipelineId = forwardID
			d.brickMovingPipelineMu.Unlock()
		}
	}
	if backwardID != "" {
		if p, err := pm.GetPipeline(backwardID); err == nil {
			if pi := d.buildPipelineInfo(p, backwardID, "backward"); pi != nil {
				pipelines["backward"] = pi
			}
			d.brickMovingPipelineMu.Lock()
			if d.brickMovingPipelines[key] == nil {
				d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
			}
			d.brickMovingPipelines[key].ActiveBackwardPipelineId = backwardID
			d.brickMovingPipelineMu.Unlock()
		}
	}
	// 兜底：Manager 中无实例时（如刚应用尚未轮询到、或 key 不一致），用已保存的 config 展示路径，避免提币记录只显示「未配置」
	d.brickMovingPipelineMu.RLock()
	cfg2 := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	if cfg2 != nil {
		if pipelines["forward"] == nil && len(cfg2.ForwardNodes) > 0 {
			edges := cfg2.ForwardEdges
			if edges == nil {
				edges = []map[string]interface{}{}
			}
			pipelines["forward"] = map[string]interface{}{
				"nodes":  sliceMapToInterface(cfg2.ForwardNodes),
				"edges":  sliceMapToInterface(edges),
				"status": "idle",
			}
		}
		if pipelines["backward"] == nil && len(cfg2.BackwardNodes) > 0 {
			edges := cfg2.BackwardEdges
			if edges == nil {
				edges = []map[string]interface{}{}
			}
			pipelines["backward"] = map[string]interface{}{
				"nodes":  sliceMapToInterface(cfg2.BackwardNodes),
				"edges":  sliceMapToInterface(edges),
				"status": "idle",
			}
		}
	}
	priorityMap := map[string]int{"running": 4, "completed": 3, "failed": 2, "idle": 1}
	if len(pipelines) == 1 {
		for _, p := range pipelines {
			if p != nil {
				pipelineInfo = p.(map[string]interface{})
				break
			}
		}
	} else if len(pipelines) > 1 {
		bestPriority := -1
		for _, p := range pipelines {
			if p != nil {
				pi := p.(map[string]interface{})
				if s, ok := pi["status"].(string); ok && priorityMap[s] > bestPriority {
					bestPriority = priorityMap[s]
					pipelineInfo = pi
				}
			}
		}
	}
	return pipelines, pipelineInfo
}

// buildFlipPipelines 构建翻转充提两条 pipeline 的状态（token / usdt），供提币历史页展示运行中闪烁、完成绿、失败红。
// 状态约定：inFlip 时 status 来自 Pipeline.Status()，可能为 idle|running|paused|completed|failed；非 inFlip 且在 60s 内仅写入 completed|failed。
// 前端约定：仅 completed→成功、failed→失败，其余（running/idle/paused/空）统一展示为运行中。
func (d *Dashboard) buildFlipPipelines(triggerSymbol, triggerIDStr string) map[string]interface{} {
	out := map[string]interface{}{}
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingInFlipModeMu.RLock()
	inFlip := d.brickMovingInFlipMode[key]
	d.brickMovingInFlipModeMu.RUnlock()

	pipeNameToken := "brick-flip-" + triggerSymbol + "-" + triggerIDStr + "-token"
	pipeNameUsdt := "brick-flip-" + triggerSymbol + "-" + triggerIDStr + "-usdt"
	pm := pipeline.GetPipelineManager()

	if inFlip {
		if pToken, err := pm.GetPipeline(pipeNameToken); err == nil && pToken != nil {
			if pi := d.buildPipelineInfo(pToken, pipeNameToken, "flip-token"); pi != nil {
				out["token"] = pi
			} else {
				out["token"] = map[string]interface{}{"status": "running"}
			}
		} else {
			out["token"] = map[string]interface{}{"status": "running"}
		}
		if pUsdt, err := pm.GetPipeline(pipeNameUsdt); err == nil && pUsdt != nil {
			if pi := d.buildPipelineInfo(pUsdt, pipeNameUsdt, "flip-usdt"); pi != nil {
				out["usdt"] = pi
			} else {
				out["usdt"] = map[string]interface{}{"status": "running"}
			}
		} else {
			out["usdt"] = map[string]interface{}{"status": "running"}
		}
		return out
	}

	d.lastFlipPipelineResultMu.RLock()
	r := d.lastFlipPipelineResult[key]
	d.lastFlipPipelineResultMu.RUnlock()
	if r.EndTime.IsZero() || time.Since(r.EndTime) > 60*time.Second {
		if !r.EndTime.IsZero() && time.Since(r.EndTime) > 60*time.Second {
			d.lastFlipPipelineResultMu.Lock()
			delete(d.lastFlipPipelineResult, key)
			d.lastFlipPipelineResultMu.Unlock()
		}
		return out
	}
	out["token"] = map[string]interface{}{"status": r.TokenStatus}
	out["usdt"] = map[string]interface{}{"status": r.UsdtStatus}
	return out
}

// getPipelineReachability 返回四条 pipeline 各自的可达性（正常充提 A→B/B→A + 智能翻转 B-A 币/A-B USDT）
// 未配置或未探测的方向视为「未知」不展示不可达警告（reason 为空时强制 reachable=true）
func (d *Dashboard) getPipelineReachability(triggerSymbol, triggerIDStr string) (
	forwardReachable, backwardReachable bool, forwardReason, backwardReason string,
	mirrorForwardReachable, mirrorBackwardReachable bool, mirrorForwardReason, mirrorBackwardReason string) {
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	if cfg == nil {
		return true, true, "", "", true, true, "", ""
	}
	fwdReach, bwdReach := cfg.ForwardReachable, cfg.BackwardReachable
	fwdReason, bwdReason := cfg.ForwardReachableReason, cfg.BackwardReachableReason
	mirrorFwdReach, mirrorBwdReach := cfg.MirrorForwardReachable, cfg.MirrorBackwardReachable
	mirrorFwdReason, mirrorBwdReason := cfg.MirrorForwardReachableReason, cfg.MirrorBackwardReachableReason
	if len(cfg.ForwardNodes) == 0 && fwdReason == "" {
		fwdReach, fwdReason = true, ""
	}
	if len(cfg.BackwardNodes) == 0 && bwdReason == "" {
		bwdReach, bwdReason = true, ""
	}
	if mirrorFwdReason == "" {
		mirrorFwdReach, mirrorFwdReason = true, ""
	}
	if mirrorBwdReason == "" {
		mirrorBwdReach, mirrorBwdReason = true, ""
	}
	d.logger.Debugf("route-probe getPipelineReachability: key=%s fwd=%v bwd=%v mirrorFwd=%v mirrorBwd=%v",
		key, fwdReach, bwdReach, mirrorFwdReach, mirrorBwdReach)
	return fwdReach, bwdReach, fwdReason, bwdReason, mirrorFwdReach, mirrorBwdReach, mirrorFwdReason, mirrorBwdReason
}

// buildWithdrawHistoryMirrors 构建智能翻转所需的镜像 pipeline（B-A 币 / A-B USDT）
func (d *Dashboard) buildWithdrawHistoryMirrors(triggerSymbol, triggerIDStr string) (mirrorForward, mirrorBackward map[string]interface{}) {
	mirrorForward = map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}}
	mirrorBackward = map[string]interface{}{"nodes": []interface{}{}, "edges": []interface{}{}}
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	d.brickMovingPipelineMu.RUnlock()
	if cfg == nil {
		return mirrorForward, mirrorBackward
	}
	if len(cfg.ForwardNodes) > 0 {
		mn, me := mirrorPipelineNodeMaps(cfg.ForwardNodes, cfg.ForwardEdges)
		if mn != nil {
			mirrorForward["nodes"] = sliceMapToInterface(mn)
			mirrorForward["edges"] = sliceMapToInterface(me)
		}
	}
	if len(cfg.BackwardNodes) > 0 {
		mn, me := mirrorPipelineNodeMaps(cfg.BackwardNodes, cfg.BackwardEdges)
		if mn != nil {
			mirrorBackward["nodes"] = sliceMapToInterface(mn)
			mirrorBackward["edges"] = sliceMapToInterface(me)
		}
	}
	return mirrorForward, mirrorBackward
}

// getStepLabelForEdge 根据边的节点类型返回步骤描述：进行中(stepLabel)、已完成(stepLabelDone)
func getStepLabelForEdge(fromNode, toNode pipeline.Node) (stepLabel, stepLabelDone string) {
	fromType := fromNode.GetType()
	toType := toNode.GetType()
	fromChain := pipeline.ChainIDFromNodeID(fromNode.GetID())
	toChain := pipeline.ChainIDFromNodeID(toNode.GetID())
	needsBridge := fromType == pipeline.NodeTypeOnchain && toType == pipeline.NodeTypeOnchain &&
		fromChain != "" && toChain != "" && fromChain != toChain

	if needsBridge {
		return "跨链中...", "跨链协议已执行，跨链回款已收到"
	}
	if fromType == pipeline.NodeTypeExchange && toType == pipeline.NodeTypeOnchain {
		return "交易所提币中...", "交易所提币已发出，链上已收到"
	}
	if fromType == pipeline.NodeTypeExchange && toType == pipeline.NodeTypeExchange {
		return "交易所提币中...", "交易所提币已发出，目标已收到"
	}
	if fromType == pipeline.NodeTypeOnchain && toType == pipeline.NodeTypeExchange {
		return "链上充值中...", "链上充值已发出，交易所已收到"
	}
	return "转账中...", "已到账"
}

// buildPipelineInfo 构建 pipeline 信息（辅助函数）
func (d *Dashboard) buildPipelineInfo(p *pipeline.Pipeline, pipelineID, direction string) map[string]interface{} {
	nodes := p.Nodes()
	nodeInfos := make([]map[string]interface{}, 0, len(nodes))
	for i, node := range nodes {
		balance, _ := node.CheckBalance()
		nodeInfo := map[string]interface{}{
			"id":      node.GetID(),
			"name":    node.GetName(),
			"type":    string(node.GetType()),
			"asset":   node.GetAsset(),
			"balance": balance,
			"index":   i,
		}
		nodeInfos = append(nodeInfos, nodeInfo)
	}

	edgeInfos := make([]map[string]interface{}, 0)
	currentStep := p.CurrentStep()
	status := p.Status()
	for i := 0; i < len(nodes)-1; i++ {
		fromNode := nodes[i]
		toNode := nodes[i+1]
		stepNum := i + 1

		edgeStatus := "pending"
		if status == pipeline.PipelineStatusRunning {
			if stepNum == currentStep {
				edgeStatus = "running"
			} else if stepNum < currentStep {
				edgeStatus = "success"
			}
		} else if status == pipeline.PipelineStatusCompleted {
			edgeStatus = "success"
		} else if status == pipeline.PipelineStatusFailed {
			if stepNum < currentStep {
				edgeStatus = "success"
			} else if stepNum == currentStep {
				edgeStatus = "failed"
			}
		}

		// 边描述：优先用 Pipeline 执行时写入的真实状态（SetCurrentStepPhase），否则回退到静态推断
		stepLabel, stepLabelDone := getStepLabelForEdge(fromNode, toNode)
		if stepNum == currentStep && status == pipeline.PipelineStatusRunning {
			if realLabel := p.GetCurrentStepLabel(); realLabel != "" {
				stepLabel = realLabel
			}
		}

		edgeInfo := map[string]interface{}{
			"from":          fromNode.GetID(),
			"to":            toNode.GetID(),
			"fromName":      fromNode.GetName(),
			"toName":        toNode.GetName(),
			"step":          stepNum,
			"status":        edgeStatus,
			"stepLabel":     stepLabel,
			"stepLabelDone": stepLabelDone,
		}
		edgeInfos = append(edgeInfos, edgeInfo)
	}

	lastError := p.GetLastError()
	lastErrorIsWarning := strings.HasPrefix(lastError, "[WARN]")
	btStatus, btStep, btFrom, btTo, btErr, btEnd := p.GetBalanceTransferState()
	btStepLabel, btStepLabelDone := "", ""
	if btStep > 0 && btStep <= len(nodes) {
		fromN := nodes[btStep-1]
		toN := nodes[btStep]
		btStepLabel, btStepLabelDone = getStepLabelForEdge(fromN, toN)
		if btStatus == "running" {
			if realLabel := p.GetCurrentStepLabel(); realLabel != "" {
				btStepLabel = realLabel
			}
		}
	}
	balanceTransfer := map[string]interface{}{
		"status":        btStatus,
		"step":          btStep,
		"fromName":      btFrom,
		"toName":        btTo,
		"lastError":     btErr,
		"lastEnd":       btEnd.UnixMilli(),
		"stepLabel":     btStepLabel,
		"stepLabelDone": btStepLabelDone,
	}
	return map[string]interface{}{
		"pipelineId":         pipelineID,
		"direction":          direction,
		"currentStep":        currentStep,
		"status":             string(status),
		"lastError":          lastError,
		"lastErrorIsWarning": lastErrorIsWarning,
		"nodes":              nodeInfos,
		"edges":              edgeInfos,
		"balanceTransfer":    balanceTransfer,
	}
}

// PipelineBalanceResult 供 HTTP 展示的余额结果（可选字段）
type PipelineBalanceResult struct {
	Balance      float64
	Threshold    float64
	BalanceAsset string
	NodeID       string
	NodeName     string
	IsBackward   bool
}

// getBalanceAndThresholdFromPipeline 根据已存在的 pipeline 与 hedging 配置计算首节点余额与阈值（供 runAutoWithdrawTick 按「已存在 pipeline」轮询时复用）。
func (d *Dashboard) getBalanceAndThresholdFromPipeline(p *pipeline.Pipeline, triggerSymbol, triggerIDStr, direction string, hedging *brickMovingHedgingConfig) (result PipelineBalanceResult, err error) {
	result.IsBackward = direction == "backward"
	nodes := p.Nodes()
	if len(nodes) == 0 {
		return result, fmt.Errorf("pipeline has no nodes")
	}

	firstNode := nodes[0]
	result.NodeID = firstNode.GetID()
	result.NodeName = firstNode.GetName()
	isBackward := direction == "backward"
	var quoteAsset string
	if isBackward {
		if len(nodes) >= 2 {
			if edge, has := p.GetEdgeConfig(firstNode.GetID(), nodes[1].GetID()); has && edge != nil && edge.Asset != "" {
				quoteAsset = edge.Asset
			}
		}
		if quoteAsset == "" {
			quoteAsset = d.getQuoteAssetForTrigger(triggerSymbol, triggerIDStr)
		}
	}

	if isBackward && firstNode.GetType() == pipeline.NodeTypeExchange && quoteAsset != "" {
		if exchangeNode, ok := firstNode.(*pipeline.ExchangeNode); ok {
			bal, getBalErr := exchangeNode.GetBalanceForAsset(quoteAsset)
			if getBalErr != nil {
				return result, fmt.Errorf("failed to get %s balance: %w", quoteAsset, getBalErr)
			}
			result.Balance = bal
			result.BalanceAsset = quoteAsset
		} else {
			bal, getBalErr := firstNode.GetAvailableBalance()
			if getBalErr != nil {
				return result, fmt.Errorf("failed to get balance: %w", getBalErr)
			}
			result.Balance = bal
			result.BalanceAsset = firstNode.GetAsset()
		}
	} else {
		bal, getBalErr := firstNode.GetAvailableBalance()
		if getBalErr != nil {
			return result, fmt.Errorf("failed to get balance: %w", getBalErr)
		}
		result.Balance = bal
		result.BalanceAsset = firstNode.GetAsset()
		// backward 且首节点为链上（币本位余额）时，阈值按 USDT 计算，需将余额转为 USDT 再与阈值比较
		if isBackward && quoteAsset != "" && result.BalanceAsset != quoteAsset {
			coinAsset := strings.TrimSuffix(triggerSymbol, "USDT")
			if coinAsset == triggerSymbol {
				coinAsset = strings.TrimSuffix(triggerSymbol, "USDC")
			}
			baseSymbol := coinAsset + quoteAsset
			if pm := position.GetPositionManager(); pm != nil {
				if priceData := pm.GetLatestPrice(baseSymbol); priceData != nil && priceData.BidPrice > 0 {
					result.Balance = result.Balance * priceData.BidPrice
					result.BalanceAsset = quoteAsset
				}
			}
		}
	}

	// 计算阈值（与前端一致：forward 固定/仓位百分比，backward 固定时用计价资产阈值）；hedging 由调用方传入
	if hedging != nil && hedging.AutoWithdrawEnabled {
		if hedging.AutoWithdrawUseFixed {
			fixedSize := hedging.AutoWithdrawFixedSize
			if fixedSize <= 0 {
				fixedSize = hedging.Size
			}
			if isBackward && quoteAsset != "" {
				coinAsset := strings.TrimSuffix(triggerSymbol, "USDT")
				if coinAsset == triggerSymbol {
					coinAsset = strings.TrimSuffix(triggerSymbol, "USDC")
				}
				baseSymbol := coinAsset + quoteAsset
				if pm := position.GetPositionManager(); pm != nil {
					if priceData := pm.GetLatestPrice(baseSymbol); priceData != nil && priceData.BidPrice > 0 {
						result.Threshold = fixedSize * priceData.BidPrice
					}
				}
			} else {
				result.Threshold = fixedSize
			}
		} else {
			pos := hedging.Position
			pct := hedging.AutoWithdrawPercentage
			if pct <= 0 {
				pct = 0
			}
			if pos > 0 {
				if isBackward && quoteAsset != "" {
					coinAsset := strings.TrimSuffix(triggerSymbol, "USDT")
					if coinAsset == triggerSymbol {
						coinAsset = strings.TrimSuffix(triggerSymbol, "USDC")
					}
					baseSymbol := coinAsset + quoteAsset
					if pm := position.GetPositionManager(); pm != nil {
						if priceData := pm.GetLatestPrice(baseSymbol); priceData != nil && priceData.BidPrice > 0 {
							result.Threshold = pos * pct * priceData.BidPrice
						} else {
							result.Threshold = pos * pct
						}
					} else {
						result.Threshold = pos * pct
					}
				} else {
					result.Threshold = pos * pct
				}
			}
		}
	}

	return result, nil
}

// getPipelineBalanceAndThreshold 获取指定 trigger 与方向的 pipeline 首节点余额及用于自动充提的阈值（供 HTTP handleGetPipelineBalance 等复用）。
// 按 key(symbol, triggerIDStr) 查配置与 pipeline；triggerIDStr 非空时按 trigger 区分。
func (d *Dashboard) getPipelineBalanceAndThreshold(triggerSymbol, direction, triggerIDStr string) (result PipelineBalanceResult, err error) {
	key := pipelineConfigKey(triggerSymbol, triggerIDStr)
	d.brickMovingPipelineMu.RLock()
	cfg := d.brickMovingPipelines[key]
	var activeID string
	if cfg != nil {
		if direction == "forward" {
			activeID = cfg.ActiveForwardPipelineId
		} else {
			activeID = cfg.ActiveBackwardPipelineId
		}
	}
	d.brickMovingPipelineMu.RUnlock()

	if activeID == "" {
		if triggerIDStr != "" {
			activeID = "brick-" + triggerSymbol + "-" + triggerIDStr + "-" + direction
		} else {
			activeID = "brick-" + triggerSymbol + "-" + direction
		}
	}

	pm := pipeline.GetPipelineManager()
	p, getErr := pm.GetPipeline(activeID)
	if getErr != nil {
		return result, fmt.Errorf("pipeline not found: %w", getErr)
	}

	d.brickMovingPipelineMu.Lock()
	if d.brickMovingPipelines[key] == nil {
		d.brickMovingPipelines[key] = &brickMovingPipelineConfig{}
	}
	if direction == "forward" {
		d.brickMovingPipelines[key].ActiveForwardPipelineId = activeID
	} else {
		d.brickMovingPipelines[key].ActiveBackwardPipelineId = activeID
	}
	d.brickMovingPipelineMu.Unlock()

	d.brickMovingHedgingMu.RLock()
	hedging := d.brickMovingHedgingCfgs[key]
	d.brickMovingHedgingMu.RUnlock()

	return d.getBalanceAndThresholdFromPipeline(p, triggerSymbol, triggerIDStr, direction, hedging)
}

// runAutoWithdrawLoop 后端自动充提轮询：按周期检查已启用 autoWithdraw 的 symbol，余额满足阈值时执行 pipeline run。
func (d *Dashboard) runAutoWithdrawLoop() {
	const interval = 2 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// 启动时立即执行一次检测，不等待首个 interval
	d.runAutoWithdrawTick()
	for range ticker.C {
		d.runAutoWithdrawTick()
	}
}

// runAutoWithdrawTick 执行一次自动充提检测：仅对「已存在于 PipelineManager 的 brick-* pipeline」按首节点余额与阈值判断是否触发 run。
// 逻辑：遍历已注册的 pipeline，解析 (symbol, triggerIDStr, direction)，仅当该 key 或 symbol 下开启了自动充提且余额达标时执行，避免按每个 triggerId 查不存在的 pipeline 导致刷屏。
func (d *Dashboard) runAutoWithdrawTick() {
	pm := pipeline.GetPipelineManager()
	all := pm.ListPipelines()
	for _, p := range all {
		name := p.Name()
		symbol, triggerIDStr, direction, ok := pipeline.ParseBrickPipelineName(name)
		if !ok || symbol == "" || direction == "" {
			continue
		}

		key := pipelineConfigKey(symbol, triggerIDStr)
		d.brickMovingInFlipModeMu.RLock()
		inFlip := d.brickMovingInFlipMode[key]
		d.brickMovingInFlipModeMu.RUnlock()
		if inFlip {
			continue
		}
		d.brickMovingHedgingMu.RLock()
		hedging := d.brickMovingHedgingCfgs[key]
		d.brickMovingHedgingMu.RUnlock()

		if hedging == nil || !hedging.AutoWithdrawEnabled {
			continue
		}

		res, err := d.getBalanceAndThresholdFromPipeline(p, symbol, triggerIDStr, direction, hedging)
		if err != nil {
			d.logger.Debugf("Auto-withdraw skip %s %s (pipeline %s): %v", symbol, direction, name, err)
			continue
		}
		thresholdRatio := 0.95
		if direction == "backward" {
			thresholdRatio = 0.70
		}
		shouldRun := (res.Threshold > 0 && res.Balance >= res.Threshold*thresholdRatio) || (res.Threshold <= 0 && res.Balance > 0)
		if !shouldRun {
			continue
		}
		useAllBalance := direction == "backward" && res.Threshold > 0 && res.Balance >= res.Threshold*0.70 && res.Balance < res.Threshold
		if _, runErr := d.runBrickMovingPipelineInternal(symbol, direction, triggerIDStr, useAllBalance); runErr == nil {
			d.logger.Infof("Auto-withdraw triggered: %s (%s) pipeline=%s useAllBalance=%v", symbol, direction, name, useAllBalance)
		}
	}
}

// handleGetPipelineBalance 获取 pipeline 第一个节点的余额（用于自动检测提币）
func (d *Dashboard) handleGetPipelineBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	triggerIDStr := r.URL.Query().Get("triggerId")
	direction := r.URL.Query().Get("direction")
	if triggerSymbol == "" || direction == "" {
		http.Error(w, "triggerSymbol and direction are required", http.StatusBadRequest)
		return
	}

	res, err := d.getPipelineBalanceAndThreshold(triggerSymbol, direction, triggerIDStr)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"balance": 0,
			"error":   err.Error(),
		})
		return
	}

	resp := map[string]interface{}{
		"balance":      res.Balance,
		"balanceAsset": res.BalanceAsset,
		"nodeId":       res.NodeID,
		"nodeName":     res.NodeName,
		"isBackward":   res.IsBackward,
	}
	if res.IsBackward && res.Threshold > 0 {
		resp["usdtThreshold"] = res.Threshold
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleSearchChains 搜索交易所对某资产支持的充提网络。
// GET /api/brick-moving/chains/search?q=<exchangeType>&symbol=<asset>
// 返回该交易所支持的 withdraw/deposit 网络列表。
func (d *Dashboard) handleSearchChains(w http.ResponseWriter, r *http.Request) {
	exchangeType := r.URL.Query().Get("q")
	symbol := r.URL.Query().Get("symbol")

	if exchangeType == "" {
		http.Error(w, "q parameter (exchange type) is required", http.StatusBadRequest)
		return
	}
	if symbol == "" {
		symbol = "USDT"
	}

	wm := position.GetWalletManager()
	if wm == nil {
		writeJSONError(w, http.StatusInternalServerError, "WalletManager not initialized")
		return
	}

	cex := wm.GetExchange(exchangeType)
	if cex == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("exchange %s not found", exchangeType))
		return
	}

	if _, ok := cex.(exchange.DepositWithdrawProvider); !ok {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("exchange %s does not support deposit/withdraw", exchangeType))
		return
	}

	type networkInfo struct {
		Network   string `json:"network"`
		ChainID   string `json:"chainId,omitempty"`
		Available bool   `json:"available"`
		Type      string `json:"type"`
		Fee       string `json:"fee,omitempty"`
		MinAmount string `json:"minAmount,omitempty"`
	}

	var results []networkInfo

	if wnl, ok := cex.(exchange.WithdrawNetworkLister); ok {
		withdrawNets, err := wnl.GetWithdrawNetworks(symbol)
		if err == nil {
			for _, wn := range withdrawNets {
				results = append(results, networkInfo{
					Network:   wn.Network,
					ChainID:   wn.ChainID,
					Available: wn.WithdrawEnable,
					Type:      "withdraw",
					Fee:       wn.WithdrawFee,
					MinAmount: wn.WithdrawMin,
				})
			}
		}
	}

	// DepositNetworkLister 是可选接口，并非所有交易所都实现
	if dnl, ok := cex.(exchange.DepositNetworkLister); ok {
		depositNets, err := dnl.GetDepositNetworks(symbol)
		if err == nil {
			for _, dn := range depositNets {
				results = append(results, networkInfo{
					Network:   dn.Network,
					ChainID:   dn.ChainID,
					Available: dn.WithdrawEnable,
					Type:      "deposit",
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"exchange": exchangeType,
		"symbol":   symbol,
		"networks": results,
	})
}

// handleGetBrickMovingTriggerPriceDiff 获取 trigger 的实时价差
// 支持 triggerId：同 symbol 多 trigger 时按 triggerId 取该 trigger 自己的价差，避免与其它 trigger 串数据
func (d *Dashboard) handleGetBrickMovingTriggerPriceDiff(w http.ResponseWriter, r *http.Request) {
	triggerSymbol := r.URL.Query().Get("triggerSymbol")
	if triggerSymbol == "" {
		http.Error(w, "triggerSymbol is required", http.StatusBadRequest)
		return
	}

	var diffAB, diffBA float64
	var exists bool

	if triggerIDStr := r.URL.Query().Get("triggerId"); triggerIDStr != "" {
		if id, err := strconv.ParseUint(triggerIDStr, 10, 64); err == nil {
			if tm, ok := d.triggerManager.(*trigger.TriggerManager); ok {
				if tg, err := tm.GetTriggerByIDAsProto(id); err == nil && tg != nil {
					if brickTrigger, ok := tg.(*trigger.BrickMovingTrigger); ok {
						diffAB, diffBA, exists = brickTrigger.GetLatestPriceDiff()
					}
				}
			}
		}
	}

	if !exists {
		statisticsManager := statistics.GetStatisticsManager()
		if statisticsManager != nil {
			diffAB, diffBA, exists = statisticsManager.GetLatestPriceDiff(triggerSymbol)
		}
	}

	result := map[string]interface{}{
		"diffAB": diffAB,
		"diffBA": diffBA,
		"exists": exists,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
