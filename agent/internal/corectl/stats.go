package corectl

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"vpsmon/proto"
	pb "vpsmon/proto/singbox/v2rayapi"
)

type statsClient struct {
	conn    *grpc.ClientConn
	retryAt time.Time
}

func addCounter(a, b int64) int64 {
	if b < 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
func ParseStats(stats []*pb.Stat) proto.CoreStats {
	inbounds := map[string]proto.Counter{}
	users := map[string]proto.Counter{}
	for _, s := range stats {
		if s == nil || s.Value < 0 {
			continue
		}
		p := strings.Split(s.Name, ">>>")
		if len(p) != 4 || p[1] == "" || len(p[1]) > 256 || p[2] != "traffic" {
			continue
		}
		var dest map[string]proto.Counter
		switch p[0] {
		case "user":
			dest = users
		case "inbound":
			dest = inbounds
		default:
			continue
		}
		c := dest[p[1]]
		c.Name = p[1]
		switch p[3] {
		case "uplink":
			c.Up = addCounter(c.Up, s.Value)
		case "downlink":
			c.Down = addCounter(c.Down, s.Value)
		default:
			continue
		}
		dest[c.Name] = c
	}
	return proto.CoreStats{Type: proto.TypeCoreStats, TS: time.Now().Unix(), Inbounds: counterSlice(inbounds), Users: counterSlice(users)}
}
func counterSlice(m map[string]proto.Counter) []proto.Counter {
	out := make([]proto.Counter, 0, len(m))
	for _, c := range m {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b proto.Counter) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func (m *Manager) Stats(ctx context.Context) (proto.CoreStats, error) {
	if err := m.verifyOwned(); err != nil {
		return proto.CoreStats{}, err
	}
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	if m.statsClient == nil {
		m.statsClient = &statsClient{}
	}
	c := m.statsClient
	if time.Now().Before(c.retryAt) {
		return proto.CoreStats{}, fmt.Errorf("stats RPC unavailable; retrying after 60s")
	}
	if c.conn == nil {
		conn, err := grpc.NewClient("passthrough:///"+m.statsAddress, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20)))
		if err != nil {
			return proto.CoreStats{}, err
		}
		c.conn = conn
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := pb.NewStatsServiceClient(c.conn).QueryStats(ctx, &pb.QueryStatsRequest{Reset_: true})
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.retryAt = time.Now().Add(60 * time.Second)
		return proto.CoreStats{}, fmt.Errorf("sing-box stats RPC failed: %w", err)
	}
	return ParseStats(r.Stat), nil
}
func (m *Manager) Close() {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	if m.statsClient != nil && m.statsClient.conn != nil {
		_ = m.statsClient.conn.Close()
		m.statsClient.conn = nil
	}
}

func (m *Manager) reportStats(ctx context.Context) {
	// Retry a failed send before resetting another batch of counters. During an
	// outage this bounds pending memory and leaves new traffic in sing-box.
	if m.pendingStats.Type != "" {
		if m.send == nil || m.send(m.pendingStats) != nil {
			return
		}
		m.pendingStats = proto.CoreStats{}
	}
	st, err := m.Stats(ctx)
	m.mu.Lock()
	before := m.statsError
	m.statsError = ""
	if err != nil {
		m.statsError = err.Error()
	}
	after := m.statsError
	m.mu.Unlock()
	if before != after {
		m.emitState(ctx, "", nil)
	}
	if err != nil {
		return
	}
	if m.send != nil && m.send(st) != nil {
		m.pendingStats = st
	}
}
