package config

import "example.com/m/v2/pkg/bitget-golang-sdk-api/constants"

const (
	BaseUrl = "https://api.bitget.com"
	WsUrl   = "wss://ws.bitget.com/mix/v1/stream"

	ApiKey        = ""
	SecretKey     = ""
	PASSPHRASE    = ""
	TimeoutSecond = 30
	SignType      = constants.SHA256
)
