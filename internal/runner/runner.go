package runner

import "time"

// Runner 主流程编排（路由探测已移除）
type Runner struct {
	threshold float64
}

// Options Runner 运行参数（保留结构兼容旧调用）
type Options struct {
	DetectConcurrency int
	DetectTimeout     time.Duration
}

// New 创建 Runner
func New(threshold float64) *Runner {
	return &Runner{threshold: threshold}
}

// NewWithOptions 创建 Runner（兼容旧调用签名，det 参数已废弃）
func NewWithOptions(_ interface{}, threshold float64, _ Options) *Runner {
	return &Runner{threshold: threshold}
}
