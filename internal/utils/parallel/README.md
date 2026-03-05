# parallel
> 这是一个处理协程并发的包

### 进行一些定时任务的管理

```go
package main

import (
	"time"
    
	"super-brush/pkg/parallel"
)

func goJobFirst() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			//do some job
		}
	}
}

func goJobSecond() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			//do some job
		}
	}
}

func main() {
	//new
	goGroup := parallel.NewRoutineGroup()
	//定义任务组
	functions := []func(){goJobFirst, goJobSecond}
	//开始执行任务
	goGroup.Parallel(functions...)
	//wait 可选
	goGroup.Wait()

	return
}

```


### 一些其他用途

```go
package main

import (
	"time"
    
	"super-brush/pkg/parallel"
)

func goJobFirst() {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			//do some job
		}
	}
}

func goJobSecond() {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			//do some job
		}
	}
}

func main() {
	//安全的进行go func()
	parallel.GoSafe(func() {
		//do some job async
	})
	//安全的进行 func()
	parallel.RunSafe(func() {
		//do some job sync
	})

	//管理一些异步任务 并且一定要wait
	functions := []func(){goJobFirst, goJobSecond}
	//这个组件必然会wait
	parallel.Parallel(functions...)

}

```