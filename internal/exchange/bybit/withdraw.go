package bybit

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

var _ exchange.DepositWithdrawProvider = (*bybit)(nil)
var _ exchange.WithdrawNetworkLister = (*bybit)(nil)
var _ exchange.DepositNetworkLister = (*bybit)(nil)

// Deposit 获取充币地址
func (b *bybit) Deposit(asset string, network string) (*model.DepositAddress, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"coin": asset,
	}

	if network != "" {
		params["chainType"] = network
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BybitRestBaseUrl, constants.BybitDepositAddressPath, queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit address failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Coin            string `json:"coin"`
			ChainType       string `json:"chainType"`
			AddressDeposit  string `json:"addressDeposit"`
			TagDeposit      string `json:"tagDeposit"`
			Chain           string `json:"chain"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit address response failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	return &model.DepositAddress{
		Asset:   resp.Result.Coin,
		Address: resp.Result.AddressDeposit,
		Network: resp.Result.ChainType,
		Memo:    resp.Result.TagDeposit,
	}, nil
}

// Withdraw 提币
func (b *bybit) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	body := map[string]interface{}{
		"coin":    req.Asset,
		"chain":   req.Network,
		"address": req.Address,
		"amount":  strconv.FormatFloat(req.Amount, 'f', -1, 64),
	}

	if req.Memo != "" {
		body["tag"] = req.Memo
	}

	jsonBody, _ := json.Marshal(body)
	requestBody := string(jsonBody)

	signature := signRequest(timestamp, apiKey, recvWindow, requestBody, secretKey)
	apiURL := fmt.Sprintf("%s%s", constants.BybitRestBaseUrl, constants.BybitWithdrawPath)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoPostWithHeaders(apiURL, requestBody, headers)
	if err != nil {
		return nil, fmt.Errorf("withdraw failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			ID string `json:"id"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw response failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	return &model.WithdrawResponse{
		WithdrawID: resp.Result.ID,
		Status:     "PENDING",
		CreateTime: time.Now(),
	}, nil
}

// GetDepositHistory 查询充币记录
func (b *bybit) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 50 {
		limit = 50 // Bybit 最大限制
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"limit": strconv.Itoa(limit),
	}

	if asset != "" {
		params["coin"] = asset
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BybitRestBaseUrl, constants.BybitDepositHistoryPath, queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Rows []struct {
				Coin          string `json:"coin"`
				Chain         string `json:"chain"`
				Amount        string `json:"amount"`
				TxID          string `json:"txID"`
				Status        int    `json:"status"` // 0:待确认, 1:已确认, 2:失败
				ToAddress     string `json:"toAddress"`
				Tag           string `json:"tag"`
				DepositFee    string `json:"depositFee"`
				SuccessAt     string `json:"successAt"`
				Confirmations string `json:"confirmations"`
				TxIndex       string `json:"txIndex"`
				BlockHeight   string `json:"blockHeight"`
			} `json:"rows"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit history response failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	records := make([]model.DepositRecord, 0, len(resp.Result.Rows))
	for _, r := range resp.Result.Rows {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		switch r.Status {
		case 0:
			status = "PENDING"
		case 1:
			status = "COMPLETED"
		case 2:
			status = "FAILED"
		}

		createTime := time.Now()
		if r.SuccessAt != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z07:00", r.SuccessAt); err == nil {
				createTime = t
			} else if t, err := time.Parse("2006-01-02 15:04:05", r.SuccessAt); err == nil {
				createTime = t
			}
		}

		records = append(records, model.DepositRecord{
			TxHash:     r.TxID,
			Asset:      r.Coin,
			Amount:     amount,
			Network:    r.Chain,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// GetWithdrawHistory 查询提币记录
func (b *bybit) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 50 {
		limit = 50 // Bybit 最大限制
	}

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"limit": strconv.Itoa(limit),
	}

	if asset != "" {
		params["coin"] = asset
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BybitRestBaseUrl, constants.BybitWithdrawHistoryPath, queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get withdraw history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Rows []struct {
				ID            string `json:"id"`
				Coin          string `json:"coin"`
				Chain         string `json:"chain"`
				Amount        string `json:"amount"`
				TxID          string `json:"txID"`
				Status        string `json:"status"` // SecurityCheck, Pending, Success, CancelByUser, Reject, Fail, Blocked
				ToAddress     string `json:"toAddress"`
				Tag           string `json:"tag"`
				WithdrawFee   string `json:"withdrawFee"`
				CreateTime    string `json:"createTime"`
				UpdateTime    string `json:"updateTime"`
			} `json:"rows"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw history response failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	records := make([]model.WithdrawRecord, 0, len(resp.Result.Rows))
	for _, r := range resp.Result.Rows {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		switch r.Status {
		case "SecurityCheck", "Pending":
			status = "PENDING"
		case "Success":
			status = "COMPLETED"
		case "CancelByUser", "Reject", "Fail", "Blocked":
			status = "FAILED"
		}

		createTime := time.Now()
		if r.CreateTime != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z07:00", r.CreateTime); err == nil {
				createTime = t
			} else if t, err := time.Parse("2006-01-02 15:04:05", r.CreateTime); err == nil {
				createTime = t
			}
		}

		records = append(records, model.WithdrawRecord{
			WithdrawID: r.ID,
			TxHash:     r.TxID,
			Asset:      r.Coin,
			Amount:     amount,
			Network:    r.Chain,
			Address:    r.ToAddress,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// bybitNetworkToChainID Bybit API 返回的 chainType 名 → 链 ID（与跨链协议一致）
var bybitNetworkToChainID = map[string]string{
	"ETH":              "1",
	"ERC20":            "1",
	"ETHEREUM":         "1",
	"BSC":              "56",
	"BEP20":            "56",
	"BNB":              "56",
	"BNB SMART CHAIN":  "56",
	"MATIC":            "137",
	"POLYGON":          "137",
	"POLYGON POS":      "137",
	"ARBITRUM":         "42161",
	"ARB":              "42161",
	"ARBITRUM ONE":     "42161",
	"OPTIMISM":         "10",
	"OP":               "10",
	"OP MAINNET":       "10",
	"AVAX":             "43114",
	"AVAXC":            "43114",
	"CAVAX":            "43114",
	"BASE":             "8453",
	"FTM":              "250",
	"FANTOM":           "250",
	"LINEA":            "59144",
	"SCROLL":           "534352",
	"MANTLE":           "5000",
	"MANTLE NETWORK":   "5000",
	"ZKSYNC":           "324",
	"ZKSYNC ERA":       "324",
}

// GetWithdrawNetworks 查询某资产在 Bybit 支持的提现网络（GET /v5/asset/coin/query-info）
func (b *bybit) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"coin": asset,
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)
	apiURL := fmt.Sprintf("%s/v5/asset/coin/query-info?%s", constants.BybitRestBaseUrl, queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get coin info failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Rows []struct {
				Name         string `json:"name"`
				Coin         string `json:"coin"`
				RemainAmount string `json:"remainAmount"`
			Chains       []struct {
				ChainType         string `json:"chainType"`
				Chain             string `json:"chain"`
				Confirmation      string `json:"confirmation"`
				WithdrawFee       string `json:"withdrawFee"`
				DepositMin        string `json:"depositMin"`
				WithdrawMin       string `json:"withdrawMin"`
				MinAccuracy       string `json:"minAccuracy"`
				ChainDeposit      string `json:"chainDeposit"`   // "1" 表示支持充币
				ChainWithdraw     string `json:"chainWithdraw"`  // "1" 表示支持提币
				WithdrawPercentage string `json:"withdrawPercentage"`
				WithdrawChainId   string `json:"withdrawChainId"`
				ChainId           string `json:"chainId"`
				ContractAddress   string `json:"contractAddress"`
			} `json:"chains"`
			} `json:"rows"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse coin info failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	var out []model.WithdrawNetworkInfo
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))

	for _, row := range resp.Result.Rows {
		if strings.ToUpper(row.Coin) != assetUpper {
			continue
		}

		for _, chain := range row.Chains {
			// 只返回支持提币的链
			if chain.ChainWithdraw != "1" {
				continue
			}

			chainType := strings.ToUpper(strings.TrimSpace(chain.ChainType))
			chainID := bybitNetworkToChainID[chainType]
			if chainID == "" {
				if idx := strings.Index(chainType, "("); idx > 0 {
					chainID = bybitNetworkToChainID[strings.TrimSpace(chainType[:idx])]
				}
			}
			if chainID == "" {
				if chain.ChainId != "" {
					chainID = chain.ChainId
				} else if chain.WithdrawChainId != "" {
					chainID = chain.WithdrawChainId
				} else if chainType != "" {
					chainID = chainType
				}
			}

			// 解析最小/最大提币量（Bybit 可能不提供最大提币量，使用 remainAmount 作为参考）
			withdrawMin := chain.WithdrawMin
			withdrawMax := ""
			if row.RemainAmount != "" {
				withdrawMax = row.RemainAmount
			}

			out = append(out, model.WithdrawNetworkInfo{
				Network:         chain.ChainType,
				ChainID:         chainID,
				WithdrawEnable:  true,
				WithdrawFee:     chain.WithdrawFee,
				WithdrawMin:     withdrawMin,
				WithdrawMax:     withdrawMax,
				IsDefault:       false,
				ContractAddress: chain.ContractAddress,
			})
		}
		break
	}

	return out, nil
}

// GetDepositNetworks 查询某资产支持的充币网络（仅返回 chainDeposit="1" 的）
func (b *bybit) GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	recvWindow := "5000"

	params := map[string]string{
		"coin": asset,
	}

	queryString := buildQueryString(params)
	signature := signRequest(timestamp, apiKey, recvWindow, queryString, secretKey)
	apiURL := fmt.Sprintf("%s/v5/asset/coin/query-info?%s", constants.BybitRestBaseUrl, queryString)

	headers := buildHeaders(apiKey, timestamp, signature, recvWindow)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get coin info failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		RetCode int    `json:"retCode"`
		RetMsg  string `json:"retMsg"`
		Result  struct {
			Rows []struct {
				Name         string `json:"name"`
				Coin         string `json:"coin"`
				Chains       []struct {
					ChainType     string `json:"chainType"`
					Chain         string `json:"chain"`
					ChainDeposit  string `json:"chainDeposit"`
					ChainWithdraw string `json:"chainWithdraw"`
					WithdrawFee   string `json:"withdrawFee"`
					DepositMin    string `json:"depositMin"`
					WithdrawMin   string `json:"withdrawMin"`
					WithdrawChainId string `json:"withdrawChainId"`
					ChainId       string `json:"chainId"`
				} `json:"chains"`
			} `json:"rows"`
		} `json:"result"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse coin info failed: %w, response: %s", err, responseBody)
	}

	if resp.RetCode != 0 {
		return nil, fmt.Errorf("bybit API error: code=%d, msg=%s", resp.RetCode, resp.RetMsg)
	}

	var out []model.WithdrawNetworkInfo
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))

	for _, row := range resp.Result.Rows {
		if strings.ToUpper(row.Coin) != assetUpper {
			continue
		}
		for _, chain := range row.Chains {
			if chain.ChainDeposit != "1" {
				continue
			}

			chainType := strings.ToUpper(strings.TrimSpace(chain.ChainType))
			chainID := bybitNetworkToChainID[chainType]
			if chainID == "" {
				if idx := strings.Index(chainType, "("); idx > 0 {
					chainID = bybitNetworkToChainID[strings.TrimSpace(chainType[:idx])]
				}
			}
			if chainID == "" {
				if chain.ChainId != "" {
					chainID = chain.ChainId
				} else if chain.WithdrawChainId != "" {
					chainID = chain.WithdrawChainId
				} else if chainType != "" {
					chainID = chainType
				}
			}

			out = append(out, model.WithdrawNetworkInfo{
				Network:        chain.ChainType,
				ChainID:        chainID,
				WithdrawEnable: chain.ChainWithdraw == "1",
				WithdrawFee:    chain.WithdrawFee,
				WithdrawMin:    chain.DepositMin,
				IsDefault:      false,
			})
		}
		break
	}

	return out, nil
}
