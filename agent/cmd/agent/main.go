// Command agent 是装在每台 VPS 上的采集与受控执行进程。
//
// 步骤 01 只做骨架：解析参数、打印版本、退出；采集与 WSS 传输见步骤 04。
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// version 由构建时 -ldflags "-X main.version=$(VERSION)" 注入。
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "/etc/vps-agent/config.yaml", "配置文件路径")
		showVersion = flag.Bool("version", false, "打印版本后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	slog.Info("agent skeleton", "version", version, "config", *configPath)
}
