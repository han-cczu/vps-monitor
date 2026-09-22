package proxyobserve

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

var ErrInvalid = errors.New("invalid proxy observation")

// ErrStale 表示这一轮分页已经不能落库：它采集之后服务端清理过观测记录，
// 或探针重新协商了观测会话。探针没有做错任何事，丢掉即可。
var ErrStale = errors.New("stale proxy observation pages")

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

// ValidInstanceIdentifier 判断实例 id 是否是协议允许的形式。
func ValidInstanceIdentifier(id string) bool { return identifier.MatchString(id) }

// NewSession 生成一个观测会话标识。会话由服务端签发：协商成功时登记一次，
// 清理观测记录时再换一个，于是清理之前采集的分页会被判过期。
func NewSession() string {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "s" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(nonce[:])
}

type assembly struct {
	session            string
	seq, last          int64
	next, pages, bytes int
	complete           bool
	at                 int64
	started            time.Time
	// droppedSeq 是被丢弃的那一轮分页的序号：丢掉之后同一序号的分页一律不再接受。
	droppedSeq int64
	items      map[string]proto.ObservedInstance
}

type Service struct {
	db      *store.DB
	mu      sync.Mutex
	pending map[int64]*assembly

	// Session 返回某节点当前生效的观测会话（没有连接时为空串）。由 Hub 提供。
	Session func(id int64) string
	// Supersede 作废某节点当前的观测会话并下发新的会话，返回是否真的换掉了
	// （节点离线时为 false：没有连接就没有在途分页可挡）。由 Hub 提供。
	Supersede func(id int64) bool

	now func() time.Time
}

func New(db *store.DB) *Service {
	return &Service{db: db, pending: map[int64]*assembly{}, now: time.Now}
}

// superseded 报告某条连接签发的会话是否已经作废。Observation session 是服务端
// 自己签发的，清理观测记录时会换一个，于是清理之前采集的分页无论何时到达都会
// 在这里被判过期——这是不依赖任何一方时钟的屏障。
//
// 没装配回调时按未作废处理（没有 Hub 就没有会话可言）；装配了回调却拿到空会话，
// 说明这条连接当前没有有效观测会话：离线、退避，或者还没协商完 hello 就发了观测
// 消息。这时必须拒绝旧消息——「连接已 detach」不等于「进入处理流程的旧回调都结束
// 了」：Hub 先摘连接表再异步关连接、取消上下文，读循环里带着旧会话的回调随时可能
// 落到这里；空会话一律放行的话，detach 之后才做的清理挡不住它。
func (s *Service) superseded(id int64, session string) bool {
	if s.Session == nil {
		return false
	}
	current := s.Session(id)
	return current == "" || current != session
}

func safe(s string, n int) bool {
	if len(s) > n || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func validInstance(i proto.ObservedInstance) bool {
	if !identifier.MatchString(i.ID) || (i.Core != "sing-box" && i.Core != "xray") || (i.Ownership != "external" && i.Ownership != "managed") || len(i.Inbounds) > 64 || len(i.Ports) > 512 || len(i.ConfigPaths) > 16 || len(i.Issues) > 64 {
		return false
	}
	for s, n := range map[string]int{i.Source: 48, i.Version: 64, i.Service: 128, i.Binary: 1024, i.Namespace: 128, i.ProcessStart: 32, i.StatsStatus: 48} {
		if !safe(s, n) {
			return false
		}
	}
	for _, p := range i.ConfigPaths {
		if !safe(p, 1024) {
			return false
		}
	}
	for _, s := range i.Issues {
		if !safe(s, 96) {
			return false
		}
	}
	for _, p := range i.Ports {
		if !safe(p.Address, 128) || p.Port < 1 || p.Port > 65535 || p.Network != "tcp" && p.Network != "udp" {
			return false
		}
	}
	for _, in := range i.Inbounds {
		if !identifier.MatchString(in.ID) || !safe(in.Tag, 128) || !safe(in.Protocol, 48) || !safe(in.Listen, 128) || !safe(in.Port, 128) || !safe(in.Transport, 48) || in.Users != nil && (*in.Users < 0 || *in.Users > 1000000) {
			return false
		}
		if u := in.Usage; u != nil {
			if u.Scope != "manager_total" && u.Scope != "reference" || u.Source != "x_ui_database" && u.Source != "sing-box_api" && u.Source != "xray_api" {
				return false
			}
			for _, v := range []*int64{u.Up, u.Down, u.Limit, u.ExpireAt} {
				if v != nil && *v < 0 {
					return false
				}
			}
		}
	}
	return true
}

// Receive rejects replay, out-of-order/incomplete pages, unknown fields and
// oversized inventories. A database transaction publishes only whole scans.
//
// session 由 Hub 在分发时给出，是服务端为这条连接签发的观测会话。清理观测记录会
// 换掉会话：清理之前采集的分页，无论第一页是否已经到达，都会在落库前被判过期。
func (s *Service) Receive(ctx context.Context, id int64, session string, raw []byte) error {
	if len(raw) > proto.ProxyMaxFrame || session == "" {
		return ErrInvalid
	}
	var p proto.ProxyObservation
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Type != proto.TypeProxyObservation || p.Session != session || p.Sequence < 1 || p.Page < 0 || p.Pages < 1 || p.Pages > proto.ProxyMaxPages || p.Page >= p.Pages || len(p.Instances) > 1 {
		return ErrInvalid
	}
	for _, i := range p.Instances {
		if !validInstance(i) {
			return ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 已经不属于当前这一代的分页直接丢弃：它可能是清理前的在途结果，
	// 也可能来自一条已经没有有效观测会话的连接。
	if s.superseded(id, session) {
		return ErrStale
	}
	a := s.pending[id]
	if a == nil || a.session != session {
		a = &assembly{session: session}
		s.pending[id] = a
	}
	if p.Sequence == a.droppedSeq {
		return ErrStale
	}
	if p.Page == 0 {
		if p.Sequence <= a.last || p.Sequence <= a.seq {
			return ErrInvalid
		}
		a.seq = p.Sequence
		a.next = 0
		a.pages = p.Pages
		a.complete = p.ScanComplete
		a.at = p.CollectedAt
		a.started = s.now()
		a.items = map[string]proto.ObservedInstance{}
		a.bytes = 0
	}
	if p.Sequence != a.seq || p.Page != a.next || p.Pages != a.pages || p.ScanComplete != a.complete || p.CollectedAt != a.at || s.now().Sub(a.started) > 30*time.Second {
		// 这一轮已经拼不完整：丢掉整轮，并记住序号，剩下的分页不能靠后到而拼出一份半截快照。
		s.dropLocked(id)
		return ErrStale
	}
	a.bytes += len(raw)
	if a.bytes > 4<<20 {
		return ErrInvalid
	}
	for _, item := range p.Instances {
		if old, ok := a.items[item.ID]; ok {
			before, after := old, item
			before.Inbounds = nil
			after.Inbounds = nil
			if !reflect.DeepEqual(before, after) {
				return ErrInvalid
			}
			item.Inbounds = append(old.Inbounds, item.Inbounds...)
		}
		if len(item.Inbounds) > proto.ProxyMaxInbounds {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, in := range item.Inbounds {
			if seen[in.ID] {
				return ErrInvalid
			}
			seen[in.ID] = true
		}
		a.items[item.ID] = item
	}
	if len(a.items) > proto.ProxyMaxInstances {
		return ErrInvalid
	}
	a.next++
	if a.next != a.pages {
		return nil
	}
	items := make([]proto.ObservedInstance, 0, len(a.items))
	count := 0
	for _, i := range a.items {
		items = append(items, i)
		count += len(i.Inbounds)
	}
	if count > 4096 {
		return ErrInvalid
	}
	// 落库前再核对一次会话：清理可能正好发生在这批分页组装的过程中。
	if s.superseded(id, session) {
		s.dropLocked(id)
		return ErrStale
	}
	if err := s.db.SaveProxyObservations(ctx, id, items, a.complete, s.now().Unix(), a.at); err != nil {
		return err
	}
	a.last = a.seq
	a.items = nil
	return nil
}

// Forget 丢掉该节点的组装状态（节点被删除时调用）。
func (s *Service) Forget(id int64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
}

// HandleDelete 删除面板保存的某一条观测记录，返回这条记录原来是否存在。
//
// 节点离线时也允许清理：面板里的历史记录与本机状态无关。删除会丢弃未拼完的分页，
// 并作废观测会话，所以删除之前采集、删除之后才到达的在途分页不会把它写回来。
func (s *Service) HandleDelete(ctx context.Context, id int64, instance string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found, err := s.db.DeleteProxyObservation(ctx, id, instance)
	if err != nil || !found {
		return false, err
	}
	s.invalidateLocked(id)
	return true, nil
}

// HandleReset 清空该节点在面板保存的全部观测快照。
//
// 只动观测数据：探针、真实代理、配置、托管记录和账务数据都不受影响。
// 清空丢弃未拼完的分页并作废观测会话，清理之前采集的扫描结果一律不会再落库。
func (s *Service) HandleReset(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.db.ClearProxyObservations(ctx, id); err != nil {
		return err
	}
	s.invalidateLocked(id)
	return nil
}

// invalidateLocked 作废该节点当前这一代观测会话，并丢掉正在拼装的分页。
//
// 换会话与清数据都在 s.mu 之内完成：Receive 要么赶在清理之前落库（随后被清掉），
// 要么在清理之后看到新会话而被拒绝。节点离线时换会话是空操作，但空会话本身
// 就是「不属于任何一代」，清理之后到达的旧分页照样会被 superseded 拦下。
func (s *Service) invalidateLocked(id int64) {
	if s.Supersede != nil {
		s.Supersede(id)
	}
	s.dropLocked(id)
}

// dropLocked 丢弃该节点正在拼装的分页，并记住它的序号。调用方必须持有 s.mu。
func (s *Service) dropLocked(id int64) {
	a := s.pending[id]
	if a == nil {
		return
	}
	if a.seq > 0 {
		a.droppedSeq = a.seq
	}
	a.next, a.pages, a.bytes, a.items = 0, 0, 0, nil
}

// Count 返回该节点在面板保存的观测记录条数，用于删除/重置前的审计与日志。
func (s *Service) Count(ctx context.Context, id int64) int {
	rows, _, err := s.db.ProxyObservations(ctx, id)
	if err != nil {
		return 0
	}
	return len(rows)
}

// List 返回该节点保存的全部记录（当前实例在前、历史记录在后）和节点级扫描状态；
// 扫描状态还没写入过时返回 nil，调用方据此区分「等待首个快照」与「完整扫描后没有实例」。
//
// stale 只说明这一行不是刚刚收到的数据，不代表实例已经消失：不完整扫描、离线、
// 超时都不会把实例标成 absent。
func (s *Service) List(ctx context.Context, id int64, online bool) ([]store.ProxyObservationRow, *store.ProxyScanState, error) {
	rows, scan, err := s.db.ProxyObservations(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	for i := range rows {
		if !online || rows[i].Absent || s.now().Unix()-rows[i].ReceivedAt > 90 {
			rows[i].Stale = true
		}
	}
	if scan != nil {
		scan.Stale = !online || s.now().Unix()-scan.ReceivedAt > 90
	}
	return rows, scan, nil
}
