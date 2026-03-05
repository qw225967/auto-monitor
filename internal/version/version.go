package version

import "fmt"

var (
	// CommitHash Git 提交哈希 (short)
	CommitHash = "dev"
	// BuildTime 构建时间
	BuildTime = "unknown"
)

// GetVersion 返回组合后的版本号字符串
// 格式: gitShortHash/yy:mm-dd hh:mm
func GetVersion() string {
	if BuildTime == "unknown" {
		return CommitHash
	}
	return fmt.Sprintf("%s/%s", CommitHash, BuildTime)
}
