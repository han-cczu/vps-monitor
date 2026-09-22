package proxyobserve

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

// hubSessions 模拟 Hub 为每条在线连接签发的观测会话：协商时登记，清理观测记录时换掉。
type hubSessions struct {
	mu      sync.Mutex
	current map[int64]string
}

func newHubSessions() *hubSessions { return &hubSessions{current: map[int64]string{}} }

func (h *hubSessions) set(id int64, session string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.current[id] = session
}

func (h *hubSessions) get(id int64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current[id]
}

// rotate 模拟 AgentHub.RotateProxySession：只有在线连接才会换，离线返回 false。
func (h *hubSessions) rotate(id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current[id] == "" {
		return false
	}
	h.current[id] = NewSession()
	return true
}

func fixture(t *testing.T) (*Service, int64, *hubSessions) {
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
	service := New(db)
	hub := newHubSessions()
	hub.set(node.ID, "current")
	service.Session = hub.get
	service.Supersede = hub.rotate
	return service, node.ID, hub
}

func frameIn(t *testing.T, session string, seq int64, page, pages int, items ...proto.ObservedInstance) []byte {
	t.Helper()
	b, err := json.Marshal(proto.ProxyObservation{Type: proto.TypeProxyObservation, Session: session, Sequence: seq, Page: page, Pages: pages, ScanComplete: true, CollectedAt: 1, Instances: items})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func frame(t *testing.T, seq int64, page, pages int, items ...proto.ObservedInstance) []byte {
	t.Helper()
	return frameIn(t, "current", seq, page, pages, items...)
}

// scanIn 返回把扫描标志与采集时刻改掉之后的一帧。
func scanIn(t *testing.T, session string, complete bool, at int64, seq int64, page, pages int, items ...proto.ObservedInstance) []byte {
	t.Helper()
	var p proto.ProxyObservation
	if err := json.Unmarshal(frameIn(t, session, seq, page, pages, items...), &p); err != nil {
		t.Fatal(err)
	}
	p.ScanComplete = complete
	p.CollectedAt = at
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// scanPagesIn 按探针的真实分页方式生成一整轮分页：每个实例一页，空扫描也是一页。
func scanPagesIn(t *testing.T, session string, seq int64, at int64, complete bool, items ...proto.ObservedInstance) [][]byte {
	t.Helper()
	total := len(items)
	if total == 0 {
		total = 1
	}
	out := [][]byte{}
	for index, item := range items {
		out = append(out, scanIn(t, session, complete, at, seq, index, total, item))
	}
	if len(out) == 0 {
		out = append(out, scanIn(t, session, complete, at, seq, 0, total))
	}
	return out
}

func scanPages(t *testing.T, seq int64, at int64, complete bool, items ...proto.ObservedInstance) [][]byte {
	t.Helper()
	return scanPagesIn(t, "current", seq, at, complete, items...)
}

func instance(id string) proto.ObservedInstance {
	return proto.ObservedInstance{ID: id, Core: "xray", Ownership: "external", Source: "generic", StatsStatus: "not_configured", Inbounds: []proto.ObservedInbound{{ID: "a", Protocol: "vless", Port: "1234", InConfig: true}}, ConfigReadAt: 1, LastSuccess: 1}
}
func ids(rows []store.ProxyObservationRow, absent bool) []string {
	out := []string{}
	for _, row := range rows {
		if row.Absent == absent {
			out = append(out, row.ID)
		}
	}
	return out
}
func receiveIn(t *testing.T, s *Service, id int64, session string, raws ...[]byte) error {
	t.Helper()
	for _, raw := range raws {
		if err := s.Receive(context.Background(), id, session, raw); err != nil {
			return err
		}
	}
	return nil
}
func receiveAll(t *testing.T, s *Service, id int64, raws ...[]byte) error {
	t.Helper()
	return receiveIn(t, s, id, "current", raws...)
}

func TestAtomicPagesReplayAndLastKnownSnapshots(t *testing.T) {
	s, id, _ := fixture(t)
	ctx := context.Background()
	item := instance("one")
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 2, item)); err != nil {
		t.Fatal(err)
	}
	rows, state, _ := s.List(ctx, id, true)
	if len(rows) != 0 || state != nil {
		t.Fatal("partial scan published")
	}
	part := item
	part.Inbounds = []proto.ObservedInbound{{ID: "b", Protocol: "hysteria"}}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 1, 2, part)); err != nil {
		t.Fatal(err)
	}
	rows, state, _ = s.List(ctx, id, true)
	if len(rows) != 1 || len(rows[0].Inbounds) != 2 || state == nil || !state.Complete {
		t.Fatal("pages not assembled")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 1, item)); err == nil {
		t.Fatal("replay accepted")
	}
	if err := s.Receive(ctx, id, "replacement", frame(t, 2, 0, 1, item)); err == nil {
		t.Fatal("old connection accepted")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 2, 1, 2, item)); err != ErrStale {
		t.Fatalf("orphan page accepted: %v", err)
	}
	item.Stale = true
	item.ConfigReadAt = 0
	item.LastSuccess = 0
	item.Inbounds = nil
	if err := s.Receive(ctx, id, "current", frame(t, 3, 0, 1, item)); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	if len(rows[0].Inbounds) != 2 || rows[0].ConfigReadAt != 1 {
		t.Fatal("restart parse failure discarded last known inbounds")
	}
	if err := s.Receive(ctx, id, "current", frame(t, 4, 0, 1)); err != nil {
		t.Fatal(err)
	}
	rows, state, _ = s.List(ctx, id, true)
	if !rows[0].Absent || !rows[0].Stale || rows[0].AbsentAt == 0 || state == nil || state.Instances != 0 {
		t.Fatal("absent snapshot not marked", rows[0], state)
	}
}
func TestRejectCredentialsLimitsAndPageMixing(t *testing.T) {
	s, id, _ := fixture(t)
	ctx := context.Background()
	i := instance("one")
	raw := frame(t, 1, 0, 1, i)
	var message map[string]any
	json.Unmarshal(raw, &message)
	message["password"] = "never-store"
	bad, _ := json.Marshal(message)
	if s.Receive(ctx, id, "current", bad) == nil {
		t.Fatal("unknown secret field accepted")
	}
	if s.Receive(ctx, id, "current", make([]byte, proto.ProxyMaxFrame+1)) == nil {
		t.Fatal("oversized frame accepted")
	}
	if s.Receive(ctx, id, "current", frame(t, 1, 0, 2, i)) != nil {
		t.Fatal("first page rejected")
	}
	i.Version = "changed"
	i.Inbounds = []proto.ObservedInbound{{ID: "b"}}
	if s.Receive(ctx, id, "current", frame(t, 1, 1, 2, i)) == nil {
		t.Fatal("mixed instance metadata accepted")
	}
	s.now = func() time.Time { return time.Now().Add(time.Minute) }
	i.Version = ""
	if s.Receive(ctx, id, "current", frame(t, 1, 1, 2, i)) == nil {
		t.Fatal("expired partial snapshot accepted")
	}
	rows, state, _ := s.List(ctx, id, true)
	if len(rows) != 0 || state != nil {
		t.Fatal("invalid scan persisted")
	}
}
func TestPartialDiscoveryDoesNotMarkMissingAndTrafficIsolation(t *testing.T) {
	s, id, _ := fixture(t)
	ctx := context.Background()
	i := instance("one")
	u, d := int64(200), int64(400)
	i.Inbounds[0].Usage = &proto.ObservedUsage{Source: "x_ui_database", Scope: "manager_total", Up: &u, Down: &d, CollectedAt: 1}
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 1, i)); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(ctx, id, "current", scanIn(t, "current", false, 2, 2, 0, 1)); err != nil {
		t.Fatal(err)
	}
	rows, state, _ := s.List(ctx, id, true)
	if len(rows) != 1 || rows[0].Absent || *rows[0].Inbounds[0].Usage.Down != 400 {
		t.Fatal("partial discovery erased known instance")
	}
	if state == nil || state.Complete || state.CollectedAt != 2 {
		t.Fatal("partial scan status not recorded", state)
	}
	var n int
	for _, table := range []string{"subscriber_traffic", "traffic_periods", "config_revisions"} {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("observation changed %s: %d %v", table, n, err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatal("deleted node retained observations")
	}
}

// 完整扫描里没出现的实例立刻退出当前列表，历史记录保留最后快照和确认消失的时刻。
func TestCompleteScanMovesMissingInstancesToHistory(t *testing.T) {
	s, id, _ := fixture(t)
	ctx := context.Background()
	s.now = func() time.Time { return time.Unix(1000, 0) }
	if err := receiveAll(t, s, id, scanPages(t, 1, 1000, true, instance("a"), instance("b"))...); err != nil {
		t.Fatal(err)
	}
	rows, state, _ := s.List(ctx, id, true)
	if len(ids(rows, false)) != 2 || len(ids(rows, true)) != 0 || state == nil || state.Instances != 2 {
		t.Fatal("two instances from one scan not stored", rows, state)
	}
	s.now = func() time.Time { return time.Unix(2000, 0) }
	if err := receiveAll(t, s, id, scanPages(t, 2, 2000, true, instance("a"))...); err != nil {
		t.Fatal(err)
	}
	rows, state, _ = s.List(ctx, id, true)
	if len(ids(rows, false)) != 1 || ids(rows, false)[0] != "a" {
		t.Fatal("current list not reduced to the rescan result", rows)
	}
	if len(ids(rows, true)) != 1 || ids(rows, true)[0] != "b" {
		t.Fatal("missing instance did not move to history", rows)
	}
	for _, row := range rows {
		if row.ID == "b" && (row.AbsentAt != 2000 || row.ReceivedAt != 1000) {
			t.Fatal("history keeps the wrong snapshot time", row)
		}
	}

	// 再次完整扫描只含 a：b 的"确认消失"时刻不能被推后。
	s.now = func() time.Time { return time.Unix(3000, 0) }
	if err := receiveAll(t, s, id, scanPages(t, 3, 3000, true, instance("a"))...); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	for _, row := range rows {
		if row.ID == "b" && row.AbsentAt != 2000 {
			t.Fatal("absent time moved on a repeated scan", row)
		}
	}

	// 不完整扫描不改变任何结论。
	s.now = func() time.Time { return time.Unix(4000, 0) }
	if err := receiveAll(t, s, id, scanPages(t, 4, 4000, false, instance("a"))...); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	if len(ids(rows, false)) != 1 || len(ids(rows, true)) != 1 {
		t.Fatal("partial scan changed the current list", rows)
	}

	// 完整空扫描：当前列表清空，实例全部进历史；扫描状态说明这是完整结果。
	s.now = func() time.Time { return time.Unix(5000, 0) }
	if err := receiveAll(t, s, id, scanPages(t, 5, 5000, true)...); err != nil {
		t.Fatal(err)
	}
	rows, state, _ = s.List(ctx, id, true)
	if len(ids(rows, false)) != 0 || len(ids(rows, true)) != 2 {
		t.Fatal("empty complete scan left current instances", rows)
	}
	if state == nil || !state.Complete || state.Instances != 0 || state.CollectedAt != 5000 || state.ReceivedAt != 5000 {
		t.Fatal("empty complete scan not recorded", state)
	}
}

// 删除一条历史记录只影响本节点本实例，清空后重新发现仍会正常出现。
func TestDeleteAndResetObservationRecords(t *testing.T) {
	s, id, hub := fixture(t)
	ctx := context.Background()
	other, err := s.db.CreateServer(ctx, store.ServerInput{Name: "other"}, "token2")
	if err != nil {
		t.Fatal(err)
	}
	hub.set(other.ID, "current")
	if err := receiveAll(t, s, id, scanPages(t, 1, 1, true, instance("a"), instance("b"))...); err != nil {
		t.Fatal(err)
	}
	if err := receiveAll(t, s, id, scanPages(t, 2, 2, true, instance("a"))...); err != nil {
		t.Fatal(err)
	}
	if err := receiveAll(t, s, other.ID, scanPages(t, 1, 1, true, instance("b"))...); err != nil {
		t.Fatal(err)
	}
	found, err := s.HandleDelete(ctx, id, "b")
	if err != nil || !found {
		t.Fatal("history record not deleted", found, err)
	}
	found, err = s.HandleDelete(ctx, id, "b")
	if err != nil || found {
		t.Fatal("second delete reported a record", found, err)
	}
	rows, _, _ := s.List(ctx, id, true)
	if len(rows) != 1 || rows[0].ID != "a" {
		t.Fatal("delete removed the wrong record", rows)
	}
	otherRows, _, _ := s.List(ctx, other.ID, true)
	if len(otherRows) != 1 || otherRows[0].ID != "b" {
		t.Fatal("delete crossed node boundary", otherRows)
	}

	// 删除之后探针在这一代会话里重新发现同一条实例：恢复显示。
	if err := receiveIn(t, s, id, hub.get(id), scanPagesIn(t, hub.get(id), 3, 3, true, instance("a"), instance("b"))...); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	if len(ids(rows, false)) != 2 {
		t.Fatal("rediscovered instance missing", rows)
	}

	if err := s.HandleReset(ctx, id); err != nil {
		t.Fatal(err)
	}
	rows, state, _ := s.List(ctx, id, true)
	otherRows, otherState, _ := s.List(ctx, other.ID, true)
	if len(rows) != 0 || state != nil {
		t.Fatal("reset left records behind", rows, state)
	}
	if len(otherRows) != 1 || otherState == nil {
		t.Fatal("reset crossed node boundary", otherRows, otherState)
	}
}

// 清理与上报并发：清理之前采集的扫描结果一律不能落库——包括第一页在清理之后
// 才到达的那些，以及清理后重放的旧消息；清理之后的新扫描必须正常入库。
func TestCleanupBlocksInFlightScansBothWays(t *testing.T) {
	for _, cleanup := range []string{"delete", "reset"} {
		t.Run(cleanup, func(t *testing.T) {
			s, id, hub := fixture(t)
			ctx := context.Background()
			old := hub.get(id)
			if err := receiveAll(t, s, id, scanPages(t, 1, 1, true, instance("a"), instance("b"))...); err != nil {
				t.Fatal(err)
			}
			// 探针已经采集了下一轮（sequence=2），但第一页还堵在路上。
			inflight := scanPages(t, 2, 2, true, instance("a"))

			if cleanup == "delete" {
				if _, err := s.HandleDelete(ctx, id, "b"); err != nil {
					t.Fatal(err)
				}
			} else if err := s.HandleReset(ctx, id); err != nil {
				t.Fatal(err)
			}
			fresh := hub.get(id)
			if fresh == old {
				t.Fatal("清理没有更换观测会话")
			}

			// 第一页迟到：属于上一代，必须整轮丢弃。
			if err := receiveIn(t, s, id, old, inflight...); err != ErrStale {
				t.Fatalf("in-flight first page accepted: %v", err)
			}
			rows, _, _ := s.List(ctx, id, true)
			if len(rows) != (map[string]int{"delete": 1, "reset": 0})[cleanup] {
				t.Fatalf("in-flight scan wrote records back: %v", rows)
			}

			// 旧会话上重放已经提交过的序号同样不再被接受。
			if err := s.Receive(ctx, id, old, frameIn(t, old, 1, 0, 1, instance("a"))); err != ErrStale {
				t.Fatalf("old session replay accepted: %v", err)
			}
			// 新会话里本轮结果正常入库。
			if err := receiveIn(t, s, id, fresh, scanPagesIn(t, fresh, 3, 3, true, instance("a"))...); err != nil {
				t.Fatal(err)
			}
			rows, _, _ = s.List(ctx, id, true)
			if len(ids(rows, false)) != 1 || ids(rows, false)[0] != "a" {
				t.Fatalf("new scan after cleanup not stored: %v", rows)
			}
		})
	}
}

// 清理前已收到第一页的那一轮：剩下的分页不能靠后到而拼出半截快照。
func TestDroppedScanPagesCannotComplete(t *testing.T) {
	s, id, hub := fixture(t)
	ctx := context.Background()
	a, b := instance("a"), instance("b")
	if err := receiveAll(t, s, id, scanPages(t, 1, 1, true, a, b)...); err != nil {
		t.Fatal(err)
	}
	// 新一轮只发了一半，清理随即发生。
	half := scanPages(t, 2, 2, true, a, b)[0]
	if err := s.Receive(ctx, id, "current", half); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleReset(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(ctx, id, "current", scanPages(t, 2, 2, true, a, b)[1]); err != ErrStale {
		t.Fatalf("second page of the dropped scan accepted: %v", err)
	}
	rows, _, _ := s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatalf("dropped scan published records: %v", rows)
	}
	// 新会话可以正常提交。
	fresh := hub.get(id)
	if err := receiveIn(t, s, id, fresh, scanPagesIn(t, fresh, 3, 3, true, a)...); err != nil {
		t.Fatal(err)
	}
	rows, _, _ = s.List(ctx, id, true)
	if len(ids(rows, false)) != 1 {
		t.Fatalf("new session could not publish: %v", rows)
	}
}

// 离线节点清理时没有会话可换：已经收到过第一页的那一轮仍然不能补完。
func TestOfflineCleanupStillDropsReceivedPages(t *testing.T) {
	s, id, hub := fixture(t)
	ctx := context.Background()
	if err := receiveAll(t, s, id, scanPages(t, 1, 1, true, instance("a"))...); err != nil {
		t.Fatal(err)
	}
	hub.set(id, "") // 探针断开
	if err := s.HandleReset(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(ctx, id, "current", scanPages(t, 1, 1, true, instance("a"))[0]); err != ErrStale {
		t.Fatalf("offline cleanup accepted a replayed page: %v", err)
	}
	rows, state, _ := s.List(ctx, id, true)
	if len(rows) != 0 || state != nil {
		t.Fatalf("offline cleanup left records: %v %v", rows, state)
	}
}

// 部分分页超时作废后，剩下的分页也不能拼出半截快照。
func TestExpiredPartialScanIsDroppedWholesale(t *testing.T) {
	s, id, _ := fixture(t)
	ctx := context.Background()
	if err := s.Receive(ctx, id, "current", frame(t, 1, 0, 2, instance("a"))); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(time.Minute) }
	if err := s.Receive(ctx, id, "current", frame(t, 1, 1, 2, instance("b"))); err != ErrStale {
		t.Fatalf("expired page accepted: %v", err)
	}
	if err := s.Receive(ctx, id, "current", frame(t, 2, 1, 2, instance("b"))); err != ErrStale {
		t.Fatalf("orphan page after expiry accepted: %v", err)
	}
	rows, _, _ := s.List(ctx, id, true)
	if len(rows) != 0 {
		t.Fatal("expired scan published rows", rows)
	}
}

// 离线（当前会话为空）时，清理之前采集的分页一律不能落库——包括清理之后
// 才到达的第一页，以及清理前已经收到过第一页的那一轮的后续分页。
func TestOfflineEmptySessionRejectsCleanupRace(t *testing.T) {
	for _, cleanup := range []string{"delete", "reset"} {
		t.Run(cleanup, func(t *testing.T) {
			s, id, hub := fixture(t)
			ctx := context.Background()
			old := hub.get(id)
			if err := receiveAll(t, s, id, scanPages(t, 1, 1, true, instance("a"), instance("b"))...); err != nil {
				t.Fatal(err)
			}
			// 探针已经采集了下一轮（sequence=2），第一页还堵在路上。
			inflight := scanPages(t, 2, 2, true, instance("a"))
			// 清理前已收到第一页的那一轮，剩下的分页同样还在路上。
			half := scanPages(t, 3, 3, true, instance("a"), instance("b"))

			hub.set(id, "") // 连接已 detach：当前没有有效观测会话
			if err := s.Receive(ctx, id, old, half[0]); err != ErrStale {
				t.Fatalf("离线节点接受了旧会话的第一页：%v", err)
			}
			if cleanup == "delete" {
				if _, err := s.HandleDelete(ctx, id, "b"); err != nil {
					t.Fatal(err)
				}
			} else if err := s.HandleReset(ctx, id); err != nil {
				t.Fatal(err)
			}

			// 清理之后才到达的第一页：空会话就是「不属于任何一代」。
			if err := receiveIn(t, s, id, old, inflight...); err != ErrStale {
				t.Fatalf("清理后到达的空会话分页被接受：%v", err)
			}
			// 旧会话上重放已经提交过的序号同样不再被接受。
			if err := s.Receive(ctx, id, old, frameIn(t, old, 1, 0, 1, instance("a"))); err != ErrStale {
				t.Fatalf("离线旧会话重放被接受：%v", err)
			}
			rows, state, _ := s.List(ctx, id, false)
			if cleanup == "delete" {
				// 删掉的 b 不能因为迟到分页又回来，a 仍然是唯一保留的记录。
				if len(rows) != 1 || rows[0].ID != "a" {
					t.Fatalf("离线删除后被写回：rows=%v", rows)
				}
			} else if len(rows) != 0 || state != nil {
				// 重置之后连扫描状态一起清掉，旧的完整扫描结论不能再出现在页面上。
				t.Fatalf("离线重置后被写回：rows=%v scan=%v", rows, state)
			}

			// 探针重连并重新协商：新会话照常上报。
			fresh := NewSession()
			hub.set(id, fresh)
			if err := receiveIn(t, s, id, fresh, scanPagesIn(t, fresh, 1, 9, true, instance("a"))...); err != nil {
				t.Fatal(err)
			}
			rows, _, _ = s.List(ctx, id, true)
			if len(ids(rows, false)) != 1 || ids(rows, false)[0] != "a" {
				t.Fatalf("重连后的新会话没能入库：%v", rows)
			}
		})
	}
}

// 装配了会话回调、但当前节点根本没有会话时，任何分页都不能落库：
// 这覆盖「新连接尚未协商 hello」「离线」「已删除的旧连接」三种情况。
func TestNoCurrentSessionRejectsEveryPage(t *testing.T) {
	s, id, hub := fixture(t)
	ctx := context.Background()
	hub.set(id, "")
	for _, session := range []string{"never-issued", ""} {
		if err := s.Receive(ctx, id, session, frameIn(t, session, 1, 0, 1, instance("a"))); err == nil {
			t.Fatalf("会话 %q 的分页被接受", session)
		}
		rows, state, _ := s.List(ctx, id, false)
		if len(rows) != 0 || state != nil {
			t.Fatalf("没有有效会话时写入了记录：%v %v", rows, state)
		}
	}
	// 未装配 Hub 的单进程部署仍然照旧工作。
	offline := New(s.db)
	if err := offline.Receive(ctx, id, "current", frameIn(t, "current", 1, 0, 1, instance("b"))); err != nil {
		t.Fatal(err)
	}
	rows, _, _ := offline.List(ctx, id, true)
	if len(ids(rows, false)) != 1 || ids(rows, false)[0] != "b" {
		t.Fatalf("未装配会话回调时无法落库：%v", rows)
	}
}
