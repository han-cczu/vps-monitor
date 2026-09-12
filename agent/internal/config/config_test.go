package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte("server: wss://panel.example.com/api/agent/ws\ntoken: abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case cfg.ReportInterval != 1:
		t.Errorf("report_interval=%d want 1", cfg.ReportInterval)
	case cfg.LogLevel != "info":
		t.Errorf("log_level=%q want info", cfg.LogLevel)
	case !slices.Equal(cfg.DiskMounts, []string{"/"}):
		t.Errorf("disk_mounts=%v want [/]", cfg.DiskMounts)
	case !slices.Equal(cfg.Interfaces.Exclude, DefaultExcludeInterfaces):
		t.Errorf("exclude=%v want %v", cfg.Interfaces.Exclude, DefaultExcludeInterfaces)
	}

	// 空配置也要能解析出一套默认值：--once 在没装过的机器上要用
	empty, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty.ReportInterval != 1 || len(empty.DiskMounts) != 1 {
		t.Errorf("空配置默认值不对：%+v", empty)
	}
}

func TestParseOverrides(t *testing.T) {
	const raw = `
server: ws://127.0.0.1:9000/api/agent/ws
token: "  spaced  "
report_interval: 5
interfaces:
  exclude: ["lo", "eth1"]
disk_mounts: ["/", "/data"]
log_level: debug
`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case cfg.Token != "spaced":
		t.Errorf("token 没去掉首尾空白：%q", cfg.Token)
	case cfg.ReportInterval != 5:
		t.Errorf("report_interval=%d want 5", cfg.ReportInterval)
	case !slices.Equal(cfg.Interfaces.Exclude, []string{"lo", "eth1"}):
		t.Errorf("exclude=%v", cfg.Interfaces.Exclude)
	case !slices.Equal(cfg.DiskMounts, []string{"/", "/data"}):
		t.Errorf("disk_mounts=%v", cfg.DiskMounts)
	case cfg.LogLevel != "debug":
		t.Errorf("log_level=%q", cfg.LogLevel)
	}

	// exclude 显式写成空数组 = 全部网卡都算
	cfg2, err := Parse([]byte("interfaces:\n  exclude: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg2.Interfaces.Exclude) != 0 {
		t.Errorf("显式空 exclude 被默认值覆盖了：%v", cfg2.Interfaces.Exclude)
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"不是 YAML": "server: [unclosed\n",
		"间隔为负":    "report_interval: -1\n",
		"间隔超上限":   "report_interval: 3600\n",
		"日志级别不对":  "log_level: verbose\n",
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: 应该报错", name)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Server: "wss://panel.example.com/api/agent/ws", Token: "t"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法配置被拒：%v", err)
	}
	if err := (Config{Server: "ws://127.0.0.1:9000/api/agent/ws", Token: "t"}).Validate(); err != nil {
		t.Fatalf("ws:// 也该接受：%v", err)
	}

	bad := []Config{
		{Token: "t"}, // 缺 server
		{Server: "wss://panel.example.com/api/agent/ws"},               // 缺 token
		{Server: "https://panel.example.com/api/agent/ws", Token: "t"}, // 协议不对
		{Server: "panel.example.com", Token: "t"},                      // 没有协议
		{Server: "wss://", Token: "t"},                                 // 没有 host
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("应该被拒：%+v", c)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if !os.IsNotExist(errUnwrapAll(err)) {
		t.Fatalf("文件不存在时应能识别出 ErrNotExist，得到 %v", err)
	}
}

func TestLoadReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("server: wss://x/api/agent/ws\ntoken: tok\nreport_interval: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "tok" || cfg.ReportInterval != 3 {
		t.Fatalf("读出来的配置不对：%+v", cfg)
	}
}

// errUnwrapAll 剥到最内层的错误，Load 把 os 的错误包了一层。
func errUnwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		inner := u.Unwrap()
		if inner == nil {
			return err
		}
		err = inner
	}
}
