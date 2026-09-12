// Package collector 采集节点的静态信息与秒级指标。
//
// 原则：单项采集失败记 WARN 并填零值，不让整条 metrics 缺席——
// 面板上少一个数字，比曲线断一截好判断。
package collector

import (
	"log/slog"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"

	"vpsmon/proto"
)

// unknown 是采集不到时的占位。LXC / OpenVZ 上 host.Info() 的部分字段就是空的。
const unknown = "unknown"

// CollectHost 采集静态信息。diskMounts 是要统计总容量的挂载点。
func CollectHost(diskMounts []string) proto.HostInfo {
	out := proto.HostInfo{
		Hostname: unknown,
		OS:       unknown,
		Kernel:   unknown,
		Arch:     runtime.GOARCH,
		CPUModel: unknown,
		Cores:    runtime.NumCPU(),
	}

	if info, err := host.Info(); err == nil {
		if info.Hostname != "" {
			out.Hostname = info.Hostname
		}
		if os := formatOS(info.Platform, info.PlatformVersion, info.OS); os != "" {
			out.OS = os
		}
		if info.KernelVersion != "" {
			out.Kernel = info.KernelVersion
		}
		if info.KernelArch != "" {
			out.Arch = info.KernelArch
		}
		out.BootTime = int64(info.BootTime)
	} else {
		slog.Warn("采集主机信息失败", "err", err)
	}

	if cpus, err := cpu.Info(); err == nil && len(cpus) > 0 {
		if model := strings.TrimSpace(cpus[0].ModelName); model != "" {
			out.CPUModel = model
		}
	} else if err != nil {
		slog.Warn("采集 CPU 型号失败", "err", err)
	}

	if vm, err := mem.VirtualMemory(); err == nil {
		out.MemTotal = int64(vm.Total)
	} else {
		slog.Warn("采集内存总量失败", "err", err)
	}

	total, _ := diskTotals(diskMounts)
	out.DiskTotal = total

	out.IPv4, out.IPv6 = ProbeIPStacks()
	return out
}

// formatOS 把发行版与版本拼成 "Debian 12"；拿不到就退回 GOOS。
func formatOS(platform, version, osName string) string {
	platform = strings.TrimSpace(platform)
	version = strings.TrimSpace(version)
	switch {
	case platform != "" && version != "":
		return capitalize(platform) + " " + version
	case platform != "":
		return capitalize(platform)
	case osName != "":
		return capitalize(osName)
	default:
		return ""
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// diskTotals 求所有挂载点的总容量与已用量之和。
//
// 同一个设备被挂两次（bind mount）会被重复计算，所以按设备去重。
func diskTotals(mounts []string) (total, used int64) {
	seen := map[string]bool{}
	partitions, err := disk.Partitions(false)
	if err != nil {
		partitions = nil
	}
	device := func(mount string) string {
		for _, p := range partitions {
			if p.Mountpoint == mount {
				return p.Device
			}
		}
		return mount
	}

	for _, m := range mounts {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		dev := device(m)
		if seen[dev] {
			continue
		}
		usage, err := disk.Usage(m)
		if err != nil {
			slog.Warn("采集磁盘用量失败", "mount", m, "err", err)
			continue
		}
		seen[dev] = true
		total += int64(usage.Total)
		used += int64(usage.Used)
	}
	return total, used
}
