package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Server 是 servers 表的一行：面板里手工维护的节点配置。
//
// TokenHash 是 agent token 的 sha256 hex，明文只在创建与重置时返回一次，库里不存。
type Server struct {
	ID               int64
	Name             string
	Region           string // ISO 3166-1 alpha-2，空表示未填
	GroupName        string
	Tags             []string
	SortOrder        int64
	TokenHash        string
	PublicHost       string
	Price            float64
	Currency         string
	BillingCycle     string  // month | quarter | year | once
	ExpireAt         *string // YYYY-MM-DD，nil 表示不设到期
	AutoRenew        bool
	TrafficLimit     int64 // 字节，0 = 不限
	TrafficResetDay  int
	TrafficResetMode string // monthly | days (30 calendar days)
	TrafficMode      string // out | in | sum | max
	BandwidthLabel   string
	Note             string
	CreatedAt        int64 // Unix 秒
	UpdatedAt        int64
}

// ServerInput 是创建 / 更新节点时的可写字段。token 与时间戳不在其中，由 store 自己管。
type ServerInput struct {
	Name             string
	Region           string
	GroupName        string
	Tags             []string
	SortOrder        int64
	PublicHost       string
	Price            float64
	Currency         string
	BillingCycle     string
	ExpireAt         *string
	AutoRenew        bool
	TrafficLimit     int64
	TrafficResetDay  int
	TrafficResetMode string
	TrafficPeriod    *TrafficPeriod // initial period, only used on creation
	TrafficMode      string
	BandwidthLabel   string
	Note             string
}

const serverColumns = `id, name, region, group_name, tags, sort_order, token_hash, public_host,
	price, currency, billing_cycle, expire_at, auto_renew,
	traffic_limit, traffic_reset_day, traffic_mode, bandwidth_label, note, created_at, updated_at, traffic_reset_mode`

func scanServer(row scanner) (*Server, error) {
	var (
		s         Server
		tagsJSON  string
		expireAt  sql.NullString
		autoRenew int64
	)
	err := row.Scan(&s.ID, &s.Name, &s.Region, &s.GroupName, &tagsJSON, &s.SortOrder, &s.TokenHash, &s.PublicHost,
		&s.Price, &s.Currency, &s.BillingCycle, &expireAt, &autoRenew,
		&s.TrafficLimit, &s.TrafficResetDay, &s.TrafficMode, &s.BandwidthLabel, &s.Note, &s.CreatedAt, &s.UpdatedAt, &s.TrafficResetMode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	s.AutoRenew = autoRenew != 0
	if expireAt.Valid {
		v := expireAt.String
		s.ExpireAt = &v
	}
	s.Tags = decodeTags(tagsJSON)
	return &s, nil
}

// decodeTags 把库里的 JSON 数组解成切片。解不出来就当空——标签不值得让整行读取失败。
func decodeTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var tags []string
	if err := json.Unmarshal([]byte(raw), &tags); err != nil || tags == nil {
		return []string{}
	}
	return tags
}

func encodeTags(tags []string) string {
	if tags == nil {
		tags = []string{}
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ListServers 返回全部节点，按 sort_order、id 升序。
func (db *DB) ListServers(ctx context.Context) ([]Server, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+serverColumns+" FROM servers ORDER BY sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Server{}
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// GetServer 按 ID 取节点，没有则返回 ErrNotFound。
func (db *DB) GetServer(ctx context.Context, id int64) (*Server, error) {
	return scanServer(db.QueryRowContext(ctx, "SELECT "+serverColumns+" FROM servers WHERE id = ?", id))
}

// FindServerByTokenHash 按 token 的 sha256 hex 查节点，没有则返回 ErrNotFound。步骤 05 的 agent 鉴权用。
func (db *DB) FindServerByTokenHash(ctx context.Context, tokenHash string) (*Server, error) {
	return scanServer(db.QueryRowContext(ctx, "SELECT "+serverColumns+" FROM servers WHERE token_hash = ?", tokenHash))
}

// CreateServer 新建节点。tokenHash 必须已经是哈希值，调用方负责生成明文 token。
func (db *DB) CreateServer(ctx context.Context, in ServerInput, tokenHash string) (*Server, error) {
	now := time.Now().Unix()
	var id int64
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO servers (name, region, group_name, tags, sort_order, token_hash, public_host,
			price, currency, billing_cycle, expire_at, auto_renew,
			traffic_limit, traffic_reset_day, traffic_mode, bandwidth_label, note, created_at, updated_at, traffic_reset_mode)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Name, in.Region, in.GroupName, encodeTags(in.Tags), in.SortOrder, tokenHash, in.PublicHost,
			in.Price, in.Currency, in.BillingCycle, expireAtArg(in.ExpireAt), boolToInt(in.AutoRenew),
			in.TrafficLimit, in.TrafficResetDay, in.TrafficMode, in.BandwidthLabel, in.Note, now, now, resetModeArg(in.TrafficResetMode),
		)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		if p := in.TrafficPeriod; p != nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO traffic_periods(server_id,period_start,next_reset) VALUES(?,?,?)`, id, p.Start, p.NextReset)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	db.InvalidateSubscriptions()
	return db.GetServer(ctx, id)
}

// UpdateServer 覆盖节点的可写字段，返回更新后的行；节点不存在返回 ErrNotFound。
func (db *DB) UpdateServer(ctx context.Context, id int64, in ServerInput, periodChanges ...TrafficPeriodChange) (*Server, error) {
	var updated *Server
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE servers SET name = ?, region = ?, group_name = ?, tags = ?, sort_order = ?, public_host = ?,
			price = ?, currency = ?, billing_cycle = ?, expire_at = ?, auto_renew = ?,
			traffic_limit = ?, traffic_reset_day = ?, traffic_mode = ?, bandwidth_label = ?, note = ?, updated_at = ?, traffic_reset_mode = ?
		 WHERE id = ?`,
			in.Name, in.Region, in.GroupName, encodeTags(in.Tags), in.SortOrder, in.PublicHost,
			in.Price, in.Currency, in.BillingCycle, expireAtArg(in.ExpireAt), boolToInt(in.AutoRenew),
			in.TrafficLimit, in.TrafficResetDay, in.TrafficMode, in.BandwidthLabel, in.Note, time.Now().Unix(), resetModeArg(in.TrafficResetMode),
			id,
		)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		for _, change := range periodChanges {
			if err := changeTrafficPeriod(ctx, tx, id, change); err != nil {
				return err
			}
		}
		updated, err = scanServer(tx.QueryRowContext(ctx, "SELECT "+serverColumns+" FROM servers WHERE id=?", id))
		return err
	})
	if err != nil {
		return nil, err
	}
	db.InvalidateSubscriptions()
	return updated, nil
}

func resetModeArg(mode string) string {
	if mode == "" {
		return "monthly"
	}
	return mode
}

// UpdateServerTokenHash 替换 agent token 的哈希（重置 token），节点不存在返回 ErrNotFound。
func (db *DB) UpdateServerTokenHash(ctx context.Context, id int64, tokenHash string) error {
	res, err := db.ExecContext(ctx,
		"UPDATE servers SET token_hash = ?, updated_at = ? WHERE id = ?",
		tokenHash, time.Now().Unix(), id,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteServer 删除节点。server_host_info 等子表靠外键 ON DELETE CASCADE 一起删，
// 前提是连接上开了 foreign_keys（见 Open 的 DSN）。
func (db *DB) DeleteServer(ctx context.Context, id int64) error {
	defer db.InvalidateSubscriptions()
	res, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountServers 返回节点总数。
func (db *DB) CountServers(ctx context.Context) (int, error) {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM servers").Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func expireAtArg(v *string) any {
	if v == nil || *v == "" {
		return nil
	}
	return *v
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
