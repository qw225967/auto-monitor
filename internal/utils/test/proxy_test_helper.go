package test

import (
	"os"

	"auto-arbitrage/internal/config"
)

// SetupProxyForTest 为测试设置代理配置
// proxyURL: 代理地址，如果为空则使用环境变量或默认配置
// 返回清理函数，可以在 defer 中调用以恢复原始状态
func SetupProxyForTest(proxyURL string) func() {
	proxyConfig := config.GetProxyConfig()

	// 保存原始状态
	originalProxy := proxyConfig.GetProxyURLString()
	originalEnabled := proxyConfig.IsProxyEnabled()

	// 设置新的代理配置
	if proxyURL != "" {
		proxyConfig.SetProxyURL(proxyURL)
	} else {
		// 如果未指定代理URL，尝试从环境变量读取
		// 代理配置管理器会自动读取环境变量
		if os.Getenv("HTTP_PROXY") == "" && os.Getenv("HTTPS_PROXY") == "" {
			// 如果环境变量也未设置，使用默认配置（DefaultProxyURL）
			// 代理配置管理器会自动处理
		}
	}

	// 返回清理函数
	return func() {
		if originalEnabled && originalProxy != "" {
			proxyConfig.SetProxyURL(originalProxy)
		} else {
			proxyConfig.DisableProxy()
		}
	}
}

// SetupProxyFromEnv 从环境变量设置代理（兼容旧代码）
// 如果环境变量未设置，则设置默认值
// 返回清理函数，可以在 defer 中调用
func SetupProxyFromEnv(defaultProxyURL string) func() {
	proxyConfig := config.GetProxyConfig()

	// 保存原始状态
	originalProxy := proxyConfig.GetProxyURLString()
	originalEnabled := proxyConfig.IsProxyEnabled()

	// 检查环境变量
	httpProxy := os.Getenv("HTTP_PROXY")
	httpsProxy := os.Getenv("HTTPS_PROXY")

	if httpProxy == "" && httpsProxy == "" {
		// 如果环境变量未设置，使用默认值
		if defaultProxyURL != "" {
			os.Setenv("HTTP_PROXY", defaultProxyURL)
			os.Setenv("HTTPS_PROXY", defaultProxyURL)
			// 重新初始化代理配置以读取新的环境变量
			proxyConfig.SetProxyURL(defaultProxyURL)
		}
	} else {
		// 环境变量已设置，代理配置管理器会自动读取
		// 显式设置以确保立即生效
		if httpProxy != "" {
			proxyConfig.SetProxyURL(httpProxy)
		} else if httpsProxy != "" {
			proxyConfig.SetProxyURL(httpsProxy)
		}
	}

	// 返回清理函数
	return func() {
		// 清理环境变量（如果是我们设置的）
		if httpProxy == "" && httpsProxy == "" && defaultProxyURL != "" {
			os.Unsetenv("HTTP_PROXY")
			os.Unsetenv("HTTPS_PROXY")
		}

		// 恢复原始代理配置
		if originalEnabled && originalProxy != "" {
			proxyConfig.SetProxyURL(originalProxy)
		} else {
			proxyConfig.DisableProxy()
		}
	}
}

// CleanupProxyForTest 清理测试代理配置
func CleanupProxyForTest() {
	proxyConfig := config.GetProxyConfig()
	proxyConfig.DisableProxy()
}

// DisableProxyForTest 禁用代理（用于测试不需要代理的场景）
// 返回清理函数，可以在 defer 中调用
func DisableProxyForTest() func() {
	proxyConfig := config.GetProxyConfig()

	// 保存原始状态
	originalProxy := proxyConfig.GetProxyURLString()
	originalEnabled := proxyConfig.IsProxyEnabled()

	// 禁用代理
	proxyConfig.DisableProxy()

	// 返回清理函数
	return func() {
		if originalEnabled && originalProxy != "" {
			proxyConfig.SetProxyURL(originalProxy)
		}
	}
}
