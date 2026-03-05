package okx

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"auto-arbitrage/constants"
	"auto-arbitrage/internal/exchange"
	"auto-arbitrage/internal/model"
)

// getAccountBalance 请求 GET /api/v5/account/balance（可选 ccy），需签名
func (o *okx) getAccountBalance(ccy string) ([]okxBalanceDetail, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.getAccountBalanceNoLock(ccy)
}

// getAccountBalanceNoLock 请求交易账户余额，调用方必须已持有 o.mu 的锁（Lock 或 RLock）
func (o *okx) getAccountBalanceNoLock(ccy string) ([]okxBalanceDetail, error) {
	requestPath := constants.OkexPathAccountBalance
	if ccy != "" {
		requestPath += "?ccy=" + url.QueryEscape(ccy)
	}
	apiURL := constants.OkexBaseUrl + requestPath
	apiKey, secretKey, passphrase := o.getAPIKeys()
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	body, err := o.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("okx get balance: %w", err)
	}
	if err := CheckOKXAPIError(body); err != nil {
		return nil, err
	}
	var resp struct {
		Code string `json:"code"`
		Data []struct {
			Details []okxBalanceDetail `json:"details"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("parse balance: %w", err)
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Details) == 0 {
		return nil, nil
	}
	return resp.Data[0].Details, nil
}

type okxBalanceDetail struct {
	Ccy       string `json:"ccy"`
	Eq        string `json:"eq"`
	AvailEq   string `json:"availEq"`
	AvailBal  string `json:"availBal"`
	FrozenBal string `json:"frozenBal"`
	CashBal   string `json:"cashBal"`
}

func (d *okxBalanceDetail) toModel(updateTime time.Time) *model.Balance {
	total, _ := strconv.ParseFloat(d.Eq, 64)
	avail := parseFirstNonEmpty(d.AvailEq, d.AvailBal, d.CashBal)
	locked, _ := strconv.ParseFloat(d.FrozenBal, 64)
	if avail == 0 && total > 0 {
		avail = total - locked
	}
	return &model.Balance{
		Asset:      d.Ccy,
		Available:  avail,
		Locked:     locked,
		Total:      total,
		UpdateTime: updateTime,
	}
}

func parseFirstNonEmpty(ss ...string) float64 {
	for _, s := range ss {
		if s != "" {
			v, _ := strconv.ParseFloat(s, 64)
			return v
		}
	}
	return 0
}

// GetBalance 返回 USDT 余额（统一账户下主要计价资产）
func (o *okx) GetBalance() (*model.Balance, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	details, err := o.getAccountBalance("USDT")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for i := range details {
		b := details[i].toModel(now)
		if b.Asset == "USDT" {
			return b, nil
		}
	}
	return &model.Balance{Asset: "USDT", Available: 0, Locked: 0, Total: 0, UpdateTime: now}, nil
}

// GetSpotBalances 获取资金账户余额（GET /api/v5/asset/balances），独立于交易账户
func (o *okx) GetSpotBalances() (map[string]*model.Balance, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}

	requestPath := constants.OkexPathAssetBalances
	apiURL := constants.OkexBaseUrl + requestPath
	apiKey, secretKey, passphrase := o.getAPIKeys()
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	body, err := o.restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("okx get asset balances: %w", err)
	}
	if err := CheckOKXAPIError(body); err != nil {
		return nil, err
	}

	var resp struct {
		Code string `json:"code"`
		Data []struct {
			Ccy       string `json:"ccy"`
			Bal       string `json:"bal"`
			AvailBal  string `json:"availBal"`
			FrozenBal string `json:"frozenBal"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("parse asset balances: %w", err)
	}

	now := time.Now()
	out := make(map[string]*model.Balance)
	for _, d := range resp.Data {
		total, _ := strconv.ParseFloat(d.Bal, 64)
		avail, _ := strconv.ParseFloat(d.AvailBal, 64)
		frozen, _ := strconv.ParseFloat(d.FrozenBal, 64)
		if total > 0 {
			out[d.Ccy] = &model.Balance{
				Asset:      d.Ccy,
				Available:  avail,
				Locked:     frozen,
				Total:      total,
				UpdateTime: now,
			}
		}
	}
	return out, nil
}

// GetFuturesBalances 获取交易账户余额（GET /api/v5/account/balance），包含合约保证金
func (o *okx) GetFuturesBalances() (map[string]*model.Balance, error) {
	return o.GetAllBalances()
}

// GetAllBalances 获取所有币种余额（GET /api/v5/account/balance 不传 ccy）
func (o *okx) GetAllBalances() (map[string]*model.Balance, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	details, err := o.getAccountBalance("")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make(map[string]*model.Balance)
	for i := range details {
		b := details[i].toModel(now)
		if b.Asset != "" {
			out[b.Asset] = b
		}
	}
	return out, nil
}

// getAccountPositions 请求 GET /api/v5/account/positions，可选 instType、instId，需签名
func (o *okx) getAccountPositions(instType, instId string) ([]okxPositionData, error) {
	requestPath := constants.OkexPathAccountPositions
	params := url.Values{}
	if instType != "" {
		params.Set("instType", instType)
	}
	if instId != "" {
		params.Set("instId", instId)
	}
	if len(params) > 0 {
		requestPath += "?" + params.Encode()
	}
	apiURL := constants.OkexBaseUrl + requestPath
	apiKey, secretKey, passphrase := o.getAPIKeys()
	timestamp := GetOKXTimestamp()
	sign := BuildOKXSign(timestamp, "GET", requestPath, "", secretKey)
	headers := BuildOKXHeaders(apiKey, sign, timestamp, passphrase)

	o.mu.RLock()
	restClient := o.restClient
	o.mu.RUnlock()

	body, err := restClient.DoGetWithHeaders(apiURL, headers)
	if err != nil {
		return nil, fmt.Errorf("okx get positions: %w", err)
	}
	if err := CheckOKXAPIError(body); err != nil {
		return nil, err
	}
	var resp struct {
		Code string            `json:"code"`
		Data []okxPositionData `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return nil, fmt.Errorf("parse positions: %w", err)
	}
	return resp.Data, nil
}

type okxPositionData struct {
	InstId  string `json:"instId"`
	PosSide string `json:"posSide"`
	Pos     string `json:"pos"`
	AvgPx   string `json:"avgPx"`
	MarkPx  string `json:"markPx"`
	Upl     string `json:"upl"`
	Lever   string `json:"lever"`
	UTime   string `json:"uTime"`
}

func (p *okxPositionData) toModel() *model.Position {
	size, _ := strconv.ParseFloat(p.Pos, 64)
	if size == 0 {
		return &model.Position{
			Symbol:     FromOKXInstId(p.InstId),
			Size:       0,
			UpdateTime: time.Now(),
		}
	}
	var side model.PositionSide
	switch p.PosSide {
	case "long", "net":
		side = model.PositionSideLong
	case "short":
		side = model.PositionSideShort
	default:
		if size > 0 {
			side = model.PositionSideLong
		} else {
			side = model.PositionSideShort
			size = -size
		}
	}
	entryPrice, _ := strconv.ParseFloat(p.AvgPx, 64)
	markPrice, _ := strconv.ParseFloat(p.MarkPx, 64)
	unrealizedPnl, _ := strconv.ParseFloat(p.Upl, 64)
	leverage, _ := strconv.Atoi(p.Lever)
	updateTime := time.Now()
	if p.UTime != "" {
		if ms, err := strconv.ParseInt(p.UTime, 10, 64); err == nil {
			updateTime = time.UnixMilli(ms)
		}
	}
	return &model.Position{
		Symbol:        FromOKXInstId(p.InstId),
		Side:          side,
		Size:          size,
		EntryPrice:    entryPrice,
		MarkPrice:     markPrice,
		UnrealizedPnl: unrealizedPnl,
		Leverage:      leverage,
		UpdateTime:    updateTime,
	}
}

// GetPosition 查询指定合约持仓（instId 用 ToOKXSwapInstId(symbol)）
func (o *okx) GetPosition(symbol string) (*model.Position, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	if symbol == "" {
		return nil, exchange.ErrInvalidSymbol
	}
	instId := ToOKXSwapInstId(symbol)
	list, err := o.getAccountPositions("SWAP", instId)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return &model.Position{Symbol: symbol, Size: 0, UpdateTime: time.Now()}, nil
	}
	return list[0].toModel(), nil
}

// GetPositions 查询所有合约持仓（instType=SWAP）
func (o *okx) GetPositions() ([]*model.Position, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if !o.isInitialized {
		return nil, exchange.ErrNotInitialized
	}
	list, err := o.getAccountPositions("SWAP", "")
	if err != nil {
		return nil, err
	}
	out := make([]*model.Position, 0, len(list))
	for i := range list {
		pos := list[i].toModel()
		if pos.Size != 0 {
			out = append(out, pos)
		}
	}
	return out, nil
}
