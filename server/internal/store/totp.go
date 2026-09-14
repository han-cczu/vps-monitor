package store

import (
	"context"
	"crypto/sha256"
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

// An atomic conditional insert blocks concurrent reuse, including repeated digits in adjacent steps.
func (db *DB) ConsumeTOTP(ctx context.Context, id, step int64, code string, now time.Time) error {
	hash := sha256.Sum256([]byte(code))
	s := hex.EncodeToString(hash[:])
	res, err := db.ExecContext(ctx, `INSERT INTO totp_used(user_id,step,code_hash,used_at)
 SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM totp_used WHERE user_id=? AND (step=? OR (code_hash=? AND used_at>?)))
 ON CONFLICT(user_id,step) DO NOTHING`, id, step, s, now.Unix(), id, step, s, now.Unix()-90)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrTOTPReplay
	}
	return nil
}
