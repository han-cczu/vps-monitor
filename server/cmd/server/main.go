// Command server 是面板服务端：汇聚 agent 上报、提供 REST 与 WebSocket、内嵌前端。
//
// 步骤 02：配置、SQLite + 迁移、JWT 登录、初始管理员、审计、内嵌前端。
// 步骤 05：agent 与浏览器的 WebSocket 接入、在线判定、每秒快照广播。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // 静态二进制 / 精简镜像里也能加载 VM_TZ

	"vpsmon/server/internal/api"
	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/clock"
	"vpsmon/server/internal/config"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
	"vpsmon/server/web"
)

// version 由构建时 -ldflags "-X main.version=$(VERSION)" 注入。
var version = "dev"

func main() {
	// 先装一个 info 级别的 JSON logger，配置解析出错也能按同样格式打出来
	setLogger(slog.LevelInfo)

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
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

	limiter := auth.NewLimiter(auth.DefaultMaxFailures, auth.DefaultWindow, auth.DefaultLockout)
	go limiter.Run(ctx, time.Minute)

	realtime := hub.New(db, tokens)
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
