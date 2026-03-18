package pipeline

import (
	"strconv"
	"strings"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
)

const maxRouteProbeHops = 4 // 路径最多 4 个节点（含 链→交易所→链→交易所 展开后以便 merge 得到 WithdrawNetworkChainID）

// RouteProbe 执行提币路由探测：对 path 中每段调用报价或估算，返回总耗时与损耗。
// path 仅含资产持有节点，最多 maxRouteProbeHops 个；跨链段由 bridgeManager.GetBridgeQuote 报价。
func RouteProbe(req *model.RouteProbeRequest, bridgeManager *bridge.Manager) (*model.RouteProbeResult, error) {
	path := req.Path
	if len(path) == 0 && req.Source != "" && req.Destination != "" {
		// 简单解析：Source/Destination 作为单跳或占位，这里只支持显式 path
		path = []string{req.Source, req.Destination}
	}
	if len(path) < 2 {
		return &model.RouteProbeResult{
			Path:     path,
			Segments: []model.SegmentProbeResult{},
		}, nil
	}
	if len(path) > maxRouteProbeHops {
		path = path[:maxRouteProbeHops]
	}

	probeAmount := req.ProbeAmount
	if probeAmount == "" {
		probeAmount = "100"
	}
	symbol := req.Symbol
	if symbol == "" {
		symbol = "USDT"
	}

	segments := make([]model.SegmentProbeResult, 0, len(path)-1)
	var totalTime int64
	totalFee := "0"

	for i := 0; i < len(path)-1; i++ {
		fromID := path[i]
		toID := path[i+1]
		fromChain := ChainIDFromNodeID(fromID)
		toChain := ChainIDFromNodeID(toID)

		seg := model.SegmentProbeResult{
			FromNodeID:       fromID,
			ToNodeID:         toID,
			Fee:              "0",
			EstimatedTimeSec: 120,
			Available:        true,
		}

		if fromChain != "" && toChain != "" && fromChain != toChain {
			// 跨链段：若请求指定了 BridgeProtocol 则使用该协议，否则用第一个可用协议；并回填 BridgeProtocol 供前端展示
			seg.Type = model.SegmentTypeBridge
			if bridgeManager != nil {
				quoteReq := &model.BridgeQuoteRequest{
					FromChain: fromChain,
					ToChain:   toChain,
					FromToken: symbol,
					ToToken:   symbol,
					Amount:    probeAmount,
				}
				quote, err := bridgeManager.GetBridgeQuote(quoteReq)
				if err != nil || quote == nil || len(quote.Protocols) == 0 {
					seg.Available = false
					seg.Fee = "0"
					seg.EstimatedTimeSec = 300
					if err != nil {
						seg.RawInfo = map[string]interface{}{"reason": "bridge quote failed: " + err.Error()}
					}
				} else {
					var pq *model.ProtocolQuote
					if req.BridgeProtocol != "" && req.BridgeProtocol != "auto" {
						for i := range quote.Protocols {
							if strings.EqualFold(quote.Protocols[i].Protocol, req.BridgeProtocol) {
								pq = &quote.Protocols[i]
								break
							}
						}
						if pq == nil {
							seg.Available = false
							seg.RawInfo = map[string]interface{}{"reason": "protocol " + req.BridgeProtocol + " not found or not supported for this segment"}
						}
					}
					if pq == nil && len(quote.Protocols) > 0 {
						pq = &quote.Protocols[0]
					}
					if pq != nil {
						seg.BridgeProtocol = pq.Protocol
						if pq.Supported {
							seg.Fee = pq.Fee
							if pq.EstimatedTime > 0 {
								seg.EstimatedTimeSec = pq.EstimatedTime
							}
						} else {
							seg.Available = false
							if pq.RawInfo != nil {
								seg.RawInfo = pq.RawInfo
							} else {
								seg.RawInfo = map[string]interface{}{"reason": "protocol " + pq.Protocol + " does not support this asset on this chain pair"}
							}
						}
					}
				}
			} else {
				seg.Available = false
			}
		} else {
			// 提币/充币段（交易所 <-> 链）或 交易所→交易所 段
			// fromChain!="" && toChain=="" → chain→exchange = deposit
			// fromChain=="" && toChain!="" → exchange→chain = withdraw
			if fromChain == "" && toChain == "" {
				seg.Type = model.SegmentTypeExchangeToExchange
			} else if fromChain == "" && toChain != "" {
				seg.Type = model.SegmentTypeWithdraw
			} else if fromChain != "" && toChain == "" {
				seg.Type = model.SegmentTypeDeposit
			} else {
				// fromChain != "" && toChain != "" && fromChain == toChain (same chain transfer)
				seg.Type = model.SegmentTypeDeposit
			}
			seg.EstimatedTimeSec = 60
			if seg.Type == model.SegmentTypeExchangeToExchange {
				seg.EstimatedTimeSec = 120
				seg.RawInfo = map[string]interface{}{
					"feeNote": "Exchange withdrawal fee applies (varies by asset and network)",
				}
			} else if seg.Type == model.SegmentTypeWithdraw {
				seg.RawInfo = map[string]interface{}{
					"feeNote": "Exchange withdrawal fee applies (check exchange fee schedule)",
				}
			}
		}

		totalTime += seg.EstimatedTimeSec
		if seg.Fee != "" && seg.Fee != "0" {
			if f, err := strconv.ParseFloat(seg.Fee, 64); err == nil {
				if tf, err := strconv.ParseFloat(totalFee, 64); err == nil {
					totalFee = strconv.FormatFloat(tf+f, 'f', -1, 64)
				}
			}
		}
		segments = append(segments, seg)
	}

	// 合并「交易所→链→交易所」为单段，链作为边的属性（提现网络），与 bridge 协议一样展示
	path, segments = mergeExchangeChainExchangeSegments(path, segments)
	totalTime = int64(0)
	totalFee = "0"
	for _, s := range segments {
		totalTime += s.EstimatedTimeSec
		if s.Fee != "" && s.Fee != "0" {
			if f, err := strconv.ParseFloat(s.Fee, 64); err == nil {
				if tf, err := strconv.ParseFloat(totalFee, 64); err == nil {
					totalFee = strconv.FormatFloat(tf+f, 'f', -1, 64)
				}
			}
		}
	}

	return &model.RouteProbeResult{
		Path:                  path,
		Segments:              segments,
		TotalEstimatedTimeSec: totalTime,
		TotalFee:              totalFee,
	}, nil
}

// mergeExchangeChainExchangeSegments 将 path 中「交易所→链→交易所」两段合并为一段「交易所→交易所」，链 ID 写入 WithdrawNetworkChainID（表示该段使用该链提现）。
// 返回合并后的 path（去掉中间链节点）与 segments。
func mergeExchangeChainExchangeSegments(path []string, segments []model.SegmentProbeResult) ([]string, []model.SegmentProbeResult) {
	if len(segments) < 2 {
		return path, segments
	}
	var outPath []string
	var outSegs []model.SegmentProbeResult
	outPath = append(outPath, path[0])
	for i := 0; i < len(segments); i++ {
		cur := segments[i]
		// 是否满足：当前段 交易所→链，下一段 链→交易所
		fromChain := ChainIDFromNodeID(cur.FromNodeID)
		toChain := ChainIDFromNodeID(cur.ToNodeID)
			if fromChain == "" && toChain != "" && i+1 < len(segments) {
			next := segments[i+1]
			nextToChain := ChainIDFromNodeID(next.ToNodeID)
			if cur.ToNodeID == next.FromNodeID && nextToChain == "" {
				// 合并为 交易所→交易所，提现网络为中间链
				merged := model.SegmentProbeResult{
					FromNodeID:             cur.FromNodeID,
					ToNodeID:               next.ToNodeID,
					Type:                   model.SegmentTypeExchangeToExchange,
					WithdrawNetworkChainID: toChain,
					Fee:                    cur.Fee,
					EstimatedTimeSec:       cur.EstimatedTimeSec + next.EstimatedTimeSec,
					Available:              cur.Available && next.Available,
				}
				if next.Fee != "" && next.Fee != "0" {
					if merged.Fee == "" || merged.Fee == "0" {
						merged.Fee = next.Fee
					} else if f1, e1 := strconv.ParseFloat(merged.Fee, 64); e1 == nil {
						if f2, e2 := strconv.ParseFloat(next.Fee, 64); e2 == nil {
							merged.Fee = strconv.FormatFloat(f1+f2, 'f', -1, 64)
						}
					}
				}
				outSegs = append(outSegs, merged)
				outPath = append(outPath, next.ToNodeID)
				i++
				continue
			}
		}
		outSegs = append(outSegs, cur)
		if i+1 < len(path) {
			outPath = append(outPath, path[i+1])
		}
	}
	return outPath, outSegs
}
