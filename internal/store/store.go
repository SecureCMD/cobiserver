// Package store implements persistence for users, sync progress, web
// sessions and password reset tokens on top of SQLite.
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrExists       = errors.New("already exists")
	ErrInvalidInput = errors.New("invalid input")
)

// Store holds two separate *sql.DB handles onto the same SQLite file: one
// restricted to a single connection for all writes, and one with a small
// pool for concurrent reads. SQLite allows only one writer at a time no
// matter how many connections you open, so funneling every write through
// a single connection avoids SQLITE_BUSY entirely instead of relying on
// busy_timeout retries; WAL mode then lets reads proceed concurrently
// with that writer without blocking on it.
type Store struct {
	write *sql.DB
	read  *sql.DB
}

// DefaultMaxReadConns is used when maxReadConns <= 0 is passed to Open.
const DefaultMaxReadConns = 10

func Open(dbPath string, maxReadConns int) (*Store, error) {
	if dir := filepath.Dir(dbPath); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	if maxReadConns <= 0 {
		maxReadConns = DefaultMaxReadConns
	}
	dsn := dbPath + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"

	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)

	read, err := sql.Open("sqlite", dsn)
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(maxReadConns)

	s := &Store{write: write, read: read}
	if err := s.migrate(); err != nil {
		write.Close()
		read.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	errW := s.write.Close()
	errR := s.read.Close()
	if errW != nil {
		return errW
	}
	return errR
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL DEFAULT '',
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS progress (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			document TEXT NOT NULL,
			percentage REAL,
			progress TEXT,
			device TEXT,
			device_id TEXT,
			metadata TEXT,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, document)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS password_reset_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token_hash TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL,
			used_at TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_progress_user ON progress(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reset_user ON password_reset_tokens(user_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.write.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w: %s", err, stmt)
		}
	}
	return nil
}

func randomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func now() time.Time { return time.Now().UTC() }
