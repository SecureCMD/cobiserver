package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID           int64
	Username     string
	Email        string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

// ValidUsername reports whether the username is an acceptable identifier.
func ValidUsername(u string) bool { return usernameRe.MatchString(u) }

// MD5Hex returns the lowercase hex MD5 digest of a plaintext password, the
// same transform the KOReader client applies before it ever talks to the
// server. It is intentionally weak on its own; we always bcrypt the result
// before persisting it (see hashMD5Hex).
func MD5Hex(plain string) string {
	sum := md5.Sum([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func hashMD5Hex(md5hex string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(md5hex), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CreateUser creates a user given the MD5(password) digest. Callers with a
// plaintext password should pass MD5Hex(plain) as md5hex; the KOReader sync
// protocol itself hands us an MD5 digest directly.
func (s *Store) CreateUser(username, md5hex, email string) (*User, error) {
	username = strings.TrimSpace(username)
	if !ValidUsername(username) {
		return nil, ErrInvalidInput
	}
	if len(md5hex) == 0 {
		return nil, ErrInvalidInput
	}
	hash, err := hashMD5Hex(md5hex)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(
		`INSERT INTO users (username, email, password_hash) VALUES (?, ?, ?)`,
		username, email, hash,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrExists
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	// The very first account on a fresh server automatically becomes
	// admin, regardless of whether it was created through the KOReader
	// app's own "Register" button, the web UI, or the CLI, so there is
	// always a way into /admin/users without touching the database.
	if n, err := s.CountUsers(); err == nil && n == 1 {
		_, _ = s.db.Exec(`UPDATE users SET is_admin = 1 WHERE id = ?`, id)
	}
	return s.GetUserByID(id)
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func scanUser(row interface {
	Scan(dest ...any) error
}) (*User, error) {
	var u User
	var isAdmin int
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &isAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.IsAdmin = isAdmin != 0
	return &u, nil
}

func (s *Store) GetUserByID(id int64) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, email, password_hash, is_admin, created_at, updated_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByUsername(username string) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, email, password_hash, is_admin, created_at, updated_at FROM users WHERE username = ?`, username)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	if email == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRow(`SELECT id, username, email, password_hash, is_admin, created_at, updated_at FROM users WHERE email = ?`, email)
	return scanUser(row)
}

// VerifyMD5 checks a username/MD5(password) pair, as used by both the
// KOReader sync protocol and (indirectly) the web login form.
func (s *Store) VerifyMD5(username, md5hex string) (*User, bool, error) {
	u, err := s.GetUserByUsername(username)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(md5hex)) != nil {
		return nil, false, nil
	}
	return u, true, nil
}

func (s *Store) SetPasswordMD5(userID int64, md5hex string) error {
	hash, err := hashMD5Hex(md5hex)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, hash, userID)
	return err
}

func (s *Store) SetEmail(userID int64, email string) error {
	_, err := s.db.Exec(`UPDATE users SET email = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, email, userID)
	return err
}

func (s *Store) SetAdmin(username string, admin bool) error {
	v := 0
	if admin {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE users SET is_admin = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?`, v, username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(username string) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE username = ?`, username)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, email, password_hash, is_admin, created_at, updated_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}
