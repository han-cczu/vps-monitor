// Command server 是面板服务端：汇聚 agent 上报、提供 REST 与 WebSocket、内嵌前端。
//
// 步骤 02：配置、SQLite + 迁移、JWT 登录、初始管理员、审计、内嵌前端。
// 步骤 05：agent 与浏览器的 WebSocket 接入、在线判定、每秒快照广播。
// 步骤 07：backup / reset-password / version 三个子命令，以及镜像自带 agent 的同步。
// 步骤 08：秒级指标按分钟聚合落库、按小时降采样、按保留期清理。
//
// 不带子命令就是启动服务端。子命令都是运维用的一次性操作，跑完即退出，
// 和正在运行的服务端共用同一个数据目录（SQLite 的 WAL 模式允许多进程访问）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // 静态二进制 / 精简镜像里也能加载 VM_TZ

	"vpsmon/server/internal/agentdist"
	"vpsmon/server/internal/api"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/billing"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/config"
	"vpsmon/server/internal/corefiles"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/metrics"
	"vpsmon/server/internal/ping"
	"vpsmon/server/internal/proxy"
	"vpsmon/server/internal/store"
	"vpsmon/server/internal/traffic"
	"vpsmon/server/web"
)

// version 由构建时 -ldflags "-X main.version=$(VERSION)" 注入。
var version = "dev"

func main() {
	// 先装一个 info 级别的 JSON logger，配置解析出错也能按同样格式打出来
	setLogger(slog.LevelInfo)

	if err := dispatch(os.Args[1:]); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// dispatch 按子命令分发。没有子命令就是启动服务端。
func dispatch(args []string) error {
	if len(args) == 0 {
		return run()
	}

	switch args[0] {
	case "version":
		fmt.Println(version)
		return nil
	case "backup":
		return backupCommand(args[1:])
	case "reset-password":
		return resetPasswordCommand(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		printUsage()
		return fmt.Errorf("未知的子命令 %q", args[0])
	}
}

func printUsage() {
	fmt.Print(`用法：
  server                      启动服务端（读 VM_* 环境变量）
  server backup [路径]        备份数据库；不给路径就写到 {VM_DATA_DIR}/backup/vm-YYYY-MM-DD.db
  server reset-password 用户名  重置密码，打印一个新的随机密码
  server version              打印版本
`)
}

// backupCommand 实现 server backup [路径]。
func backupCommand(args []string) error {
	if len(args) > 1 {
		return errors.New("backup 最多接一个路径参数")
	}

	cfg, db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	path := ""
	if len(args) == 1 {
		path = args[0]
	} else {
		// 默认文件名带日期，配合 backup.sh 的「保留 14 天」正好一天一个
		path = filepath.Join(cfg.BackupDir(), fmt.Sprintf("vm-%s.db", clock.Now().Format("2006-01-02")))
	}

	if err := db.BackupTo(context.Background(), path); err != nil {
		return err
	}

	// 这一句是给人看的，不走 slog 的 JSON——运维在终端里跑，JSON 反而难读
	fmt.Println(path)
	return nil
}

// resetPasswordCommand 实现 server reset-password 用户名。
//
// 生成一个新的随机密码并打印，不接受手工指定：命令行参数会进 shell 历史和 ps 输出。
func resetPasswordCommand(args []string) error {
	if len(args) != 1 {
		return errors.New("用法：server reset-password 用户名")
	}
	username := args[0]

	_, db, err := openStore()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	user, err := db.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("用户 %q 不存在", username)
		}
		return err
	}

	password, err := auth.RandomPassword(16)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := db.UpdateUserPassword(ctx, user.ID, hash); err != nil {
		return err
	}

	fmt.Printf("用户 %s 的新密码：%s\n", username, password)
	fmt.Println("（只显示这一次，登录后请到「设置 → 账号」自行修改）")
	return nil
}

// openStore 给子命令用：读配置、开库、跑迁移。
//
// 也跑迁移是有意的：运维可能在换镜像之后、服务端还没起来之前就先备份一次。
func openStore() (config.Config, *store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, err
	}

	loc, err := cfg.Location()
	if err != nil {
		return cfg, nil, err
	}
	clock.SetLocation(loc)

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return cfg, nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		db.Close()
		return cfg, nil, err
	}
	return cfg, db, nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level, _ := config.ParseLogLevel(cfg.LogLevel)
	setLogger(level)

	if err := os.MkdirAll(cfg.DataDir, config.DataDirPerm); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}

	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	clock.SetLocation(loc)

	secret, err := config.EnsureJWTSecret(cfg)
	if err != nil {
		return err
	}
	tokens, err := auth.NewTokens(secret, auth.TokenTTL)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.Migrate(ctx); err != nil {
		return err
	}
	if err := auth.EnsureAdmin(ctx, db); err != nil {
		return err
	}

	// 镜像自带的 agent 产物同步到数据目录，节点从 /install.sh 与 /agent/{file} 下载的就是它
	if err := agentdist.Sync(cfg.AgentDist, cfg.AgentDir(), version); err != nil {
		return err
	}

	limiter := auth.NewLimiter(auth.DefaultMaxFailures, auth.DefaultWindow, auth.DefaultLockout)
	cores, err := corefiles.New(filepath.Join(cfg.DataDir, "corefiles"), db)
	if err != nil {
		return err
	}
	defer cores.Close()
	if err := cores.Reconcile(ctx); err != nil {
		return fmt.Errorf("restore current core: %w", err)
	}
	go limiter.Run(ctx, time.Minute)

	realtime := hub.New(db, tokens)
	accountant := traffic.New(db, realtime.Bus)
	if err := accountant.Load(ctx); err != nil {
		return fmt.Errorf("load traffic: %w", err)
	}
	realtime.Agents.OnMetrics(accountant.OnMetrics)
	realtime.Registry.SetTrafficSource(accountant.SnapshotFor)
	trafficCtx, stopTraffic := context.WithCancel(context.Background())
	trafficDone := make(chan struct{})
	go func() { defer close(trafficDone); accountant.Run(trafficCtx) }()
	defer func() { stopTraffic(); <-trafficDone }()
	go billing.New(db, realtime.Bus, realtime.InvalidateConfig).Run(ctx)
	pings := ping.New(db, realtime.Agents)
	if err := pings.ReloadTasks(ctx); err != nil {
		return fmt.Errorf("load ping tasks: %w", err)
	}
	if err := pings.Cleanup(ctx); err != nil {
		return fmt.Errorf("clean ping results: %w", err)
	}
	if err := pings.Warm(ctx); err != nil {
		return fmt.Errorf("warm ping results: %w", err)
	}
	realtime.Agents.OnPing(pings.OnPing)
	realtime.Agents.SetConfigBuilder(func(id int64) any { return pings.BuildConfig(id, hub.DefaultReportInterval) })
	realtime.Registry.SetPingSource(pings.SnapshotFor)
	pingCtx, stopPing := context.WithCancel(context.Background())
	pingDone := make(chan struct{})
	go func() { defer close(pingDone); pings.Run(pingCtx) }()
	// HTTP 收尾后停止接收结果，等待最后一批落库，再关闭数据库。
	defer func() { stopPing(); <-pingDone }()

	reconciler := proxy.NewReconciler(db, proxy.ReconcilerOptions{
		Agents: realtime.Agents, Check: cores.CheckConfig,
		Artifact: func(ctx context.Context, v, a string) (corefiles.Artifact, error) {
			f, meta, err := cores.Open(v, a)
			if f != nil {
				f.Close()
			}
			return meta, err
		},
		Failed: func(id int64) {
			realtime.Bus.Publish(hub.Event{Kind: hub.EventCoreApplyFailed, ServerID: id, At: clock.Now()})
		},
	})
	realtime.Agents.OnCore(reconciler.Handle, reconciler.OnAgentHello)
	realtime.Registry.SetCoreSource(reconciler.SnapshotFor)
	proxyCtx, stopProxy := context.WithCancel(context.Background())
	proxyDone := make(chan struct{})
	go func() { defer close(proxyDone); reconciler.Run(proxyCtx) }()
	defer func() { stopProxy(); <-proxyDone }()

	// 指标聚合：每条 metrics 先进内存桶，每分钟落一次库；每小时降采样并清理过期数据
	aggregator := metrics.New(db)
	realtime.Agents.OnMetrics(aggregator.OnMetrics)
	go aggregator.Run(ctx)
	go metrics.NewRollup(db).Run(ctx)

	go realtime.Run(ctx)

	handler := api.NewRouter(api.Deps{
		DB:             db,
		Tokens:         tokens,
		Limiter:        limiter,
		Version:        version,
		Web:            web.Handler(),
		TrustedProxies: cfg.TrustedProxies,
		DataDir:        cfg.DataDir,
		PublicURL:      cfg.PublicURL,
		Hub:            realtime,
		Ping:           pings,
		CoreFiles:      cores,
		Proxy:          proxy.New(db, reconciler),
		Reconciler:     reconciler,
		Traffic:        accountant,
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting",
			"listen", cfg.Listen,
			"version", version,
			"data_dir", cfg.DataDir,
			"tz", cfg.TZ,
			"public_url", cfg.PublicURL,
			"trusted_proxies", describeProxies(cfg.TrustedProxies),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen %s: %w", cfg.Listen, err)
		}
	case <-ctx.Done():
	}

	slog.Info("server stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	slog.Info("server stopped")
	return nil
}

// describeProxies 把可信代理配置写成一句人能看懂的话，放进启动日志方便核对。
func describeProxies(tp config.TrustedProxies) string {
	switch {
	case len(tp.CIDRs) > 0:
		return "cidr:" + strings.Join(tp.CIDRs, ",")
	case tp.Count > 0:
		return fmt.Sprintf("hops:%d", tp.Count)
	default:
		return "direct (客户端 IP 取 TCP 连接地址，不采信代理头)"
	}
}

func setLogger(level slog.Level) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
