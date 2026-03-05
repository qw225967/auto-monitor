package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func debugLogOFTRegistry(location, message string, data map[string]interface{}, hypothesisId string) {}

// LayerZero OFT List API 文档: https://docs.layerzero.network/v2/tools/api/oft-reference
// Base URL: https://metadata.layerzero-api.com/v1/metadata/experiment/ofts
// GET /list 支持查询参数: chainNames, symbols, protocols, contractAddresses（逗号分隔）

// LayerZeroOFTListURL 官方 OFT 列表 API 地址（List 接口无需 API Key）
const LayerZeroOFTListURL = "https://metadata.layerzero-api.com/v1/metadata/experiment/ofts/list"

// lzOFTListResponse 对应 API 返回：map[symbol][]lzOFTItem
type lzOFTListResponse map[string][]lzOFTItem

type lzOFTItem struct {
	Name             string                   `json:"name"`
	SharedDecimals   int                      `json:"sharedDecimals"`
	EndpointVersion  string                   `json:"endpointVersion"`
	Deployments      map[string]lzDeployment  `json:"deployments"`
}

type lzDeployment struct {
	Address       string `json:"address"`
	LocalDecimals int    `json:"localDecimals"`
	Type          string `json:"type"` // OFT, OFT_ADAPTER 等
}

// LayerZeroChainNameToID 官方 API 使用链名称，本地使用 chainId，此处为默认映射
// 参考: https://docs.layerzero.network/v2/deployments/chains
var LayerZeroChainNameToID = map[string]string{
	"ethereum":     "1",
	"bsc":          "56",
	"bnb":          "56",
	"polygon":      "137",
	"arbitrum":     "42161",
	"optimism":     "10",
	"avalanche":    "43114",
	"base":         "8453",
	"fantom":       "250",
	"linea":        "59144",
	"zksync":       "324",
	"zksync-era":   "324",
	"mantle":       "5000",
	"scroll":       "534352",
	"polygon-zkevm":"1101",
	"gnosis":       "100",
	"celo":         "42220",
	"moonbeam":     "1284",
	"kava":         "2222",
}

// nonEVMChainNames 非 EVM 链标记，LayerZero API 可能返回但本系统不支持执行
var nonEVMChainNames = map[string]bool{
	"solana":   true,
	"aptos":    true,
	"sui":      true,
	"tron":     true,
	"sei":      true,
	"injective": true,
}

// IsNonEVMChain 判断链名是否为非 EVM 链（不支持 EVM 合约调用）
func IsNonEVMChain(chainName string) bool {
	return nonEVMChainNames[strings.ToLower(chainName)]
}

// OFTToken 描述一个支持 LayerZero OFT 的代币
type OFTToken struct {
	ChainID                string    `json:"chainId"`                         // 链 ID，如 "1"、"56"
	Symbol                 string    `json:"symbol"`                          // 代币符号，如 "USDT"
	Address                string    `json:"address"`                         // OFT 合约地址（用于 LayerZero 桥接）
	UnderlyingTokenAddress string    `json:"underlyingTokenAddress,omitempty"` // 底层 ERC-20 地址（仅 OFT Adapter 有值，用于 balanceOf 等标准 ERC-20 调用）
	Decimals               int       `json:"decimals"`                        // 精度（可选，用于后续精度换算）
	Enabled                bool      `json:"enabled"`                         // 是否启用
	UpdatedAt              time.Time `json:"updatedAt"`                       // 最近一次刷新时间
	Source                 string    `json:"source"`                          // 数据来源（manual / auto / file:xxx）
}

// GetERC20Address 返回用于 ERC-20 操作（balanceOf、transfer 等）的地址。
// 如果是 OFT Adapter，返回底层 ERC-20 地址；否则返回 OFT 地址本身。
func (t *OFTToken) GetERC20Address() string {
	if t.UnderlyingTokenAddress != "" {
		return t.UnderlyingTokenAddress
	}
	return t.Address
}

// OFTTokenKey 统一的 map key 格式（供 layerzero 等子包使用）
func OFTTokenKey(chainID, symbol string) string {
	return fmt.Sprintf("%s:%s", chainID, symbol)
}

// OFTRegistry 管理一组 OFT 支持列表，便于查询和定时刷新
type OFTRegistry struct {
	mu     sync.RWMutex
	tokens map[string]*OFTToken // key: "chainId:symbol"
}

// NewOFTRegistry 创建一个空的 OFTRegistry
func NewOFTRegistry() *OFTRegistry {
	return &OFTRegistry{
		tokens: make(map[string]*OFTToken),
	}
}

// LoadFromFile 从 JSON 文件加载 OFT 列表（覆盖当前内存中的列表）
//
// JSON 结构示例：
// [
//   {
//     "chainId": "56",
//     "symbol": "USDT",
//     "address": "0x....",
//     "decimals": 18,
//     "enabled": true
//   }
// ]
func (r *OFTRegistry) LoadFromFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve oft registry path failed: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read oft registry file failed: %w", err)
	}

	var list []*OFTToken
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("unmarshal oft registry json failed: %w", err)
	}

	now := time.Now()

	r.mu.Lock()
	defer r.mu.Unlock()

	r.tokens = make(map[string]*OFTToken, len(list))
	for _, t := range list {
		if t == nil {
			continue
		}
		if t.ChainID == "" || t.Symbol == "" || t.Address == "" {
			continue
		}
		if !t.UpdatedAt.IsZero() {
			// 保留文件里写死的时间
		} else {
			t.UpdatedAt = now
		}
		if t.Source == "" {
			t.Source = "file:" + absPath
		}
		key := OFTTokenKey(t.ChainID, t.Symbol)
		r.tokens[key] = t
	}

	return nil
}

// Get 获取指定链和符号的 OFTToken
func (r *OFTRegistry) Get(chainID, symbol string) (*OFTToken, bool) {
	if r == nil {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.tokens[OFTTokenKey(chainID, symbol)]
	if !ok || t == nil || !t.Enabled {
		return nil, false
	}
	return t, true
}

// List 返回当前所有启用的 OFTToken 副本
func (r *OFTRegistry) List() []OFTToken {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]OFTToken, 0, len(r.tokens))
	for _, t := range r.tokens {
		if t == nil || !t.Enabled {
			continue
		}
		result = append(result, *t)
	}
	return result
}

// Upsert 插入或更新单个 OFTToken（用于手动调整或运行时更新）
func (r *OFTRegistry) Upsert(token OFTToken) {
	if r == nil {
		return
	}
	if token.ChainID == "" || token.Symbol == "" || token.Address == "" {
		return
	}
	if token.UpdatedAt.IsZero() {
		token.UpdatedAt = time.Now()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := OFTTokenKey(token.ChainID, token.Symbol)
	// 拷贝一份，避免外部修改
	t := token
	r.tokens[key] = &t
}

// LayerZeroAPIListOpts 调用 LayerZero OFT List API 时的可选参数（对应文档中的 Query Parameters）
type LayerZeroAPIListOpts struct {
	ChainNames        string // 逗号分隔，如 "ethereum,bsc,arbitrum"
	Symbols           string // 逗号分隔，如 "USDT,ZRO"
	Protocols         string // 逗号分隔
	ContractAddresses string // 逗号分隔
}

// LoadFromLayerZeroAPI 从 LayerZero 官方 API 拉取 OFT 列表并合并到当前注册表
// 参考: https://docs.layerzero.network/v2/tools/api/oft-reference
// baseURL 为空时使用 LayerZeroOFTListURL；chainNameToID 为空时使用 LayerZeroChainNameToID
func (r *OFTRegistry) LoadFromLayerZeroAPI(ctx context.Context, baseURL string, opts *LayerZeroAPIListOpts, chainNameToID map[string]string) error {
	if r == nil {
		return fmt.Errorf("OFTRegistry is nil")
	}
	if baseURL == "" {
		baseURL = LayerZeroOFTListURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("parse oft list url: %w", err)
	}
	if u.RawQuery == "" && opts != nil {
		q := u.Query()
		if opts.ChainNames != "" {
			q.Set("chainNames", opts.ChainNames)
		}
		if opts.Symbols != "" {
			q.Set("symbols", opts.Symbols)
		}
		if opts.Protocols != "" {
			q.Set("protocols", opts.Protocols)
		}
		if opts.ContractAddresses != "" {
			q.Set("contractAddresses", opts.ContractAddresses)
		}
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request oft list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oft list api status %d", resp.StatusCode)
	}
	var raw lzOFTListResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return fmt.Errorf("decode oft list: %w", err)
	}

	// #region agent log
	{
		apiSymbols := make([]string, 0, len(raw))
		apiDetail := make(map[string]interface{})
		for sym, items := range raw {
			apiSymbols = append(apiSymbols, sym)
			for idx, item := range items {
				chains := make([]string, 0, len(item.Deployments))
				for cn := range item.Deployments {
					chains = append(chains, cn)
				}
				apiDetail[fmt.Sprintf("%s[%d]", sym, idx)] = map[string]interface{}{"name": item.Name, "chains": chains}
			}
		}
		debugData := map[string]interface{}{"requestURL": u.String(), "statusCode": resp.StatusCode, "returnedSymbols": apiSymbols, "detail": apiDetail}
		debugLogOFTRegistry("oft_registry.go:LoadFromLayerZeroAPI:afterDecode", "API response parsed", debugData, "H1,H2")
	}
	// #endregion

	nameToID := chainNameToID
	if nameToID == nil {
		nameToID = LayerZeroChainNameToID
	}
	now := time.Now()
	for symbol, items := range raw {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		for _, item := range items {
			if item.Deployments == nil {
				continue
			}
			for chainName, dep := range item.Deployments {
				chainName = strings.ToLower(strings.TrimSpace(chainName))
				if chainName == "" || dep.Address == "" {
					continue
				}
			if IsNonEVMChain(chainName) {
				continue
			}
			chainID, ok := nameToID[chainName]
			if !ok {
				chainID = chainName
			}
			// #region agent log
			debugLogOFTRegistry("oft_registry.go:LoadFromLayerZeroAPI:upsert", "Upserting OFT token", map[string]interface{}{
				"chainName": chainName, "chainID": chainID, "mapped": ok, "symbol": symbol, "address": dep.Address,
			}, "H1")
			// #endregion
			t := OFTToken{
				ChainID:   chainID,
				Symbol:    symbol,
				Address:   strings.TrimSpace(dep.Address),
				Decimals:  dep.LocalDecimals,
				Enabled:   true,
				UpdatedAt: now,
				Source:    "layerzero-api",
			}
			r.Upsert(t)
			}
		}
	}
	return nil
}

