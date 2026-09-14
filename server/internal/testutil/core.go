// Package testutil contains small fixtures used only by repository tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// CoreBinary builds a tiny ELF with sing-box-shaped build metadata for upload validation tests.
// This is a synthetic fixture, not a functioning proxy. Real stats validation lives in ci/sing-box-stats.
func CoreBinary(t *testing.T, version, arch, tags string) []byte {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":               "module github.com/sagernet/sing-box\n\ngo 1.25.5\n",
		"constant/version.go":  "package constant\nvar Version = \"unknown\"\n",
		"cmd/sing-box/main.go": "package main\nimport \"github.com/sagernet/sing-box/constant\"\nfunc main(){println(constant.Version)}\n",
	} {
		path := filepath.Join(dir, name)
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	out := filepath.Join(dir, "core")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-tags", tags, "-ldflags", "-s -w -X github.com/sagernet/sing-box/constant.Version="+strings.TrimPrefix(version, "v"), "-o", out, "./cmd/sing-box")
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOWORK", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS":
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=")
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build fixture: %v %s", e, output)
	}
	data, e := os.ReadFile(out)
	if e != nil {
		t.Fatal(e)
	}
	return data
}
