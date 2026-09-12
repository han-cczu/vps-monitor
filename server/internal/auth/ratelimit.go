package auth

import (
	"context"
	"sync"
	"time"
)

// 登录限速默认参数（设计方案 §6.7）：同一 IP 15 分钟内失败 5 次即锁 15 分钟。
const (
	DefaultMaxFailures = 5
	DefaultWindow      = 15 * time.Minute
	DefaultLockout     = 15 * time.Minute
)

// Limiter 按 key（通常是客户端 IP）统计登录失败：窗口内失败达到上限即锁定一段时间。
// 全部在内存里，进程重启即清零；单管理员面板够用。
type Limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	lockout time.Duration
	now     func() time.Time
	buckets map[string]*bucket
}

type bucket struct {
	fails       []time.Time // 窗口内的失败时刻
	lockedUntil time.Time   // 零值表示未锁定
}

// NewLimiter 创建限速器：window 内失败 maxFailures 次即锁定 lockout。
func NewLimiter(maxFailures int, window, lockout time.Duration) *Limiter {
	return &Limiter{
		max:     maxFailures,
		window:  window,
		lockout: lockout,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Locked 返回 key 当前是否处于锁定期，以及还要等多久。锁定已过期的记录会顺手清掉。
func (l *Limiter) Locked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		return false, 0
	}
	now := l.now()
	if now.Before(b.lockedUntil) {
		return true, b.lockedUntil.Sub(now)
	}
	if !b.lockedUntil.IsZero() {
		// 锁已到期：重新开始计数
		delete(l.buckets, key)
	}
	return false, 0
}

// Fail 记录一次失败。返回这次失败之后是否处于锁定期，以及剩余锁定时长。
func (l *Limiter) Fail(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{}
		l.buckets[key] = b
	}
	if now.Before(b.lockedUntil) {
		return true, b.lockedUntil.Sub(now)
	}
	b.lockedUntil = time.Time{}

	kept := b.fails[:0]
	for _, ts := range b.fails {
		if now.Sub(ts) < l.window {
			kept = append(kept, ts)
		}
	}
	b.fails = append(kept, now)

	if len(b.fails) >= l.max {
		b.lockedUntil = now.Add(l.lockout)
		b.fails = nil
		return true, l.lockout
	}
	return false, 0
}

// Reset 清掉 key 的记录（登录成功后调用）。
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Cleanup 删除既不在锁定期、窗口内也没有失败记录的 key，返回删除数量。
func (l *Limiter) Cleanup() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	removed := 0
	for key, b := range l.buckets {
		if now.Before(b.lockedUntil) {
			continue
		}
		alive := false
		for _, ts := range b.fails {
			if now.Sub(ts) < l.window {
				alive = true
				break
			}
		}
		if !alive {
			delete(l.buckets, key)
			removed++
		}
	}
	return removed
}

// Run 每隔 interval 调用一次 Cleanup，直到 ctx 结束。放在后台 goroutine 里跑。
func (l *Limiter) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Cleanup()
		}
	}
}
