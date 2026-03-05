package binance

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/exchange"
	"github.com/qw225967/auto-monitor/internal/model"
	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

var _ exchange.DepositWithdrawProvider = (*binance)(nil)

// Deposit 获取充币地址
func (b *binance) Deposit(asset string, network string) (*model.DepositAddress, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	log := logger.GetLoggerInstance().Named("binance.deposit").Sugar()
	mappedNetwork := network
	if network != "" {
		mappedNetwork = mapNetworkName(network)
	}
	log.Infow("Deposit request", "asset", asset, "network", network, "mappedNetwork", mappedNetwork)

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"coin":       asset,
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	if network != "" {
		// 将通用网络名称映射到 Binance API 需要的网络名称
		// 例如：BEP20 -> BSC, ERC20 -> ETH, TRC20 -> TRX
		network = mapNetworkName(network)
		params["network"] = network
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceDepositAddressPath, queryStr)

	// 添加重试机制，处理网络错误（如 EOF）
	maxRetries := 3
	var responseBody string
	var err error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		responseBody, err = restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
		if err == nil {
			break
		}

		errStr := err.Error()
		// 检查是否是网络错误（EOF, connection closed 等）
		isNetworkError := strings.Contains(errStr, "EOF") ||
			strings.Contains(errStr, "connection closed") ||
			strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "read tcp") ||
			strings.Contains(errStr, "write tcp")

		if isNetworkError && attempt < maxRetries {
			// 网络错误，等待后重试（需要重新生成时间戳和签名）
			waitTime := time.Duration(attempt) * time.Second
			log := logger.GetLoggerInstance().Named("binance.deposit").Sugar()
			log.Warnf("Get deposit address failed (attempt %d/%d): %v, retrying after %v...",
				attempt, maxRetries, err, waitTime)
			time.Sleep(waitTime)

			// 重新生成时间戳和签名
			timestamp = time.Now().UnixMilli()
			params["timestamp"] = strconv.FormatInt(timestamp, 10)
			params = b.buildSignedParams(params, secretKey)
			queryStr = buildQueryString(params)
			apiURL = fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceDepositAddressPath, queryStr)
			continue
		}

		// 非网络错误或已达到最大重试次数
		break
	}

	if err != nil {
		return nil, fmt.Errorf("get deposit address failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		log.Warnw("Deposit API error (e.g. -4018 currency does not exist)", "asset", asset, "network", network, "mappedNetwork", mappedNetwork, "responseBody", responseBody, "err", err)
		return nil, err
	}

	var resp struct {
		Address string `json:"address"`
		Coin    string `json:"coin"`
		Tag     string `json:"tag"`
		URL     string `json:"url"`
		Network string `json:"network"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit address response failed: %w, response: %s", err, responseBody)
	}

	return &model.DepositAddress{
		Asset:   resp.Coin,
		Address: resp.Address,
		Network: resp.Network,
		Memo:    resp.Tag,
	}, nil
}

// Withdraw 提币
func (b *binance) Withdraw(req *model.WithdrawRequest) (*model.WithdrawResponse, error) {
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

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"coin":       req.Asset,
		"address":    req.Address,
		"amount":     strconv.FormatFloat(req.Amount, 'f', -1, 64),
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	// 网络名称映射：Binance API 可能需要特定的网络名称格式
	// 例如：BEP20 -> BSC, ERC20 -> ETH, TRC20 -> TRX
	network := req.Network
	if network != "" {
		network = mapNetworkName(network)
		params["network"] = network
	}

	if req.Memo != "" {
		params["addressTag"] = req.Memo
	}

	// walletType: 0=现货钱包（默认），1=资金钱包
	// 对于统一账户，需要明确指定从哪个钱包提币
	if req.WalletType != nil {
		params["walletType"] = strconv.Itoa(*req.WalletType)
	}

	// 添加日志记录请求参数（不记录敏感信息）
	log := logger.GetLoggerInstance().Named("binance.withdraw").Sugar()
	log.Debugf("Withdraw request: asset=%s, amount=%.8f, network=%s, address=%s, walletType=%v",
		req.Asset, req.Amount, req.Network, req.Address, req.WalletType)

	params = b.buildSignedParams(params, secretKey)
	formData := buildFormData(params)
	apiURL := fmt.Sprintf("%s%s", constants.BinanceRestBaseSpotUrl, constants.BinanceWithdrawPath)

	headers := buildHeaders(apiKey)
	headers["Content-Type"] = "application/x-www-form-urlencoded"

	responseBody, err := restClient.DoPostWithHeaders(apiURL, formData, headers)
	if err != nil {
		log.Errorf("Withdraw API call failed: %v", err)
		return nil, fmt.Errorf("withdraw failed: %w", err)
	}

	// 记录响应（用于调试）
	log.Debugf("Withdraw API response: %s", responseBody)

	if err := checkAPIError(responseBody); err != nil {
		log.Errorf("Withdraw API error: %v", err)
		return nil, err
	}

	var resp struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw response failed: %w, response: %s", err, responseBody)
	}

	return &model.WithdrawResponse{
		WithdrawID: resp.ID,
		Status:     "PENDING",
		CreateTime: time.Now(),
	}, nil
}

// GetDepositHistory 查询充币记录
func (b *binance) GetDepositHistory(asset string, limit int) ([]model.DepositRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 1000 {
		limit = 1000 // Binance 最大限制
	}

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"limit":      strconv.Itoa(limit),
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	if asset != "" {
		params["coin"] = asset
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceDepositHistoryPath, queryStr)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get deposit history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		Amount        string `json:"amount"`
		Coin          string `json:"coin"`
		Network       string `json:"network"`
		Status        int    `json:"status"` // 0:待处理, 1:成功, 6:已取消
		Address       string `json:"address"`
		AddressTag    string `json:"addressTag"`
		TxID          string `json:"txId"`
		InsertTime    int64  `json:"insertTime"`
		TransferType  int    `json:"transferType"` // 1:内部转账, 0:外部转账
		UnlockConfirm int    `json:"unlockConfirm"`
		ConfirmTimes  string `json:"confirmTimes"`
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse deposit history response failed: %w, response: %s", err, responseBody)
	}

	records := make([]model.DepositRecord, 0, len(resp))
	for _, r := range resp {
		amount, _ := strconv.ParseFloat(r.Amount, 64)
		status := "PENDING"
		if r.Status == 1 {
			status = "COMPLETED"
		} else if r.Status == 6 {
			status = "CANCELED"
		}

		createTime := time.Unix(r.InsertTime/1000, 0)
		if r.InsertTime == 0 {
			createTime = time.Now()
		}

		records = append(records, model.DepositRecord{
			TxHash:     r.TxID,
			Asset:      r.Coin,
			Amount:     amount,
			Network:    r.Network,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// GetWithdrawHistory 查询提币记录
func (b *binance) GetWithdrawHistory(asset string, limit int) ([]model.WithdrawRecord, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	apiKey, secretKey := b.getAPIKeys()
	restClient := b.restClient

	if limit <= 0 || limit > 1000 {
		limit = 1000 // Binance 最大限制
	}

	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"limit":      strconv.Itoa(limit),
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}

	if asset != "" {
		params["coin"] = asset
	}

	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceWithdrawHistoryPath, queryStr)

	responseBody, err := restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get withdraw history failed: %w", err)
	}

	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}

	var resp []struct {
		ID              string `json:"id"`
		Amount          string `json:"amount"`
		TransactionFee  string `json:"transactionFee"`
		Coin            string `json:"coin"`
		Status          int    `json:"status"` // 0:邮件确认, 1:确认中, 2:成功, 3:已取消, 4:待确认, 5:失败, 6:已拒绝
		Address         string `json:"address"`
		AddressTag      string `json:"addressTag"`
		TxID            string `json:"txId"`
		ApplyTime       string `json:"applyTime"`
		Network         string `json:"network"`
		TransferType    int    `json:"transferType"` // 1:内部转账, 0:外部转账
		WithdrawOrderID string `json:"withdrawOrderId"`
		Info            string `json:"info"`
		ConfirmNo       int    `json:"confirmNo"`
		WalletType      int    `json:"walletType"` // 0:现货钱包, 1:资金钱包
	}

	if err := json.Unmarshal([]byte(responseBody), &resp); err != nil {
		return nil, fmt.Errorf("parse withdraw history response failed: %w, response: %s", err, responseBody)
	}

	records := make([]model.WithdrawRecord, 0, len(resp))
	log := logger.GetLoggerInstance().Named("binance.withdraw").Sugar()

	for _, r := range resp {
		amount, _ := strconv.ParseFloat(r.Amount, 64)

		// 按照 Binance 最新文档映射状态:
		// 0: Email Sent, 2: Awaiting Approval, 3: Rejected, 4: Processing, 6: Completed
		status := "PENDING"
		switch r.Status {
		case 0:
			status = "PENDING" // 邮件已发送，等待确认
		case 1:
			// 旧版可能存在该状态，统一视为处理中
			status = "PROCESSING"
		case 2:
			status = "AWAITING_APPROVAL" // 等待审核
		case 3:
			status = "REJECTED" // 已拒绝
		case 4:
			status = "PROCESSING" // 处理中
		case 5:
			status = "FAILED" // 失败
		case 6:
			status = "COMPLETED" // 提币完成（资产已扣除并上链）
		default:
			// 未知状态，记录原始值用于调试
			log.Warnf("Unknown withdraw status: %d for ID: %s, Info: %s", r.Status, r.ID, r.Info)
			status = "UNKNOWN"
		}

		// 记录详细信息（特别是 COMPLETED / REJECTED 状态）
		//if status == "COMPLETED" || status == "REJECTED" {
		//	log.Warnf("Withdraw record: ID=%s, Status=%d (%s), TxID=%s, Info=%s, Network=%s, Address=%s",
		//		r.ID, r.Status, status, r.TxID, r.Info, r.Network, r.Address)
		//}

		createTime := time.Now()
		if r.ApplyTime != "" {
			// Binance 返回的时间格式: "2021-01-01 12:00:00"
			if t, err := time.Parse("2006-01-02 15:04:05", r.ApplyTime); err == nil {
				createTime = t
			}
		}

		records = append(records, model.WithdrawRecord{
			WithdrawID: r.ID,
			TxHash:     r.TxID,
			Asset:      r.Coin,
			Amount:     amount,
			Network:    r.Network,
			Address:    r.Address,
			Status:     status,
			CreateTime: createTime,
		})
	}

	return records, nil
}

// mapNetworkName 将通用网络名称映射到 Binance API 需要的网络名称
// Binance API 文档：https://developers.binance.com/docs/wallet/capital/withdraw
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
	}
	if mapped, ok := networkMap[strings.ToUpper(network)]; ok {
		return mapped
	}
	return network
}

// binanceNetworkToChainID Binance API 返回的 network 名 → 链 ID（与 LayerZero 等跨链协议一致）
var binanceNetworkToChainID = map[string]string{
	"ETH":      "1",     // Ethereum
	"ERC20":    "1",     // Ethereum（部分币种用此名）
	"BSC":      "56",    // BNB Chain
	"BEP20":    "56",    // BNB Chain
	"BNB":      "56",    // 部分接口用 BNB 表示 BSC
	"MATIC":    "137",   // Polygon
	"POLYGON":  "137",   // Polygon
	"ARBITRUM": "42161", // Arbitrum One
	"ARB":      "42161",
	"OPTIMISM": "10", // Optimism
	"OP":       "10",
	"AVAX":     "43114", // Avalanche C-Chain
	"AVAXC":    "43114",
	"BASE":     "8453", // Base
	"FTM":      "250",  // Fantom
	"TRX":      "",     // Tron 无统一 chainId，跨链常用 1/56/137 等，此处留空
	"TRC20":    "",     // Tron
	"SOL":      "",     // Solana 留空
}

var _ exchange.WithdrawNetworkLister = (*binance)(nil)
var _ exchange.DepositNetworkLister = (*binance)(nil)

// GetWithdrawNetworks 查询某资产在 Binance 支持的提现网络（GET /sapi/v1/capital/config/getall）
func (b *binance) GetWithdrawNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	apiKey, secretKey := b.getAPIKeys()
	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}
	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceCapitalConfigGetAll, queryStr)
	responseBody, err := b.restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get capital config failed: %w", err)
	}
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	var coins []struct {
		Coin        string `json:"coin"`
		NetworkList []struct {
			Network         string `json:"network"`
			WithdrawEnable  bool   `json:"withdrawEnable"`
			WithdrawFee     string `json:"withdrawFee"`
			WithdrawMin     string `json:"withdrawMin"`
			WithdrawMax     string `json:"withdrawMax"`
			IsDefault       bool   `json:"isDefault"`
			ContractAddress string `json:"contractAddress"`
		} `json:"networkList"`
	}
	if err := json.Unmarshal([]byte(responseBody), &coins); err != nil {
		return nil, fmt.Errorf("parse capital config failed: %w", err)
	}
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	var out []model.WithdrawNetworkInfo
	for _, c := range coins {
		if strings.ToUpper(c.Coin) != assetUpper {
			continue
		}
		for _, n := range c.NetworkList {
			if !n.WithdrawEnable {
				continue
			}
			chainID := binanceNetworkToChainID[strings.ToUpper(n.Network)]
			if chainID == "" && n.Network != "" {
				chainID = n.Network
			}
			out = append(out, model.WithdrawNetworkInfo{
				Network:         n.Network,
				ChainID:         chainID,
				WithdrawEnable:  true,
				WithdrawFee:     n.WithdrawFee,
				WithdrawMin:     n.WithdrawMin,
				WithdrawMax:     n.WithdrawMax,
				IsDefault:       n.IsDefault,
				ContractAddress: n.ContractAddress,
			})
		}
		break
	}
	return out, nil
}

// GetDepositNetworks 查询某资产支持的充币网络（仅返回 depositEnable=true 的）
func (b *binance) GetDepositNetworks(asset string) ([]model.WithdrawNetworkInfo, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	apiKey, secretKey := b.getAPIKeys()
	timestamp := time.Now().UnixMilli()
	params := map[string]string{
		"timestamp":  strconv.FormatInt(timestamp, 10),
		"recvWindow": constants.BinanceRecvWindow,
	}
	params = b.buildSignedParams(params, secretKey)
	queryStr := buildQueryString(params)
	apiURL := fmt.Sprintf("%s%s?%s", constants.BinanceRestBaseSpotUrl, constants.BinanceCapitalConfigGetAll, queryStr)
	responseBody, err := b.restClient.DoGetWithHeaders(apiURL, buildHeaders(apiKey))
	if err != nil {
		return nil, fmt.Errorf("get capital config failed: %w", err)
	}
	if err := checkAPIError(responseBody); err != nil {
		return nil, err
	}
	var coins []struct {
		Coin        string `json:"coin"`
		NetworkList []struct {
			Network       string `json:"network"`
			DepositEnable bool   `json:"depositEnable"`
			WithdrawEnable bool  `json:"withdrawEnable"`
			WithdrawFee   string `json:"withdrawFee"`
			DepositMin    string `json:"depositMin"`
			IsDefault     bool   `json:"isDefault"`
		} `json:"networkList"`
	}
	if err := json.Unmarshal([]byte(responseBody), &coins); err != nil {
		return nil, fmt.Errorf("parse capital config failed: %w", err)
	}
	assetUpper := strings.ToUpper(strings.TrimSpace(asset))
	var out []model.WithdrawNetworkInfo
	for _, c := range coins {
		if strings.ToUpper(c.Coin) != assetUpper {
			continue
		}
		for _, n := range c.NetworkList {
			if !n.DepositEnable {
				continue
			}
			chainID := binanceNetworkToChainID[strings.ToUpper(n.Network)]
			if chainID == "" && n.Network != "" {
				chainID = n.Network
			}
			out = append(out, model.WithdrawNetworkInfo{
				Network:        n.Network,
				ChainID:        chainID,
				WithdrawEnable: n.WithdrawEnable,
				WithdrawFee:    n.WithdrawFee,
				WithdrawMin:    n.DepositMin,
				IsDefault:      n.IsDefault,
			})
		}
		break
	}
	return out, nil
}
