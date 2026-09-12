package store

import (
	"context"
	"errors"
	"testing"
)

func sampleInput(name string) ServerInput {
	return ServerInput{
		Name:            name,
		Region:          "HK",
		GroupName:       "亚洲",
		Tags:            []string{"V4", "V6"},
		SortOrder:       10,
		PublicHost:      "hk1.example.com",
		Price:           10.79,
		Currency:        "USD",
		BillingCycle:    "month",
		AutoRenew:       true,
		TrafficLimit:    4_000_000_000_000,
		TrafficResetDay: 24,
		TrafficMode:     "max",
		BandwidthLabel:  "1 Gbps",
		Note:            "备注",
	}
}

func TestServersCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if n, err := db.CountServers(ctx); err != nil || n != 0 {
		t.Fatalf("count=%d err=%v want 0", n, err)
	}
	if _, err := db.GetServer(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	expire := "2026-09-24"
	in := sampleInput("hk-01")
	in.ExpireAt = &expire

	s, err := db.CreateServer(ctx, in, "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case s.ID == 0 || s.CreatedAt == 0 || s.UpdatedAt == 0:
		t.Fatalf("bad row %+v", s)
	case s.Name != "hk-01" || s.Region != "HK" || s.GroupName != "亚洲":
		t.Fatalf("bad row %+v", s)
	case len(s.Tags) != 2 || s.Tags[0] != "V4" || s.Tags[1] != "V6":
		t.Fatalf("tags=%v", s.Tags)
	case s.ExpireAt == nil || *s.ExpireAt != expire:
		t.Fatalf("expire_at=%v", s.ExpireAt)
	case !s.AutoRenew || s.TrafficResetDay != 24 || s.TrafficMode != "max":
		t.Fatalf("bad row %+v", s)
	case s.Price != 10.79 || s.Currency != "USD":
		t.Fatalf("bad row %+v", s)
	case s.TokenHash != "hash-1":
		t.Fatalf("token_hash=%q", s.TokenHash)
	}

	got, err := db.GetServer(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != s.Name || got.TokenHash != s.TokenHash {
		t.Fatalf("get mismatch: %+v vs %+v", got, s)
	}

	// 更新：清空到期日、换标签
	up := sampleInput("hk-01-renamed")
	up.Tags = nil
	up.ExpireAt = nil
	up.AutoRenew = false
	updated, err := db.UpdateServer(ctx, s.ID, up)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case updated.Name != "hk-01-renamed":
		t.Fatalf("name=%q", updated.Name)
	case updated.ExpireAt != nil:
		t.Fatalf("expire_at=%v want nil", *updated.ExpireAt)
	case len(updated.Tags) != 0:
		t.Fatalf("tags=%v want empty", updated.Tags)
	case updated.AutoRenew:
		t.Fatal("auto_renew want false")
	case updated.TokenHash != "hash-1":
		t.Fatalf("update 不该动 token_hash，得到 %q", updated.TokenHash)
	case updated.CreatedAt != s.CreatedAt:
		t.Fatalf("created_at 被改了：%d → %d", s.CreatedAt, updated.CreatedAt)
	}

	if _, err := db.UpdateServer(ctx, 9999, up); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update 不存在的节点：want ErrNotFound, got %v", err)
	}

	if err := db.DeleteServer(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteServer(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除：want ErrNotFound, got %v", err)
	}
}

func TestListServersOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	mk := func(name string, sortOrder int64, hash string) int64 {
		in := sampleInput(name)
		in.SortOrder = sortOrder
		s, err := db.CreateServer(ctx, in, hash)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	third := mk("c", 5, "h3")
	first := mk("a", 1, "h1")
	second := mk("b", 1, "h2") // 同 sort_order 时按 id 升序，排在 first 之后

	list, err := db.ListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d want 3", len(list))
	}
	want := []int64{first, second, third}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("顺序不对：%d 位是 %d，期望 %d", i, list[i].ID, id)
		}
	}
}

func TestServerTokenHash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s, err := db.CreateServer(ctx, sampleInput("hk-01"), "old-hash")
	if err != nil {
		t.Fatal(err)
	}

	found, err := db.FindServerByTokenHash(ctx, "old-hash")
	if err != nil || found.ID != s.ID {
		t.Fatalf("find by token: %+v err=%v", found, err)
	}

	if err := db.UpdateServerTokenHash(ctx, s.ID, "new-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FindServerByTokenHash(ctx, "old-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("旧 token 仍能查到：%v", err)
	}
	if found, err := db.FindServerByTokenHash(ctx, "new-hash"); err != nil || found.ID != s.ID {
		t.Fatalf("新 token 查不到：%+v err=%v", found, err)
	}
	if err := db.UpdateServerTokenHash(ctx, 9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	// token_hash 唯一：两台节点不能共用一个 token
	if _, err := db.CreateServer(ctx, sampleInput("hk-02"), "new-hash"); err == nil {
		t.Fatal("重复 token_hash 应该被 UNIQUE 约束挡下")
	}
}

func TestHostInfoUpsertAndCascade(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	s, err := db.CreateServer(ctx, sampleInput("hk-01"), "hash-1")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.GetHostInfo(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	h := HostInfo{
		ServerID: s.ID, Hostname: "hk-01", OS: "Debian 12", Kernel: "6.1.0", Arch: "x86_64",
		CPUModel: "AMD EPYC 7B13", Cores: 4, MemTotal: 8_318_000_000, DiskTotal: 93_500_000_000,
		BootTime: 1_756_100_000, IPv4: true, IPv6: false, PublicIP: "1.2.3.4", AgentVersion: "0.1.0",
	}
	if err := db.UpsertHostInfo(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetHostInfo(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case got.Hostname != "hk-01" || got.Cores != 4 || got.MemTotal != 8_318_000_000:
		t.Fatalf("bad host info %+v", got)
	case !got.IPv4 || got.IPv6:
		t.Fatalf("ipv4=%v ipv6=%v", got.IPv4, got.IPv6)
	case got.UpdatedAt == 0:
		t.Fatal("updated_at 没填")
	}

	// 再来一次是覆盖而不是插入
	h.Hostname = "hk-01-new"
	h.IPv6 = true
	if err := db.UpsertHostInfo(ctx, h); err != nil {
		t.Fatal(err)
	}
	all, err := db.ListHostInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[s.ID].Hostname != "hk-01-new" || !all[s.ID].IPv6 {
		t.Fatalf("upsert 没覆盖：%+v", all)
	}

	// 节点不存在时外键挡住
	if err := db.UpsertHostInfo(ctx, HostInfo{ServerID: 9999}); err == nil {
		t.Fatal("外键约束应该挡下孤儿 host info")
	}

	// 删节点级联删 host info
	if err := db.DeleteServer(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetHostInfo(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("host info 没被级联删除：%v", err)
	}
}
