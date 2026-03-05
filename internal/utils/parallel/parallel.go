package parallel

func Parallel(fns ...func()) {
	if len(fns) == 0 {
		return
	}
	group := NewRoutineGroup()
	for _, fn := range fns {
		group.GoSafe(fn)
	}
	group.Wait()
}
