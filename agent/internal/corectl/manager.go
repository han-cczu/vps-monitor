package corectl

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vpsmon/proto"
)

type Options struct {
	ObserveOnly                 bool
	ExternalPresent             func() bool
	Server, Token, StatsAddress string
	Paths                       Paths
	Runner                      Runner
	Send                        func(any) error
	Connected                   func() bool
}

type Manager struct {
	invalidOwnership                           bool
	observeOnly                                bool
	externalPresent                            func() bool
	paths                                      Paths
	runner                                     Runner
	server, token, statsAddress                string
	send                                       func(any) error
	connected                                  func() bool
	mu                                         sync.Mutex
	record                                     record
	lastError, statsError                      string
	op                                         sync.Mutex
	queue                                      chan any
	stateRequested                             chan struct{}
	ready                                      chan struct{}
	lastState                                  proto.CoreState
	versionMu                                  sync.Mutex
	versionStamp                               string
	versionCache                               string
	listening                                  func() ([]string, error)
	healthTimeout, healthInterval              time.Duration
	pollInterval, stateInterval, statsInterval time.Duration
	statsMu                                    sync.Mutex
	statsClient                                *statsClient
	pendingStats                               proto.CoreStats
}

func New(o Options) (*Manager, error) {
	if o.Paths.Binary == "" {
		o.Paths = DefaultPaths()
	}
	if o.Runner == nil {
		o.Runner = execRunner{}
	}
	if o.StatsAddress == "" {
		o.StatsAddress = "127.0.0.1:10085"
	}
	if err := ValidateStatsAddress(o.StatsAddress); err != nil {
		return nil, err
	}
	r, err := readRecord(o.Paths.State)
	invalidOwnership := err != nil
	if invalidOwnership {
		r = record{}
	}
	m := &Manager{invalidOwnership: invalidOwnership, observeOnly: o.ObserveOnly, externalPresent: o.ExternalPresent, paths: o.Paths, runner: o.Runner, server: o.Server, token: o.Token, statsAddress: o.StatsAddress, send: o.Send, connected: o.Connected, record: r,
		queue: make(chan any, 4), stateRequested: make(chan struct{}, 1), ready: make(chan struct{}), healthTimeout: 5 * time.Second, healthInterval: 200 * time.Millisecond, pollInterval: 10 * time.Second, stateInterval: 60 * time.Second, statsInterval: 10 * time.Second}
	m.listening = func() ([]string, error) { return Listening(m.paths.Proc) }
	return m, nil
}

// Handle validates before queueing. Unknown commands never reach an executor.
func (m *Manager) Handle(raw []byte) error {
	var env proto.Envelope
	// The envelope intentionally permits the body fields; the concrete decode below is strict.
	if len(raw) > MaxConfigSize+65536 {
		return fmt.Errorf("core message too large")
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid core message JSON")
	}
	var job any
	var err error
	switch env.Type {
	case proto.TypeCoreApply:
		var a proto.CoreApply
		err = decode(raw, &a)
		if err == nil {
			err = Validate(a)
		}
		job = a
	case proto.TypeCoreAction:
		var a proto.CoreAction
		err = decode(raw, &a)
		if err == nil {
			err = validateAction(a)
		}
		job = a
	case proto.TypeCoreLogs:
		var a proto.CoreLogsReq
		err = decode(raw, &a)
		if err == nil && (a.Kind != "error" || a.Lines < 1 || a.Lines > 1000 || len(a.ReqID) > 128) {
			err = fmt.Errorf("logs require kind=error and lines=1..1000")
		}
		job = a
	default:
		return fmt.Errorf("unsupported core message type")
	}
	if err != nil {
		return err
	}
	if err := m.guardJob(job); err != nil {
		return err
	}
	select {
	case m.queue <- job:
		return nil
	default:
		return fmt.Errorf("core operation queue is full (4 waiting)")
	}
}
func (m *Manager) RequestState() {
	select {
	case m.stateRequested <- struct{}{}:
	default:
	}
}

func (m *Manager) Ready() <-chan struct{} { return m.ready }

// Rejected uses the most recent observation; a malformed message must not block
// the websocket reader on systemctl or execute any command.
func (m *Manager) Rejected(reqID string, err error) proto.CoreState {
	m.mu.Lock()
	st := m.lastState
	m.mu.Unlock()
	st.Type = proto.TypeCoreState
	st.Core = "sing-box"
	st.ReqID = reqID
	if st.Listening == nil {
		st.Listening = []string{}
	}
	if st.Firewall == "" {
		st.Firewall = "none"
	}
	message := boundedError(err.Error())
	st.Error = &message
	return st
}

// Execute is shared by the local CLI and the single queue worker. A process lock
// also excludes a CLI operation while the daemon is applying a configuration.
func (m *Manager) Execute(ctx context.Context, job any) (proto.CoreState, error) {
	m.op.Lock()
	defer m.op.Unlock()
	var reqID string
	switch a := job.(type) {
	case proto.CoreApply:
		reqID = a.ReqID
		if err := Validate(a); err != nil {
			return m.emitState(ctx, reqID, err), err
		}
	case proto.CoreAction:
		reqID = a.ReqID
		if err := validateAction(a); err != nil {
			return m.emitState(ctx, reqID, err), err
		}
	default:
		err := fmt.Errorf("unsupported core operation")
		return m.emitState(ctx, "", err), err
	}
	err := m.withLock(ctx, func() error {
		if err := m.guardJob(job); err != nil {
			return err
		}
		if err := m.recoverApply(ctx); err != nil {
			return err
		}
		switch a := job.(type) {
		case proto.CoreApply:
			return m.apply(ctx, a)
		case proto.CoreAction:
			if a.Action == "install" {
				return m.install(ctx, a)
			}
			if err := m.service(ctx, a.Action); err != nil {
				return err
			}
			if a.Action != "stop" {
				return m.healthy(ctx, m.snapshot().Ports)
			}
		}
		return nil
	})
	m.setError(err)
	return m.emitState(ctx, reqID, err), err
}
func (m *Manager) withLock(ctx context.Context, f func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.paths.State), 0700); err != nil {
		return err
	}
	unlock, err := fileLock(m.paths.State + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	// Another local CLI may have changed the persisted revision since construction.
	r, err := readRecord(m.paths.State)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.record = r
	m.mu.Unlock()
	return f()
}

func (m *Manager) Run(ctx context.Context) {
	m.op.Lock()
	err := m.withLock(ctx, func() error {
		if err := m.migrateOwner(ctx); err != nil {
			return nil
		}
		return m.recoverApply(ctx)
	})
	m.op.Unlock()
	m.setError(err)
	close(m.ready)
	done := make(chan struct{})
	go func() { defer close(done); m.monitor(ctx) }()
	defer func() { <-done; m.Close() }()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-m.queue:
			if a, ok := job.(proto.CoreLogsReq); ok {
				if err := m.verifyOwned(); err != nil {
					m.emitState(ctx, a.ReqID, err)
					continue
				}
				txt, err := Tail(m.paths.Log, a.Lines)
				if err != nil {
					m.emitState(ctx, a.ReqID, err)
				} else if m.send != nil {
					_ = m.send(proto.CoreLogs{Type: proto.TypeCoreLogs, Kind: a.Kind, Text: txt, ReqID: a.ReqID})
				}
				continue
			}
			_, _ = m.Execute(ctx, job)
		}
	}
}

func (m *Manager) monitor(ctx context.Context) {
	poll := time.NewTicker(m.pollInterval)
	state := time.NewTicker(m.stateInterval)
	stats := time.NewTicker(m.statsInterval)
	logTick := time.NewTicker(24 * time.Hour)
	defer logTick.Stop()
	if err := m.maintainLog(); err != nil {
		slog.Warn("sing-box log maintenance", "err", err)
	}
	defer poll.Stop()
	defer state.Stop()
	defer stats.Stop()
	last := m.State(ctx)
	m.emitState(ctx, "", nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-logTick.C:
			if err := m.maintainLog(); err != nil {
				slog.Warn("sing-box log maintenance", "err", err)
			}
		case <-m.stateRequested:
			m.emitState(ctx, "", nil)
		case <-state.C:
			m.emitState(ctx, "", nil)
		case <-poll.C:
			st := m.State(ctx)
			if st.Running != last.Running {
				slog.Info("sing-box 状态变化", "running", st.Running)
				if m.send != nil {
					_ = m.send(st)
				}
			}
			last = st
		case <-stats.C:
			if m.verifyOwned() != nil {
				continue
			}
			if m.connected != nil && !m.connected() {
				continue
			}
			if running, _ := m.active(ctx); !running {
				continue
			}
			m.reportStats(ctx)
		}
	}
}
