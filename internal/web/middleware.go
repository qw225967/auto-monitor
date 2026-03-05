package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// responseWriter 包装 http.ResponseWriter 以捕获响应数据
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	body       *bytes.Buffer
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		body:           &bytes.Buffer{},
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	rw.body.Write(b)
	return rw.ResponseWriter.Write(b)
}

// loggingMiddleware HTTP 请求日志中间件
func (d *Dashboard) loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		//startTime := time.Now()

		// 读取请求体（用于记录入参）
		var requestBody []byte
		if r.Body != nil {
			requestBody, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(requestBody))
		}

		// 包装 ResponseWriter 以捕获响应
		rw := newResponseWriter(w)

		// 执行实际的处理器
		next(rw, r)

		// 计算耗时
		//duration := time.Since(startTime)

		// 构建请求参数信息
		requestParams := map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
			"query":  r.URL.Query(),
		}

		// 如果有请求体，尝试解析 JSON
		if len(requestBody) > 0 {
			var jsonData interface{}
			if err := json.Unmarshal(requestBody, &jsonData); err == nil {
				requestParams["body"] = jsonData
			} else {
				requestParams["body"] = string(requestBody)
			}
		}

		// 构建响应信息
		responseInfo := map[string]interface{}{
			"statusCode": rw.statusCode,
		}

		// 尝试解析响应体为 JSON
		responseBody := rw.body.Bytes()
		if len(responseBody) > 0 {
			var jsonData interface{}
			if err := json.Unmarshal(responseBody, &jsonData); err == nil {
				responseInfo["body"] = jsonData
			} else {
				// 如果不是 JSON，截取前 500 字符
				bodyStr := string(responseBody)
				if len(bodyStr) > 500 {
					bodyStr = bodyStr[:500] + "..."
				}
				responseInfo["body"] = bodyStr
			}
		}

		// 记录日志
		//d.logger.Infow("HTTP Request",
		//	"method", r.Method,
		//	"path", r.URL.Path,
		//	"remoteAddr", r.RemoteAddr,
		//	"requestParams", requestParams,
		//	"response", responseInfo,
		//	"duration", duration.String(),
		//	"durationMs", duration.Milliseconds(),
		//)
	}
}
