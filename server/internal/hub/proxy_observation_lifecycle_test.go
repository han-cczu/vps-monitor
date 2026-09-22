package hub

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
	"vpsmon/server/internal/proxyobserve"
	"vpsmon/server/internal/store"
)

// 这一组用例特意把 AgentHub 与 proxyobserve.Service 一起装起来跑：
//
//	Hub.dispatch 取会话 → 观测服务验收分页 → 落库
//
// 中间每一段的时序都是异步的（连接关闭要等对端回帧、旧连接的读循环可能还停在
// 某一条消息的处理里、面板清理又是另一个 HTTP 协程），只测 Service 自己的
// session/Supersede 回调无法覆盖这些交错。
//
// 这里必须同时装配 Session 与 Supersede：只装 Supersede 时 Service 无法区分
// 「没有 Hub」和「有 Hub 但没有有效会话」，空会话那条路径就测不出来。
type observeFixture struct {
	hub     *AgentHub
	service *proxyobserve.Service
	db      *store.DB
	id      int64
}

func newObserveFixture(t *testing.T) *observeFixture {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	node, err := db.CreateServer(context.Background(), store.ServerInput{Name: "observed"}, "token")
	if err != nil {
		t.Fatal(err)
	}
	ah := NewAgentHub(db, nil, nil)
	service := proxyobserve.New(db)
	service.Session = ah.ProxySession
	service.Supersede = ah.RotateProxySession
	ah.OnObservation(func(ctx context.Context, id int64, session string, raw []byte) {
		_ = service.Receive(ctx, id, session, raw)
	})
	return &observeFixture{hub: ah, service: service, db: db, id: node.ID}
}

func (f *observeFixture) attach(session string) *agentConn {
	ac := &agentConn{serverID: f.id, send: make(chan []byte, 4), proxySession: session, proxyManagement: "external"}
	f.hub.conns[f.id] = ac
	// 模拟 AgentHub 里那条 writeLoop：真实连接会一直消费下发队列。
	// 不消费的话，反复换会话会把 4 个槽位填满，触发 SendTo 里「队列已满 → 断开连接」
	// 的分支；那条分支要真的 socket 才能安全走完，而它并不是这些用例要测的东西。
	go func() {
		for range ac.send {
		}
	}()
	return ac
}

func (f *observeFixture) detach() {
	f.hub.mu.Lock()
	delete(f.hub.conns, f.id)
	f.hub.mu.Unlock()
}

// dispatch 模拟读循环把一条消息交给分发层。
func (f *observeFixture) dispatch(t *testing.T, raw []byte) {
	t.Helper()
	f.hub.dispatch(context.Background(), f.id, raw)
}

func (f *observeFixture) rows(t *testing.T) []store.ProxyObservationRow {
	t.Helper()
	rows, _, err := f.service.List(context.Background(), f.id, true)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func items(raw []byte) []proto.ObservedInstance {
	var p proto.ProxyObservation
	_ = json.Unmarshal(raw, &p)
	return p.Instances
}

// instance 造一条协议允许的观测实例。
func observedInstance(id string, extra ...func(*proto.ObservedInstance)) proto.ObservedInstance {
	i := proto.ObservedInstance{ID: id, Core: "xray", Ownership: "external", Source: "generic", StatsStatus: "not_configured", ConfigPaths: []string{}, Ports: []proto.ObservedPort{}, Inbounds: []proto.ObservedInbound{{ID: "a", Protocol: "vless", Port: "1234", InConfig: true}}, ConfigReadAt: 1, LastSuccess: 1}
	for _, fn := range extra {
		fn(&i)
	}
	return i
}

// run 造一轮完整扫描并直接投给分发层。
func (f *observeFixture) run(t *testing.T, session string, seq, at int64, complete bool, tt ...proto.ObservedInstance) {
	t.Helper()
	pages := len(tt)
	if pages == 0 {
		pages = 1
	}
	for index := 0; index < pages; index++ {
		page := []proto.ObservedInstance{}
		if index < len(tt) {
			page = []proto.ObservedInstance{tt[index]}
		}
		raw, err := json.Marshal(proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: session, Sequence: seq, Page: index, Pages: pages, ScanComplete: complete, CollectedAt: at, Instances: page})
		if err != nil {
			t.Fatal(err)
		}
		f.dispatch(t, raw)
	}
}

// TestObservationSessionRotationBlocksContactAfterCleanup 复现「清理后旧连接仍在发」：
//
//	探针连接后已经协商过会话，面板随后删掉记录并重置观测。
//	清理只换了 Service 看到的会话，Hub 自己那条连接上的会话没动——
//	于是旧分页在服务端从头到尾看起来都是「当前会话」，直接写回了刚清掉的库。
func TestObservationSessionRotationBlocksContactAfterCleanup(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	old := proxyobserve.NewSession()
	ac := f.attach(old)
	f.run(t, old, 1, 1, true, observedInstance("a"))

	if !f.hub.RotateProxySession(f.id) {
		t.Fatal("在线连接必须能换会话")
	}
	if got := ac.proxySession; got == "" || got == old {
		t.Fatalf("Hub 连接上的会话没有换掉：%q", got)
	}
	serviceSide := f.service.Session(f.id)
	if serviceSide == old {
		t.Fatal("观测服务看到的仍是旧会话")
	}
	// 更换会话是异步落地的（新 config 先下发，探针确认后旧连接才被移除），
	// 所以这里先让那条旧连接照旧上报：它仍然以为自己是当前连接，
	// 而服务端对旧会话的拒绝不能被「空会话」这条路径遮住。
	f.run(t, old, 2, 2, true, observedInstance("zombie"))
	for _, row := range f.rows(t) {
		if row.ID == "zombie" {
			t.Fatalf("清理前的连接把旧分页写回了清空后的库：%+v", row)
		}
	}

	// 探针如实清空记录时也是同一条路。
	f.run(t, serviceSide, 3, 3, true, observedInstance("b"))
	if _, err := f.service.HandleDelete(ctx, f.id, "b"); err != nil {
		t.Fatal(err)
	}
	f.run(t, serviceSide, 4, 4, true, observedInstance("zombie2"))
	for _, row := range f.rows(t) {
		if row.ID == "zombie2" {
			t.Fatalf("删除记录后旧连接把分页写回：%+v", row)
		}
	}

	// 探针收到新 config 并重连：旧连接必须先退场，新会话才在连接表里生效。
	rotated := f.service.Session(f.id)
	f.detach()
	fresh := f.attach(rotated)
	f.run(t, fresh.proxySession, 5, 5, true, observedInstance("c"))
	current := []string{}
	for _, row := range f.rows(t) {
		if !row.Absent {
			current = append(current, row.ID)
		}
	}
	if len(current) != 1 || current[0] != "c" {
		t.Fatalf("新会话没能正常工作：%v", current)
	}
}

// TestDisconnectDoesNotSanctionDetachedConnectionPages 复现「连接已 detach、旧上下文仍在跑」：
//
//	Hub 的 Disconnect 先 detach 再关连接，而关闭要等对端回帧；
//	旧连接的读循环这时还停在 dispatch 里，拿到的会话已经是空串。
//	「空会话 = 没有在途分页」这个前提在 service.superseded 里不成立，
//	detach 之后才被清理的记录仍会被这条老消息写回来。
func TestDisconnectDoesNotSanctionDetachedConnectionPages(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	session := proxyobserve.NewSession()
	f.attach(session)

	// 清理发生在 detach 之后：探针已经不在连接表里，换会话拿不到新会话，
	// 但读循环里还有一条已经通过 dispatch 的旧消息。
	f.detach()
	if f.service.Session(f.id) != "" {
		t.Fatal("detach 之后不该还能看到会话")
	}
	if err := f.service.HandleReset(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	f.run(t, session, 1, 1, true, observedInstance("stale"))
	rows, _, err := f.service.List(ctx, f.id, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("断线后的旧回调把清理掉的记录写了回来：%+v", rows)
	}

	// 真正没在观测的连接（空会话）本来就不该走到这里；一旦走到也必须拒绝。
	f.run(t, "", 2, 2, true, observedInstance("empty"))
	if rows, _, _ = f.service.List(ctx, f.id, false); len(rows) != 0 {
		t.Fatalf("空会话被当成有效会话写库：%+v", rows)
	}

	// 重新连上并协商：新会话照常工作。
	fresh := proxyobserve.NewSession()
	f.attach(fresh)
	f.run(t, fresh, 1, 3, true, observedInstance("fresh"))
	if rows, _, _ = f.service.List(ctx, f.id, true); len(rows) != 1 || rows[0].ID != "fresh" {
		t.Fatalf("重连后无法恢复上报：%+v", rows)
	}
}

// TestCleanupAfterDisconnectKeepsRecordsCleared 覆盖「清理后才断线」：
// 清理时连接还在（会话换了），随后连接才消失。清理前采集的分页仍然不能落库。
func TestCleanupAfterDisconnectKeepsRecordsCleared(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	old := proxyobserve.NewSession()
	f.attach(old)
	f.run(t, old, 1, 1, true, observedInstance("a"))

	if err := f.service.HandleReset(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	f.detach()
	f.run(t, old, 2, 2, true, observedInstance("late"))
	if rows, _, _ := f.service.List(ctx, f.id, false); len(rows) != 0 {
		t.Fatalf("断线后迟到分页写回：%+v", rows)
	}
}

// TestInFlightPageAcrossCleanupIsRejected 覆盖「旧消息已进入 dispatch，暂停在反序列化之前」：
// 服务端取出旧会话之后连接被移除、记录被清理，随后旧回调才恢复执行。
//
// 这正是 superseded 里不能把空会话当成放行理由的原因：连接从 Hub 移除之后，
// 回调看到的是空会话，但这条消息属于清理之前的那一代。
func TestInFlightPageAcrossCleanupIsRejected(t *testing.T) {
	for _, cleanup := range []string{"delete", "reset"} {
		t.Run(cleanup, func(t *testing.T) {
			f := newObserveFixture(t)
			ctx := context.Background()
			old := proxyobserve.NewSession()
			f.attach(old)
			f.run(t, old, 1, 1, true, observedInstance("a"), observedInstance("b"))

			inFlight := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			service := f.service
			f.hub.OnObservation(func(ctx context.Context, id int64, session string, raw []byte) {
				close(inFlight)
				<-release // 暂停在「分发已拿到会话、处理还没开始」的那一刻
				_ = service.Receive(ctx, id, session, raw)
				close(done)
			})

			raw, err := json.Marshal(proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: old, Sequence: 2, Page: 0, Pages: 1, ScanComplete: true, CollectedAt: 2, Instances: []proto.ObservedInstance{observedInstance("inflight")}})
			if err != nil {
				t.Fatal(err)
			}
			go f.dispatch(t, raw)
			<-inFlight

			// 连接先被移除（读循环还在跑），随后面板清理。
			f.detach()
			if f.service.Session(f.id) != "" {
				t.Fatal("detach 之后不该还能看到会话")
			}
			if cleanup == "delete" {
				if _, err := f.service.HandleDelete(ctx, f.id, "a"); err != nil {
					t.Fatal(err)
				}
			} else if err := f.service.HandleReset(ctx, f.id); err != nil {
				t.Fatal(err)
			}
			close(release)
			<-done

			rows, scan, err := f.service.List(ctx, f.id, false)
			if err != nil {
				t.Fatal(err)
			}
			if cleanup == "delete" {
				if len(rows) != 1 || rows[0].ID != "b" {
					t.Fatalf("删除之后在途分页改动了记录：%+v", rows)
				}
			} else if len(rows) != 0 || scan != nil {
				t.Fatalf("重置之后在途分页写回：%+v %+v", rows, scan)
			}
		})
	}
}

// TestUnnegotiatedConnectionObservationIsIgnored 覆盖「新连接尚未协商」：
// 探针还没发 hello（或 hello 没声明观测能力），会话为空，观测消息不应落库。
func TestUnnegotiatedConnectionObservationIsIgnored(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	f.attach("")
	f.run(t, "", 1, 1, true, observedInstance("a"))
	if rows, _, _ := f.service.List(ctx, f.id, true); len(rows) != 0 {
		t.Fatalf("未协商的连接写入了观测记录：%+v", rows)
	}
}

// TestRotateSessionIsSafeUnderConcurrentDispatch 用 -race 盯住「换会话 vs 分发」的交错：
// 一边不断换会话（等价于反复清理），一边并行投递分页，任何一条被接受的都必须属于
// 最后一次轮换之前就已经存在的会话。
//
// 两条会话来源交错着送：多数时候拿 Hub 连接表里的当前会话，偶尔拿分发开始前读到的
// 那个旧会话（等价于「消息已经在读循环里、轮换发生在它之后」）。旧会话在轮换后必须
// 被拒绝，而连表里的会话总是最新的——这样这个用例不依赖轮换恰好落在哪一条投递上。
func TestRotateSessionIsSafeUnderConcurrentDispatch(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	ac := f.attach(proxyobserve.NewSession())
	initial := ac.proxySession

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f.hub.RotateProxySession(f.id)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	for seq := int64(1); seq <= 40; seq++ {
		f.hub.mu.Lock()
		session := ac.proxySession
		f.hub.mu.Unlock()
		if seq%4 == 0 {
			session = initial
		}
		raw, err := json.Marshal(proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: session, Sequence: seq, Page: 0, Pages: 1, ScanComplete: true, CollectedAt: seq, Instances: []proto.ObservedInstance{observedInstance("a")}})
		if err != nil {
			t.Fatal(err)
		}
		f.dispatch(t, raw)
	}
	close(stop)
	wg.Wait()

	rows, _, err := f.service.List(ctx, f.id, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) > 1 {
		t.Fatalf("并发轮换期间落库的记录数不对：%+v", rows)
	}
}

// TestDetachedConnectionCannotWriteAfterNodeDeleted 覆盖节点被删除的场景：
// 连接已 detach、节点已从 servers 表移除，旧回调不能把观测记录写回。
func TestDetachedConnectionCannotWriteAfterNodeDeleted(t *testing.T) {
	f := newObserveFixture(t)
	ctx := context.Background()
	session := proxyobserve.NewSession()
	f.attach(session)
	f.detach()
	if _, err := f.db.ExecContext(ctx, `DELETE FROM servers WHERE id=?`, f.id); err != nil {
		t.Fatal(err)
	}
	f.run(t, session, 1, 1, true, observedInstance("a"))
	if rows := f.rows(t); len(rows) != 0 {
		t.Fatalf("已删除节点的旧连接写入了观测记录：%+v", rows)
	}
}
