// Package hub 维护每台节点的实时状态：agent 连接、在线判定、浏览器快照广播。
//
// 分工：
//   - state.go   Registry（内存态）与对外的 ServerView 快照结构
//   - agent.go   GET /api/agent/ws：agent 接入、鉴权、消息分发
//   - client.go  GET /api/ws：浏览器接入、每秒广播
//   - offline.go 离线扫描
//   - events.go  上下线事件总线
//
// 在线状态是内存态，不入库：进程重启后 agent 几秒内就会重连补齐，
// 为它维护一份持久化副本只会带来"库里写着在线、其实早就没了"的假象。
package hub

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

// TypeSnapshot 是 server → 浏览器的全量快照消息。
//
// 放在 hub 而不是 proto：proto 是 agent 与 server 共享的包（必须零依赖），
// 而 ServerView 里带着价格、账期这些只属于面板的字段，agent 不该知道。
const TypeSnapshot = "snapshot"

// configTTL 是静态配置（名称、地区、价格等）的缓存时长。
//
// Snapshot() 每秒被调用一次，不能每次都查库；写接口会主动失效缓存，
// 所以这个 60 秒只是兜底，正常改配置是立刻生效的。
const configTTL = 60 * time.Second

// ServerState 是一台节点的内存态。
type ServerState struct {
	ID           int64
	Online       bool
	LastSeen     time.Time // 服务端收到最后一条 metrics 的时刻，不用 agent 上报的 ts
	PublicIP     string    // 从 agent 连接地址记下的公网 IP
	AgentVersion string
	Host         proto.HostInfo
	Latest       *proto.Metrics // 最后一条 metrics，没收到过是 nil

	// 后续步骤追加：PingRecent（09）、Core（13）、Traffic（18）
}

// configLister 是 Registry 需要的库能力，抽成接口方便测试塞假数据。
type configLister interface {
	ListServers(ctx context.Context) ([]store.Server, error)
	ListHostInfo(ctx context.Context) (map[int64]store.HostInfo, error)
}

// Registry 保存全部节点的内存态，并负责把它和库里的静态配置拼成快照。
type Registry struct {
	mu sync.RWMutex
	m  map[int64]*ServerState

	db  configLister
	now func() time.Time

	// pingSource 由 ping 服务在装配时挂上；没挂时快照里的 ping 恒为空数组。
	pingSource    func(serverID int64) []PingView
	coreSource    func(serverID int64) any
	trafficSource func(serverID int64) *TrafficView

	cacheMu sync.Mutex
	cache   []store.Server
	cacheAt time.Time
}

// NewRegistry 新建注册表。db 用来读节点的静态配置。
func NewRegistry(db configLister) *Registry {
	return &Registry{
		m:   map[int64]*ServerState{},
		db:  db,
		now: time.Now,
	}
}

// Get 返回一台节点的状态副本，没有记录返回 false。
//
// 返回副本而不是 *ServerState（设计文档 §4.1 写的是指针）：指针一旦逃出锁外，
// 调用方读 Latest 的同时 agent 协程可能正在写，就是一个数据竞争。
// Latest 指向的 Metrics 本身只替换不原地改，所以随副本共享是安全的。
func (r *Registry) Get(id int64) (ServerState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	st, ok := r.m[id]
	if !ok {
		return ServerState{}, false
	}
	return *st, true
}

// Update 在写锁内修改一台节点的状态，记录不存在时先建一条。
func (r *Registry) Update(id int64, fn func(*ServerState)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	st, ok := r.m[id]
	if !ok {
		st = &ServerState{ID: id}
		r.m[id] = st
	}
	fn(st)
}

// UpdateExisting 只在记录已存在时修改，不会隐式新建。
//
// agent 消息走这条路：节点被删除后，在途的 metrics 不该把它的内存态复活
// （复活出来的记录不在配置表里，快照看不见它，只会一直占着内存）。
func (r *Registry) UpdateExisting(id int64, fn func(*ServerState)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	st, ok := r.m[id]
	if !ok {
		return false
	}
	fn(st)
	return true
}

// WarmHostInfo 用库里的静态信息预热内存态。
//
// 不预热的话，服务端刚重启、agent 还没重连的那段时间里，快照的 cores /
// mem.total / disk.total / v4 / v6 全是 0，而 REST 同时返回着库里的真值——
// 同一份数据两个接口对不上，步骤 06 的卡片会先闪一下空值。
func (r *Registry) WarmHostInfo(ctx context.Context) error {
	hosts, err := r.db.ListHostInfo(ctx)
	if err != nil {
		return err
	}

	for id, h := range hosts {
		r.Update(id, func(st *ServerState) {
			st.Host = proto.HostInfo{
				Hostname:  h.Hostname,
				OS:        h.OS,
				Kernel:    h.Kernel,
				Arch:      h.Arch,
				CPUModel:  h.CPUModel,
				Cores:     h.Cores,
				MemTotal:  h.MemTotal,
				DiskTotal: h.DiskTotal,
				BootTime:  h.BootTime,
				IPv4:      h.IPv4,
				IPv6:      h.IPv6,
			}
			st.PublicIP = h.PublicIP
			st.AgentVersion = h.AgentVersion
		})
	}
	return nil
}

// SetPingSource 挂上 ping 数据源。启动装配时调一次。
func (r *Registry) SetPingSource(fn func(serverID int64) []PingView) {
	r.pingSource = fn
}

// SetCoreSource is registered once before serving/broadcasting.
func (r *Registry) SetCoreSource(fn func(int64) any) { r.coreSource = fn }

func (r *Registry) SetTrafficSource(fn func(int64) *TrafficView) { r.trafficSource = fn }

// Remove 删掉一台节点的内存态（节点被删除时调用）。
func (r *Registry) Remove(id int64) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}

// Status 返回节点的在线状态与最后一次上报时刻，供 REST 接口合并。
// 没有记录（从没连过）时返回 false, nil。
func (r *Registry) Status(id int64) (bool, *int64) {
	st, ok := r.Get(id)
	if !ok {
		return false, nil
	}
	return st.Online, unixPtr(st.LastSeen)
}

// MarkOnline 把节点标记为在线并刷新 LastSeen，返回它是否刚从离线变过来。
func (r *Registry) MarkOnline(id int64) bool {
	changed := false
	r.Update(id, func(st *ServerState) {
		changed = !st.Online
		st.Online = true
		st.LastSeen = r.now()
	})
	return changed
}

// MarkStale 把超过 timeout 没上报的在线节点判为离线，返回这一轮新掉线的节点 ID。
func (r *Registry) MarkStale(timeout time.Duration) []int64 {
	now := r.now()

	r.mu.Lock()
	defer r.mu.Unlock()

	var offline []int64
	for id, st := range r.m {
		if st.Online && now.Sub(st.LastSeen) > timeout {
			st.Online = false
			offline = append(offline, id)
		}
	}
	return offline
}

// InvalidateConfig 让静态配置缓存立即失效，节点增删改后调用。
//
// 只清时间戳、保留数据：失效的意思是"下次取的时候重新查库"，不是"把手里的数据扔掉"。
// 扔掉的话，紧接着的一次查库失败就没有旧数据可退，又回到了卡片整体消失的老问题。
func (r *Registry) InvalidateConfig() {
	r.cacheMu.Lock()
	r.cacheAt = time.Time{}
	r.cacheMu.Unlock()
}

// servers 返回节点静态配置，带 configTTL 的缓存。
//
// 查库失败时**不覆盖已有缓存、也不刷新时间戳**：宁可继续用上一份旧数据，
// 也不能让一次瞬时失败把所有浏览器的节点卡片整体清空一分钟。
// 不刷新时间戳同时意味着下一次调用会立刻重试，而不是干等到 TTL 到期。
func (r *Registry) servers(ctx context.Context) ([]store.Server, error) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	if !r.cacheAt.IsZero() && r.now().Sub(r.cacheAt) < configTTL {
		return r.cache, nil
	}

	list, err := r.db.ListServers(ctx)
	if err != nil {
		return r.cache, err
	}
	r.cache, r.cacheAt = list, r.now()
	return list, nil
}

// MemView / SwapView / DiskView / NetView / ConnView 是快照里的分组字段。
//
// 字段名要短——十几台节点每秒广播一帧，字段名占的字节比数值还多。
type MemView struct {
	Used  int64 `json:"used"`
	Total int64 `json:"total"`
}

type SwapView struct {
	Used int64 `json:"used"`
}

type DiskView struct {
	Used  int64 `json:"used"`
	Total int64 `json:"total"`
}

type NetView struct {
	Up       int64 `json:"up"`        // 上行瞬时速率，字节/秒
	Down     int64 `json:"down"`      // 下行瞬时速率
	OutTotal int64 `json:"out_total"` // 累计发出字节
	InTotal  int64 `json:"in_total"`  // 累计收到字节
}

type ConnView struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}

type TrafficView struct {
	Used              int64  `json:"used"`
	Limit             int64  `json:"limit"`
	Mode              string `json:"mode"`
	In                int64  `json:"in"`
	Out               int64  `json:"out"`
	PeriodStart       int64  `json:"period_start"`
	PeriodEndExpected int64  `json:"period_end_expected"`
}

// ServerView 是广播给浏览器的单台节点快照（设计方案 §7.3）。
//
// 它不是 proto.Metrics 的复用而是投影：字段被刻意改短、重新分组，
// 并且合进了库里的配置（名称、价格、到期）。改这里的字段名会直接打到前端，
// 有 golden 测试（state_test.go + testdata/snapshot.json）守着。
type ServerView struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Region string   `json:"region"`
	Group  string   `json:"group"`
	Tags   []string `json:"tags"`
	Sort   int64    `json:"sort"`

	Online   bool   `json:"online"`
	LastSeen *int64 `json:"last_seen"`
	V4       bool   `json:"v4"`
	V6       bool   `json:"v6"`

	CPU    float64    `json:"cpu"`
	Cores  int        `json:"cores"`
	Mem    MemView    `json:"mem"`
	Swap   SwapView   `json:"swap"`
	Disk   DiskView   `json:"disk"`
	Load   [3]float64 `json:"load"`
	Net    NetView    `json:"net"`
	Conn   ConnView   `json:"conn"`
	Procs  int        `json:"procs"`
	Uptime int64      `json:"uptime"`

	ExpireAt  *string `json:"expire_at"`
	Bandwidth string  `json:"bandwidth"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Cycle     string  `json:"cycle"`

	// 字段顺序按 docs/plan/06 §4.6 的示例来：traffic、ping、core。
	// golden 测试比的是完整 JSON 字符串，换顺序它会红。
	Traffic *TrafficView `json:"traffic"`
	// 延迟任务（步骤 09）。没有适用任务时是空数组；尚无结果用 last_ts=null 表示。
	Ping []PingView `json:"ping"`
	Core any        `json:"core"` // 步骤 13：核心状态摘要
}

// PingView 是快照里的一条延迟信息。
//
// 方块序列（最近 30 次）不进快照：那是每台节点每任务 30 个点，每秒广播一遍太浪费，
// 前端用 /api/servers/{id}/ping/recent 单独拉，一分钟刷一次。
type PingView struct {
	TaskID int64  `json:"task_id"`
	Name   string `json:"name"`
	// 最近一次的延迟，毫秒；丢包为 null
	Latency *float64 `json:"latency"`
	// 最近窗口内的丢包率，0–100
	Loss   float64 `json:"loss"`
	LastTS *int64  `json:"last_ts"` // nil 表示尚未收到探测结果
}

// Snapshot 是广播帧。
type Snapshot struct {
	Type    string       `json:"type"`
	TS      int64        `json:"ts"`
	Servers []ServerView `json:"servers"`
}

// SnapshotViews 按库里的节点顺序（sort_order, id）组装全量快照。
//
// 库读不出来时用上一次的缓存顶上；连缓存都没有（刚启动就查库失败）才返回空列表，
// 并记一条日志——广播协程不能因为一次查库失败就停掉。
func (r *Registry) SnapshotViews(ctx context.Context) []ServerView {
	servers, err := r.servers(ctx)
	if err != nil {
		slog.Error("快照读取节点配置失败", "err", err)
	}

	out := make([]ServerView, 0, len(servers))
	for i := range servers {
		out = append(out, r.viewFor(&servers[i]))
	}
	return out
}

// SnapshotFrame 组装一帧完整的 snapshot 消息。
func (r *Registry) SnapshotFrame(ctx context.Context) Snapshot {
	return Snapshot{
		Type:    TypeSnapshot,
		TS:      r.now().Unix(),
		Servers: r.SnapshotViews(ctx),
	}
}

// viewFor 把一行配置和内存态拼成一条 ServerView。
func (r *Registry) viewFor(s *store.Server) ServerView {
	v := ServerView{
		ID:     s.ID,
		Name:   s.Name,
		Region: s.Region,
		Group:  s.GroupName,
		Tags:   s.Tags,
		Sort:   s.SortOrder,

		ExpireAt:  s.ExpireAt,
		Bandwidth: s.BandwidthLabel,
		Price:     s.Price,
		Currency:  s.Currency,
		Cycle:     s.BillingCycle,

		Ping: []PingView{},
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}

	if r.pingSource != nil {
		if views := r.pingSource(s.ID); views != nil {
			v.Ping = views
		}
	}
	if r.coreSource != nil {
		v.Core = r.coreSource(s.ID)
	}
	if r.trafficSource != nil {
		v.Traffic = r.trafficSource(s.ID)
		if v.Traffic != nil {
			v.Net.OutTotal = v.Traffic.Out
			v.Net.InTotal = v.Traffic.In
		}
	}

	st, ok := r.Get(s.ID)
	if !ok {
		return v
	}

	v.Online = st.Online
	v.LastSeen = unixPtr(st.LastSeen)
	v.V4 = st.Host.IPv4
	v.V6 = st.Host.IPv6
	v.Cores = st.Host.Cores
	v.Mem.Total = st.Host.MemTotal
	v.Disk.Total = st.Host.DiskTotal

	// 掉线后保留最后一次的数值：卡片上置灰显示旧值，比全部归零更有用。
	if m := st.Latest; m != nil {
		v.CPU = m.CPU
		v.Mem.Used = m.MemUsed
		v.Swap.Used = m.SwapUsed
		v.Disk.Used = m.DiskUsed
		v.Load = m.Load
		v.Net = NetView{Up: m.Net.TxRate, Down: m.Net.RxRate, OutTotal: m.Net.TxTotal, InTotal: m.Net.RxTotal}
		v.Conn = ConnView{TCP: m.TCP, UDP: m.UDP}
		v.Procs = m.Procs
		v.Uptime = m.Uptime
	}
	if v.Traffic != nil {
		v.Net.OutTotal = v.Traffic.Out
		v.Net.InTotal = v.Traffic.In
	}
	return v
}

func unixPtr(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	v := t.Unix()
	return &v
}
