package proxyobserve

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	_ "modernc.org/sqlite"
	"vpsmon/proto"
	pb "vpsmon/proto/singbox/v2rayapi"
)

const maxDatabaseBytes = 32 << 20

// SQLite is opened only on an Agent-owned copy. Source DB/WAL are read as
// ordinary files; SQLite never creates SHM, a journal or a lock beside them.
func copyDatabase(source, scratch string) (string, func(), error) {
	if _, err := os.Stat(source + "-journal"); !os.IsNotExist(err) {
		return "", nil, errChanged
	}
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return "", nil, errRead
	}
	dir, err := os.MkdirTemp(scratch, "sqlite-")
	if err != nil {
		return "", nil, errRead
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	fail := func() (string, func(), error) { cleanup(); return "", nil, errChanged }
	paths := []string{source, source + "-wal"}
	before := make([]os.FileInfo, 2)
	var total int64
	for i, p := range paths {
		st, e := os.Stat(p)
		if i == 1 && os.IsNotExist(e) {
			continue
		}
		if e != nil || !st.Mode().IsRegular() || st.Size() > maxDatabaseBytes {
			return fail()
		}
		before[i] = st
		total += st.Size()
	}
	if total > maxDatabaseBytes {
		return fail()
	}
	for i, p := range paths {
		if before[i] == nil {
			continue
		}
		b, e := readStable(p, maxDatabaseBytes)
		if e != nil {
			return fail()
		}
		name := "snapshot.db"
		if i == 1 {
			name += "-wal"
		}
		if os.WriteFile(filepath.Join(dir, name), b, 0600) != nil {
			return fail()
		}
	}
	for i, p := range paths {
		after, e := os.Stat(p)
		if before[i] == nil {
			if !os.IsNotExist(e) {
				return fail()
			}
			continue
		}
		if e != nil || !os.SameFile(before[i], after) || before[i].Size() != after.Size() || before[i].ModTime() != after.ModTime() {
			return fail()
		}
	}
	if _, err := os.Stat(source + "-journal"); !os.IsNotExist(err) {
		return fail()
	}
	return filepath.Join(dir, "snapshot.db"), cleanup, nil
}

func readManagerUsage(ctx context.Context, source, scratch string, item *proto.ObservedInstance, tags map[string]string, at int64) error {
	copy, cleanup, err := copyDatabase(source, scratch)
	if err != nil {
		return err
	}
	defer cleanup()
	db, err := sql.Open("sqlite", copy+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(100)")
	if err != nil {
		return errRead
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// The explicit projection is also a schema allowlist. No ORM migrations or
	// queries of manager accounts, password hashes or panel settings occur.
	rows, err := db.QueryContext(ctx, `SELECT id,up,down,total,expiry_time,enable,listen,port,protocol,tag,CASE WHEN length(settings)<=65536 THEN settings ELSE NULL END,CASE WHEN length(stream_settings)<=65536 THEN stream_settings ELSE NULL END FROM inbounds LIMIT 1025`)
	if err != nil {
		return errors.New("manager_schema_unsupported")
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		if n > proto.ProxyMaxInbounds {
			item.Truncated = true
			item.Issues = append(item.Issues, "manager_inbounds_limit")
			break
		}
		var id, up, down, limit, expiry, enabled, port sql.NullInt64
		var listen, protocol, tag, settings, stream sql.NullString
		if rows.Scan(&id, &up, &down, &limit, &expiry, &enabled, &listen, &port, &protocol, &tag, &settings, &stream) != nil {
			return errRead
		}
		index := -1
		for i, in := range item.Inbounds {
			if tag.String != "" && tags[in.ID] == tag.String {
				if index >= 0 {
					return errors.New("manager_inbound_ambiguous")
				}
				index = i
			}
		}
		if index < 0 {
			for i, in := range item.Inbounds {
				if in.Port == strconv.FormatInt(port.Int64, 10) && in.Protocol == protocol.String {
					if index >= 0 {
						return errors.New("manager_inbound_ambiguous")
					}
					index = i
				}
			}
		}
		if index < 0 {
			in := proto.ObservedInbound{ID: "manager-" + strconv.FormatInt(id.Int64, 10), Tag: label(tag.String, 128), Protocol: label(protocol.String, 48), Listen: label(listen.String, 128), Port: strconv.FormatInt(port.Int64, 10), InConfig: false}
			in.Users = count(obj([]byte(settings.String))["clients"])
			s := obj([]byte(stream.String))
			in.Transport = label(str(s["network"]), 48)
			security := str(s["security"])
			in.TLS = security == "tls" || security == "reality"
			in.Reality = security == "reality"
			item.Inbounds = append(item.Inbounds, in)
			index = len(item.Inbounds) - 1
		}
		if enabled.Valid {
			v := enabled.Int64 != 0
			item.Inbounds[index].Enabled = &v
		}
		counter := func(v sql.NullInt64) *int64 {
			if !v.Valid || v.Int64 < 0 {
				return nil
			}
			value := v.Int64
			return &value
		}
		item.Inbounds[index].Usage = &proto.ObservedUsage{Source: "x_ui_database", Scope: "manager_total", Up: counter(up), Down: counter(down), Limit: counter(limit), ExpireAt: counter(expiry), CollectedAt: at}
	}
	if rows.Err() != nil {
		return errRead
	}
	return nil
}

func queryReferenceStats(ctx context.Context, core, address string) ([]*pb.Stat, error) {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	number, e := strconv.Atoi(port)
	if err != nil || e != nil || number < 1 || number > 65535 || ip == nil || !ip.IsLoopback() {
		return nil, errors.New("stats_address_not_loopback")
	}
	conn, err := grpc.NewClient("passthrough:///"+address, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(proto.ProxyMaxFrame)))
	if err != nil {
		return nil, errRead
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request := &pb.QueryStatsRequest{Pattern: "inbound>>>", Reset_: false}
	response := new(pb.QueryStatsResponse)
	method := "/v2ray.core.app.stats.command.StatsService/QueryStats"
	// Xray's official QueryStats wire fields 1/2 and response Stat fields 1/2
	// match these messages, but its service namespace is deliberately separate.
	if core == "xray" {
		method = "/xray.app.stats.command.StatsService/QueryStats"
	}
	if conn.Invoke(ctx, method, request, response) != nil {
		return nil, errRead
	}
	return response.Stat, nil
}

func applyReferenceStats(items []proto.ObservedInbound, tags map[string]string, stats []*pb.Stat, core string, at int64) {
	values := map[string]*proto.ObservedUsage{}
	for _, s := range stats {
		if s == nil || s.Value < 0 {
			continue
		}
		p := strings.Split(s.Name, ">>>")
		if len(p) != 4 || p[0] != "inbound" || p[2] != "traffic" {
			continue
		}
		u := values[p[1]]
		if u == nil {
			u = &proto.ObservedUsage{Source: core + "_api", Scope: "reference", CollectedAt: at}
			values[p[1]] = u
		}
		v := s.Value
		switch p[3] {
		case "uplink":
			u.Up = &v
		case "downlink":
			u.Down = &v
		}
	}
	for i, in := range items {
		if rawTag, ok := tags[in.ID]; ok {
			items[i].Usage = values[rawTag]
		}
	}
}
