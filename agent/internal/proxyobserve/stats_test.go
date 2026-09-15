package proxyobserve

import (
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"vpsmon/proto"
	pb "vpsmon/proto/singbox/v2rayapi"
)

func TestManagerSnapshotReadsWALWithoutTouchingSource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x-ui.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, query := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`, `CREATE TABLE inbounds(id INTEGER,up INTEGER,down INTEGER,total INTEGER,expiry_time INTEGER,enable INTEGER,listen TEXT,port INTEGER,protocol TEXT,tag TEXT,settings TEXT,stream_settings TEXT)`, `INSERT INTO inbounds VALUES(1,12,34,999,0,1,'::',23356,'hysteria','test','{"clients":[{"password":"secret"}]}','{"network":"hysteria","security":"tls"}')`, `INSERT INTO inbounds VALUES(2,50,60,0,0,0,'::',22903,'vless','disabled','{}','{}')`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(p + suffix)
		if err != nil {
			t.Fatal(err)
		}
		before[suffix] = string(raw)
	}
	item := proto.ObservedInstance{Inbounds: []proto.ObservedInbound{{ID: "tag", Port: "23356", Protocol: "hysteria", InConfig: true}}}
	if err := readManagerUsage(context.Background(), p, t.TempDir(), &item, map[string]string{"tag": "test"}, 123); err != nil {
		t.Fatal(err)
	}
	if len(item.Inbounds) != 2 || item.Inbounds[0].Usage == nil || *item.Inbounds[0].Usage.Up != 12 || *item.Inbounds[0].Usage.Down != 34 || item.Inbounds[1].InConfig || *item.Inbounds[1].Enabled {
		t.Fatalf("snapshot did not use committed WAL rows: %+v", item.Inbounds)
	}
	for suffix, want := range before {
		raw, _ := os.ReadFile(p + suffix)
		if string(raw) != want {
			t.Fatalf("source %q changed", suffix)
		}
	}
}

func TestStatsNamespacesAndNoReset(t *testing.T) {
	for _, core := range []string{"sing-box", "xray"} {
		t.Run(core, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			calls := make(chan string, 2)
			server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
				method, _ := grpc.MethodFromServerStream(stream)
				req := new(pb.QueryStatsRequest)
				if err := stream.RecvMsg(req); err != nil {
					return err
				}
				if req.Reset_ {
					t.Error("external reset requested")
				}
				calls <- method
				return stream.SendMsg(&pb.QueryStatsResponse{Stat: []*pb.Stat{{Name: "inbound>>>tag>>>traffic>>>uplink", Value: 123}}})
			}))
			go server.Serve(listener)
			defer server.Stop()
			stats, err := queryReferenceStats(context.Background(), core, listener.Addr().String())
			if err != nil || len(stats) != 1 {
				t.Fatal(err)
			}
			want := "/v2ray.core.app.stats.command.StatsService/QueryStats"
			if core == "xray" {
				want = "/xray.app.stats.command.StatsService/QueryStats"
			}
			if <-calls != want {
				t.Fatal("wrong service namespace")
			}
		})
	}
	for _, addr := range []string{"localhost:10085", "0.0.0.0:10085", "8.8.8.8:10085", "127.0.0.1:0", "[::1]:99999"} {
		if _, err := queryReferenceStats(context.Background(), "xray", addr); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

func TestRollbackJournalIsNeverIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "active.db")
	os.WriteFile(p, []byte("incomplete database"), 0600)
	os.WriteFile(p+"-journal", []byte("active rollback transaction"), 0600)
	if _, cleanup, err := copyDatabase(p, t.TempDir()); err == nil {
		cleanup()
		t.Fatal("active rollback journal ignored")
	}
}
