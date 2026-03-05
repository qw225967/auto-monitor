package parallel

import (
	"runtime"
	"sync"

	"github.com/qw225967/auto-monitor/internal/utils/logger"
)

var _logger = logger.GetLoggerInstance().Sugar()

type RoutineGroup struct {
	waitGroup sync.WaitGroup
}

func NewRoutineGroup() *RoutineGroup {
	return new(RoutineGroup)
}

func (g *RoutineGroup) Go(fn func()) {
	g.waitGroup.Add(1)

	go func() {
		defer g.waitGroup.Done()
		fn()
	}()
}

func (g *RoutineGroup) GoSafe(fn func()) {
	g.waitGroup.Add(1)

	GoSafe(func() {
		defer g.waitGroup.Done()
		fn()
	})
}

func (g *RoutineGroup) Parallel(fns ...func()) {
	if len(fns) == 0 {
		return
	}
	for _, fn := range fns {
		g.GoSafe(fn)
	}
}

// Wait waits all running functions to be done.
func (g *RoutineGroup) Wait() {
	g.waitGroup.Wait()
}

func GoSafe(fn func()) {
	go RunSafe(fn)
}

func RunSafe(fn func()) {
	defer Recover()
	fn()
}

func Recover(cleanups ...func()) {
	for _, cleanup := range cleanups {
		cleanup()
	}
	if p := recover(); p != nil {
		buf := make([]byte, 10240)
		runtime.Stack(buf, false)
		_logger.Fatalf("recover, info=%v, err=%v", string(buf), p)
	}
}
