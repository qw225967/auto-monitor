package onchain

import (
	"fmt"
	"sync"

	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/onchain/bridge"
	"github.com/qw225967/auto-monitor/internal/utils/rest"
)

var _ OnchainClient = (*okdex)(nil)

type okdex struct {
	mu            sync.RWMutex
	isInitialized bool
	restClient    rest.RestClient
	bridgeManager *bridge.Manager
}

// NewOkdex 创建 OK DEX 客户端（仅 quote + bridge）
func NewOkdex() OnchainClient {
	return &okdex{}
}

// Init 初始化
func (o *okdex) Init() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.isInitialized {
		return nil
	}

	o.restClient.InitRestClient()

	manager := config.GetOkexKeyManager()
	if err := manager.Init(); err != nil {
		return fmt.Errorf("init OKEx key manager: %w", err)
	}

	o.isInitialized = true
	return nil
}

// QueryDexQuotePrice 查询 DEX 报价（v6 接口，仅询价不交易）
func (o *okdex) QueryDexQuotePrice(fromTokenAddress, toTokenAddress, chainIndex, amount, fromTokenDecimals string) (string, error) {
	o.mu.RLock()
	initialized := o.isInitialized
	o.mu.RUnlock()

	if !initialized {
		return "", ErrNotInitialized
	}
	return o.queryDexQuotePrice(fromTokenAddress, toTokenAddress, chainIndex, amount, fromTokenDecimals)
}

// SetBridgeManager 设置跨链协议管理器
func (o *okdex) SetBridgeManager(manager *bridge.Manager) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bridgeManager = manager
}

// BridgeToken 跨链转账
func (o *okdex) BridgeToken(req *model.BridgeRequest) (*model.BridgeResponse, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}
	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}
	return o.bridgeManager.BridgeToken(req)
}

// GetBridgeStatus 查询跨链状态
func (o *okdex) GetBridgeStatus(txHash string, fromChain, toChain string) (*model.BridgeStatus, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}
	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}
	return o.bridgeManager.GetBridgeStatus(txHash, fromChain, toChain, "")
}

// GetBridgeQuote 获取跨链报价
func (o *okdex) GetBridgeQuote(req *model.BridgeQuoteRequest) (*model.BridgeQuote, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, ErrNotInitialized
	}
	if o.bridgeManager == nil {
		return nil, fmt.Errorf("bridge manager not initialized")
	}
	return o.bridgeManager.GetBridgeQuote(req)
}
