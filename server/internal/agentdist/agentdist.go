// Package agentdist 把镜像自带的 agent 产物同步到数据目录，供节点下载。
//
// 步骤 03 的 /install.sh 与 /agent/{file} 读的是 {DataDir}/agent/，而镜像里带的是
// /app/agent-dist/。启动时同步一次，于是「升级镜像」自动等于「升级可下载的 agent」，
// 运维不用再手工往数据卷里拷二进制。
package agentdist

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// VersionFile 记录数据目录里当前那份产物是哪个版本，用来决定要不要覆盖。
const VersionFile = "VERSION"

// 允许同步的文件名。和 api/static.go 的下载白名单保持一致——
// 多拷的文件既下载不到，也只是白占数据卷。
var allowed = map[string]bool{
	"vps-agent-linux-amd64": true,
	"vps-agent-linux-arm64": true,
	"install.sh":            true,
	"uninstall.sh":          true,
}

// Sync 在需要时把 src 里的 agent 产物复制到 dst。
//
// src 为空或不存在时直接跳过（本地开发就是这种情况）。
// 只有当 dst 里的 VERSION 与 version 不一致时才复制：镜像每次重启都全量拷一遍
// 没有意义，数据卷在慢盘上时还会拖慢启动。
func Sync(src, dst, version string) error {
	if src == "" {
		return nil
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("agent 产物目录不存在，跳过同步", "dir", src)
			return nil
		}
		return fmt.Errorf("读取 agent 产物目录 %s: %w", src, err)
	}

	current, err := readVersion(dst)
	if err != nil {
		return err
	}
	if current == version && current != "" {
		slog.Debug("agent 产物已是当前版本，跳过同步", "version", version)
		return nil
	}

	if err := os.MkdirAll(dst, 0o700); err != nil {
		return fmt.Errorf("创建 %s: %w", dst, err)
	}

	copied := 0
	for _, entry := range entries {
		if entry.IsDir() || !allowed[entry.Name()] {
			continue
		}
		if err := copyFile(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
		copied++
	}

	if copied == 0 {
		slog.Warn("agent 产物目录里没有可同步的文件", "dir", src)
		return nil
	}

	// 版本文件最后写：中途失败时下次启动会重来一遍，而不是留下一份「版本对得上、
	// 文件其实没拷全」的假象。
	if err := os.WriteFile(filepath.Join(dst, VersionFile), []byte(version), 0o600); err != nil {
		return fmt.Errorf("写入版本文件: %w", err)
	}

	slog.Info("agent 产物已同步", "version", version, "files", copied, "dir", dst)
	return nil
}

func readVersion(dst string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dst, VersionFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("读取版本文件: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

// copyFile 先写临时文件再改名，避免节点正好在这一刻下到半截文件。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", src, err)
	}
	defer in.Close()

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return fmt.Errorf("创建 %s: %w", tmp, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("复制 %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换 %s: %w", dst, err)
	}
	return nil
}
