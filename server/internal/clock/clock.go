// Package clock 保存面板的业务时区（VM_TZ）。
//
// 账期滚动、月度重置、每日提醒这类按日历算的任务都用 clock.Now() / clock.Location()，
// 不直接用 time.Now()，这样容器里 TZ 是 UTC 也不影响结算日。
package clock

import (
	"sync/atomic"
	"time"
)

var loc atomic.Pointer[time.Location]

func init() {
	loc.Store(time.Local)
}

// SetLocation 设置业务时区，启动时调用一次。
func SetLocation(l *time.Location) {
	if l != nil {
		loc.Store(l)
	}
}

// Location 返回业务时区。
func Location() *time.Location {
	return loc.Load()
}

// Now 返回业务时区下的当前时间。
func Now() time.Time {
	return time.Now().In(Location())
}
