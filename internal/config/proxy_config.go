package config

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

const (
	// DefaultProxyURL 默认代理地址
	// 如果环境变量未设置代理，且该常量不为空，则使用该常量作为默认代理地址
	//DefaultProxyURL = "http://127.0.0.1:9876"
	DefaultProxyURL = ""
)

// ProxyConfig 代理配置管理器（单例模式）
type ProxyConfig struct {
	proxyURL *url.URL
	useProxy bool
	mu       sync.RWMutex // 保护并发访问
}

var (
	proxyConfigInstance *ProxyConfig
	proxyConfigOnce     sync.Once
)

// GetProxyConfig 获取代理配置单例
func GetProxyConfig() *ProxyConfig {
	proxyConfigOnce.Do(func() {
		proxyConfigInstance = &ProxyConfig{
			useProxy: false,
		}
		proxyConfigInstance.init()
	})
	return proxyConfigInstance
}

// init 初始化代理配置（从环境变量或默认常量读取）
func (pc *ProxyConfig) init() {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// 优先从环境变量读取
	proxyURLStr := os.Getenv("HTTP_PROXY")
	if proxyURLStr == "" {
		proxyURLStr = os.Getenv("HTTPS_PROXY")
	}
	if proxyURLStr == "" {
		proxyURLStr = os.Getenv("PROXY_URL") // 自定义环境变量
	}

	if proxyURLStr == "" {
		proxyURLStr = os.Getenv("http_proxy")
	}
	if proxyURLStr == "" {
		proxyURLStr = os.Getenv("https_proxy") // 自定义环境变量
	}

	// 如果环境变量都为空，且默认常量不为空，则使用默认常量
	if proxyURLStr == "" && DefaultProxyURL != "" {
		proxyURLStr = DefaultProxyURL
	}

	if proxyURLStr != "" {
		if parsedURL, err := url.Parse(proxyURLStr); err == nil {
			pc.proxyURL = parsedURL
			pc.useProxy = true
		}
	}
}

// SetProxyURL 设置代理地址（代码配置，优先级高于环境变量）
// proxyURLStr: 代理地址，例如 "http://127.0.0.1:9876"，如果为空字符串则禁用代理
func (pc *ProxyConfig) SetProxyURL(proxyURLStr string) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if proxyURLStr == "" {
		pc.useProxy = false
		pc.proxyURL = nil
		return nil
	}

	parsedURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return fmt.Errorf("解析代理地址失败: %w", err)
	}

	pc.proxyURL = parsedURL
	pc.useProxy = true
	return nil
}

// GetProxyURL 获取代理 URL
func (pc *ProxyConfig) GetProxyURL() *url.URL {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	if !pc.useProxy {
		return nil
	}
	return pc.proxyURL
}

// GetProxyURLString 获取代理 URL 字符串
func (pc *ProxyConfig) GetProxyURLString() string {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	if !pc.useProxy || pc.proxyURL == nil {
		return ""
	}
	return pc.proxyURL.String()
}

// IsProxyEnabled 是否启用代理
func (pc *ProxyConfig) IsProxyEnabled() bool {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.useProxy
}

// EnableProxy 启用代理（使用当前配置的代理地址）
func (pc *ProxyConfig) EnableProxy() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.proxyURL != nil {
		pc.useProxy = true
	}
}

// DisableProxy 禁用代理
func (pc *ProxyConfig) DisableProxy() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.useProxy = false
}

// CreateTransport 创建带代理配置的 HTTP Transport
func (pc *ProxyConfig) CreateTransport() *http.Transport {
	transport := &http.Transport{}
	if pc.IsProxyEnabled() {
		proxyURL := pc.GetProxyURL()
		if proxyURL != nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}

	return transport
}

// CreateClient 创建带代理配置的 HTTP Client
// timeout: 超时时间，例如 time.Second * 10
func (pc *ProxyConfig) CreateClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: pc.CreateTransport(),
	}
}

// CreateClientWithDefaultTimeout 创建带代理配置的 HTTP Client（使用默认超时时间）
// 默认超时时间为 10 秒
func (pc *ProxyConfig) CreateClientWithDefaultTimeout() *http.Client {
	return pc.CreateClient(time.Duration(10) * time.Second)
}
