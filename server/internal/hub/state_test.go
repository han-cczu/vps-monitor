package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

// fakeStore 是 hub 需要的那点库能力的假实现。
type fakeStore struct {
	mu      sync.Mutex
	servers []store.Server
	byToken map[string]store.Server
	hosts   []store.HostInfo
	stored  map[int64]store.HostInfo // ListHostInfo 的返回值（预热用）
	listErr error
	lists   int // ListServers 被真正调用的次数，用来验证缓存
}

func (f *fakeStore) ListServers(context.Context) ([]store.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.servers, nil
}

func (f *fakeStore) ListHostInfo(context.Context) (map[int64]store.HostInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stored == nil {
		return map[int64]store.HostInfo{}, nil
	}
	return f.stored, nil
}

func (f *fakeStore) FindServerByTokenHash(_ context.Context, hash string) (*store.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.byToken[hash]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &s, nil
}

func (f *fakeStore) UpsertHostInfo(_ context.Context, h store.HostInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, h)
	return nil
}

func (f *fakeStore) hostCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hosts)
}

func (f *fakeStore) lastHost() store.HostInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.hosts) == 0 {
		return store.HostInfo{}
	}
	return f.hosts[len(f.hosts)-1]
}

func (f *fakeStore) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// sampleServer 是文档 §4.6 那个例子对应的配置行。
func sampleServer() store.Server {
	expire := "2027-09-10"
	return store.Server{
		ID:             1,
		Name:           "深圳-阿里云-01",
		Region:         "CN",
		Tags:           []string{},
		SortOrder:      0,
		Price:          99,
		Currency:       "CNY",
		BillingCycle:   "year",
		ExpireAt:       &expire,
		BandwidthLabel: "3Mbps",
	}
}

func sampleMetrics() *proto.Metrics {
	return &proto.Metrics{
		Type:     proto.TypeMetrics,
		TS:       1757660000,
		CPU:      3.02,
		MemUsed:  458000000,
		SwapUsed: 0,
		DiskUsed: 9720000000,
		Load:     [3]float64{0.04, 0.03, 0},
		Net: proto.NetStat{
			RxTotal: 1557000000,
			TxTotal: 126900000,
			RxRate:  169,
			TxRate:  303,
		},
		TCP:    23,
		UDP:    4,
		Procs:  112,
		Uptime: 172800,
	}
}

// TestSnapshotMatchesGolden 是 Go 与前端之间的契约测试。
//
// testdata/snapshot.json 是照着 docs/plan/05-实时Hub.md §4.6 手写的，不是从代码 dump 的：
// 改了 ServerView 的字段名或结构，这里会先红，而不是等步骤 06 在浏览器里发现卡片空白。
func TestSnapshotMatchesGolden(t *testing.T) {
	at := time.Unix(1757660000, 0)

	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)
	reg.now = func() time.Time { return at }

	reg.Update(1, func(st *ServerState) {
		st.Online = true
		st.LastSeen = at
		st.Host = proto.HostInfo{Cores: 2, MemTotal: 1690000000, DiskTotal: 42000000000, IPv4: true}
		st.Latest = sampleMetrics()
	})

	got, err := json.Marshal(reg.SnapshotFrame(context.Background()))
	if err != nil {
		t.Fatalf("序列化快照: %v", err)
	}

	raw, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatalf("读 golden: %v", err)
	}
	want := strings.TrimSpace(string(raw))

	if string(got) != want {
		t.Errorf("快照与 golden 不一致\n got: %s\nwant: %s", got, want)
	}
}

func TestSnapshotWithoutStateKeepsConfigFields(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)

	views := reg.SnapshotViews(context.Background())
	if len(views) != 1 {
		t.Fatalf("期望 1 条，得到 %d", len(views))
	}

	v := views[0]
	if v.Online || v.LastSeen != nil {
		t.Errorf("从没连过的节点应当 online=false、last_seen=null，得到 %v / %v", v.Online, v.LastSeen)
	}
	if v.Name != "深圳-阿里云-01" || v.Price != 99 {
		t.Errorf("静态配置字段丢了：%+v", v)
	}
	if v.Tags == nil || v.Ping == nil {
		t.Error("tags 与 ping 必须是空数组而不是 null，前端直接 map")
	}
}

// TestSnapshotKeepsLastValuesWhenOffline 保证掉线后卡片还能显示最后一次的数值。
func TestSnapshotKeepsLastValuesWhenOffline(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)

	reg.Update(1, func(st *ServerState) {
		st.Online = false
		st.LastSeen = time.Unix(1757660000, 0)
		st.Latest = sampleMetrics()
	})

	v := reg.SnapshotViews(context.Background())[0]
	if v.Online {
		t.Error("应当是离线")
	}
	if v.CPU != 3.02 || v.Procs != 112 {
		t.Errorf("掉线后应保留最后一次数值，得到 cpu=%v procs=%v", v.CPU, v.Procs)
	}
}

func TestConfigCacheHitsAndInvalidates(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)

	for range 3 {
		reg.SnapshotViews(context.Background())
	}
	if n := db.listCount(); n != 1 {
		t.Fatalf("60 秒内应当只查库 1 次，实际 %d 次", n)
	}

	reg.InvalidateConfig()
	reg.SnapshotViews(context.Background())
	if n := db.listCount(); n != 2 {
		t.Fatalf("失效后应当重新查库，实际累计 %d 次", n)
	}
}

func TestConfigCacheExpiresAfterTTL(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)

	now := time.Unix(1757660000, 0)
	reg.now = func() time.Time { return now }

	reg.SnapshotViews(context.Background())
	now = now.Add(configTTL + time.Second)
	reg.SnapshotViews(context.Background())

	if n := db.listCount(); n != 2 {
		t.Fatalf("超过 TTL 应当重新查库，实际 %d 次", n)
	}
}

// TestSnapshotSurvivesStoreError 保证查库失败时广播不会中断。
func TestSnapshotSurvivesStoreError(t *testing.T) {
	db := &fakeStore{listErr: context.DeadlineExceeded}
	reg := NewRegistry(db)

	views := reg.SnapshotViews(context.Background())
	if views == nil {
		t.Fatal("查库失败也要返回空切片而不是 nil，否则 JSON 里是 null")
	}
	if len(views) != 0 {
		t.Fatalf("期望空列表，得到 %d 条", len(views))
	}
}

// TestSnapshotFrameStaysSmall 覆盖验收第 7 条：十几台节点的单帧要小于 15 KB（压缩前）。
//
// 这里每台都填满实时数据、名字标签都按真实长度给，比"只有一台在线"的手工实测更接近上限。
func TestSnapshotFrameStaysSmall(t *testing.T) {
	const nodes = 15

	expire := "2027-09-10"
	servers := make([]store.Server, 0, nodes)
	for i := 1; i <= nodes; i++ {
		servers = append(servers, store.Server{
			ID:             int64(i),
			Name:           fmt.Sprintf("香港-BandwagonHost-节点%02d", i),
			Region:         "HK",
			GroupName:      "生产",
			Tags:           []string{"中转", "高优先级"},
			SortOrder:      int64(i),
			Price:          168.5,
			Currency:       "CNY",
			BillingCycle:   "year",
			ExpireAt:       &expire,
			BandwidthLabel: "1Gbps",
		})
	}

	reg := NewRegistry(&fakeStore{servers: servers})
	for i := 1; i <= nodes; i++ {
		reg.Update(int64(i), func(st *ServerState) {
			st.Online = true
			st.LastSeen = time.Unix(1757660000, 0)
			st.Host = proto.HostInfo{Cores: 20, MemTotal: 51370958848, DiskTotal: 299910557696, IPv4: true, IPv6: true}
			st.Latest = sampleMetrics()
		})
	}

	frame, err := json.Marshal(reg.SnapshotFrame(context.Background()))
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}

	const limit = 15 << 10
	if len(frame) >= limit {
		t.Errorf("%d 台节点的单帧 %d 字节，超过 %d 字节上限", nodes, len(frame), limit)
	}
	t.Logf("%d 台节点满负载单帧 %d 字节（上限 %d）", nodes, len(frame), limit)
}

// TestConfigCacheKeepsStaleDataOnError 是本轮审查查出的问题的回归测试。
//
// 原实现把查库错误连同新鲜时间戳一起缓存，一次瞬时失败会让所有浏览器的节点卡片
// 整体消失 60 秒，且期间不重试——而同文件的注释写的恰恰是"返回上一次缓存"。
func TestConfigCacheKeepsStaleDataOnError(t *testing.T) {
	db := &fakeStore{servers: []store.Server{sampleServer()}}
	reg := NewRegistry(db)

	if got := len(reg.SnapshotViews(context.Background())); got != 1 {
		t.Fatalf("先预热一份好缓存，期望 1 条，得到 %d", got)
	}

	reg.InvalidateConfig()
	db.mu.Lock()
	db.listErr = context.DeadlineExceeded
	db.mu.Unlock()

	if got := len(reg.SnapshotViews(context.Background())); got != 1 {
		t.Errorf("查库失败应当继续用上一份缓存，得到 %d 条", got)
	}

	before := db.listCount()
	reg.SnapshotViews(context.Background())
	if db.listCount() != before+1 {
		t.Error("失败之后应当立刻重试，而不是把失败当成一次成功的缓存等满 TTL")
	}

	db.mu.Lock()
	db.listErr = nil
	db.mu.Unlock()
	if got := len(reg.SnapshotViews(context.Background())); got != 1 {
		t.Errorf("库恢复后应当正常返回，得到 %d 条", got)
	}
}

// TestUpdateExistingDoesNotResurrect 覆盖"节点删除后在途 metrics 不该复活内存态"。
func TestUpdateExistingDoesNotResurrect(t *testing.T) {
	reg := NewRegistry(&fakeStore{})

	reg.MarkOnline(7)
	reg.Remove(7)

	if reg.UpdateExisting(7, func(*ServerState) {}) {
		t.Error("记录已删除，UpdateExisting 应当返回 false")
	}
	if _, ok := reg.Get(7); ok {
		t.Error("已删除的节点不该被重新建出来")
	}
}

// TestWarmHostInfoFillsStaticFields 覆盖"服务端重启后快照不该把 cores / mem.total 显示成 0"。
func TestWarmHostInfoFillsStaticFields(t *testing.T) {
	db := &fakeStore{
		servers: []store.Server{sampleServer()},
		stored: map[int64]store.HostInfo{
			1: {ServerID: 1, Hostname: "node-a", Cores: 2, MemTotal: 1690000000, DiskTotal: 42000000000,
				IPv4: true, PublicIP: "203.0.113.7", AgentVersion: "0.1.0"},
		},
	}
	reg := NewRegistry(db)

	if err := reg.WarmHostInfo(context.Background()); err != nil {
		t.Fatalf("预热: %v", err)
	}

	v := reg.SnapshotViews(context.Background())[0]
	if v.Cores != 2 || v.Mem.Total != 1690000000 || !v.V4 {
		t.Errorf("预热后快照应当带上库里的静态信息，得到 %+v", v)
	}
	if v.Online || v.LastSeen != nil {
		t.Error("预热只填静态信息，不该把节点标成在线")
	}
}

func TestStatusReportsUnknownServer(t *testing.T) {
	reg := NewRegistry(&fakeStore{})

	online, lastSeen := reg.Status(42)
	if online || lastSeen != nil {
		t.Errorf("没有记录的节点应当是 false / nil，得到 %v / %v", online, lastSeen)
	}
}
