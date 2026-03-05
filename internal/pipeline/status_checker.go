package pipeline

import (
	"fmt"
	"time"

	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/onchain/bridge"
)

// StatusChecker 状态检查器
// 提供统一的状态检查接口，封装不同节点类型的检查逻辑
type StatusChecker struct {
	logger interface {
		Infof(format string, args ...interface{})
		Warnf(format string, args ...interface{})
	}
}

// NewStatusChecker 创建状态检查器
func NewStatusChecker() *StatusChecker {
	return &StatusChecker{}
}

// CheckWithdrawStatus 检查交易所提币状态
// 通过轮询交易所的提币历史记录来判断是否完成
func (sc *StatusChecker) CheckWithdrawStatus(node Node, withdrawID string, maxWait time.Duration, interval time.Duration) (bool, error) {
	if withdrawID == "" {
		return false, fmt.Errorf("withdrawID is empty")
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return false, fmt.Errorf("withdraw status check timeout after %v", maxWait)
			}

			confirmed, err := node.CheckWithdrawStatus(withdrawID)
			if err != nil {
				// 某些错误可能是暂时的（如网络问题），继续重试
				continue
			}
			if confirmed {
				return true, nil
			}
		}
	}
}

// CheckDepositStatus 检查充币到账状态
// 通过轮询充币历史记录来判断是否到账
func (sc *StatusChecker) CheckDepositStatus(node Node, txHash string, maxWait time.Duration, interval time.Duration) (bool, error) {
	if txHash == "" {
		return false, fmt.Errorf("txHash is empty")
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return false, fmt.Errorf("deposit status check timeout after %v", maxWait)
			}

			confirmed, err := node.CheckDepositStatus(txHash)
			if err != nil {
				// 某些错误可能是暂时的，继续重试
				continue
			}
			if confirmed {
				return true, nil
			}
		}
	}
}

// CheckBridgeStatus 检查跨链状态（跨链为边上的行为，由 bridge.Manager 查询）
func (sc *StatusChecker) CheckBridgeStatus(mgr *bridge.Manager, bridgeID string, fromChain, toChain string, maxWait time.Duration, interval time.Duration) (*model.BridgeStatus, error) {
	if bridgeID == "" {
		return nil, fmt.Errorf("bridgeID is empty")
	}
	if mgr == nil {
		return nil, fmt.Errorf("bridge manager is nil")
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("bridge status check timeout after %v", maxWait)
			}

			protocol := protocolFromBridgeID(bridgeID)
			status, err := mgr.GetBridgeStatus(bridgeID, fromChain, toChain, protocol)
			if err != nil {
				continue
			}
			if status != nil {
				if status.Status == "COMPLETED" {
					return status, nil
				}
				if status.Status == "FAILED" {
					return status, fmt.Errorf("bridge failed: bridgeID=%s", bridgeID)
				}
			}
		}
	}
}

// CheckOnchainTxStatus 检查链上交易状态（通过 OnchainClient）
// 这是一个辅助函数，供 OnchainNode 使用
func (sc *StatusChecker) CheckOnchainTxStatus(client interface {
	GetTxResult(txHash, chainIndex string) (model.TradeResult, error)
}, txHash, chainIndex string, maxWait time.Duration, interval time.Duration) (bool, error) {
	if txHash == "" {
		return false, fmt.Errorf("txHash is empty")
	}

	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return false, fmt.Errorf("onchain tx status check timeout after %v", maxWait)
			}

			result, err := client.GetTxResult(txHash, chainIndex)
			if err != nil {
				// 某些错误可能是暂时的，继续重试
				continue
			}

			// 根据 TradeResult 的状态判断
			// 假设 TradeResult.Status 为 "success" 或类似值表示成功
			if result.Status == "success" || result.Status == "SUCCESS" {
				return true, nil
			}
			if result.Status == "fail" || result.Status == "FAILED" {
				return false, fmt.Errorf("transaction failed: %s", result.ErrorMsg)
			}
			// 其他状态继续等待
		}
	}
}
