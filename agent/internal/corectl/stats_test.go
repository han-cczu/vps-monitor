package corectl

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"vpsmon/proto"
	pb "vpsmon/proto/singbox/v2rayapi"
)

type fakeStatsServer struct {
	pb.UnimplementedStatsServiceServer
	mu    sync.Mutex
	value int64
	calls int
}

func (s *fakeStatsServer) QueryStats(_ context.Context, r *pb.QueryStatsRequest) (*pb.QueryStatsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	v := s.value
	if r.Reset_ {
		s.value = 0
	}
	return &pb.QueryStatsResponse{Stat: []*pb.Stat{{Name: "user>>>sub-1>>>traffic>>>downlink", Value: v}}}, nil
}
func TestStatsWirePathResetAndSendRetry(t *testing.T) {
	m, _ := fixture(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	fake := &fakeStatsServer{value: 100}
	pb.RegisterStatsServiceServer(s, fake)
	go s.Serve(l)
	defer s.Stop()
	m.statsAddress = l.Addr().String()
	if pb.StatsService_QueryStats_FullMethodName != "/v2ray.core.app.stats.command.StatsService/QueryStats" {
		t.Fatal("wrong upstream runtime method")
	}
	st, err := m.Stats(context.Background())
	if err != nil || len(st.Users) != 1 || st.Users[0].Down != 100 {
		t.Fatalf("%+v %v", st, err)
	}
	st, err = m.Stats(context.Background())
	if err != nil || st.Users[0].Down != 0 {
		t.Fatalf("reset failed %+v %v", st, err)
	}
	fake.mu.Lock()
	fake.value = 200
	fake.mu.Unlock()
	fail := true
	var delivered []proto.CoreStats
	m.send = func(v any) error {
		if st, ok := v.(proto.CoreStats); ok {
			if fail {
				return errors.New("disconnected")
			}
			delivered = append(delivered, st)
		}
		return nil
	}
	m.reportStats(context.Background())
	m.reportStats(context.Background())
	fake.mu.Lock()
	calls := fake.calls
	fake.value = 50
	fake.mu.Unlock()
	if calls != 3 {
		t.Fatalf("reset more traffic while send pending: calls=%d", calls)
	}
	fail = false
	m.reportStats(context.Background())
	if len(delivered) != 2 || delivered[0].Users[0].Down != 200 || delivered[1].Users[0].Down != 50 {
		t.Fatalf("lost failed-send batch: %+v", delivered)
	}
}
func TestParseStats(t *testing.T) {
	st := ParseStats([]*pb.Stat{
		{Name: "user>>>u>>>traffic>>>uplink", Value: 5}, {Name: "user>>>u>>>traffic>>>downlink", Value: 7},
		{Name: "inbound>>>i>>>traffic>>>uplink", Value: 8}, {Name: "outbound>>>x>>>traffic>>>uplink", Value: 999},
		{Name: "user>>>u>>>traffic>>>uplink", Value: -1}, {Name: "user>>>u>>>bad>>>uplink", Value: 999}, {Name: "broken", Value: 999}, nil,
	})
	if len(st.Users) != 1 || st.Users[0].Up != 5 || st.Users[0].Down != 7 || len(st.Inbounds) != 1 || st.Inbounds[0].Up != 8 {
		t.Fatalf("%+v", st)
	}
}
