package gate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
)

var _ exchange.DepositWithdrawProvider = (*gateExchange)(nil)
var _ exchange.WithdrawNetworkLister = (*gateExchange)(nil)
var _ exchange.DepositNetworkLister = (*gateExchange)(nil)

// Deposit 获取充币地址
func (g *gateExchange) Deposit(asset string, network string) (*model.DepositAddress, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	queryString := fmt.Sprintf("currency=%s", asset)
	if network != "" {
		queryString += fmt.Sprintf("&chain=%s", network)
	}

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/deposit_address"
	signature := signRequest("GET", url, queryString, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, url, queryString)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit address failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Currency string `json:"currency"`
		Chain    string `json:"chain"`
		Address  string `json:"address"`
		MultichainAddresses []struct {
			Chain   string `json:"chain"`
			Address string `json:"address"`
		} `json:"multichain_addresses"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit address failed: %w, response: %s", err, responseBody)
	}

	return &model.DepositAddress{
		Asset:   resp.Currency,
		Address: resp.Address,
		Network: resp.Chain,
		Memo:    "", // Gate.io 通常不需要 memo
	}, nil
}

// Withdraw 提币
func (g *gateExchange) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	body := map[string]interface{}{
		"currency": req.Asset,
		"chain":    req.Network,
		"address":  req.Address,
		"amount":   strconv.FormatFloat(req.Amount, 'f', -1, 64),
	}

	if req.Memo != "" {
		body["memo"] = req.Memo
	}

	jsonBody, _ := json.Marshal(body)
	payloadString := string(jsonBody)

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/withdrawals"
	queryString := ""
	signature := signRequest("POST", url, queryString, payloadString, secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s", constants.GateRestBaseUrl, url)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoPostWithHeaders(apiURL, payloadString, headers)
	if err != nil {
		return nil, fmt.Errorf("withdraw failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		ID        string `json:"id"`
		Currency  string `json:"currency"`
		Chain     string `json:"chain"`
		Amount    string `json:"amount"`
		Address   string `json:"address"`
		Status    string `json:"status"`
		CreatedAt int64  `json:"created_at"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw response failed: %w, response: %s", err, responseBody)
	}

	status := "PENDING"
	switch resp.Status {
	case "DONE":
		status = "COMPLETED"
	case "CANCEL", "FAIL":
		status = "FAILED"
	default:
		status = "PENDING"
	}

	createTime := time.Now()
	if resp.CreatedAt > 0 {
		createTime = time.Unix(resp.CreatedAt, 0)
	}

	return &model.WithdrawResponse{
		WithdrawID: resp.ID,
		Status:     status,
		CreateTime: createTime,
	}, nil
}

// GetDepositHistory 查询充币记录
func (g *gateExchange) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // Gate.io 最大限制
	}

	queryString := fmt.Sprintf("limit=%d", limit)
	if asset != "" {
		queryString += fmt.Sprintf("&currency=%s", asset)
	}

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/deposits"
	signature := signRequest("GET", url, queryString, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, url, queryString)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		ID        string `json:"id"`
		Currency  string `json:"currency"`
		Chain     string `json:"chain"`
		Amount    string `json:"amount"`
		TxID      string `json:"txid"`
		Status    string `json:"status"` // DONE, PENDING, FAIL
		Address   string `json:"address"`
		CreatedAt int64  `json:"created_at"`
		UpdatedAt int64  `json:"updated_at"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit history failed: %w, response: %s", err, responseBody)
	}

	records := make([]model.DepositRecord, 0, len(resp))
	for _, r := range resp {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		switch r.Status {
		case "DONE":
			status = "COMPLETED"
		case "FAIL":
			status = "FAILED"
		default:
			status = "PENDING"
		}

		createTime := time.Now()
		if r.CreatedAt > 0 {
			createTime = time.Unix(r.CreatedAt, 0)
		}

		records = append(records, model.DepositRecord{
			TxHash:     r.TxID,
			Asset:      r.Currency,
			Amount:     amount,
			Network:    r.Chain,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// GetWithdrawHistory 查询提币记录
func (g *gateExchange) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // Gate.io 最大限制
	}

	queryString := fmt.Sprintf("limit=%d", limit)
	if asset != "" {
		queryString += fmt.Sprintf("&currency=%s", asset)
	}

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/withdrawals"
	signature := signRequest("GET", url, queryString, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, url, queryString)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get withdraw history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		ID        string `json:"id"`
		Currency  string `json:"currency"`
		Chain     string `json:"chain"`
		Amount    string `json:"amount"`
		TxID      string `json:"txid"`
		Status    string `json:"status"` // DONE, PENDING, CANCEL, FAIL
		Address   string `json:"address"`
		Memo      string `json:"memo"`
		CreatedAt int64  `json:"created_at"`
		UpdatedAt int64  `json:"updated_at"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw history failed: %w, response: %s", err, responseBody)
	}

	records := make([]model.WithdrawRecord, 0, len(resp))
	for _, r := range resp {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		switch r.Status {
		case "DONE":
			status = "COMPLETED"
		case "CANCEL", "FAIL":
			status = "FAILED"
		default:
			status = "PENDING"
		}

		createTime := time.Now()
		if r.CreatedAt > 0 {
			createTime = time.Unix(r.CreatedAt, 0)
		}

		records = append(records, model.WithdrawRecord{
			WithdrawID: r.ID,
			TxHash:     r.TxID,
			Asset:      r.Currency,
			Amount:     amount,
			Network:    r.Chain,
			Address:    r.Address,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// gateNetworkToChainID Gate.io API 返回的 chain 名 → 链 ID（与跨链协议一致）
var gateNetworkToChainID = map[string]string{
	"ETH":      "1",
	"ERC20":    "1",
	"BSC":      "56",
	"BEP20":    "56",
	"BNB":      "56",
	"MATIC":    "137",
	"POLYGON":  "137",
	"ARBITRUM": "42161",
	"ARB":      "42161",
	"ARBEVM":   "42161",
	"OPTIMISM": "10",
	"OP":       "10",
	"OPETH":    "10",
	"AVAX":     "43114",
	"AVAXC":    "43114",
	"AVAX_C":   "43114",
	"BASE":     "8453",
	"FTM":      "250",
	"LINEA":    "59144",
	"SCROLL":   "534352",
	"MANTLE":   "5000",
	"ZKSYNC":   "324",
}

// GetWithdrawNetworks 查询某资产在 Gate.io 支持的提现网络（GET /api/v4/wallet/currency_chains）
func (g *gateExchange) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	queryString := fmt.Sprintf("currency=%s", asset)

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/currency_chains"
	signature := signRequest("GET", url, queryString, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, url, queryString)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get currency chains failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		Chain              string `json:"chain"`
		NameCn             string `json:"name_cn"`
		NameEn             string `json:"name_en"`
		ContractAddress    string `json:"contract_address"`
		IsDisabled         int    `json:"is_disabled"`
		IsDepositDisabled  int    `json:"is_deposit_disabled"`
		IsWithdrawDisabled int    `json:"is_withdraw_disabled"`
		WithdrawFee        string `json:"withdraw_fee"`
		MinWithdrawAmount  string `json:"min_withdraw_amount"`
		MaxWithdrawAmount  string `json:"max_withdraw_amount"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse currency chains failed: %w, response: %s", err, responseBody)
	}

	var out []model.WithdrawNetworkInfo
	for _, chain := range resp {
		// 只返回支持提币的链
		if chain.IsWithdrawDisabled == 1 || chain.IsDisabled == 1 {
			continue
		}

		chainName := strings.ToUpper(chain.Chain)
		chainID := gateNetworkToChainID[chainName]
		
		// 如果映射表中没有，尝试使用 chain 名称本身
		if chainID == "" && chain.Chain != "" {
			chainID = chain.Chain
		}

		out = append(out, model.WithdrawNetworkInfo{
			Network:         chain.Chain,
			ChainID:         chainID,
			WithdrawEnable:  true,
			WithdrawFee:     chain.WithdrawFee,
			WithdrawMin:     chain.MinWithdrawAmount,
			WithdrawMax:     chain.MaxWithdrawAmount,
			IsDefault:       false,
			ContractAddress: chain.ContractAddress,
		})
	}

	return out, nil
}

// GetDepositNetworks 查询某资产支持的充币网络（仅返回 is_deposit_disabled=0 的）
func (g *gateExchange) GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := g.getAPIKeys()
	restClient := g.restClient

	queryString := fmt.Sprintf("currency=%s", asset)

	timestamp := getCurrentTimestamp()
	url := "/api/v4/wallet/currency_chains"
	signature := signRequest("GET", url, queryString, "", secretKey, timestamp)
	apiURL := fmt.Sprintf("%s%s?%s", constants.GateRestBaseUrl, url, queryString)

	headers := buildHeaders(apiKey, signature, timestamp)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get currency chains failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		Chain              string `json:"chain"`
		IsDisabled         int    `json:"is_disabled"`
		IsDepositDisabled  int    `json:"is_deposit_disabled"`
		IsWithdrawDisabled int    `json:"is_withdraw_disabled"`
		WithdrawFee        string `json:"withdraw_fee"`
		MinDepositAmount   string `json:"min_deposit_amount"`
		MinWithdrawAmount  string `json:"min_withdraw_amount"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse currency chains failed: %w, response: %s", err, responseBody)
	}

	var out []model.WithdrawNetworkInfo
	for _, chain := range resp {
		if chain.IsDepositDisabled == 1 || chain.IsDisabled == 1 {
			continue
		}

		chainName := strings.ToUpper(chain.Chain)
		chainID := gateNetworkToChainID[chainName]
		if chainID == "" && chain.Chain != "" {
			chainID = chain.Chain
		}

		out = append(out, model.WithdrawNetworkInfo{
			Network:        chain.Chain,
			ChainID:        chainID,
			WithdrawEnable: chain.IsWithdrawDisabled == 0,
			WithdrawFee:    chain.WithdrawFee,
			WithdrawMin:    chain.MinDepositAmount,
			IsDefault:      false,
		})
	}

	return out, nil
}
