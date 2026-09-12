package collector

import (
	"os"
	"strconv"
	"strings"
)

// procNetSockstat 是连接数的来源。读文件比 gopsutil 的 net.Connections 快几个数量级：
// 后者要遍历 /proc/*/fd 把每个套接字都解析一遍，在连接数上万的机器上要几百毫秒。
const (
	procNetSockstat  = "/proc/net/sockstat"
	procNetSockstat6 = "/proc/net/sockstat6"
	procDir          = "/proc"
)

// connStats 是某一时刻的连接数。
type connStats struct {
	tcp int
	udp int
}

// readConnStats 读 sockstat 与 sockstat6 并相加。读不到（非 Linux、容器里没挂 /proc）时返回零值。
func readConnStats() connStats {
	var out connStats
	for _, path := range []string{procNetSockstat, procNetSockstat6} {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := parseSockstat(string(raw))
		out.tcp += s.tcp
		out.udp += s.udp
	}
	return out
}

// parseSockstat 从 sockstat / sockstat6 的内容里取 TCP 与 UDP 的 inuse 数。
//
// sockstat 形如：
//
//	sockets: used 213
//	TCP: inuse 12 orphan 0 tw 3 alloc 20 mem 2
//	UDP: inuse 4 mem 1
//
// sockstat6 的字段名不一样（TCP6: inuse 5 / UDP6: inuse 2），所以按前缀匹配、按 key 取值。
func parseSockstat(content string) connStats {
	var out connStats
	for _, line := range strings.Split(content, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "TCP", "TCP6":
			out.tcp += fieldValue(rest, "inuse")
		case "UDP", "UDP6":
			out.udp += fieldValue(rest, "inuse")
		}
	}
	return out
}

// fieldValue 在 "inuse 12 orphan 0 tw 3" 这样的 key value 序列里取某个 key 的值。
func fieldValue(s, key string) int {
	fields := strings.Fields(s)
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == key {
			n, err := strconv.Atoi(fields[i+1])
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// readProcCount 数 /proc 下的纯数字目录，即进程数。读不到时返回 0。
func readProcCount() int {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if isAllDigits(e.Name()) {
			n++
		}
	}
	return n
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
