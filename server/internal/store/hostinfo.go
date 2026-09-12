package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// HostInfo 是 server_host_info 表的一行：agent 上报的静态信息，每台节点只留最新一份。
//
// 步骤 03 只建表与读写方法，真正的写入者是步骤 05 的 agent hub（收到 hello 时 upsert）。
type HostInfo struct {
	ServerID     int64
	Hostname     string
	OS           string
	Kernel       string
	Arch         string
	CPUModel     string
	Cores        int
	MemTotal     int64
	DiskTotal    int64
	BootTime     int64 // Unix 秒
	IPv4         bool  // agent 探测到的 IPv4 可达性
	IPv6         bool
	PublicIP     string // 服务端从 agent 连接地址记下的公网 IP
	AgentVersion string
	UpdatedAt    int64 // Unix 秒
}

const hostInfoColumns = `server_id, COALESCE(hostname, ''), COALESCE(os, ''), COALESCE(kernel, ''),
	COALESCE(arch, ''), COALESCE(cpu_model, ''), COALESCE(cores, 0), COALESCE(mem_total, 0),
	COALESCE(disk_total, 0), COALESCE(boot_time, 0), COALESCE(ipv4, 0), COALESCE(ipv6, 0),
	COALESCE(public_ip, ''), COALESCE(agent_version, ''), COALESCE(updated_at, 0)`

func scanHostInfo(row scanner) (*HostInfo, error) {
	var (
		h          HostInfo
		ipv4, ipv6 int64
	)
	err := row.Scan(&h.ServerID, &h.Hostname, &h.OS, &h.Kernel, &h.Arch, &h.CPUModel, &h.Cores,
		&h.MemTotal, &h.DiskTotal, &h.BootTime, &ipv4, &ipv6, &h.PublicIP, &h.AgentVersion, &h.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	h.IPv4 = ipv4 != 0
	h.IPv6 = ipv6 != 0
	return &h, nil
}

// UpsertHostInfo 写入（或覆盖）一台节点的静态信息。UpdatedAt 由本方法填当前时间。
// 节点不存在时外键约束会让写入失败。
func (db *DB) UpsertHostInfo(ctx context.Context, h HostInfo) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO server_host_info (server_id, hostname, os, kernel, arch, cpu_model, cores,
			mem_total, disk_total, boot_time, ipv4, ipv6, public_ip, agent_version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(server_id) DO UPDATE SET
			hostname = excluded.hostname, os = excluded.os, kernel = excluded.kernel,
			arch = excluded.arch, cpu_model = excluded.cpu_model, cores = excluded.cores,
			mem_total = excluded.mem_total, disk_total = excluded.disk_total, boot_time = excluded.boot_time,
			ipv4 = excluded.ipv4, ipv6 = excluded.ipv6, public_ip = excluded.public_ip,
			agent_version = excluded.agent_version, updated_at = excluded.updated_at`,
		h.ServerID, h.Hostname, h.OS, h.Kernel, h.Arch, h.CPUModel, h.Cores,
		h.MemTotal, h.DiskTotal, h.BootTime, boolToInt(h.IPv4), boolToInt(h.IPv6),
		h.PublicIP, h.AgentVersion, time.Now().Unix(),
	)
	return err
}

// GetHostInfo 取一台节点的静态信息，没有上报过则返回 ErrNotFound。
func (db *DB) GetHostInfo(ctx context.Context, serverID int64) (*HostInfo, error) {
	return scanHostInfo(db.QueryRowContext(ctx,
		"SELECT "+hostInfoColumns+" FROM server_host_info WHERE server_id = ?", serverID))
}

// ListHostInfo 返回全部节点的静态信息，按 server_id 索引，供列表接口一次取齐。
func (db *DB) ListHostInfo(ctx context.Context) (map[int64]HostInfo, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+hostInfoColumns+" FROM server_host_info")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int64]HostInfo{}
	for rows.Next() {
		h, err := scanHostInfo(rows)
		if err != nil {
			return nil, err
		}
		out[h.ServerID] = *h
	}
	return out, rows.Err()
}
