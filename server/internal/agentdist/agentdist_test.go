package agentdist

import (
	"os"
	"path/filepath"
	"testing"
)

// ----------------------------------------------------------------------

// writeDist 造一份镜像里那样的产物目录。
func writeDist(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	return string(raw)
}

func TestSyncCopiesWhitelistedFiles(t *testing.T) {
	src := writeDist(t, map[string]string{
		"vps-agent-linux-amd64": "amd64-binary",
		"vps-agent-linux-arm64": "arm64-binary",
		"install.sh":            "#!/usr/bin/env bash",
		"uninstall.sh":          "#!/usr/bin/env bash",
		// 白名单之外的东西不该被拷过去：下载接口也不认它，拷了只是白占数据卷
		"README.md": "不该出现在数据卷里",
	})
	dst := filepath.Join(t.TempDir(), "agent")

	if err := Sync(src, dst, "v1.0.0"); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, name := range []string{"vps-agent-linux-amd64", "vps-agent-linux-arm64", "install.sh", "uninstall.sh"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("%s 应当被同步过去: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); !os.IsNotExist(err) {
		t.Error("白名单之外的文件不该被同步")
	}
	if got := read(t, filepath.Join(dst, VersionFile)); got != "v1.0.0" {
		t.Errorf("版本文件应当是 v1.0.0，得到 %q", got)
	}
}

// TestSyncSkipsWhenVersionMatches 覆盖「每次重启都全量拷一遍」这个浪费。
func TestSyncSkipsWhenVersionMatches(t *testing.T) {
	src := writeDist(t, map[string]string{"install.sh": "第一版"})
	dst := filepath.Join(t.TempDir(), "agent")

	if err := Sync(src, dst, "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	// 源变了但版本号没变：跳过，文件保持旧内容
	if err := os.WriteFile(filepath.Join(src, "install.sh"), []byte("第二版"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Sync(src, dst, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "install.sh")); got != "第一版" {
		t.Errorf("同版本应当跳过同步，得到 %q", got)
	}

	// 版本号变了：覆盖
	if err := Sync(src, dst, "v1.1.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "install.sh")); got != "第二版" {
		t.Errorf("新版本应当覆盖，得到 %q", got)
	}
}

func TestSyncSkipsWhenDisabledOrMissing(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "agent")

	// 本地开发不设 VM_AGENT_DIST
	if err := Sync("", dst, "dev"); err != nil {
		t.Errorf("源为空应当直接跳过，得到 %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("跳过时不该建出目标目录")
	}

	// 目录不存在只记一条 WARN，不能让服务端起不来
	if err := Sync(filepath.Join(t.TempDir(), "nope"), dst, "dev"); err != nil {
		t.Errorf("源目录不存在应当跳过，得到 %v", err)
	}
}

// TestSyncOverwritesPartialDir 覆盖「上次拷到一半挂了」的情况：
// 没有版本文件就一定会重拷，不会留下残缺的一份。
func TestSyncOverwritesPartialDir(t *testing.T) {
	src := writeDist(t, map[string]string{
		"install.sh":            "完整的",
		"vps-agent-linux-amd64": "完整的",
	})
	dst := filepath.Join(t.TempDir(), "agent")

	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "install.sh"), []byte("残缺的"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Sync(src, dst, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "install.sh")); got != "完整的" {
		t.Errorf("没有版本文件时应当重拷，得到 %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "vps-agent-linux-amd64")); err != nil {
		t.Errorf("缺的文件应当补齐: %v", err)
	}
}
