package auth

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(c *fakeClock) *Limiter {
	l := NewLimiter(DefaultMaxFailures, DefaultWindow, DefaultLockout)
	l.now = c.now
	return l
}

func TestLimiterLocksAfterMaxFailures(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
	l := newTestLimiter(c)

	for i := 1; i < DefaultMaxFailures; i++ {
		if locked, _ := l.Fail("1.2.3.4"); locked {
			t.Fatalf("locked after %d failures", i)
		}
		if locked, _ := l.Locked("1.2.3.4"); locked {
			t.Fatalf("Locked() true after %d failures", i)
		}
	}

	locked, retry := l.Fail("1.2.3.4")
	if !locked || retry != DefaultLockout {
		t.Fatalf("5th failure: locked=%v retry=%v", locked, retry)
	}

	// 锁定期内：Locked 为真，剩余时间递减；再 Fail 也不延长
	c.advance(5 * time.Minute)
	locked, retry = l.Locked("1.2.3.4")
	if !locked || retry != 10*time.Minute {
		t.Fatalf("after 5m: locked=%v retry=%v", locked, retry)
	}
	if locked, retry := l.Fail("1.2.3.4"); !locked || retry != 10*time.Minute {
		t.Fatalf("Fail during lock: locked=%v retry=%v", locked, retry)
	}

	// 其他 IP 不受影响
	if locked, _ := l.Locked("5.6.7.8"); locked {
		t.Fatal("other key locked")
	}

	// 15 分钟到：解锁，且重新计数
	c.advance(10 * time.Minute)
	if locked, _ := l.Locked("1.2.3.4"); locked {
		t.Fatal("still locked after lockout elapsed")
	}
	if locked, _ := l.Fail("1.2.3.4"); locked {
		t.Fatal("first failure after unlock must not lock again")
	}
}

func TestLimiterWindowSlides(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
	l := newTestLimiter(c)

	for i := 0; i < DefaultMaxFailures-1; i++ {
		l.Fail("k")
	}
	// 窗口过了，旧失败不再计数
	c.advance(DefaultWindow + time.Second)
	if locked, _ := l.Fail("k"); locked {
		t.Fatal("failures outside the window must not count")
	}
}

func TestLimiterResetAndCleanup(t *testing.T) {
	c := &fakeClock{t: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)}
	l := newTestLimiter(c)

	l.Fail("recent")
	l.Fail("stale")
	l.Fail("reset-me")
	l.Reset("reset-me")
	if _, ok := l.buckets["reset-me"]; ok {
		t.Fatal("Reset did not drop the key")
	}

	// 快到窗口末尾：recent 再失败一次保持新鲜，locked 此时才锁上（锁定期比窗口晚结束）
	c.advance(DefaultWindow - time.Second)
	l.Fail("recent")
	for i := 0; i < DefaultMaxFailures; i++ {
		l.Fail("locked")
	}

	// 越过窗口：只有 stale 该被清掉
	c.advance(2 * time.Second)
	if removed := l.Cleanup(); removed != 1 {
		t.Fatalf("removed=%d want 1 (stale)", removed)
	}
	if _, ok := l.buckets["locked"]; !ok {
		t.Fatal("locked key must survive cleanup while lock is active")
	}
	if _, ok := l.buckets["recent"]; !ok {
		t.Fatal("recent key must survive cleanup")
	}
	if _, ok := l.buckets["stale"]; ok {
		t.Fatal("stale key must be removed")
	}

	// 锁定期与窗口都过了：剩下的也该清光
	c.advance(DefaultLockout + DefaultWindow)
	if removed := l.Cleanup(); removed != 2 {
		t.Fatalf("removed=%d want 2 (locked + recent)", removed)
	}
	if len(l.buckets) != 0 {
		t.Fatalf("buckets left: %d", len(l.buckets))
	}
}
