package collector

import (
	"strings"

	"github.com/shirou/gopsutil/v4/net"
)

// ifaceTotals 是某一时刻所有计入统计的网卡收发累计值之和。
type ifaceTotals struct {
	rx uint64
	tx uint64
}

// readIfaceTotals 按 exclude 规则过滤网卡后，把收发累计值求和。
func readIfaceTotals(exclude []string) (ifaceTotals, error) {
	counters, err := net.IOCounters(true)
	if err != nil {
		return ifaceTotals{}, err
	}

	var total ifaceTotals
	for _, c := range counters {
		if matchAny(c.Name, exclude) {
			continue
		}
		total.rx += c.BytesRecv
		total.tx += c.BytesSent
	}
	return total, nil
}

// matchAny 判断网卡名是否命中任意一条通配规则。
//
// 只支持末尾一个 `*`（`docker*`、`br-*`），够用且不会像 filepath.Match 那样
// 因为名字里的 `[` 之类字符返回错误。
func matchAny(name string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "*") {
			if strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
				return true
			}
			continue
		}
		if name == p {
			return true
		}
	}
	return false
}

// rate 按两次采样算每秒速率。
//
// 计数器回绕（机器重启、32 位计数器翻转、网卡被重建）时新值会小于旧值，
// 这时记 0 而不是一个天文数字——宁可丢一个采样点，也不能让曲线炸出尖峰。
func rate(prev, cur uint64, seconds float64) int64 {
	if seconds <= 0 || cur < prev {
		return 0
	}
	return int64(float64(cur-prev) / seconds)
}
