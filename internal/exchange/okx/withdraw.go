package okx

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
	"auto-arbitrage/internal/utils/logger"
)

func okxDebugLog(location, message string, data map[string]interface{}, hypothesisId string) {}

var _ exchange.DepositWithdrawProvider = (*okx)(nil)
var _ exchange.WithdrawNetworkLister = (*okx)(nil)
var _ exchange.DepositNetworkLister = (*okx)(nil)

// Deposit 获取充币地址
func (o *okx) Deposit(asset string, network string) (*model.DepositAddress, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := o.getAPIKeys()
	restClient := o.restClient

	// OKX API v5: GET /api/v5/asset/deposit-address
	// 注意：此 API 只接受 ccy 参数，不支持 chain 参数
	// 返回该币种所有支持的链的充币地址列表，需要从返回结果中筛选
	requestPath := "/api/v5/asset/deposit-address"
	params := url.Values{}
	params.Add("ccy", asset)

	queryStr := params.Encode()
	if queryStr != "" {
		requestPath += "?" + queryStr
	}

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	// 添加重试机制，处理网络错误
	maxRetries := 3
	var responseBody string
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		responseBody, err = restClient.DoGetWithHeaders(apiURL, headers)
		if err == nil {
			break
		}

		errStr := err.Error()
		isNetworkError := strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "connection closed") ||
			strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "read tcp") ||
			strings.Contains(errStr, "write tcp")

		if isNetworkError && attempt < maxRetries {
			waitTime := time.Duration(attempt) * time.Second
			log := logger.GetLoggerInstance().Named("okx.deposit").Sugar()
			log.Warnf("Get deposit address failed (attempt %d/%d): %v, retrying after %v...",
				attempt, maxRetries, err, waitTime)
			time.Sleep(waitTime)

			// 重新生成时间戳和签名
			timestamp = GetOKXTimestamp()
			sign = BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
			headers = BuildOKXHeaders(apiKey, sign, timestamp, passphrase)
			continue
		}

		break
	}

	if err != nil {
		return nil, fmt.Errorf("get deposit address failed: %w", err)
	}

	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Addr  string `json:"addr"`
			Ccy   string `json:"ccy"`
			Chain string `json:"chain"`
			Tag   string `json:"tag"`
			Memo  string `json:"memo"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit address response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("no deposit address found for asset %s", asset)
	}

	// 如果指定了 network，从返回列表中筛选匹配的链
	var selectedData *struct {
		Addr  string `json:"addr"`
		Ccy   string `json:"ccy"`
		Chain string `json:"chain"`
		Tag   string `json:"tag"`
		Memo  string `json:"memo"`
	}

	if network != "" {
		// OKX API 返回的 chain 格式为 "{CCY}-{NETWORK}"，如 "USDT-ERC20", "USDT-TRC20"
		// 需要将通用网络名称映射到 OKX chain 格式中的网络部分
		targetNetworkSuffix := mapNetworkToOKXChainSuffix(network)
		expectedChainPrefix := fmt.Sprintf("%s-", strings.ToUpper(asset))

		for i := range resp.Data {
			chain := resp.Data[i].Chain
			// 匹配 chain 字段，支持两种格式：
			// 1. 精确匹配：chain == "{CCY}-{NETWORK}"
			// 2. 前缀匹配：chain 以 "{CCY}-{NETWORK}" 开头（处理带括号的情况，如 "USDT-ERC20" 和 "USDT-X Layer (USDT0)"）
			if strings.HasPrefix(chain, expectedChainPrefix) {
				chainSuffix := chain[len(expectedChainPrefix):]
				// 移除可能的括号内容，如 "X Layer (USDT0)" -> "X Layer"
				if idx := strings.Index(chainSuffix, " ("); idx > 0 {
					chainSuffix = chainSuffix[:idx]
				}
				// 比较网络后缀（不区分大小写）
				if strings.EqualFold(strings.TrimSpace(chainSuffix), strings.TrimSpace(targetNetworkSuffix)) {
					selectedData = &resp.Data[i]
					break
				}
			}
		}
		if selectedData == nil {
			return nil, fmt.Errorf("no deposit address found for asset %s on network %s", asset, network)
		}
	} else {
		// 未指定 network，返回第一个地址（通常是默认链）
		selectedData = &resp.Data[0]
	}

	memo := selectedData.Tag
	if memo == "" {
		memo = selectedData.Memo
	}

	return &model.DepositAddress{
		Asset:   selectedData.Ccy,
		Address: selectedData.Addr,
		Network: selectedData.Chain,
		Memo:    memo,
	}, nil
}

// getFundingBalanceAvail 查询资金账户某币种可用余额（GET /api/v5/asset/balances），调用方需已持锁
func (o *okx) getFundingBalanceAvail(ccy string) (float64, error) {
	requestPath := constants.OkexPathAssetBalances
	if ccy != "" {
		requestPath += "?ccy=" + url.QueryEscape(ccy)
	}
	apiKey, secretKey, passphrase := o.getAPIKeys()
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)
	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	responseBody, err := o.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return 0, fmt.Errorf("get funding balance failed: %w", err)
	}
	if err := CheckOKXAPIError(responseBody); err != nil {
		return 0, err
	}
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Ccy       string `json:"ccy"`
			Bal       string `json:"bal"`
			AvailBal  string `json:"availBal"`
			FrozenBal string `json:"frozenBal"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return 0, fmt.Errorf("parse funding balance response failed: %w, response: %s", err, responseBody)
	}
	if resp.Code != "0" {
		return 0, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}
	ccyUpper := strings.ToUpper(ccy)
	for i := range resp.Data {
		if strings.ToUpper(resp.Data[i].Ccy) == ccyUpper {
			avail, _ := strconv.ParseFloat(resp.Data[i].AvailBal, 64)
			return avail, nil
		}
	}
	return 0, nil
}

// transferTradingToFunding 从交易账户划转到资金账户（POST /api/v5/asset/transfer）
// 官方文档：from/to 为 String，6=资金账户，18=交易账户
func (o *okx) transferTradingToFunding(ccy string, amount float64) error {
	requestPath := constants.OkexPathAssetTransfer
	amtStr := strconv.FormatFloat(amount, 'f', -1, 64)
	bodyMap := map[string]interface{}{
		"ccy":  ccy,
		"amt":  amtStr,
		"from": "18", // 18：交易账户
		"to":   "6",  // 6：资金账户
	}
	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return fmt.Errorf("marshal transfer request failed: %w", err)
	}
	bodyStr := string(bodyBytes)

	apiKey, secretKey, passphrase := o.getAPIKeys()
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "POST", requestPath, bodyStr, secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)
	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	responseBody, err := o.restClient.DoPostWithHeaders(apiURL, bodyStr, headers)
	if err != nil {
		return fmt.Errorf("transfer trading to funding failed: %w", err)
	}
	if err := CheckOKXAPIError(responseBody); err != nil {
		return err
	}
	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return fmt.Errorf("parse transfer response failed: %w, response: %s", err, responseBody)
	}
	if resp.Code != "0" {
		return fmt.Errorf("okx transfer API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// Withdraw 提币（仅从资金账户发起；不足时自动从交易账户划转到资金账户）
func (o *okx) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
	chainBuilt := resolveOKXWithdrawChain(o, req.Asset, req.Network)
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	if req == nil {
		return nil, fmt.Errorf("withdraw request is nil")
	}

	log := logger.GetLoggerInstance().Named("okx.withdraw").Sugar()

	// 1. 查资金账户余额
	fundingAvail, err := o.getFundingBalanceAvail(req.Asset)
	if err != nil {
		log.Errorf("get funding balance failed: %v", err)
		return nil, fmt.Errorf("get funding balance failed: %w", err)
	}
	log.Infow("Funding account balance", "asset", req.Asset, "avail", fundingAvail, "required", req.Amount)
	// 2. 不足则从交易账户划转到资金账户
	if fundingAvail < (req.Amount + 0.5) {
		needTransfer := (req.Amount + 0.5) - fundingAvail
		// 查交易账户该币种可用余额
		tradingDetails, err := o.getAccountBalanceNoLock(req.Asset)
		if err != nil {
			log.Errorf("get trading balance failed: %v", err)
			return nil, fmt.Errorf("get trading balance failed: %w", err)
		}
		var tradingAvail float64
		for i := range tradingDetails {
			if strings.EqualFold(tradingDetails[i].Ccy, req.Asset) {
				tradingAvail = parseFirstNonEmpty(tradingDetails[i].AvailEq, tradingDetails[i].AvailBal, tradingDetails[i].CashBal)
				break
			}
		}
		if tradingAvail < needTransfer {
			return nil, fmt.Errorf("trading account available %.8f %s is less than needed transfer amount %.8f, cannot fund withdrawal",
				tradingAvail, req.Asset, needTransfer)
		}
		log.Infow("Transferring from trading to funding", "asset", req.Asset, "amount", needTransfer)
		if err := o.transferTradingToFunding(req.Asset, needTransfer); err != nil {
			log.Errorf("transfer trading to funding failed: %v", err)
			return nil, fmt.Errorf("transfer trading to funding failed: %w", err)
		}
		log.Infow("Transfer trading to funding succeeded", "asset", req.Asset, "amount", needTransfer)
	}

	apiKey, secretKey, passphrase := o.getAPIKeys()
	restClient := o.restClient

	// OKX API v5: POST /api/v5/asset/withdrawal
	// 请求体格式: amt, dest, ccy, chain, toAddr；特定主体用户需额外提供 rcvrInfo
	// 示例: {"amt":"1","dest":"4","ccy":"BTC","chain":"BTC-Bitcoin","toAddr":"...","rcvrInfo":{...}}
	// chain 必须与 Get Currencies 返回完全一致，否则 51000 Parameter chainName error（尤其 USDT 等多链资产）
	requestPath := "/api/v5/asset/withdrawal"

	// 构建请求体（按协议字段：amt, dest, ccy, chain, toAddr）
	// OKX 51000 "Parameter chainName error" 表示 chain 的值不在该资产支持的链列表中，需与 Get Currencies/Get Deposit Address 返回的 chain 完全一致
	bodyMap := map[string]interface{}{
		"amt":    strconv.FormatFloat(req.Amount, 'f', -1, 64),
		"dest":   "4", // 4: 提币到链上地址（3: 内部转账）
		"ccy":    req.Asset,
		"chain":  chainBuilt,
		"toAddr": req.Address,
	}

	if req.Memo != "" {
		bodyMap["tag"] = req.Memo
	}

	// 特定主体用户需提供 rcvrInfo（含 walletType：exchange=提币到交易所钱包，private=提币到私人钱包）
	if req.RcvrInfo != nil {
		bodyMap["rcvrInfo"] = map[string]interface{}{
			"walletType":    req.RcvrInfo.WalletType, // 必填：exchange | private
			"exchId":        req.RcvrInfo.ExchId,
			"rcvrFirstName": req.RcvrInfo.RcvrFirstName, // 公司时可填公司名称
			"rcvrLastName":  req.RcvrInfo.RcvrLastName,  // 公司时可填 "N/A"
		}
	}

	bodyBytes, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, fmt.Errorf("marshal withdraw request failed: %w", err)
	}
	bodyStr := string(bodyBytes)

	// 添加日志记录请求参数（不记录敏感信息），便于排查 51000 Parameter chainName error
	log.Infow("Withdraw request (chain sent to OKX)", "asset", req.Asset, "network", req.Network, "chainBuilt", chainBuilt, "amount", req.Amount)
	log.Debugf("Withdraw request: asset=%s, amount=%.8f, network=%s, address=%s, hasRcvrInfo=%v",
		req.Asset, req.Amount, req.Network, req.Address, req.RcvrInfo != nil)

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "POST", requestPath, bodyStr, secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	responseBody, err := restClient.DoPostWithHeaders(apiURL, bodyStr, headers)
	if err != nil {
		log.Errorf("Withdraw API call failed: %v", err)
		return nil, fmt.Errorf("withdraw failed: %w", err)
	}

	// 记录响应（用于调试）
	log.Debugf("Withdraw API response: %s", responseBody)

	if err := CheckOKXAPIError(responseBody); err != nil {
		log.Errorf("Withdraw API error: %v", err)
		// 51000 多为 chain 值错误：需与 OKX 该资产支持的 chain 完全一致（如 USDT-Arbitrum One），可调 GetWithdrawNetworks(asset) 查看
		if strings.Contains(err.Error(), "51000") || strings.Contains(err.Error(), "chainName") {
			log.Warnw("OKX 51000/chainName error: check chain value", "asset", req.Asset, "network", req.Network, "chainBuilt", chainBuilt, "responseBody", responseBody)
			// 打出该资产在 OKX 支持的提现 chain 列表，便于对比 chainBuilt 与真实列表
			if list, listErr := o.GetWithdrawNetworks(req.Asset); listErr == nil {
				chains := make([]string, 0, len(list))
				for _, n := range list {
					chains = append(chains, n.Network)
				}
				log.Warnw("OKX GetWithdrawNetworks(asset) for comparison", "asset", req.Asset, "chains", chains)
			}
		}
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			WdID     string `json:"wdId"`
			Ccy      string `json:"ccy"`
			Chain    string `json:"chain"`
			Amt      string `json:"amt"`
			ClientId string `json:"clientId"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("withdraw response has no data")
	}

	wdID := resp.Data[0].WdID
	return &model.WithdrawResponse{
		WithdrawID: wdID,
		Status:     "PENDING",
		CreateTime: time.Now(),
	}, nil
}

// GetDepositHistory 查询充币记录
func (o *okx) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := o.getAPIKeys()
	restClient := o.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // OKX 最大限制
	}

	// OKX API v5: GET /api/v5/asset/deposit-history
	requestPath := "/api/v5/asset/deposit-history"
	params := url.Values{}
	params.Add("limit", strconv.Itoa(limit))
	if asset != "" {
		params.Add("ccy", asset)
	}

	queryStr := params.Encode()
	if queryStr != "" {
		requestPath += "?" + queryStr
	}

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get deposit history failed: %w", err)
	}

	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Amt                 string `json:"amt"`
			Ccy                 string `json:"ccy"`
			Chain               string `json:"chain"`
			State               string `json:"state"` // 0:等待确认, 1:确认中, 2:已到账, 3:充值失败
			TxID                string `json:"txId"`
			From                string `json:"from"`
			To                  string `json:"to"`
			DepId               string `json:"depId"`
			Ts                  string `json:"ts"` // 时间戳（毫秒）
			ActualDepBlkConfirm string `json:"actualDepBlkConfirm"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit history response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	records := make([]model.DepositRecord, 0, len(resp.Data))
	for _, r := range resp.Data {
		amount, _ := strconv.ParseFloat(r.Amt, 64)

		status := "PENDING"
		switch r.State {
		case "0":
			status = "PENDING" // 等待确认
		case "1":
			status = "PROCESSING" // 确认中
		case "2":
			status = "COMPLETED" // 已到账
		case "3":
			status = "FAILED" // 充值失败
		default:
			status = "UNKNOWN"
		}

		createTime := time.Now()
		if r.Ts != "" {
			if ts, err := strconv.ParseInt(r.Ts, 10, 64); err == nil {
				createTime = time.Unix(ts/1000, 0)
			}
		}

		records = append(records, model.DepositRecord{
			TxHash:     r.TxID,
			Asset:      r.Ccy,
			Amount:     amount,
			Network:    r.Chain,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// GetWithdrawHistory 查询提币记录
func (o *okx) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey, passphrase := o.getAPIKeys()
	restClient := o.restClient

	if limit <= 0 || limit > 100 {
		limit = 100 // OKX 最大限制
	}

	// OKX API v5: GET /api/v5/asset/withdrawal-history
	requestPath := "/api/v5/asset/withdrawal-history"
	params := url.Values{}
	params.Add("limit", strconv.Itoa(limit))
	if asset != "" {
		params.Add("ccy", asset)
	}

	queryStr := params.Encode()
	if queryStr != "" {
		requestPath += "?" + queryStr
	}

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get withdraw history failed: %w", err)
	}

	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			WdID     string `json:"wdId"`
			Amt      string `json:"amt"`
			Ccy      string `json:"ccy"`
			Chain    string `json:"chain"`
			State    string `json:"state"` // 0:等待提币, 1:提币中, 2:提币成功, 3:提币失败, 4:撤销中, 5:已撤销, 6:撤销失败
			TxID     string `json:"txId"`
			To       string `json:"to"`
			Tag      string `json:"tag"`
			Fee      string `json:"fee"`
			FeeCcy   string `json:"feeCcy"`
			Ts       string `json:"ts"` // 时间戳（毫秒）
			ClientId string `json:"clientId"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw history response failed: %w, response: %s", err, responseBody)
	}

	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	records := make([]model.WithdrawRecord, 0, len(resp.Data))
	log := logger.GetLoggerInstance().Named("okx.withdraw").Sugar()

	for _, r := range resp.Data {
		amount, _ := strconv.ParseFloat(r.Amt, 64)

		// 按照 OKX API 文档映射状态
		status := "PENDING"
		switch r.State {
		case "0":
			status = "PENDING" // 等待提币
		case "1":
			status = "PROCESSING" // 提币中
		case "2":
			status = "COMPLETED" // 提币成功
		case "3":
			status = "FAILED" // 提币失败
		case "4":
			status = "CANCELLING" // 撤销中
		case "5":
			status = "CANCELED" // 已撤销
		case "6":
			status = "CANCEL_FAILED" // 撤销失败
		default:
			log.Warnf("Unknown withdraw status: %s for ID: %s", r.State, r.WdID)
			status = "UNKNOWN"
		}

		createTime := time.Now()
		if r.Ts != "" {
			if ts, err := strconv.ParseInt(r.Ts, 10, 64); err == nil {
				createTime = time.Unix(ts/1000, 0)
			}
		}

		records = append(records, model.WithdrawRecord{
			WithdrawID: r.WdID,
			TxHash:     r.TxID,
			Asset:      r.Ccy,
			Amount:     amount,
			Network:    r.Chain,
			Address:    r.To,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// resolveOKXWithdrawChain 优先用 GetWithdrawNetworks 返回的原始 chain 字符串（与 Get Currencies 一致），避免 51000。
// 若根据 network 能解析出 chainID 且在提现网络列表中找到对应项，则用其 Network；否则回退到 buildOKXWithdrawChain。
func resolveOKXWithdrawChain(o *okx, asset, network string) string {
	log := logger.GetLoggerInstance().Named("okx.withdraw").Sugar()
	network = strings.TrimSpace(network)
	if network == "" {
		chainBuilt := buildOKXWithdrawChain(asset, network)
		log.Infow("resolveOKXWithdrawChain: fallback (empty network)", "asset", asset, "network", network, "chainBuilt", chainBuilt)
		return chainBuilt
	}
	// 将 executor 传入的通用网络名（如 ARBITRUM、ERC20）转为 chainID，与 GetWithdrawNetworks 返回的 ChainID 一致
	chainID := strings.TrimSpace(okxNetworkToChainID[strings.ToUpper(network)])
	if chainID == "" {
		chainBuilt := buildOKXWithdrawChain(asset, network)
		log.Infow("resolveOKXWithdrawChain: fallback (no chainID for network)", "asset", asset, "network", network, "chainBuilt", chainBuilt)
		return chainBuilt
	}
	nets, err := o.GetWithdrawNetworks(asset)
	if err != nil {
		chainBuilt := buildOKXWithdrawChain(asset, network)
		log.Infow("resolveOKXWithdrawChain: fallback (GetWithdrawNetworks error)", "asset", asset, "network", network, "chainID", chainID, "err", err.Error(), "chainBuilt", chainBuilt)
		return chainBuilt
	}
	for _, n := range nets {
		nid := strings.TrimSpace(n.ChainID)
		if nid == chainID && n.Network != "" {
			log.Infow("resolveOKXWithdrawChain: hit", "asset", asset, "network", network, "chainID", chainID, "chainBuilt", n.Network)
			return n.Network
		}
	}
	chainBuilt := buildOKXWithdrawChain(asset, network)
	log.Infow("resolveOKXWithdrawChain: fallback (no match in list)", "asset", asset, "network", network, "chainID", chainID, "listLen", len(nets), "chainBuilt", chainBuilt)
	return chainBuilt
}

// buildOKXWithdrawChain 构建提币请求的 chain 字段，格式为 "{CCY}-{Network}"，如 "BTC-Bitcoin", "USDT-ERC20"
// 需与 OKX Get Currencies 返回的 chain 完全一致，否则会报 51000 Parameter chainName error
func buildOKXWithdrawChain(asset, network string) string {
	network = strings.TrimSpace(network)
	if network == "" {
		// 未指定网络时用常见默认，避免用 asset 拼出无效 chain（如 USDT-Usdt）
		switch strings.ToUpper(asset) {
		case "USDT", "USDC", "DAI":
			network = "ERC20"
		case "BTC":
			network = "Bitcoin"
		case "ETH":
			network = "Ethereum"
		default:
			network = "ERC20"
		}
	}
	suffix := mapNetworkToOKXChainSuffix(network)
	return fmt.Sprintf("%s-%s", asset, suffix)
}

// mapNetworkName 将通用网络名称映射到 OKX API 需要的网络名称
// OKX API 文档：https://www.okx.com/docs-v5/en/#rest-api-funding-get-currencies
func mapNetworkName(network string) string {
	networkMap := map[string]string{
		"BEP20":    "BSC",   // Binance Smart Chain
		"BEP2":     "BNB",   // Binance Chain
		"ERC20":    "ETH",   // Ethereum
		"TRC20":    "TRX",   // Tron
		"POLYGON":  "MATIC", // Polygon
		"ARBITRUM": "ARBITRUM",
		"OPTIMISM": "OPTIMISM",
		"AVAXC":    "AVAX", // Avalanche C-Chain
		"FTM":      "FTM",  // Fantom
		"SOL":      "SOL",  // Solana
		"BASE":     "BASE", // Base
	}
	if mapped, ok := networkMap[strings.ToUpper(network)]; ok {
		return mapped
	}
	return network
}

// mapNetworkToOKXChainSuffix 将通用网络名称映射到 OKX deposit-address API 返回的 chain 格式中的网络部分
// OKX deposit-address API 返回的 chain 格式为 "{CCY}-{NETWORK}"，如 "USDT-ERC20", "USDT-Arbitrum One"
// 此函数将通用网络名称映射到 chain 中的 NETWORK 部分
func mapNetworkToOKXChainSuffix(network string) string {
	networkUpper := strings.ToUpper(network)
	networkMap := map[string]string{
		"BTC":      "Bitcoin",           // Bitcoin 主网（Withdraw chain: BTC-Bitcoin）
		"BITCOIN":  "Bitcoin",           // Bitcoin 主网
		"ETH":      "Ethereum",          // Ethereum 主网（Withdraw chain: ETH-Ethereum）
		"ETHEREUM": "Ethereum",          // Ethereum 主网
		"ERC20":    "ERC20",             // Ethereum
		"TRC20":    "TRC20",             // Tron
		"BEP20":    "BSC",               // Binance Smart Chain
		"BEP2":     "BNB",               // Binance Chain
		"POLYGON":  "Polygon",           // Polygon
		"MATIC":    "Polygon",           // Polygon
		"ARBITRUM": "Arbitrum One",      // Arbitrum One
		"ARB":      "Arbitrum One",      // Arbitrum One
		"OPTIMISM": "Optimism",          // Optimism
		"OP":       "Optimism",          // Optimism
		"AVAXC":    "Avalanche C-Chain", // Avalanche C-Chain
		"AVAX":     "Avalanche C-Chain", // Avalanche C-Chain
		"FTM":      "Fantom",            // Fantom
		"SOL":      "Solana",            // Solana
		"SOLANA":   "Solana",            // Solana
		"BASE":     "Base",              // Base
		"TON":      "TON",               // TON
		"OKTC":     "OKTC",              // OKTC
		"XLAYER":   "X Layer",           // X Layer
		"X-LAYER":  "X Layer",           // X Layer
	}
	if mapped, ok := networkMap[networkUpper]; ok {
		return mapped
	}
	// 如果没有映射，返回原始值（首字母大写）
	if len(network) > 0 {
		return strings.ToUpper(network[:1]) + strings.ToLower(network[1:])
	}
	return network
}

// okxNetworkToChainID OKX API 返回的 network 名 → 链 ID（与 LayerZero 等跨链协议一致）
var okxNetworkToChainID = map[string]string{
	"ETH":               "1",  // Ethereum
	"ERC20":             "1",  // Ethereum
	"BSC":               "56", // BNB Chain
	"BEP20":             "56", // BNB Chain
	"BNB":               "56",
	"MATIC":             "137", // Polygon
	"POLYGON":           "137",
	"ARBITRUM":          "42161",
	"ARBITRUM ONE":      "42161",
	"ARB":               "42161",
	"OPTIMISM":          "10",
	"OP":                "10",
	"AVAX":              "43114",
	"AVALANCHE C-CHAIN": "43114",
	"AVAXC":             "43114",
	"BASE":              "8453",
	"FTM":               "250",
	"TRX":               "",
	"TRC20":             "",
	"SOL":               "",
	"SOLANA":            "",
}

// okxChainToChainID 将 OKX chain 字符串（如 "ZAMA-ERC20"、"USDT-Arbitrum One"）解析为链 ID
func okxChainToChainID(chain string) string {
	if chain == "" {
		return ""
	}
	// 先尝试完整字符串
	if id := okxNetworkToChainID[strings.ToUpper(strings.TrimSpace(chain))]; id != "" {
		return id
	}
	// OKX 格式为 {ccy}-{network}，提取 network 部分
	if idx := strings.Index(chain, "-"); idx >= 0 && idx < len(chain)-1 {
		suffix := strings.TrimSpace(chain[idx+1:])
		if id := okxNetworkToChainID[strings.ToUpper(suffix)]; id != "" {
			return id
		}
		if id := okxNetworkToChainID[suffix]; id != "" {
			return id
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "ARBITRUM") {
			return "42161"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "OPTIMISM") || strings.HasPrefix(strings.ToUpper(suffix), "OP ") {
			return "10"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "AVALANCHE") || strings.HasPrefix(strings.ToUpper(suffix), "AVAX") {
			return "43114"
		}
		if strings.HasPrefix(strings.ToUpper(suffix), "POLYGON") || strings.HasPrefix(strings.ToUpper(suffix), "MATIC") {
			return "137"
		}
	}
	return ""
}

// GetWithdrawNetworks 查询某资产在 OKX 支持的提现网络（GET /api/v5/asset/currencies）
func (o *okx) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	apiKey, secretKey, passphrase := o.getAPIKeys()

	// OKX API v5: GET /api/v5/asset/currencies
	requestPath := "/api/v5/asset/currencies"
	if asset != "" {
		requestPath += "?ccy=" + url.QueryEscape(asset)
	}

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)
	responseBody, err := o.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get currencies failed: %w", err)
	}
	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Ccy         string `json:"ccy"`
			Name        string `json:"name"`
			Chain       string `json:"chain"`
			CanDep      bool   `json:"canDep"`
			CanWd       bool   `json:"canWd"`
			CanInternal bool   `json:"canInternal"`
			MinFee      string `json:"minFee"`
			MaxFee      string `json:"maxFee"`
			MinWd       string `json:"minWd"`
			MaxWd       string `json:"maxWd"`
			MainNet     bool   `json:"mainNet"`
			CtAddr      string `json:"ctAddr"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse currencies response failed: %w", err)
	}

	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	var out []model.WithdrawNetworkInfo
	var wdRawChains []map[string]interface{}
	for _, c := range resp.Data {
		if strings.ToUpper(c.Ccy) != assetUpper {
			continue
		}
		wdRawChains = append(wdRawChains, map[string]interface{}{"chain": c.Chain, "canDep": c.CanDep, "canWd": c.CanWd})
		if !c.CanWd {
			continue
		}
		chainID := okxChainToChainID(c.Chain)
		if chainID == "" && c.Chain != "" {
			chainID = c.Chain
		}
		out = append(out, model.WithdrawNetworkInfo{
			Network:         c.Chain,
			ChainID:         chainID,
			WithdrawEnable:  c.CanWd,
			WithdrawFee:     c.MinFee,
			WithdrawMin:     c.MinWd,
			WithdrawMax:     c.MaxWd,
			IsDefault:       c.MainNet,
			ContractAddress: c.CtAddr,
		})
	}
	// #region agent log
	var wdChainIDs []string
	for _, n := range out {
		wdChainIDs = append(wdChainIDs, n.ChainID)
	}
	okxDebugLog("okx:GetWithdrawNetworks:result", "withdraw networks", map[string]interface{}{"asset": asset, "rawChains": wdRawChains, "outCount": len(out), "chainIDs": wdChainIDs}, "H3")
	// #endregion
	return out, nil
}

// GetDepositNetworks 查询某资产在 OKX 支持的充币网络（filter by CanDep，用于链→交易所路由探测）
// 与 GetWithdrawNetworks 不同：充币需 CanDep=true，提现需 CanWd=true；部分资产某链仅支持单方向
func (o *okx) GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	apiKey, secretKey, passphrase := o.getAPIKeys()

	requestPath := "/api/v5/asset/currencies"
	if asset != "" {
		requestPath += "?ccy=" + url.QueryEscape(asset)
	}

	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	apiURL := fmt.Sprintf("%s%s", constants.OkexBaseUrl, requestPath)
	responseBody, err := o.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("get currencies failed: %w", err)
	}
	if err := CheckOKXAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Ccy     string `json:"ccy"`
			Chain   string `json:"chain"`
			CanDep  bool   `json:"canDep"`
			CanWd   bool   `json:"canWd"`
			MinFee  string `json:"minFee"`
			MinWd   string `json:"minWd"`
			MaxWd   string `json:"maxWd"`
			MainNet bool   `json:"mainNet"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse currencies response failed: %w", err)
	}
	if resp.Code != "0" {
		return nil, fmt.Errorf("okx API error: code=%s, msg=%s", resp.Code, resp.Msg)
	}

	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	var out []model.WithdrawNetworkInfo
	var rawChains []map[string]interface{}
	for _, c := range resp.Data {
		if strings.ToUpper(c.Ccy) != assetUpper {
			continue
		}
		rawChains = append(rawChains, map[string]interface{}{"chain": c.Chain, "canDep": c.CanDep, "canWd": c.CanWd})
		if !c.CanDep {
			continue
		}
		chainID := okxChainToChainID(c.Chain)
		if chainID == "" && c.Chain != "" {
			chainID = c.Chain
		}
		// #region agent log
		okxDebugLog("okx:GetDepositNetworks:perRecord", "CanDep record", map[string]interface{}{"asset": asset, "rawChain": c.Chain, "chainID": chainID, "canDep": c.CanDep, "canWd": c.CanWd}, "H1")
		// #endregion
		out = append(out, model.WithdrawNetworkInfo{
			Network:        c.Chain,
			ChainID:        chainID,
			WithdrawEnable: c.CanDep,
			WithdrawFee:    c.MinFee,
			WithdrawMin:    c.MinWd,
			WithdrawMax:    c.MaxWd,
			IsDefault:      c.MainNet,
		})
	}
	// #region agent log
	var depChainIDs []string
	for _, n := range out {
		depChainIDs = append(depChainIDs, n.ChainID)
	}
	okxDebugLog("okx:GetDepositNetworks:result", "deposit networks", map[string]interface{}{"asset": asset, "rawChains": rawChains, "outCount": len(out), "chainIDs": depChainIDs}, "H1")
	// #endregion
	return out, nil
}
