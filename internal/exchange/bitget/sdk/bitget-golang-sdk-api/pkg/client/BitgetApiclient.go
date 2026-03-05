package client

import (
	"example.com/m/v2/pkg/bitget-golang-sdk-api/internal"
	"example.com/m/v2/pkg/bitget-golang-sdk-api/internal/common"
)

type BitgetApiClient struct {
	BitgetRestClient *common.BitgetRestClient
}

func (p *BitgetApiClient) Init() *BitgetApiClient {
	p.BitgetRestClient = new(common.BitgetRestClient).Init()
	return p
}

func (p *BitgetApiClient) Post(url string, params map[string]string) (string, error) {
	postBody, jsonErr := internal.ToJson(params)
	if jsonErr != nil {
		return "", jsonErr
	}
	resp, err := p.BitgetRestClient.DoPost(url, postBody)
	return resp, err
}

func (p *BitgetApiClient) Get(url string, params map[string]string) (string, error) {
	resp, err := p.BitgetRestClient.DoGet(url, params)
	return resp, err
}
