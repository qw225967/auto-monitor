package bundler

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"auto-arbitrage/internal/utils/rest"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// FortyEightClubBundler 48club bundler 实现
type FortyEightClubBundler struct {
	apiURL       string
	apiKey       string
	soulPointKey *ecdsa.PrivateKey
	restClient   rest.RestClient
}

// NewFortyEightClubBundler 创建新的 48club bundler 实例
func NewFortyEightClubBundler(apiKey string, apiURL string, soulPointPrivateKey string) (*FortyEightClubBundler, error) {
	if apiURL == "" {
		apiURL = "https://puissant-builder.48.club/"
	}

	restClient := rest.RestClient{}
	restClient.InitRestClient()

	bundler := &FortyEightClubBundler{
		apiURL:     apiURL,
		apiKey:     apiKey,
		restClient: restClient,
	}

	if soulPointPrivateKey != "" {
		privateKeyHex := cleanHexPrefix(soulPointPrivateKey)
		privateKey, err := crypto.HexToECDSA(privateKeyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid 48SoulPoint private key: %w", err)
		}
		bundler.soulPointKey = privateKey
	}

	return bundler, nil
}

// SendBundle 发送交易 bundle 到 48club
func (f *FortyEightClubBundler) SendBundle(signedTx string, chainID string) (string, error) {
	if chainID != "1" && chainID != "56" {
		return "", fmt.Errorf("48club only supports Ethereum (1) and BSC (56), got %s", chainID)
	}

	bundleParams := FortyEightClubBundleParams{
		Txs: []string{signedTx},
	}

	if f.soulPointKey != nil {
		signature, err := f.sign48SoulPointMember([]string{signedTx})
		if err != nil {
			return "", fmt.Errorf("sign 48SoulPoint failed: %w", err)
		}
		signatureHex := "0x" + hex.EncodeToString(signature)
		bundleParams.FortyEightSPSign = &signatureHex
	}

	request := FortyEightClubJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      "1",
		Method:  "eth_sendBundle",
		Params:  []FortyEightClubBundleParams{bundleParams},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %w", err)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if f.apiKey != "" {
		headers["X-API-Key"] = f.apiKey
	}

	respBody, err := f.restClient.DoPostWithHeaders(f.apiURL, string(reqBody), headers)
	if err != nil {
		return "", fmt.Errorf("send request failed: %w", err)
	}

	// 记录 bundler 响应（用于调试）
	fmt.Printf("[48club-bundler] Request URL: %s\n", f.apiURL)
	fmt.Printf("[48club-bundler] Response body: %s\n", respBody)

	var resp FortyEightClubJSONRPCResponse
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		return "", fmt.Errorf("decode response failed: %w", err)
	}

	if resp.Error != nil {
		fmt.Printf("[48club-bundler] Error response: code=%d, message=%s\n", resp.Error.Code, resp.Error.Message)
		return "", fmt.Errorf("bundle error: code=%d, message=%s", resp.Error.Code, resp.Error.Message)
	}

	if resp.Result == "" {
		return "", fmt.Errorf("empty bundle result")
	}

	fmt.Printf("[48club-bundler] Bundle sent successfully, bundleHash/result: %s\n", resp.Result)
	return resp.Result, nil
}

// GetBundleStatus 查询 bundle 状态
func (f *FortyEightClubBundler) GetBundleStatus(bundleHash string, chainID string) (string, error) {
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "1",
		"method":  "eth_getBundleStatus",
		"params":  []string{bundleHash},
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("marshal request failed: %w", err)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if f.apiKey != "" {
		headers["X-API-Key"] = f.apiKey
	}

	respBody, err := f.restClient.DoPostWithHeaders(f.apiURL, string(reqBody), headers)
	if err != nil {
		return "", fmt.Errorf("send request failed: %w", err)
	}

	var resp FortyEightClubJSONRPCResponse
	if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
		return "", fmt.Errorf("decode response failed: %w", err)
	}

	if resp.Error != nil {
		return "", fmt.Errorf("status query error: code=%d, message=%s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// SupportsChain 检查是否支持指定的链
func (f *FortyEightClubBundler) SupportsChain(chainID string) bool {
	return chainID == "1" || chainID == "56"
}

// GetName 获取 bundler 名称
func (f *FortyEightClubBundler) GetName() string {
	return "48club"
}

// FortyEightClubJSONRPCRequest 48club JSON-RPC 请求
type FortyEightClubJSONRPCRequest struct {
	JSONRPC string                      `json:"jsonrpc"`
	ID      string                      `json:"id"`
	Method  string                      `json:"method"`
	Params  []FortyEightClubBundleParams `json:"params"`
}

// FortyEightClubBundleParams 48club bundle 参数
type FortyEightClubBundleParams struct {
	Txs              []string `json:"txs"`
	BackrunTarget    *string  `json:"backrunTarget,omitempty"`
	MaxBlockNumber    *uint64  `json:"maxBlockNumber,omitempty"`
	MaxTimestamp      *uint64  `json:"maxTimestamp,omitempty"`
	RevertingTxHashes []string `json:"revertingTxHashes,omitempty"`
	NoMerge           *bool    `json:"noMerge,omitempty"`
	NoTail            *bool    `json:"noTail,omitempty"`
	FortyEightSPSign  *string  `json:"48spSign,omitempty"`
}

// FortyEightClubJSONRPCResponse 48club JSON-RPC 响应
type FortyEightClubJSONRPCResponse struct {
	JSONRPC string                      `json:"jsonrpc"`
	ID      string                      `json:"id"`
	Result  string                      `json:"result,omitempty"` // bundle hash
	Error   *FortyEightClubError        `json:"error,omitempty"`
}

// FortyEightClubError 48club 错误响应
type FortyEightClubError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// sign48SoulPointMember 为 48SoulPoint 成员签名
func (f *FortyEightClubBundler) sign48SoulPointMember(signedTxs []string) ([]byte, error) {
	if f.soulPointKey == nil {
		return nil, fmt.Errorf("48SoulPoint private key not set")
	}

	var hashes bytes.Buffer
	hashes.Grow(common.HashLength * len(signedTxs))

	for _, signedTxHex := range signedTxs {
		txBytes := common.FromHex(signedTxHex)
		var tx types.Transaction
		if err := rlp.DecodeBytes(txBytes, &tx); err != nil {
			return nil, fmt.Errorf("decode transaction failed: %w", err)
		}
		hashes.Write(tx.Hash().Bytes())
	}

	hashToSign := crypto.Keccak256(hashes.Bytes())
	signature, err := crypto.Sign(hashToSign, f.soulPointKey)
	if err != nil {
		return nil, fmt.Errorf("sign failed: %w", err)
	}

	return signature, nil
}

