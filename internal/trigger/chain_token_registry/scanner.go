package chain_token_registry

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/utils/logger"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var erc20ABI *abi.ABI
var erc20ABIOnce sync.Once

func getERC20ABI() *abi.ABI {
	erc20ABIOnce.Do(func() {
		const abiJSON = `[
			{"inputs":[],"name":"name","outputs":[{"type":"string"}],"stateMutability":"view","type":"function"},
			{"inputs":[],"name":"symbol","outputs":[{"type":"string"}],"stateMutability":"view","type":"function"},
			{"inputs":[],"name":"decimals","outputs":[{"type":"uint8"}],"stateMutability":"view","type":"function"},
			{"inputs":[],"name":"totalSupply","outputs":[{"type":"uint256"}],"stateMutability":"view","type":"function"}
		]`
		parsed, err := abi.JSON(strings.NewReader(abiJSON))
		if err == nil {
			erc20ABI = &parsed
		}
	})
	return erc20ABI
}

// ERC20Info RPC 链上验证获取的 ERC-20 合约信息
type ERC20Info struct {
	Address     string
	Name        string
	Symbol      string
	Decimals    int
	TotalSupply *big.Int
	HasCode     bool
}

// VerifyERC20 通过 RPC 验证指定链上的地址是否为有效的 ERC-20 合约，并返回合约信息。
func VerifyERC20(chainID, address string) (*ERC20Info, error) {
	client, err := getChainClient(chainID)
	if err != nil {
		return nil, fmt.Errorf("rpc connect chain %s: %w", chainID, err)
	}

	addr := common.HexToAddress(address)
	info := &ERC20Info{Address: address}

	code, err := client.CodeAt(context.Background(), addr, nil)
	if err != nil {
		return nil, fmt.Errorf("CodeAt: %w", err)
	}
	if len(code) == 0 {
		return info, nil
	}
	info.HasCode = true

	a := getERC20ABI()
	if a == nil {
		return info, fmt.Errorf("ERC20 ABI not available")
	}

	info.Symbol = callStringView(client, addr, a, "symbol")
	info.Name = callStringView(client, addr, a, "name")
	info.Decimals = callUint8View(client, addr, a, "decimals")
	info.TotalSupply = callUint256View(client, addr, a, "totalSupply")

	return info, nil
}

// VerifyAndMatchSymbol 验证地址是 ERC-20 且 symbol 匹配目标
func VerifyAndMatchSymbol(chainID, address, expectedSymbol string) (*ERC20Info, bool) {
	info, err := VerifyERC20(chainID, address)
	if err != nil || info == nil || !info.HasCode {
		return info, false
	}
	if info.Symbol == "" {
		return info, false
	}
	return info, strings.EqualFold(info.Symbol, expectedSymbol)
}

// ScanResult 单条链的扫描结果
type ScanResult struct {
	ChainID  string
	Address  string
	Info     *ERC20Info
	Source   string
	Verified bool
}

// ScanTokenOnChains 对指定 symbol 在给定的 chainIDs 上进行全链扫描，汇总多来源地址并交叉验证。
// exchanges: 可查询充提网络的交易所列表
// walletAddresses: 从 WalletInfo 获取的 per-chain 地址（chainID→address）
// bridgeAddresses: 从桥协议发现的地址（chainID→address）
func ScanTokenOnChains(
	symbol string,
	chainIDs []string,
	exchanges []exchange.Exchange,
	walletAddresses map[string]string,
	bridgeAddresses map[string]string,
) []ScanResult {
	log := logger.GetLoggerInstance().Named("ChainTokenScanner").Sugar()
	log.Infof("Starting scan for %s on %d chains", symbol, len(chainIDs))

	// 收集所有来源的候选地址: chainID → source → address
	type candidate struct {
		address string
		source  string
	}
	candidates := make(map[string][]candidate)

	// 来源 1: 交易所 API（contractAddress 字段 + 无合约的原生代币）
	for _, ex := range exchanges {
		exType := ex.GetType()
		if wl, ok := ex.(exchange.WithdrawNetworkLister); ok {
			networks, err := wl.GetWithdrawNetworks(symbol)
			if err != nil {
				log.Warnf("Exchange %s GetWithdrawNetworks(%s): %v", exType, symbol, err)
				continue
			}
			for _, n := range networks {
				if n.ChainID == "" {
					continue
				}
				addr := strings.ToLower(n.ContractAddress)
				if addr == "" {
					addr = "native"
				}
				candidates[n.ChainID] = append(candidates[n.ChainID], candidate{
					address: addr,
					source:  "exchange-api",
				})
				log.Infof("Exchange %s reported %s on chain %s: %s", exType, symbol, n.ChainID, addr)
			}
		}
	}

	// 来源 2: WalletInfo per-chain 地址
	for chainID, addr := range walletAddresses {
		if addr != "" {
			candidates[chainID] = append(candidates[chainID], candidate{
				address: strings.ToLower(addr),
				source:  "walletinfo",
			})
		}
	}

	// 来源 3: 桥协议发现的地址
	for chainID, addr := range bridgeAddresses {
		if addr != "" {
			candidates[chainID] = append(candidates[chainID], candidate{
				address: strings.ToLower(addr),
				source:  "bridge-verify",
			})
		}
	}

	// 收集所有出现过的链 ID（包括交易所返回的非 EVM 链）
	allChainIDs := make(map[string]bool)
	for _, cid := range chainIDs {
		allChainIDs[cid] = true
	}
	for cid := range candidates {
		allChainIDs[cid] = true
	}

	// 阶段 1: 对每条链的候选地址进行验证 + 交叉比对
	var results []ScanResult
	foundChains := make(map[string]bool)

	for chainID := range allChainIDs {
		cands := candidates[chainID]
		if len(cands) == 0 {
			continue
		}

		// 去重
		seen := make(map[string]bool)
		var uniqueAddrs []candidate
		for _, c := range cands {
			if !seen[c.address] {
				seen[c.address] = true
				uniqueAddrs = append(uniqueAddrs, c)
			}
		}

		isEVMChain := isNumericChainID(chainID)

		for _, c := range uniqueAddrs {
			var info *ERC20Info
			matched := false

			if isEVMChain && c.address != "native" {
				info, matched = VerifyAndMatchSymbol(chainID, c.address, symbol)
				if !matched {
					log.Debugf("Chain %s: address %s failed ERC-20 verification for %s", chainID, c.address, symbol)
					continue
				}
			} else {
				// 非 EVM 链（如 Injective/Solana/Tron）或原生代币：无法 RPC 验证，
				// 来源为 exchange-api/manual 时直接信任
				if c.source != "exchange-api" && c.source != "manual" {
					continue
				}
				matched = true
				info = &ERC20Info{Address: c.address, Symbol: symbol, HasCode: true}
			}

			// 交叉验证：计算有多少不同来源指向同一地址
			sourceCount := 0
			sourceSet := make(map[string]bool)
			for _, other := range cands {
				if strings.EqualFold(other.address, c.address) && !sourceSet[other.source] {
					sourceSet[other.source] = true
					sourceCount++
				}
			}

			verified := sourceCount >= 2 || sourceSet["exchange-api"] || sourceSet["manual"]

			bestSource := c.source
			if sourceSet["exchange-api"] {
				bestSource = "exchange-api"
			}

			results = append(results, ScanResult{
				ChainID:  chainID,
				Address:  c.address,
				Info:     info,
				Source:   bestSource,
				Verified: verified,
			})
			foundChains[chainID] = true

			log.Infof("Chain %s: %s at %s — verified=%v, sources=%d (%v)",
				chainID, symbol, c.address, verified, sourceCount, sourceSet)
			break
		}
	}

	// 阶段 2: RPC 扫链——用已发现的 EVM 合约地址在其他未覆盖的 EVM 链上探测
	// 很多 token 用 create2 或相同 deployer 在多链部署，合约地址相同
	knownEVMAddrs := make(map[string]bool)
	for _, r := range results {
		if isNumericChainID(r.ChainID) && r.Address != "native" && r.Address != "" {
			knownEVMAddrs[r.Address] = true
		}
	}

	if len(knownEVMAddrs) > 0 {
		for _, targetChainID := range chainIDs {
			if !isNumericChainID(targetChainID) || foundChains[targetChainID] {
				continue
			}
			for addr := range knownEVMAddrs {
				info, matched := VerifyAndMatchSymbol(targetChainID, addr, symbol)
				if matched {
					results = append(results, ScanResult{
						ChainID: targetChainID, Address: addr, Info: info,
						Source: "rpc-scan", Verified: false,
					})
					foundChains[targetChainID] = true
					log.Infof("Chain %s: %s at %s — discovered via RPC scan (same-address deployment)",
						targetChainID, symbol, addr)
					break
				}
			}
		}
	}

	return results
}

// ApplyResults 将扫描结果写入 registry 并持久化
func ApplyResults(reg *ChainTokenRegistry, symbol string, results []ScanResult) int {
	count := 0
	for _, r := range results {
		decimals := 18
		if r.Info != nil {
			decimals = r.Info.Decimals
		}
		reg.Upsert(symbol, r.ChainID, &TokenInfo{
			Address:   r.Address,
			Decimals:  decimals,
			Verified:  r.Verified,
			Source:    r.Source,
			UpdatedAt: time.Now(),
		})
		count++
	}
	if count > 0 {
		_ = reg.SaveToFile()
	}
	return count
}

func isNumericChainID(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// --- RPC helpers ---

var (
	clientCache   = make(map[string]*ethclient.Client)
	clientCacheMu sync.Mutex
)

func getChainClient(chainID string) (*ethclient.Client, error) {
	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	if c, ok := clientCache[chainID]; ok && c != nil {
		return c, nil
	}

	urls := constants.GetDefaultRPCURLs(chainID)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no RPC URL for chain %s", chainID)
	}

	var lastErr error
	for _, url := range urls {
		c, err := ethclient.Dial(url)
		if err != nil {
			lastErr = err
			continue
		}
		clientCache[chainID] = c
		return c, nil
	}
	return nil, fmt.Errorf("all RPCs failed for chain %s: %w", chainID, lastErr)
}

func callStringView(client *ethclient.Client, addr common.Address, a *abi.ABI, method string) string {
	data, err := a.Pack(method)
	if err != nil {
		return ""
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil || len(result) == 0 {
		return ""
	}
	outputs, err := a.Unpack(method, result)
	if err != nil || len(outputs) == 0 {
		return ""
	}
	if s, ok := outputs[0].(string); ok {
		return s
	}
	return ""
}

func callUint8View(client *ethclient.Client, addr common.Address, a *abi.ABI, method string) int {
	data, err := a.Pack(method)
	if err != nil {
		return 0
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil || len(result) == 0 {
		return 0
	}
	outputs, err := a.Unpack(method, result)
	if err != nil || len(outputs) == 0 {
		return 0
	}
	if v, ok := outputs[0].(uint8); ok {
		return int(v)
	}
	return 0
}

func callUint256View(client *ethclient.Client, addr common.Address, a *abi.ABI, method string) *big.Int {
	data, err := a.Pack(method)
	if err != nil {
		return nil
	}
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil || len(result) == 0 {
		return nil
	}
	outputs, err := a.Unpack(method, result)
	if err != nil || len(outputs) == 0 {
		return nil
	}
	if v, ok := outputs[0].(*big.Int); ok {
		return v
	}
	return nil
}
