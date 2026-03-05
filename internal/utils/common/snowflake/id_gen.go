package snowflake

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sony/sonyflake"
)

// 绝对不能改成之前的时间
var startTime = time.Date(2024, 6, 6, 6, 6, 6, 0, time.UTC)

// NewIdGen 生成 Idgen
func NewIdGen() IdGen {
	return NewIdGenWithConf(&Conf{
		StartTime: startTime,
	})
}

// NewIdGenWithConf 生成 Idgen
func NewIdGenWithConf(conf *Conf) IdGen {
	_idGen, err := sonyflake.New(sonyflake.Settings{
		StartTime:      conf.StartTime,
		MachineID:      conf.MachineID,
		CheckMachineID: conf.CheckMachineID,
	})
	// 对于初始化报错的情况 1. startTime 非法 2. MachineID 非法  3. CheckMachineID 执行后不通过
	if err != nil {
		if !errors.Is(err, sonyflake.ErrNoPrivateAddress) {
			// errors.Is(err, sonyflake.ErrStartTimeAhead) 传入的 startTime 是一个未来时间
			// errors.Is(err, sonyflake.ErrInvalidMachineID) CheckMachineID 检验不过
			panic(fmt.Sprintf("new sonyflake failed: %v", err))
		}
		// 对于 第二点 2 不传入 MachineID 默认使用 私有 ip 的低 16 位。当获取不到私有 ip 时 就会报错
		_idGen = sonyflake.NewSonyflake(sonyflake.Settings{
			StartTime: startTime,
			MachineID: func() (uint16, error) {
				return timeAsMachineID(time.Now()), nil
			},
			CheckMachineID: conf.CheckMachineID,
		})
	}
	return &idGen{sonyflake: _idGen}
}

func timeAsMachineID(time time.Time) uint16 {
	// 用当前时间的分钟和秒钟作为机器 id  uin16 max value = 65535
	atoi, _ := strconv.Atoi(time.Format("45"))
	return uint16(atoi)
}

type Conf struct {
	MachineID      func() (uint16, error)
	CheckMachineID func(uint16) bool
	StartTime      time.Time
}

type IdGen interface {
	NextId() uint64
}

type idGen struct {
	sonyflake *sonyflake.Sonyflake
}

func (r *idGen) NextId() uint64 {
	id, _ := r.sonyflake.NextID()
	return id
}
