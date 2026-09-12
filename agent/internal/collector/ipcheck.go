package collector

import (
	"net"
	"time"
)

// IPProbeTimeout 是单次探测的超时。
const IPProbeTimeout = 3 * time.Second

// 探测目标：Cloudflare 的公共 DNS（1.1.1.1 与它的 v6 地址），443 端口基本不会被拦。
//
// 这里只判断“这台机器有没有外网 v4 / v6 出口”，不关心对端是谁；
// 换成别的地址也行，改这两个常量即可。
var (
	probeV4Addr = "1.1.1.1:443"
	probeV6Addr = "[2606:4700:4700::1111]:443"
)

// ProbeIPStacks 分别用 tcp4 / tcp6 拨号，看这台机器有没有对应的外网出口。
//
// 两个探测并行，最坏情况只花一个超时的时间。
func ProbeIPStacks() (ipv4, ipv6 bool) {
	type result struct {
		v4 bool
		v6 bool
	}
	ch := make(chan result, 2)

	go func() { ch <- result{v4: probe("tcp4", probeV4Addr)} }()
	go func() { ch <- result{v6: probe("tcp6", probeV6Addr)} }()

	for range 2 {
		r := <-ch
		ipv4 = ipv4 || r.v4
		ipv6 = ipv6 || r.v6
	}
	return ipv4, ipv6
}

func probe(network, addr string) bool {
	d := net.Dialer{Timeout: IPProbeTimeout}
	conn, err := d.Dial(network, addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
