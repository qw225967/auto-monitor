# 代理配置管理器 (ProxyConfig)

统一的代理配置管理器，支持从环境变量或代码配置 HTTP/HTTPS 代理，适用于所有网络请求。

## 特性

- ✅ **单例模式**：全局唯一配置实例
- ✅ **线程安全**：支持并发访问
- ✅ **环境变量支持**：自动从环境变量读取配置
- ✅ **代码配置**：支持运行时动态设置
- ✅ **易于集成**：提供便捷的 HTTP Client 创建方法

## 快速开始

### 基本使用

```go
import "auto-arbitrage/internal/config"

// 获取代理配置（自动从环境变量读取）
proxyConfig := config.GetProxyConfig()

// 创建带代理的 HTTP Client
httpClient := proxyConfig.CreateClient(time.Second * 10)

// 使用 HTTP Client 发送请求
resp, err := httpClient.Get("https://api.example.com")
```

### 设置代理

```go
proxyConfig := config.GetProxyConfig()

// 方式一：通过代码设置
err := proxyConfig.SetProxyURL("http://127.0.0.1:9876")
if err != nil {
    log.Fatal(err)
}

// 方式二：通过环境变量（推荐）
// 设置环境变量：export HTTP_PROXY=http://127.0.0.1:9876
```

## API 文档

### 获取实例

#### `GetProxyConfig() *ProxyConfig`

获取代理配置单例。首次调用时会自动从环境变量初始化配置。

```go
proxyConfig := config.GetProxyConfig()
```

### 配置方法

#### `SetProxyURL(proxyURLStr string) error`

设置代理地址。如果传入空字符串，则禁用代理。

**参数：**
- `proxyURLStr`: 代理地址，例如 `"http://127.0.0.1:9876"`

**返回：**
- `error`: 如果代理地址格式错误则返回错误

```go
err := proxyConfig.SetProxyURL("http://127.0.0.1:9876")
if err != nil {
    log.Printf("设置代理失败: %v", err)
}
```

#### `EnableProxy()` / `DisableProxy()`

启用或禁用代理（使用当前配置的代理地址）。

```go
proxyConfig.EnableProxy()  // 启用代理
proxyConfig.DisableProxy() // 禁用代理
```

### 查询方法

#### `IsProxyEnabled() bool`

检查是否启用代理。

```go
if proxyConfig.IsProxyEnabled() {
    fmt.Println("代理已启用")
}
```

#### `GetProxyURL() *url.URL`

获取代理 URL 对象。如果未启用代理，返回 `nil`。

```go
proxyURL := proxyConfig.GetProxyURL()
if proxyURL != nil {
    fmt.Printf("代理地址: %s\n", proxyURL.String())
}
```

#### `GetProxyURLString() string`

获取代理 URL 字符串。如果未启用代理，返回空字符串。

```go
proxyStr := proxyConfig.GetProxyURLString()
if proxyStr != "" {
    fmt.Printf("代理地址: %s\n", proxyStr)
}
```

### HTTP Client 创建方法

#### `CreateClient(timeout time.Duration) *http.Client`

创建带代理配置的 HTTP Client。

**参数：**
- `timeout`: 请求超时时间，例如 `time.Second * 10`

**返回：**
- `*http.Client`: 配置好代理的 HTTP Client

```go
httpClient := proxyConfig.CreateClient(time.Second * 30)
```

#### `CreateClientWithDefaultTimeout() *http.Client`

创建带代理配置的 HTTP Client（默认超时时间为 10 秒）。

```go
httpClient := proxyConfig.CreateClientWithDefaultTimeout()
```

#### `CreateTransport() *http.Transport`

创建带代理配置的 HTTP Transport（用于自定义 HTTP Client）。

```go
transport := proxyConfig.CreateTransport()
httpClient := &http.Client{
    Timeout:   time.Second * 10,
    Transport: transport,
}
```

## 环境变量配置

代理配置管理器支持以下环境变量（按优先级排序）：

1. **HTTP_PROXY** - HTTP 代理地址
2. **HTTPS_PROXY** - HTTPS 代理地址
3. **PROXY_URL** - 通用代理地址

### 设置环境变量

```bash
# Linux/macOS
export HTTP_PROXY=http://127.0.0.1:9876
export HTTPS_PROXY=http://127.0.0.1:9876

# Windows (PowerShell)
$env:HTTP_PROXY="http://127.0.0.1:9876"
$env:HTTPS_PROXY="http://127.0.0.1:9876"
```

### 优先级说明

代码配置（`SetProxyURL()`）的优先级高于环境变量。如果通过代码设置了代理，则忽略环境变量。

## 使用示例

### 示例 1：在 RestClient 中使用

```go
package rest

import (
    "auto-arbitrage/internal/config"
    "time"
)

type RestClient struct {
    httpClient *http.Client
}

func (rs *RestClient) InitRestClient() {
    proxyConfig := config.GetProxyConfig()
    rs.httpClient = proxyConfig.CreateClient(time.Duration(10) * time.Second)
}
```

### 示例 2：动态切换代理

```go
proxyConfig := config.GetProxyConfig()

// 切换到代理 A
proxyConfig.SetProxyURL("http://proxy-a.example.com:8080")
clientA := proxyConfig.CreateClient(time.Second * 10)

// 切换到代理 B
proxyConfig.SetProxyURL("http://proxy-b.example.com:8080")
clientB := proxyConfig.CreateClient(time.Second * 10)
```

### 示例 3：条件代理

```go
proxyConfig := config.GetProxyConfig()

if needProxy {
    proxyConfig.SetProxyURL("http://127.0.0.1:9876")
} else {
    proxyConfig.DisableProxy()
}

httpClient := proxyConfig.CreateClient(time.Second * 10)
```

### 示例 4：测试环境配置

```go
func TestWithProxy(t *testing.T) {
    // 设置测试代理
    proxyConfig := config.GetProxyConfig()
    proxyConfig.SetProxyURL("http://127.0.0.1:9876")
    
    httpClient := proxyConfig.CreateClient(time.Second * 5)
    
    // 执行测试...
    
    // 清理：禁用代理
    proxyConfig.DisableProxy()
}
```

## 扩展说明

### 添加自定义 Transport 配置

如果需要自定义 Transport 的其他配置（如 TLS、连接池等），可以基于 `CreateTransport()` 进行扩展：

```go
proxyConfig := config.GetProxyConfig()
transport := proxyConfig.CreateTransport()

// 添加自定义配置
transport.MaxIdleConns = 100
transport.IdleConnTimeout = 90 * time.Second
transport.TLSClientConfig = &tls.Config{
    InsecureSkipVerify: false,
}

httpClient := &http.Client{
    Timeout:   time.Second * 10,
    Transport: transport,
}
```

### 集成到其他 HTTP 客户端

```go
// 示例：集成到第三方 HTTP 客户端库
proxyConfig := config.GetProxyConfig()
transport := proxyConfig.CreateTransport()

// 使用自定义 Transport
customClient := &http.Client{
    Timeout:   time.Second * 30,
    Transport: transport,
    // 其他配置...
}
```

### 支持配置文件

未来可以扩展支持从配置文件读取代理设置：

```go
// 扩展示例（未来实现）
func (pc *ProxyConfig) LoadFromConfig(configPath string) error {
    // 从配置文件读取代理配置
    // ...
    return nil
}
```

## 注意事项

1. **单例模式**：`GetProxyConfig()` 返回的是全局单例，所有调用共享同一配置
2. **线程安全**：所有方法都是线程安全的，可以在并发环境中使用
3. **环境变量优先级**：代码配置（`SetProxyURL()`）优先级高于环境变量
4. **错误处理**：`SetProxyURL()` 会验证代理地址格式，请务必处理返回的错误

## 相关文件

- `internal/utils/rest/rest_client.go` - REST 客户端（可集成代理配置）
- `internal/utils/notify/telegram/telegram.go` - Telegram 客户端（可集成代理配置）



