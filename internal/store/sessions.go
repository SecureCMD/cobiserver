package store

import (
	"database/sql"
	"errors"
	"time"
)

type Session struct {
	ID        string
	UserID    int64
	ExpiresAt time.Time
}

// CreateSession creates a new web session and returns the opaque session
// id to place in a cookie.
func (s *Store) CreateSession(userID int64, ttl time.Duration) (string, error) {
	id, err := randomToken(32)
	if err != nil {
		return "", err
	}
	expires := now().Add(ttl)
	_, err = s.write.Exec(`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`, id, userID, expires)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) GetSession(id string) (*Session, error) {
	row := s.read.QueryRow(`SELECT id, user_id, expires_at FROM sessions WHERE id = ?`, id)
	var sess Session
	if err := row.Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if sess.ExpiresAt.Before(now()) {
		_ = s.DeleteSession(id)
		return nil, ErrNotFound
	}
	return &sess, nil
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.write.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) DeleteSessionsForUser(userID int64) error {
	_, err := s.write.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) CleanupExpiredSessions() error {
	_, err := s.write.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now())
	return err
}
