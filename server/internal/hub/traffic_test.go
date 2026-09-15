package hub

import (
	"context"
	"testing"

	"vpsmon/proto"
	"vpsmon/server/internal/store"
)

func TestTrafficPreservesBothScopesEvenForOfflineNode(t *testing.T) {
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
	if views[0].Net.BootInTotal == nil || *views[0].Net.BootInTotal != 1000 ||
		views[0].Net.BootOutTotal == nil || *views[0].Net.BootOutTotal != 2000 {
		t.Fatalf("system counters were replaced by billing totals: %+v", views[0].Net)
	}
	if views[1].Net.BootInTotal != nil || views[1].Net.BootOutTotal != nil {
		t.Fatal("node without a sample must not invent system counters")
	}
}

func TestSystemCounterResetDoesNotResetBillingView(t *testing.T) {
	r := NewRegistry(&fakeStore{servers: []store.Server{{ID: 1}}})
	r.SetTrafficSource(func(int64) *TrafficView { return &TrafficView{In: 300, Out: 700} })
	for _, total := range []int64{5000, 0, 123} {
		r.Update(1, func(s *ServerState) {
			s.Latest = &proto.Metrics{Net: proto.NetStat{RxTotal: total, TxTotal: total}}
		})
		v := r.SnapshotViews(context.Background())[0]
		if v.Net.BootInTotal == nil || *v.Net.BootInTotal != total ||
			v.Net.BootOutTotal == nil || *v.Net.BootOutTotal != total {
			t.Fatalf("system counter reset or zero lost: %+v", v.Net)
		}
		if v.Net.InTotal != 300 || v.Net.OutTotal != 700 {
			t.Fatalf("system counter affected billing totals: %+v", v.Net)
		}
	}
}
