package config

import (
	"errors"
	"net/http"
	"os"
	"sync"

	"github.com/qw225967/auto-monitor/internal/model"
)

// OkexKeyManager OKEx API Key 管理器接口（供 onchain 使用）
type OkexKeyManager interface {
	Init() error
	GetNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error)
}

// okexKeyManagerStub 占位实现：未配置 OKEx Key 时返回错误
type okexKeyManagerStub struct{}

func (o *okexKeyManagerStub) Init() error {
	return errors.New("OKEx API keys not configured, set OKEX_APP_KEY etc")
}

func (o *okexKeyManagerStub) GetNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error) {
	return model.OkexKeyRecord{}, errors.New("OKEx API keys not configured")
}

// okexKeyManagerEnv 从环境变量读取的 OKEx Key 管理器
type okexKeyManagerEnv struct {
	appKey     string
	secretKey  string
	passphrase string
	mu         sync.Mutex
	initialized bool
}

func (o *okexKeyManagerEnv) Init() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.initialized {
		return nil
	}
	if o.appKey == "" || o.secretKey == "" || o.passphrase == "" {
		return errors.New("OKEx API keys incomplete, set OKEX_APP_KEY, OKEX_SECRET_KEY, OKEX_PASSPHRASE")
	}
	o.initialized = true
	return nil
}

func (o *okexKeyManagerEnv) GetNextAppKey(canBroadcast bool) (model.OkexKeyRecord, error) {
	return model.OkexKeyRecord{
		AppKey:       o.appKey,
		SecretKey:    o.secretKey,
		Passphrase:   o.passphrase,
		Index:        0,
		CanBroadcast: canBroadcast,
	}, nil
}

// NewOkexKeyManagerFromEnv 从环境变量创建 OKEx Key 管理器
func NewOkexKeyManagerFromEnv() OkexKeyManager {
	appKey := os.Getenv("OKEX_APP_KEY")
	secretKey := os.Getenv("OKEX_SECRET_KEY")
	passphrase := os.Getenv("OKEX_PASSPHRASE")
	if appKey == "" && secretKey == "" && passphrase == "" {
		return &okexKeyManagerStub{}
	}
	return &okexKeyManagerEnv{
		appKey:     appKey,
		secretKey:  secretKey,
		passphrase: passphrase,
	}
}

// NewOkexKeyManagerFromConfig 从 Config 创建 OKEx Key 管理器
func NewOkexKeyManagerFromConfig(appKey, secretKey, passphrase string) OkexKeyManager {
	if appKey == "" || secretKey == "" || passphrase == "" {
		return &okexKeyManagerStub{}
	}
	return &okexKeyManagerEnv{
		appKey:     appKey,
		secretKey:  secretKey,
		passphrase: passphrase,
	}
}

var okexKeyManager OkexKeyManager = &okexKeyManagerStub{}

// GetOkexKeyManager 获取 OKEx Key 管理器（默认返回 stub）
func GetOkexKeyManager() OkexKeyManager {
	return okexKeyManager
}

// SetOkexKeyManager 设置 OKEx Key 管理器（用于注入真实实现）
func SetOkexKeyManager(m OkexKeyManager) {
	okexKeyManager = m
}

// ProxyConfig 代理配置
type ProxyConfig struct {
	URL string
}

// CreateTransport 创建 HTTP Transport
func (p *ProxyConfig) CreateTransport() *http.Transport {
	return &http.Transport{}
}

var proxyConfig = &ProxyConfig{}

// GetProxyConfig 获取代理配置
func GetProxyConfig() *ProxyConfig {
	return proxyConfig
}

// GlobalConfig 全局配置（供 bridge、rest 等使用）
type GlobalConfig struct {
	MyProjectId string
	Bridge      struct {
		CCIP struct {
			RPCURLs map[string]string
		}
		LayerZero struct {
			RPCURLs map[string]string
		}
	}
}

var globalConfig *GlobalConfig

// GetGlobalConfig 获取全局配置
func GetGlobalConfig() *GlobalConfig {
	return globalConfig
}

// SetGlobalConfig 设置全局配置
func SetGlobalConfig(c *GlobalConfig) {
	globalConfig = c
}
