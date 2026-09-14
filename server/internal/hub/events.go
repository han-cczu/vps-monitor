package hub

import (
	"sync"
	"time"
)

// EventKind 是事件类型。
type EventKind string

const (
	// EventServerOnline 在节点从离线变为在线时发出。
	EventServerOnline EventKind = "server.online"
	// EventServerOffline 在离线扫描判定节点掉线时发出。
	EventServerOffline          EventKind = "server.offline"
	EventCoreApplyFailed        EventKind = "core.apply_failed"
	EventServerTrafficThreshold EventKind = "server.traffic"
	EventServerExpiringSoon     EventKind = "server.expire"
)

// Event 是总线上的一条事件。
type Event struct {
	Kind       EventKind
	ServerID   int64
	At         time.Time
	TargetType string
	TargetID   int64
	Threshold  int
	Message    string
}

// Bus 是一个极简的事件总线：发布者不阻塞，订阅者各拿一条带缓冲的通道。
//
// 步骤 19 的告警规则会订阅它。这里刻意不做持久化与重放——
// 掉线告警关心的是"现在发生了什么"，补历史是 events 表的事（步骤 19 再建）。
type Bus struct {
	mu   sync.RWMutex
	subs []chan Event
}

// Subscribe 返回一个新的订阅通道。buffer 是它的缓冲长度，至少为 1。
//
// 订阅之后不支持取消：目前的订阅者都和进程同生命周期，没必要为此加一层引用管理。
func (b *Bus) Subscribe(buffer int) <-chan Event {
	if buffer < 1 {
		buffer = 1
	}
	ch := make(chan Event, buffer)

	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()

	return ch
}

// Publish 向所有订阅者投递事件。
//
// 非阻塞：订阅者的缓冲满了就丢掉这一条，绝不能让一个慢订阅者卡住离线扫描。
func (b *Bus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}
