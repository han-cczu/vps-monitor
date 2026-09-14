// Command sing-box-stats verifies real SS2022 per-user accounting without public traffic.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protowire"
)

const payloadBytes int64 = 100_000_000
const service = "/v2ray.core.app.stats.command.StatsService/QueryStats"

type stat struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}
type evidence struct {
	Version      string `json:"version_output"`
	Method       string `json:"grpc_method"`
	BytesPerUser int64  `json:"bytes_per_user"`
	FirstUser    []stat `json:"after_u1"`
	Stats        []stat `json:"before_reset"`
	AfterReset   []stat `json:"after_reset"`
	Passed       bool   `json:"passed"`
}

// The messages follow upstream experimental/v2rayapi/stats.proto field numbers.
// Use the actual registered service name: upstream overrides ServiceDesc.ServiceName in stats.go.
type rawCodec struct{}

func (rawCodec) Name() string                  { return "proto" }
func (rawCodec) Marshal(v any) ([]byte, error) { return *v.(*[]byte), nil }
func (rawCodec) Unmarshal(data []byte, v any) error {
	*v.(*[]byte) = append([]byte(nil), data...)
	return nil
}
func query(ctx context.Context, c *grpc.ClientConn, reset bool) ([]stat, error) {
	var request, response []byte
	if reset {
		request = protowire.AppendTag(request, 2, protowire.VarintType)
		request = protowire.AppendVarint(request, 1)
	}
	if e := c.Invoke(ctx, service, &request, &response, grpc.ForceCodec(rawCodec{})); e != nil {
		return nil, e
	}
	var out []stat
	for len(response) > 0 {
		num, typ, n := protowire.ConsumeTag(response)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		response = response[n:]
		if num != 1 || typ != protowire.BytesType {
			return nil, errors.New("unexpected QueryStats response")
		}
		msg, n := protowire.ConsumeBytes(response)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		response = response[n:]
		var s stat
		for len(msg) > 0 {
			num, typ, n = protowire.ConsumeTag(msg)
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			msg = msg[n:]
			switch {
			case num == 1 && typ == protowire.BytesType:
				var name []byte
				name, n = protowire.ConsumeBytes(msg)
				s.Name = string(name)
			case num == 2 && typ == protowire.VarintType:
				var value uint64
				value, n = protowire.ConsumeVarint(msg)
				s.Value = int64(value)
			default:
				n = protowire.ConsumeFieldValue(num, typ, msg)
			}
			if n < 0 {
				return nil, protowire.ParseError(n)
			}
			msg = msg[n:]
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func main() {
	core := flag.String("core", "", "path to self-built sing-box")
	output := flag.String("output", "stats.json", "evidence output")
	flag.Parse()
	if e := run(*core, *output); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(core, output string) error {
	path, e := filepath.Abs(core)
	if e != nil || core == "" {
		return errors.New("-core is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	version, e := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if e != nil {
		return fmt.Errorf("version: %w %s", e, version)
	}
	if !strings.Contains(string(version), "with_v2ray_api") {
		return errors.New("missing with_v2ray_api")
	}
	result := evidence{Version: string(version), Method: service, BytesPerUser: payloadBytes}
	work, e := os.MkdirTemp("", "sing-box-stats-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(work)
	ports := make([]int, 3)
	for i := range ports {
		ports[i], e = freePort()
		if e != nil {
			return e
		}
	}
	ssPort, grpcPort, clientPort := ports[0], ports[1], ports[2]
	key := func() string {
		var b [16]byte
		if _, e := rand.Read(b[:]); e != nil {
			panic(e)
		}
		return base64.StdEncoding.EncodeToString(b[:])
	}
	serverKey, user1, user2 := key(), key(), key()
	server := map[string]any{
		"log":          map[string]any{"level": "error"},
		"inbounds":     []any{map[string]any{"type": "shadowsocks", "tag": "ss-in", "listen": "127.0.0.1", "listen_port": ssPort, "method": "2022-blake3-aes-128-gcm", "password": serverKey, "users": []any{map[string]any{"name": "u1", "password": user1}, map[string]any{"name": "u2", "password": user2}}}},
		"outbounds":    []any{map[string]any{"type": "direct", "tag": "direct"}},
		"experimental": map[string]any{"v2ray_api": map[string]any{"listen": fmt.Sprintf("127.0.0.1:%d", grpcPort), "stats": map[string]any{"enabled": true, "users": []string{"u1", "u2"}, "inbounds": []string{"ss-in"}}}},
	}
	stop, e := start(ctx, path, filepath.Join(work, "server.json"), server, grpcPort)
	if e != nil {
		return e
	}
	defer stop()
	conn, e := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", grpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		return e
	}
	defer conn.Close()
	chunk := make([]byte, 10000)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(payloadBytes))
		for written := int64(0); written < payloadBytes; written += int64(len(chunk)) {
			if _, e := w.Write(chunk); e != nil {
				return
			}
		}
	}))
	defer origin.Close()
	for i, userKey := range []string{user1, user2} {
		client := map[string]any{"log": map[string]any{"level": "error"}, "inbounds": []any{map[string]any{"type": "mixed", "tag": "local", "listen": "127.0.0.1", "listen_port": clientPort}}, "outbounds": []any{map[string]any{"type": "shadowsocks", "server": "127.0.0.1", "server_port": ssPort, "method": "2022-blake3-aes-128-gcm", "password": serverKey + ":" + userKey}}}
		stop, e := start(ctx, path, filepath.Join(work, fmt.Sprintf("client%d.json", i)), client, clientPort)
		if e != nil {
			return e
		}
		proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", clientPort))
		transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
		httpClient := &http.Client{Transport: transport, Timeout: time.Minute}
		resp, e := httpClient.Get(origin.URL)
		if e != nil {
			stop()
			return e
		}
		n, e := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		transport.CloseIdleConnections()
		stop()
		if e != nil || n != payloadBytes {
			return fmt.Errorf("u%d download: bytes=%d err=%v", i+1, n, e)
		}
		stats, e := query(ctx, conn, false)
		if e != nil {
			return e
		}
		if i == 0 {
			result.FirstUser = stats
			for _, s := range stats {
				if strings.HasPrefix(s.Name, "user>>>u2>>>") && s.Value != 0 {
					return errors.New("u1 traffic attributed to u2")
				}
			}
		}
	}
	result.Stats, e = query(ctx, conn, true)
	if e != nil {
		return e
	}
	values := map[string]int64{}
	for _, s := range result.Stats {
		values[s.Name] = s.Value
	}
	for _, user := range []string{"u1", "u2"} {
		download := values["user>>>"+user+">>>traffic>>>downlink"]
		if download < payloadBytes || download > payloadBytes+4096 {
			return fmt.Errorf("%s downlink unexpected: %d", user, download)
		}
		if values["user>>>"+user+">>>traffic>>>uplink"] <= 0 {
			return fmt.Errorf("%s missing uplink", user)
		}
	}
	for _, direction := range []string{"uplink", "downlink"} {
		if values["inbound>>>ss-in>>>traffic>>>"+direction] != values["user>>>u1>>>traffic>>>"+direction]+values["user>>>u2>>>traffic>>>"+direction] {
			return fmt.Errorf("inbound %s differs from user sum", direction)
		}
	}
	result.AfterReset, e = query(ctx, conn, true)
	if e != nil {
		return e
	}
	for _, s := range result.AfterReset {
		if s.Value != 0 {
			return fmt.Errorf("reset failed: %s=%d", s.Name, s.Value)
		}
	}
	result.Passed = true
	data, e := json.MarshalIndent(result, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(output, append(data, '\n'), 0600); e != nil {
		return e
	}
	fmt.Println(string(data))
	return nil
}
func freePort() (int, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return 0, e
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
func start(ctx context.Context, core, path string, config any, port int) (func(), error) {
	data, e := json.Marshal(config)
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(path, data, 0600); e != nil {
		return nil, e
	}
	if out, e := exec.CommandContext(ctx, core, "check", "-c", path).CombinedOutput(); e != nil {
		return nil, fmt.Errorf("check: %w %s", e, out)
	}
	cmd := exec.CommandContext(ctx, core, "run", "-c", path)
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	for range 100 {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if e == nil {
			c.Close()
			return stop, nil
		}
		select {
		case <-ctx.Done():
			stop()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	stop()
	return nil, fmt.Errorf("sing-box did not listen on %d", port)
}
