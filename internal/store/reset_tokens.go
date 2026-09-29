package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreatePasswordResetToken generates a one-time password reset token for a
// user and returns the plaintext token (only the SHA-256 hash is stored).
func (s *Store) CreatePasswordResetToken(userID int64, ttl time.Duration) (string, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	_, err = s.write.Exec(
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
		userID, hashToken(token), now().Add(ttl),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConsumePasswordResetToken validates a token, marks it used, and returns
// the associated user id. It fails closed on expiry, prior use, or an
// unknown token.
func (s *Store) ConsumePasswordResetToken(token string) (int64, error) {
	h := hashToken(token)
	row := s.read.QueryRow(
		`SELECT id, user_id, expires_at, used_at FROM password_reset_tokens WHERE token_hash = ?`, h,
	)
	var id, userID int64
	var expiresAt time.Time
	var usedAt sql.NullTime
	if err := row.Scan(&id, &userID, &expiresAt, &usedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if usedAt.Valid {
		return 0, ErrInvalidInput
	}
	if expiresAt.Before(now()) {
		return 0, ErrInvalidInput
	}
	if _, err := s.write.Exec(`UPDATE password_reset_tokens SET used_at = ? WHERE id = ?`, now(), id); err != nil {
		return 0, err
	}
	return userID, nil
}

func (s *Store) CleanupExpiredResetTokens() error {
	_, err := s.write.Exec(`DELETE FROM password_reset_tokens WHERE expires_at < ?`, now())
	return err
}
