// Command agent 是装在每台 VPS 上的采集与受控执行进程。
//
// 步骤 04：采集静态信息与秒级指标，通过 WSS 上报，断线自动重连。
// sing-box 相关（corectl）见步骤 11。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vpsmon/agent/internal/collector"
	"vpsmon/agent/internal/config"
	"vpsmon/agent/internal/ping"
	"vpsmon/agent/internal/transport"
	"vpsmon/proto"
)

// version 由构建时 -ldflags "-X main.version=$(VERSION)" 注入。
var version = "dev"

// hostInfoTTL 是静态信息的缓存时间。CollectHost 里的 IPv6 探测在没有 v6 的机器上
// 要等满 3 秒超时，重连时不该每次都付这个代价。
const hostInfoTTL = 5 * time.Minute

func main() {
	var (
		configPath  = flag.String("config", config.DefaultPath, "配置文件路径")
		once        = flag.Bool("once", false, "采集一次并打印 JSON 后退出，不联网上报")
		showVersion = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	setLogger(slog.LevelInfo)

	if err := run(*configPath, *once); err != nil {
		slog.Error("agent 退出", "err", err)
		os.Exit(1)
	}
}

func run(configPath string, once bool) error {
	cfg, err := loadConfig(configPath, once)
	if err != nil {
		return err
	}
	level, _ := config.ParseLogLevel(cfg.LogLevel)
	setLogger(level)

	sampler := collector.NewSampler(cfg.Interfaces.Exclude, cfg.DiskMounts)

	if once {
		return runOnce(cfg, sampler)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	host := newHostCache(cfg.DiskMounts)
	host.get() // 预热：把第一次 IP 探测的耗时放在连接之前

	// 上报间隔可以被服务端的 config 消息改，用原子量在采集循环与读循环之间传递
	var reportInterval atomic.Int64
	reportInterval.Store(int64(cfg.ReportInterval))

	// ping 调度器。结果直接经 WebSocket 回报；没连上就丢弃这一条——
	// 攒着等重连没有意义，面板要的是「现在通不通」。
	var client *transport.Client
	pinger := ping.New(func(r ping.Result) {
		if client == nil {
			return
		}
		if err := client.Send(proto.PingResult{
			Type:      proto.TypePing,
			TaskID:    r.TaskID,
			TS:        r.TS,
			LatencyMS: r.LatencyMS,
		}); err != nil && !errors.Is(err, transport.ErrNotConnected) {
			slog.Warn("上报 ping 结果失败", "task_id", r.TaskID, "err", err)
		}
	})
	defer pinger.Stop()

	client = transport.New(transport.Options{
		Server:  cfg.Server,
		Token:   cfg.Token,
		Version: version,
		Hello: func() proto.Hello {
			return proto.Hello{
				Type:         proto.TypeHello,
				ProtoVersion: proto.Version,
				Version:      version,
				Host:         host.get(),
			}
		},
		OnMessage: func(msgType string, raw []byte) {
			handleMessage(msgType, raw, &reportInterval, pinger)
		},
	})

	slog.Info("agent 启动",
		"version", version,
		"server", cfg.Server,
		"report_interval", cfg.ReportInterval,
		"disk_mounts", cfg.DiskMounts,
	)

	go client.Run(ctx)
	sampleLoop(ctx, sampler, client, &reportInterval)

	slog.Info("agent 已停止")
	return nil
}

// loadConfig 读配置。--once 模式下配置文件不存在也能跑：用默认值采集一次，
// 方便在还没装过的机器上直接 ./vps-agent --once 看采集结果。
func loadConfig(path string, once bool) (config.Config, error) {
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if once && errors.Is(err, os.ErrNotExist) {
		slog.Warn("没有配置文件，按默认值采集", "path", path)
		return config.Parse(nil)
	}
	return config.Config{}, err
}

// runOnce 采集一次 hello 与 metrics 并打印。
//
// CPU 占用与网络速率来自两次采样的差值，所以这里必须采两次、中间隔满一个间隔，
// 否则打印出来的永远是 0。
func runOnce(cfg config.Config, sampler *collector.Sampler) error {
	started := time.Now()
	sampler.Sample() // 预热，拿第一份 CPU 时间片与网卡计数

	host := collector.CollectHost(cfg.DiskMounts)

	if wait := time.Second - time.Since(started); wait > 0 {
		time.Sleep(wait)
	}

	out := struct {
		Hello   proto.Hello   `json:"hello"`
		Metrics proto.Metrics `json:"metrics"`
	}{
		Hello: proto.Hello{
			Type:         proto.TypeHello,
			ProtoVersion: proto.Version,
			Version:      version,
			Host:         host,
		},
		Metrics: sampler.Sample(),
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// sampleLoop 按当前上报间隔采集并发送，直到 ctx 结束。
//
// 没连上时直接丢弃这一帧：累计流量在网卡计数器里，断线期间的量不会丢。
func sampleLoop(ctx context.Context, sampler *collector.Sampler, client *transport.Client, interval *atomic.Int64) {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m := sampler.Sample()
			if err := client.Send(m); err != nil && !errors.Is(err, transport.ErrNotConnected) {
				slog.Warn("上报 metrics 失败", "err", err)
			}
			timer.Reset(time.Duration(interval.Load()) * time.Second)
		}
	}
}

// handleMessage 对齐上报间隔和 ping 任务；core.* 留给步骤 11/13。
func handleMessage(msgType string, raw []byte, interval *atomic.Int64, pinger *ping.Scheduler) {
	switch msgType {
	case proto.TypeConfig:
		var c proto.Config
		if err := json.Unmarshal(raw, &c); err != nil {
			slog.Warn("解析 config 失败", "err", err)
			return
		}
		if c.ReportInterval > 0 {
			next := clampInterval(c.ReportInterval)
			if next != int(interval.Load()) {
				slog.Info("上报间隔已更新", "seconds", next)
			}
			interval.Store(int64(next))
		}
		// 每次 config 都整体对齐一次，包括「一个任务都没有」——那表示全部停掉。
		// 服务端在任务增删改之后会主动重发 config，所以不需要重连也能生效。
		pinger.Apply(context.Background(), c.PingTasks)
	default:
		slog.Warn("收到暂不支持的消息类型", "type", msgType)
	}
}

func clampInterval(v int) int {
	switch {
	case v < config.MinReportInterval:
		return config.MinReportInterval
	case v > config.MaxReportInterval:
		return config.MaxReportInterval
	default:
		return v
	}
}

// hostCache 缓存静态信息，避免每次重连都重新做一遍 IP 探测。
type hostCache struct {
	mounts []string

	mu        sync.Mutex
	info      proto.HostInfo
	collected time.Time
}

func newHostCache(mounts []string) *hostCache {
	return &hostCache{mounts: mounts}
}

func (h *hostCache) get() proto.HostInfo {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.collected.IsZero() && time.Since(h.collected) < hostInfoTTL {
		return h.info
	}
	h.info = collector.CollectHost(h.mounts)
	h.collected = time.Now()
	return h.info
}

func setLogger(level slog.Level) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
