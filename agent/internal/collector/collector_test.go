package collector

import (
	"testing"

	"github.com/shirou/gopsutil/v4/cpu"
)

func TestMatchAny(t *testing.T) {
	// 与 config.DefaultExcludeInterfaces 保持一致
	exclude := []string{"lo", "docker*", "veth*", "br-*", "tun*", "tap*", "tailscale*", "wg*"}

	cases := map[string]bool{
		"lo":           true,
		"docker0":      true,
		"docker":       true,
		"veth1a2b3c":   true,
		"br-0e1f2a":    true,
		"tun0":         true,
		"tap0":         true,
		"tailscale0":   true,
		"wg0":          true,
		"eth0":         false,
		"ens3":         false,
		"enp0s3":       false,
		"eno1":         false,
		"bond0":        false,
		"local":        false, // 不能被 "lo" 前缀误伤：没有 * 的规则要求全等
		"br0":          false, // 规则是 br-*，br0 是普通网桥名，不排除
		"wireguard0":   false,
		"eth0.100":     false,
		"docker-proxy": true, // docker* 前缀命中，符合预期
	}

	for name, want := range cases {
		if got := matchAny(name, exclude); got != want {
			t.Errorf("matchAny(%q) = %v, want %v", name, got, want)
		}
	}

	if matchAny("eth0", nil) {
		t.Error("空规则不该匹配任何网卡")
	}
	if matchAny("eth0", []string{"", "   "}) {
		t.Error("空白规则应被忽略")
	}
}

func TestRate(t *testing.T) {
	cases := []struct {
		name    string
		prev    uint64
		cur     uint64
		seconds float64
		want    int64
	}{
		{"正常增长", 1000, 2000, 1, 1000},
		{"两秒间隔", 1000, 3000, 2, 1000},
		{"半秒间隔", 1000, 1500, 0.5, 1000},
		{"没有变化", 1000, 1000, 1, 0},
		{"计数器回绕", 4_294_967_000, 1000, 1, 0}, // 新值小于旧值，记 0 而不是天文数字
		{"网卡被重建", 5_000_000, 0, 1, 0},
		{"间隔为零", 1000, 2000, 0, 0},
		{"间隔为负", 1000, 2000, -1, 0},
	}

	for _, c := range cases {
		if got := rate(c.prev, c.cur, c.seconds); got != c.want {
			t.Errorf("%s: rate(%d, %d, %v) = %d, want %d", c.name, c.prev, c.cur, c.seconds, got, c.want)
		}
	}
}

func TestParseSockstat(t *testing.T) {
	const v4 = `sockets: used 213
TCP: inuse 12 orphan 0 tw 3 alloc 20 mem 2
UDP: inuse 4 mem 1
UDPLITE: inuse 0
RAW: inuse 0
FRAG: inuse 0 memory 0
`
	got := parseSockstat(v4)
	if got.tcp != 12 || got.udp != 4 {
		t.Fatalf("sockstat: tcp=%d udp=%d, want 12/4", got.tcp, got.udp)
	}

	const v6 = `TCP6: inuse 5
UDP6: inuse 2
UDPLITE6: inuse 0
RAW6: inuse 1
FRAG6: inuse 0 memory 0
`
	got6 := parseSockstat(v6)
	if got6.tcp != 5 || got6.udp != 2 {
		t.Fatalf("sockstat6: tcp=%d udp=%d, want 5/2", got6.tcp, got6.udp)
	}

	// 畸形内容不该 panic，也不该给出垃圾数字
	for _, bad := range []string{"", "garbage", "TCP:", "TCP: inuse", "TCP: inuse abc", "no colon here"} {
		s := parseSockstat(bad)
		if s.tcp != 0 || s.udp != 0 {
			t.Errorf("parseSockstat(%q) = %+v, want 零值", bad, s)
		}
	}
}

func TestFieldValue(t *testing.T) {
	const line = " inuse 12 orphan 0 tw 3 alloc 20 mem 2"
	cases := map[string]int{"inuse": 12, "orphan": 0, "tw": 3, "alloc": 20, "mem": 2, "missing": 0}
	for key, want := range cases {
		if got := fieldValue(line, key); got != want {
			t.Errorf("fieldValue(%q) = %d, want %d", key, got, want)
		}
	}
}

func TestCPUPercent(t *testing.T) {
	// 一秒里 100 个时间片：50 忙、50 空 → 50%
	prev := cpu.TimesStat{User: 100, System: 50, Idle: 800, Iowait: 50}
	cur := cpu.TimesStat{User: 130, System: 70, Idle: 840, Iowait: 60}
	// 总增量 = 30+20+40+10 = 100，空闲增量 = 40+10 = 50
	if got := cpuPercent(prev, cur); got != 50 {
		t.Errorf("cpuPercent = %v, want 50", got)
	}

	// 完全空闲
	idle := cpu.TimesStat{User: 100, Idle: 900}
	idleNext := cpu.TimesStat{User: 100, Idle: 1000}
	if got := cpuPercent(idle, idleNext); got != 0 {
		t.Errorf("全空闲 cpuPercent = %v, want 0", got)
	}

	// 满载
	busyNext := cpu.TimesStat{User: 200, Idle: 900}
	if got := cpuPercent(idle, busyNext); got != 100 {
		t.Errorf("满载 cpuPercent = %v, want 100", got)
	}

	// 时间片没动（两次采样之间没有跨过一个 tick）
	if got := cpuPercent(idle, idle); got != 0 {
		t.Errorf("无变化 cpuPercent = %v, want 0", got)
	}

	// 计数器回退（容器里迁移过、宿主重启）不该出现负数
	back := cpu.TimesStat{User: 10, Idle: 10}
	if got := cpuPercent(idle, back); got < 0 || got > 100 {
		t.Errorf("回退时 cpuPercent = %v, 应被夹在 0–100", got)
	}
}

func TestFormatOS(t *testing.T) {
	cases := []struct {
		platform, version, osName string
		want                      string
	}{
		{"debian", "12", "linux", "Debian 12"},
		{"ubuntu", "22.04", "linux", "Ubuntu 22.04"},
		{"alpine", "", "linux", "Alpine"},
		{"", "", "linux", "Linux"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		if got := formatOS(c.platform, c.version, c.osName); got != c.want {
			t.Errorf("formatOS(%q, %q, %q) = %q, want %q", c.platform, c.version, c.osName, got, c.want)
		}
	}
}

func TestSamplerFirstSampleHasNoRates(t *testing.T) {
	s := NewSampler([]string{"lo"}, nil)

	first := s.Sample()
	if first.Type != "metrics" || first.TS == 0 {
		t.Fatalf("第一帧字段不对：%+v", first)
	}
	// 差值算法的固有结果：第一帧没有上一次快照，速率与 CPU 都是 0
	if first.CPU != 0 || first.Net.RxRate != 0 || first.Net.TxRate != 0 {
		t.Errorf("第一帧不该有速率：cpu=%v rx=%d tx=%d", first.CPU, first.Net.RxRate, first.Net.TxRate)
	}

	second := s.Sample()
	if second.TS < first.TS {
		t.Errorf("时间戳倒退：%d → %d", first.TS, second.TS)
	}
	if second.CPU < 0 || second.CPU > 100 {
		t.Errorf("CPU 越界：%v", second.CPU)
	}
	if second.Net.RxRate < 0 || second.Net.TxRate < 0 {
		t.Errorf("速率为负：%+v", second.Net)
	}
}
