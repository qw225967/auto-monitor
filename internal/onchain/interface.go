package onchain

import (
	"errors"

	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
)

// OnchainClient 链上操作接口（精简版：仅 quote + bridge）
type OnchainClient interface {
	Init() error

	// Quote 询价（OK DEX v6）
	QueryDexQuotePrice(fromTokenAddress, toTokenAddress, chainIndex, amount, fromTokenDecimals string) (string, error)

	// Bridge 跨链
	SetBridgeManager(manager *bridge.Manager)
	BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error)
	GetBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error)
	GetBridgeQuote(req *model.BridgeQuoteRequest) (*model.BridgeQuote, error)
}

// 错误定义
var (
	ErrNotInitialized     = errors.New("onchain client not initialized")
	ErrInvalidToken      = errors.New("invalid token address or symbol")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrBridgeNotSupported = errors.New("bridge not supported for this chain pair")
)
