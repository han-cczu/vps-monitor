package hub

import (
	"context"
	"testing"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

func TestTrafficReplacesBootTotalsEvenForOfflineNode(t *testing.T) {
	r := NewRegistry(&fakeStore{servers: []store.Server{{ID: 1}, {ID: 2}}})
	r.SetTrafficSource(func(int64) *TrafficView { return &TrafficView{Used: 70, In: 30, Out: 70, Limit: 100, Mode: "max"} })
	r.Update(1, func(s *ServerState) {
		s.Latest = &proto.Metrics{Net: proto.NetStat{RxTotal: 1000, TxTotal: 2000, TxRate: 9}}
	})
	views := r.SnapshotViews(context.Background())
	for _, v := range views {
		if v.Traffic.Used != 70 || v.Net.InTotal != 30 || v.Net.OutTotal != 70 {
			t.Fatalf("view=%+v", v)
		}
	}
	if views[0].Net.Up != 9 {
		t.Fatal("traffic changed instantaneous rates")
	}
}
