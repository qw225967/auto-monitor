package snowflake

import (
	"github.com/sony/sonyflake"
	"time"
)

func init() {
	instance = &snowflake{
		Sonyflake: *sonyflake.NewSonyflake(sonyflake.Settings{}),
	}
}

var instance *snowflake

type snowflake struct {
	sonyflake.Sonyflake
}

func NextID() uint64 {
	snowflakeId, _err := instance.NextID()
	if _err != nil {
		return uint64(time.Now().UnixNano())
	}
	return snowflakeId
}
