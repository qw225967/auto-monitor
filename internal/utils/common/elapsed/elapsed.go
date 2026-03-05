package elapsed

import "time"

type Instance struct {
	start time.Time
	unit  Uint
}

type Uint int

const (
	Second = iota
	MilliSecond
	NanoSecond
)

// New 默认毫秒级
func New() *Instance {
	return NewWithUnit(MilliSecond)
}

func NewWithUnit(unit Uint) *Instance {
	return &Instance{
		start: time.Now(),
		unit:  unit,
	}
}

func (i *Instance) Elapsed() int64 {
	dur := time.Since(i.start)
	var out time.Duration
	switch i.unit {
	case Second:
		out = dur / time.Second
	case MilliSecond:
		out = dur / time.Millisecond
	case NanoSecond:
		out = dur
	}
	return int64(out)
}
