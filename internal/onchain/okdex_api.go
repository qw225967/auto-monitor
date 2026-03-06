package onchain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
	"github.com/qw225967/auto-monitor/internal/model"
)

// buildLoginSign 构建登录签名
func (o *okdex) buildLoginSign(timestamp, method, routerPath, body, secret string) string {
	msg := timestamp + method + routerPath + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// getNextAppKey 获取 API Key
func (o *okdex) getNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error) {
	manager := config.GetOkexKeyManager()
	return manager.GetNextAppKey(canBroadcast)
}

// restQueryServerTimestamp 查询服务器时间戳
func (o *okdex) restQueryServerTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// doAPIRequestWithBase 执行 API 请求
func (o *okdex) doAPIRequestWithBase(baseURL, method, path string, values url.Values, body string) (string, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	record, err := o.getNextAppKey(false)
	if err != nil {
		return "", err
	}

	ts := o.restQueryServerTimestamp()
	queryStr := ""
	if values != nil {
		queryStr = "?" + values.Encode()
	}
	sign := o.buildLoginSign(ts, method, path, queryStr+body, record.SecretKey)

	fullURL := fmt.Sprintf("%s/%s%s", baseURL, strings.TrimLeft(path, "/"), queryStr)

	if method == constants.HttpMethodGet {
		return o.restClient.DoGet(constants.ConnectTypeOKEX, fullURL, body, record.AppKey, sign, record.Passphrase, ts)
	}
	return o.restClient.DoPost(constants.ConnectTypeOKEX, fullURL, body, record.AppKey, sign, record.Passphrase, ts)
}

// queryDexQuotePrice 查询 DEX 报价（v6 接口）
func (o *okdex) queryDexQuotePrice(fromTokenAddress, toTokenAddress, chainIndex, amount, fromTokenDecimals string) (string, error) {
	convertedAmount, err := o.convertToDecimals(amount, fromTokenDecimals)
	if err != nil {
		return "", fmt.Errorf("convert amount: %w", err)
	}

	values := url.Values{}
	values.Add("amount", convertedAmount)
	values.Add("chainIndex", chainIndex)
	values.Add("toTokenAddress", toTokenAddress)
	values.Add("fromTokenAddress", fromTokenAddress)
	values.Add("swapMode", "exactIn")
	values.Add("slippagePercent", "0.5")

	resp, err := o.doAPIRequestWithBase(constants.OkexDexV6BaseUrl, constants.HttpMethodGet, constants.OkexDexTradePrice, values, "")
	if err != nil {
		return "", fmt.Errorf("quote request: %w", err)
	}
	return resp, nil
}
