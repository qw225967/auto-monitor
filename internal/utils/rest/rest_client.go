package rest

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/qw225967/auto-monitor/constants"
	"github.com/qw225967/auto-monitor/internal/config"
)

type RestClient struct {
	httpClient *http.Client
}

func (rs *RestClient) InitRestClient() {
	proxyConfig := config.GetProxyConfig()
	transport := proxyConfig.CreateTransport()
	rs.httpClient = &http.Client{
		Timeout:   time.Duration(10) * time.Second,
		Transport: transport,
	}
}

func (rs *RestClient) DoPost(connectType, url, params, appKey, sign, passphrase, timestamp string) (string, error) {

	buffer := strings.NewReader(params)
	request, err := http.NewRequest("POST", url, buffer)

	rs.Headers(request, connectType, appKey, timestamp, sign, passphrase)
	if err != nil {
		return "", err
	}
	response, err := rs.httpClient.Do(request)

	if err != nil {
		return "", err
	}

	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	responseBodyString := string(bodyStr)
	return responseBodyString, err
}

func (rs *RestClient) DoGet(connectType, url, params, appKey, sign, passphrase, timestamp string) (string, error) {
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	rs.Headers(request, connectType, appKey, timestamp, sign, passphrase)
	if connectType == constants.ConnectTypeOKEX {
		projectID := "请添加"
		if g := config.GetGlobalConfig(); g != nil && g.MyProjectId != "" {
			projectID = g.MyProjectId
		}
		request.Header.Add(constants.OkexDexProject, projectID)
	}
	response, err := rs.httpClient.Do(request)

	if err != nil {
		return "", err
	}

	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	responseBodyString := string(bodyStr)
	return responseBodyString, err
}

/**
 * get header
 */
func (rs *RestClient) Headers(request *http.Request, connectType, apikey string, timestamp string, sign string, passphrase string) {
	switch connectType {
	case constants.ConnectTypeBSC:
	case constants.ConnectTypeBitGet:
	case constants.ConnectTypeOKEX:
		{
			request.Header.Add(constants.OkexDexContentType, constants.OkexDexApplicationJson)
			request.Header.Add(constants.OkexDexBgAccessKey, apikey)
			request.Header.Add(constants.OkexDexBgAccessSign, sign)
			request.Header.Add(constants.OkexDexBgAccessTimestamp, timestamp)
			request.Header.Add(constants.OkexDexBgAccessPassphrase, passphrase)
			break
		}
	}

}

// DoPostWithHeaders 发送 POST 请求，支持自定义 headers
func (rs *RestClient) DoPostWithHeaders(url, body string, headers map[string]string) (string, error) {
	if rs.httpClient == nil {
		return "", fmt.Errorf("rest client not initialized, call InitRestClient() first")
	}
	buffer := strings.NewReader(body)
	request, err := http.NewRequest("POST", url, buffer)
	if err != nil {
		return "", err
	}

	// 设置自定义 headers
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := rs.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	return string(bodyStr), nil
}

// DoGetWithHeaders 发送 GET 请求，支持自定义 headers
func (rs *RestClient) DoGetWithHeaders(url string, headers map[string]string) (string, error) {
	if rs.httpClient == nil {
		return "", fmt.Errorf("rest client not initialized, call InitRestClient() first")
	}
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	// 设置自定义 headers
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := rs.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	bodyStr, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	return string(bodyStr), nil
}
