package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type AuthUser struct {
	ID           int64
	Username     string
	PasswordHash string
	LastLoginAt  sql.NullString
	Theme        string
}

type Session struct {
	ID        string
	UserID    int64
	ExpiresAt time.Time
}

const sessionTTL = 30 * 24 * time.Hour

func (db *DB) HasUsers() (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (db *DB) CreateUser(username, password string) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return 0, errors.New("username required")
	}
	if len(password) < 8 {
		return 0, errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO users(username,password_hash,theme,created_at,updated_at) VALUES(?,?,?,datetime('now'),datetime('now'))`,
		username, string(hash), "system")
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

func (db *DB) GetUserByUsername(username string) (*AuthUser, error) {
	var u AuthUser
	err := db.QueryRow(`SELECT id, username, password_hash, last_login_at, theme FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.LastLoginAt, &u.Theme)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (db *DB) GetUserByID(id int64) (*AuthUser, error) {
	var u AuthUser
	err := db.QueryRow(`SELECT id, username, password_hash, last_login_at, theme FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.LastLoginAt, &u.Theme)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (db *DB) UpdatePassword(userID int64, newPassword string) error {
	if len(newPassword) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE users SET password_hash=?, updated_at=datetime('now') WHERE id=?`, string(hash), userID)
	return err
}

func (db *DB) UpdateLastLogin(userID int64) {
	_, _ = db.Exec(`UPDATE users SET last_login_at=datetime('now') WHERE id=?`, userID)
}

func (db *DB) UpdateUsername(userID int64, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username required")
	}
	_, err := db.Exec(`UPDATE users SET username=?, updated_at=datetime('now') WHERE id=?`, username, userID)
	return err
}

func (db *DB) EnsureInitialUser(username, password string) error {
	hasUsers, err := db.HasUsers()
	if err != nil {
		return err
	}
	if hasUsers {
		return nil
	}
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return fmt.Errorf("no users exist; set auth_user/auth_pass in config to bootstrap an account")
	}
	_, err = db.CreateUser(username, password)
	return err
}

func (db *DB) UpdateTheme(userID int64, theme string) error {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "light", "dark", "system":
		_, err := db.Exec(`UPDATE users SET theme=?, updated_at=datetime('now') WHERE id=?`, strings.ToLower(theme), userID)
		return err
	default:
		return errors.New("invalid theme")
	}
}

func generateSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (db *DB) CreateSession(userID int64) (string, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(sessionTTL)
	_, err = db.Exec(`INSERT INTO sessions(id,user_id,expires_at,created_at,last_seen_at) VALUES(?,?,?,datetime('now'),datetime('now'))`,
		sessionID, userID, expiresAt.Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

func (db *DB) GetSession(id string) (*Session, error) {
	var s Session
	var expires string
	err := db.QueryRow(`SELECT id, user_id, expires_at FROM sessions WHERE id=?`, id).
		Scan(&s.ID, &s.UserID, &expires)
	if err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		_ = db.DeleteSession(id)
		return nil, err
	}
	if time.Now().UTC().After(exp) {
		_ = db.DeleteSession(id)
		return nil, sql.ErrNoRows
	}
	s.ExpiresAt = exp
	_, _ = db.Exec(`UPDATE sessions SET last_seen_at=datetime('now') WHERE id=?`, id)
	return &s, nil
}

func (db *DB) DeleteSession(id string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (db *DB) DeleteSessionsForUserExcept(userID int64, keepID string) error {
	if keepID == "" {
		_, err := db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
		return err
	}
	_, err := db.Exec(`DELETE FROM sessions WHERE user_id=? AND id<>?`, userID, keepID)
	return err
}
