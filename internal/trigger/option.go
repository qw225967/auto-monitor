package trigger

import "time"

type IntervalOpt struct {
	orderLoop             time.Duration // 下单loop循环间隔
	calcPriceDiff         time.Duration // 循环计算价差循环间隔
	calcOptimalThresholds time.Duration // 计算最优阈值循环间隔
	calcSlippage          time.Duration // 计算滑点循环间隔
	cleanupPriceDiffs     time.Duration // 清理价差数据间隔（0 表示不自动清理）
}

func defaultIntervalOpt() *IntervalOpt {
	return &IntervalOpt{
		orderLoop:             time.Millisecond * 200,
		calcPriceDiff:         time.Millisecond * 200,
		calcOptimalThresholds: time.Second * 5,
		calcSlippage:          time.Second * 2,
		cleanupPriceDiffs:     0, // 默认不自动清理，需要手动或通过 web 配置
	}
}

func (t *Trigger) SetIntervalOpt(intervalOpt *IntervalOpt) *Trigger {
	t.intervalOpt = intervalOpt
	return t
}

type SlippageOpt struct {
	limit  float64
	amount float64
}

func defaultSlippageOpt() *SlippageOpt {
	return &SlippageOpt{
		limit:  0.1,     // 默认限制0.1
		amount: 10000.0, // TODO:默认先写死，后续改为动态的
	}
}

func (t *Trigger) SetSlippageOpt(slippageOpt *SlippageOpt) *Trigger {
	t.slippageOpt = slippageOpt
	return t
}

type OrderOpt struct {
	lastExecuteTime time.Time     // 上一次执行成功的时间
	cooldown        time.Duration // 同方向两次下单最短间隔（+A-B 与 -A+B 各自独立）
}

func defaultOrderOpt() *OrderOpt {
	return &OrderOpt{
		lastExecuteTime: time.Time{},
		cooldown:        2 * time.Second, // 默认 2 秒，10 秒过长会明显压低同方向触发频率
	}
}

// OrderOptWithCooldown 指定冷却时间构造 OrderOpt，便于按需调高/调低触发频率
func OrderOptWithCooldown(cooldown time.Duration) *OrderOpt {
	return &OrderOpt{cooldown: cooldown}
}

func (t *Trigger) SetOrderOpt(orderOpt *OrderOpt) *Trigger {
	t.orderOpt = orderOpt
	return t
}

// DefaultIntervalOpt 返回默认的间隔选项（公开方法）
func DefaultIntervalOpt() *IntervalOpt {
	return defaultIntervalOpt()
}

// DefaultSlippageOpt 返回默认的滑点选项（公开方法）
func DefaultSlippageOpt() *SlippageOpt {
	return defaultSlippageOpt()
}

// DefaultOrderOpt 返回默认的订单选项（公开方法）
func DefaultOrderOpt() *OrderOpt {
	return defaultOrderOpt()
}

