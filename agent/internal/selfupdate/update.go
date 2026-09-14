// Package selfupdate downloads and replaces only the running agent, never an arbitrary path/command.
package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"vpsmon/proto"
)

type Updater struct {
	server, current, binary, arch, goos string
	queue                               chan proto.AgentUpdate
	busy                                atomic.Bool
	runVersion                          func(context.Context, string) (string, error)
	replaceProcess                      func(string) error
	send                                func(any) error
	maxSize                             int64
}

func New(server, current string, send func(any) error) (*Updater, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, err
	}
	return &Updater{server: server, current: current, binary: binary, arch: runtime.GOARCH, goos: runtime.GOOS, queue: make(chan proto.AgentUpdate, 1), runVersion: binaryVersion, replaceProcess: replaceProcess, send: send, maxSize: proto.MaxAgentBinarySize}, nil
}
func (u *Updater) Handle(raw []byte) error {
	if len(raw) > 2048 {
		return fmt.Errorf("agent update message too large")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a proto.AgentUpdate
	if err := dec.Decode(&a); err != nil {
		return fmt.Errorf("invalid agent update JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing update data")
	}
	if u.goos != "linux" {
		return fmt.Errorf("self update requires Linux")
	}
	if err := a.Validate(u.arch, u.current); err != nil {
		return err
	}
	if !u.busy.CompareAndSwap(false, true) {
		return fmt.Errorf("agent update already pending")
	}
	u.queue <- a
	return nil
}
func (u *Updater) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-u.queue:
			err := u.apply(ctx, a)
			u.busy.Store(false)
			if err != nil {
				slog.Warn("agent update failed; old binary retained", "err", err)
				if u.send != nil {
					_ = u.send(proto.Error{Type: proto.TypeError, Op: proto.TypeAgentUpdate, Message: err.Error()})
				}
			}
		}
	}
}
func downloadURL(server, file string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("invalid panel URL")
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("agent update requires WSS outside loopback")
		}
		u.Scheme = "http"
	default:
		return "", fmt.Errorf("invalid panel scheme")
	}
	u.Path = "/agent/" + file
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
func (u *Updater) apply(ctx context.Context, a proto.AgentUpdate) error {
	if u.goos != "linux" {
		return fmt.Errorf("self update requires Linux")
	}
	if err := a.Validate(u.arch, u.current); err != nil {
		return err
	}
	address, err := downloadURL(u.server, a.File)
	if err != nil {
		return err
	}
	info, err := os.Lstat(u.binary)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > proto.MaxAgentBinarySize {
		return fmt.Errorf("agent binary is not regular")
	}
	backup := u.binary + ".bak"
	if bi, err := os.Lstat(backup); err == nil && !bi.Mode().IsRegular() {
		return fmt.Errorf("agent backup is not regular")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect forbidden") }}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > u.maxSize {
		return fmt.Errorf("agent download too large")
	}
	f, err := os.CreateTemp(filepath.Dir(u.binary), ".vps-agent-new-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, u.maxSize+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n == 0 || n > u.maxSize {
		return fmt.Errorf("invalid agent download size")
	}
	if hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return fmt.Errorf("agent SHA256 mismatch")
	}
	if err = os.Chmod(tmp, 0755); err != nil {
		return err
	}
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 5*time.Second)
	v, err := u.runVersion(verifyCtx, tmp)
	verifyCancel()
	if err != nil {
		return fmt.Errorf("agent --version failed: %w", err)
	}
	if v != a.Version {
		return fmt.Errorf("agent --version does not match requested version")
	}
	if err = copyBackup(u.binary, backup); err != nil {
		return fmt.Errorf("backup current agent: %w", err)
	}
	if err = os.Rename(tmp, u.binary); err != nil {
		return fmt.Errorf("replace agent: %w", err)
	}
	// Linux exec retains the systemd PID and preserves service supervision. If exec fails, restore atomically.
	if err = u.replaceProcess(u.binary); err != nil {
		if restoreErr := os.Rename(backup, u.binary); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore old binary from %s: %w", backup, restoreErr))
		}
		return fmt.Errorf("start new agent: %w", err)
	}
	return nil // Production exec never returns on success; this path permits injected tests.
}
func copyBackup(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(dst), ".vps-agent-backup-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, err = io.Copy(f, io.LimitReader(in, proto.MaxAgentBinarySize+1))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Chmod(tmp, 0755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4096 {
		return 0, fmt.Errorf("version output too large")
	}
	return b.Buffer.Write(p)
}
func binaryVersion(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.WaitDelay = time.Second
	out := &boundedOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}
