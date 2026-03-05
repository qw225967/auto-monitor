package utils

import (
	"github.com/hashicorp/go-uuid"
)

// GenUUID 随机生成UUID
func GenUUID() string {
	generateUUID, err := uuid.GenerateUUID()
	if err != nil {
		return ""
	}
	return generateUUID
}
