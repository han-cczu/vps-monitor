package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

var ErrTOTPReplay = errors.New("TOTP already used")

func (db *DB) SetTOTP(ctx context.Context, id int64, previous, secret string, enabled bool) error {
	res, err := db.ExecContext(ctx, "UPDATE users SET totp_secret=?,totp_enabled=? WHERE id=? AND COALESCE(totp_secret,'')=?", secret, enabled, id, previous)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func (db *DB) ResetTOTP(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, "UPDATE users SET totp_secret=NULL,totp_enabled=0 WHERE id=?", id)
	return err
}

var ErrTOTPState = errors.New("TOTP credentials changed")

type TOTPChange struct {
	Secret  string
	Enabled bool
	Audit   AuditEntry
}

// ConsumeTOTP is a linearization point: the credentials used to validate the code
// must still match the row at the atomic insert, including password/secret/enabled.
func (db *DB) ConsumeTOTP(ctx context.Context, expected *User, step int64, code string, now time.Time) error {
	return db.ApplyTOTP(ctx, expected, step, code, now, nil)
}
func (db *DB) ApplyTOTP(ctx context.Context, u *User, step int64, code string, now time.Time, change *TOTPChange) error {
	if u == nil {
		return ErrTOTPState
	}
	hash := sha256.Sum256([]byte(code))
	s := hex.EncodeToString(hash[:])
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO totp_used(user_id,step,code_hash,used_at)
   SELECT id,?,?,? FROM users WHERE id=? AND password_hash=? AND COALESCE(totp_secret,'')=? AND totp_enabled=?
   AND NOT EXISTS(SELECT 1 FROM totp_used WHERE user_id=? AND (step=? OR (code_hash=? AND used_at>?)))
   ON CONFLICT(user_id,step) DO NOTHING`, step, s, now.Unix(), u.ID, u.PasswordHash, u.TOTPSecret, u.TOTPEnabled, u.ID, step, s, now.Unix()-90)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			var unchanged int
			err = tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id=? AND password_hash=? AND COALESCE(totp_secret,'')=? AND totp_enabled=?`, u.ID, u.PasswordHash, u.TOTPSecret, u.TOTPEnabled).Scan(&unchanged)
			if err != nil {
				return err
			}
			if unchanged == 0 {
				return ErrTOTPState
			}
			return ErrTOTPReplay
		}
		if change == nil {
			return nil
		}
		res, err = tx.ExecContext(ctx, `UPDATE users SET totp_secret=?,totp_enabled=? WHERE id=? AND password_hash=? AND COALESCE(totp_secret,'')=? AND totp_enabled=?`, change.Secret, change.Enabled, u.ID, u.PasswordHash, u.TOTPSecret, u.TOTPEnabled)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrTOTPState
		}
		before, after := `{"enabled":false}`, `{"enabled":false}`
		if u.TOTPEnabled {
			before = `{"enabled":true}`
		}
		if change.Enabled {
			after = `{"enabled":true}`
		}
		e := change.Audit
		_, err = tx.ExecContext(ctx, `INSERT INTO audit_log(ts,actor,action,target_type,target_id,"before","after",ip) VALUES(?,?,?,?,?,?,?,?)`, e.TS, e.Actor, e.Action, e.TargetType, e.TargetID, before, after, nullIfEmpty(e.IP))
		return err
	})
}
