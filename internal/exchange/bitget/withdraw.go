package bitget

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
)

var _ exchange.DepositWithdrawProvider = (*bitgetExchange)(nil)
var _ exchange.WithdrawNetworkLister = (*bitgetExchange)(nil)
var _ exchange.DepositNetworkLister = (*bitgetExchange)(nil)

// bitgetWithdrawRecordID 从 V1(id) 或 V2(orderId) 中取提币记录 ID
func bitgetWithdrawRecordID(id, orderID interface{}) string {
	if orderID != nil {
		s := fmt.Sprintf("%v", orderID)
		if s != "" && s != "<nil>" {
			return s
		}
	}
	if id != nil {
		s := fmt.Sprintf("%v", id)
		if s != "" && s != "<nil>" {
			return s
		}
	}
	return ""
}

// parseBitgetWithdrawStatus 解析 Bitget 提币状态：支持数字(0/1/2)和字符串(success/Pending/reject 等)
func parseBitgetWithdrawStatus(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	switch lower {
	case "0", "pending", "pending_review", "wallet_processing", "dealing", "wait_frozen",
		"wait_create_order", "pre_success", "wait_small_check", "first-audit", "recheck",
		"wallet-processing":
		return "PENDING"
	case "1", "success", "completed":
		return "COMPLETED"
	case "2", "cancel", "reject", "rejected", "pending_fail", "pending_review_fail", "wallet_fail",
		"frozen_fail", "first-reject", "recheck-reject", "wallet-fail", "fail", "failed":
		return "FAILED"
	default:
		return "PENDING"
	}
}

// Deposit 获取充币地址
func (b *bitgetExchange) Deposit(asset string, network string) (*model.DepositAddress, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := b.getAPIKeys()
	restClient := b.restClient

	queryString := fmt.Sprintf("coin=%s", asset)
	if network != "" {
		queryString += fmt.Sprintf("&chain=%s", network)
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", "/api/v2/spot/wallet/deposit-address", queryString, "", secretKey)
	apiURL := fmt.Sprintf("%s/api/v2/spot/wallet/deposit-address?%s", constants.BitgetRestBaseUrl, queryString)

	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit address failed: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Coin    string `json:"coin"`
			Chain   string `json:"chain"`
			Address string `json:"address"`
			Tag     string `json:"tag"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit address response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	return &model.DepositAddress{
		Asset:   resp.Data.Coin,
		Address: resp.Data.Address,
		Network: resp.Data.Chain,
		Memo:    resp.Data.Tag,
	}, nil
}

// Withdraw 提币
func (b *bitgetExchange) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("withdraw amount must be positive, got %.8f", req.Amount)
	}

	apiKey, secretKey, passphrase := b.getAPIKeys()
	restClient := b.restClient

	// Bitget v2 API 使用 "size" 而非 "amount"（错误 400172: sizespot.wallet.size.empty）
	amountStr := strconv.FormatFloat(req.Amount, 'f', 8, 64)

	body := map[string]interface{}{
		"coin":         req.Asset,
		"chain":        req.Network,
		"address":      req.Address,
		"size":         amountStr,
		"transferType": "on_chain", // 链上提币，错误 400172: transferType not empty
	}

	if req.Memo != "" {
		body["tag"] = req.Memo
	}

	jsonBody, _ := json.Marshal(body)
	requestBody := string(jsonBody)

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "POST", "/api/v2/spot/wallet/withdrawal", "", requestBody, secretKey)
	apiURL := fmt.Sprintf("%s/api/v2/spot/wallet/withdrawal", constants.BitgetRestBaseUrl)

	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	responseBody, err := restClient.DoPostWithHeaders(apiURL, requestBody, headers)
	if err != nil {
		return nil, fmt.Errorf("withdraw failed: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ID      interface{} `json:"id"`      // 部分版本可能返回 id
			OrderID interface{} `json:"orderId"` // 可能是 string 或 number
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	orderIDStr := bitgetWithdrawRecordID(resp.Data.ID, resp.Data.OrderID)
	if orderIDStr == "" {
		return nil, fmt.Errorf("bitget withdraw response missing orderId/id, response: %s", responseBody)
	}

	return &model.WithdrawResponse{
		WithdrawID: orderIDStr,
		Status:     "PENDING",
		CreateTime: time.Now(),
	}, nil
}

// GetDepositHistory 查询充币记录
func (b *bitgetExchange) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // Bitget 最大限制
	}

	queryString := fmt.Sprintf("limit=%d", limit)
	if asset != "" {
		queryString += fmt.Sprintf("&coin=%s", asset)
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", "/api/v2/spot/wallet/deposit-records", queryString, "", secretKey)
	apiURL := fmt.Sprintf("%s/api/v2/spot/wallet/deposit-records?%s", constants.BitgetRestBaseUrl, queryString)

	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit history failed: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Coin        string `json:"coin"`
			Chain       string `json:"chain"`
			Amount      string `json:"amount"`
			TxID        string `json:"txId"`
			Status      string `json:"status"` // 0:待确认, 1:已确认, 2:失败
			ToAddress   string `json:"toAddress"`
			Tag         string `json:"tag"`
			DepositFee  string `json:"depositFee"`
			SuccessTime string `json:"successTime"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit history response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	records := make([]model.DepositRecord, 0, len(resp.Data))
	for _, r := range resp.Data {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		switch r.Status {
		case "0":
			status = "PENDING"
		case "1":
			status = "COMPLETED"
		case "2":
			status = "FAILED"
		}

		createTime := time.Now()
		if r.SuccessTime != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z07:00", r.SuccessTime); err == nil {
				createTime = t
			} else if t, err := time.Parse("2006-01-02 15:04:05", r.SuccessTime); err == nil {
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
func (b *bitgetExchange) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // Bitget 最大限制
	}

	queryString := fmt.Sprintf("limit=%d", limit)
	if asset != "" {
		queryString += fmt.Sprintf("&coin=%s", asset)
	}

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", "/api/v2/spot/wallet/withdrawal-records", queryString, "", secretKey)
	apiURL := fmt.Sprintf("%s/api/v2/spot/wallet/withdrawal-records?%s", constants.BitgetRestBaseUrl, queryString)

	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get withdraw history failed: %w", err)
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			ID           interface{} `json:"id"`      // V1 Get Withdraw List 使用 id
			OrderID      interface{} `json:"orderId"`  // V2 withdrawal-records 可能使用 orderId
			Coin         string      `json:"coin"`
			Chain        string      `json:"chain"`
			Amount       string      `json:"amount"`
			TxID         string      `json:"txId"`
			Status       string      `json:"status"` // 0/1/2 或 success/Pending/reject 等
			ToAddress    string      `json:"toAddress"`
			Tag          string      `json:"tag"`
			WithdrawFee  string      `json:"withdrawFee"`
			CreateTime   string      `json:"createTime"`
			UpdateTime   string      `json:"updateTime"`
			CTime        string      `json:"cTime"` // V1 使用 cTime
			UTime        string      `json:"uTime"` // V1 使用 uTime
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw history response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	records := make([]model.WithdrawRecord, 0, len(resp.Data))
	for _, r := range resp.Data {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := parseBitgetWithdrawStatus(r.Status)

		createTime := time.Now()
		createTimeStr := r.CreateTime
		if createTimeStr == "" {
			createTimeStr = r.CTime // V1 使用 cTime
		}
		if createTimeStr != "" {
			if t, err := time.Parse("2006-01-02T15:04:05Z07:00", createTimeStr); err == nil {
				createTime = t
			} else if t, err := time.Parse("2006-01-02 15:04:05", createTimeStr); err == nil {
				createTime = t
			} else if ms, err := strconv.ParseInt(createTimeStr, 10, 64); err == nil && ms > 0 {
				createTime = time.UnixMilli(ms) // V1 cTime 为毫秒时间戳
			}
		}

		orderIDStr := bitgetWithdrawRecordID(r.ID, r.OrderID)
		if orderIDStr == "" {
			continue // 无法匹配的无效记录跳过
		}
		records = append(records, model.WithdrawRecord{
			WithdrawID: orderIDStr,
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

// bitgetNetworkToChainID Bitget API 返回的 network 名 → 链 ID（与跨链协议一致）
var bitgetNetworkToChainID = map[string]string{
	"ETH":          "1",
	"ERC20":        "1",
	"BSC":          "56",
	"BEP20":        "56",
	"BNB":          "56",
	"MATIC":        "137",
	"POLYGON":      "137",
	"ARBITRUM":     "42161",
	"ARB":          "42161",
	"ARBITRUMONE":  "42161",
	"OPTIMISM":     "10",
	"OP":           "10",
	"AVAX":         "43114",
	"AVAXC":        "43114",
	"AVAXC-CHAIN":  "43114",
	"BASE":         "8453",
	"FTM":          "250",
	"LINEA":        "59144",
	"SCROLL":       "534352",
	"MANTLE":       "5000",
	"ZKSYNC":       "324",
}

// GetWithdrawNetworks 查询某资产在 Bitget 支持的提现网络（GET /api/v2/spot/public/coins）
func (b *bitgetExchange) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	coins, err := b.queryBitgetCoins(asset)
	if err != nil {
		return nil, err
	}

	var out []model.WithdrawNetworkInfo
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	for _, coin := range coins {
		if strings.ToUpper(coin.Coin) != assetUpper {
			continue
		}
		for _, ch := range coin.Chains {
			if ch.Withdrawable != "true" {
				continue
			}
			chainID := bitgetNetworkToChainID[strings.ToUpper(ch.Chain)]
			if chainID == "" && ch.Chain != "" {
				chainID = ch.Chain
			}
			out = append(out, model.WithdrawNetworkInfo{
				Network:         ch.Chain,
				ChainID:         chainID,
				WithdrawEnable:  true,
				WithdrawFee:     ch.WithdrawFee,
				WithdrawMin:     ch.MinWithdrawAmount,
				WithdrawMax:     ch.MaxWithdrawAmount,
				IsDefault:       false,
				ContractAddress: ch.ContractAddress,
			})
		}
		break
	}

	return out, nil
}

// GetDepositNetworks 查询某资产支持的充币网络（仅返回 rechargeable=true 的）
func (b *bitgetExchange) GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	coins, err := b.queryBitgetCoins(asset)
	if err != nil {
		return nil, err
	}

	var out []model.WithdrawNetworkInfo
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	for _, coin := range coins {
		if strings.ToUpper(coin.Coin) != assetUpper {
			continue
		}
		for _, ch := range coin.Chains {
			if ch.Rechargeable != "true" {
				continue
			}
			chainID := bitgetNetworkToChainID[strings.ToUpper(ch.Chain)]
			if chainID == "" && ch.Chain != "" {
				chainID = ch.Chain
			}
			out = append(out, model.WithdrawNetworkInfo{
				Network:         ch.Chain,
				ChainID:         chainID,
				WithdrawEnable:  ch.Withdrawable == "true",
				WithdrawFee:     ch.WithdrawFee,
				WithdrawMin:     ch.MinDepositAmount,
				IsDefault:       false,
				ContractAddress: ch.ContractAddress,
			})
		}
		break
	}

	return out, nil
}

// bitgetCoinInfo 对应 /api/v2/spot/public/coins 返回的单个币种
type bitgetCoinInfo struct {
	Coin   string            `json:"coin"`
	Chains []bitgetChainInfo `json:"chains"`
}

// bitgetChainInfo 对应 /api/v2/spot/public/coins 返回的单条链信息
type bitgetChainInfo struct {
	Chain              string `json:"chain"`
	NeedTag            string `json:"needTag"`
	Withdrawable       string `json:"withdrawable"`
	Rechargeable       string `json:"rechargeable"`
	WithdrawFee        string `json:"withdrawFee"`
	ExtraWithdrawFee   string `json:"extraWithdrawFee"`
	DepositConfirm     string `json:"depositConfirm"`
	WithdrawConfirm    string `json:"withdrawConfirm"`
	MinDepositAmount   string `json:"minDepositAmount"`
	MinWithdrawAmount  string `json:"minWithdrawAmount"`
	MaxWithdrawAmount  string `json:"maxWithdrawAmount"`
	ContractAddress    string `json:"contractAddress"`
	BrowserUrl         string `json:"browserUrl"`
}

// queryBitgetCoins 查询 Bitget 公共端点获取币种信息（含链列表）
func (b *bitgetExchange) queryBitgetCoins(asset string) ([]bitgetCoinInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	restClient := b.restClient
	apiKey, secretKey, passphrase := b.getAPIKeys()

	queryString := fmt.Sprintf("coin=%s", asset)
	const apiPath = "/api/v2/spot/public/coins"

	timestamp := getCurrentTimestamp()
	signature := signRequest(timestamp, "GET", apiPath, queryString, "", secretKey)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BitgetRestBaseUrl, apiPath, queryString)

	headers := buildHeaders(apiKey, signature, timestamp, passphrase)
	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get coins failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string           `json:"code"`
		Msg  string           `json:"msg"`
		Data []bitgetCoinInfo `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse coins response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "00000" {
		return nil, fmt.Errorf("bitget API error: %s", responseBody)
	}

	return resp.Data, nil
}
