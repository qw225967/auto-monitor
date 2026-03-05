package bundler

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/qw225967/auto-monitor/internal/utils/rest"

	"github.com/ethereum/go-ethereum/crypto"
)

// FlashbotsBundler Flashbots bundler 实现
type FlashbotsBundler struct {
	relayURL   string
	privateKey *ecdsa.PrivateKey
	restClient rest.RestClient
}

// NewFlashbotsBundler 创建新的 Flashbots bundler 实例
func NewFlashbotsBundler(privateKeyHex string, relayURL string) (*FlashbotsBundler, error) {
	privateKeyHex = cleanHexPrefix(privateKeyHex)
	privateKey, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	if relayURL == "" {
		relayURL = "https://relay.flashbots.net"
	}

	restClient := rest.RestClient{}
	restClient.InitRestClient()

	return &FlashbotsBundler{
		relayURL:   relayURL,
		privateKey: privateKey,
		restClient: restClient,
	}, nil
}

// SendBundle 发送交易 bundle 到 Flashbots
func (f *FlashbotsBundler) SendBundle(signedTx string, chainID string) (string, error) {
	if chainID != "1" {
		return "", fmt.Errorf("Flashbots only supports Ethereum mainnet (chainID=1), got %s", chainID)
	}

	request := FlashbotsBundleRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_sendBundle",
		Params: []FlashbotsBundleParams{
			{
				Txs:         []string{signedTx},
				BlockNumber: fmt.Sprintf("0x%x", getCurrentBlockNumber()+1),
			},
		},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %w", err)
	}

	headers := map[string]string{
		"Content-Type":          "application/json",
		"X-Flashbots-Signature": f.getSignature(reqBody),
	}

	respBody, err := f.restClient.DoPostWithHeaders(f.relayURL, string(reqBody), headers)
	if err != nil {
		return "", fmt.Errorf("send request failed: %w", err)
	}

	var resp FlashbotsBundleResponse
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		return "", fmt.Errorf("decode response failed: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("bundle error: %s", resp.Error.Message)
	}

	if resp.Result == nil {
		return "", fmt.Errorf("empty bundle result")
	}

	return *resp.Result, nil
}

// GetBundleStatus 查询 bundle 状态
func (f *FlashbotsBundler) GetBundleStatus(bundleHash string, chainID string) (string, error) {
	return "pending", nil
}

// SupportsChain 检查是否支持指定的链
func (f *FlashbotsBundler) SupportsChain(chainID string) bool {
	return chainID == "1" // 只支持 Ethereum 主网
}

// GetName 获取 bundler 名称
func (f *FlashbotsBundler) GetName() string {
	return "Flashbots"
}

// getSignature 获取请求签名
func (f *FlashbotsBundler) getSignature(data []byte) string {
	hash := crypto.Keccak256(data)
	signature, _ := crypto.Sign(hash, f.privateKey)
	return hex.EncodeToString(signature)
}

// getCurrentBlockNumber 获取当前区块号（简化实现）
func getCurrentBlockNumber() uint64 {
	return uint64(time.Now().Unix() / 12)
}


// FlashbotsBundleRequest Flashbots bundle 请求
type FlashbotsBundleRequest struct {
	JSONRPC string                   `json:"jsonrpc"`
	ID      int                      `json:"id"`
	Method  string                   `json:"method"`
	Params  []FlashbotsBundleParams  `json:"params"`
}

// FlashbotsBundleParams Flashbots bundle 参数
type FlashbotsBundleParams struct {
	Txs         []string `json:"txs"`
	BlockNumber string   `json:"blockNumber"`
}

// FlashbotsBundleResponse Flashbots bundle 响应
type FlashbotsBundleResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  *string `json:"result,omitempty"`
	Error   *FlashbotsError `json:"error,omitempty"`
}

// FlashbotsError Flashbots 错误
type FlashbotsError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

