// Package store owns the MariaDB and Redis connections and the
// encrypted per-user state documents.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

//go:embed schema.sql
var schemaSQL string

// Store bundles the connections and the data cipher.
type Store struct {
	DB     *sql.DB
	Redis  *redis.Client
	Cipher *Cipher

	// stateCache holds decrypted state plaintext per user/domain for
	// stateCacheTTL: LoadState otherwise costs a MariaDB round trip
	// plus an AES-GCM decrypt plus a full JSON decode on every game
	// request. SaveState writes through, so within this process the
	// cache is never stale; the TTL only bounds writes made by
	// external tools (cmd/* helpers) while the server is running.
	stateMu    sync.Mutex
	stateCache map[string]stateCacheEntry
}

type stateCacheEntry struct {
	plain []byte
	exp   time.Time
}

const stateCacheTTL = 60 * time.Second

// Open connects to MariaDB and Redis and verifies both.
func Open(ctx context.Context, dsn, redisAddr, redisPass string, cipher *Cipher) (*Store, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(48)
	db.SetMaxIdleConns(16)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("mariadb ping: %w", err)
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr, Password: redisPass,
		PoolSize: 48, MinIdleConns: 8,
		DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second,
		WriteTimeout: 2 * time.Second, PoolTimeout: 3 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &Store{DB: db, Redis: rdb, Cipher: cipher,
		stateCache: map[string]stateCacheEntry{}}, nil
}

// Migrate applies the embedded schema (idempotent CREATE IF NOT EXISTS)
// plus additive column migrations for tables created by earlier phases.
func (s *Store) Migrate(ctx context.Context) error {
	for _, stmt := range splitSQL(schemaSQL) {
		if stmt == "" {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	extra := []string{
		"ALTER TABLE users ADD COLUMN IF NOT EXISTS legacy_hash VARCHAR(64) NULL AFTER pass_hash",
		"ALTER TABLE club_members ADD COLUMN IF NOT EXISTS joined_at DOUBLE NOT NULL DEFAULT 0",
		"ALTER TABLE friends ADD COLUMN IF NOT EXISTS friend_name VARCHAR(64) NULL",
		"ALTER TABLE friends ADD COLUMN IF NOT EXISTS created_at VARCHAR(32) NULL",
		"ALTER TABLE club_requests ADD COLUMN IF NOT EXISTS requested_at DOUBLE NOT NULL DEFAULT 0",
	}
	for _, stmt := range extra {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate extra: %w", err)
		}
	}
	return nil
}

func splitSQL(s string) []string {
	var out []string
	cur := ""
	for _, line := range splitLines(s) {
		trimmed := trimSpace(line)
		if len(trimmed) >= 2 && trimmed[:2] == "--" {
			continue
		}
		cur += line + "\n"
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ';' {
			out = append(out, cur)
			cur = ""
		}
	}
	if trimSpace(cur) != "" {
		out = append(out, cur)
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func stateCacheKey(userID int64, domain string) string {
	return fmt.Sprintf("%d/%s", userID, domain)
}

// LoadState decrypts and JSON-decodes one state domain of a user.
// found=false when the user has no document for the domain yet.
// Decrypted plaintext is cached (see Store.stateCache); every call
// still unmarshals into a fresh value, so callers keep the same
// mutate-then-SaveState semantics as an uncached load.
func (s *Store) LoadState(ctx context.Context, userID int64, domain string, out any) (bool, error) {
	key := stateCacheKey(userID, domain)
	s.stateMu.Lock()
	if e, ok := s.stateCache[key]; ok && time.Now().Before(e.exp) {
		plain := e.plain
		s.stateMu.Unlock()
		if err := json.Unmarshal(plain, out); err != nil {
			return false, fmt.Errorf("decode state %s: %w", domain, err)
		}
		return true, nil
	}
	s.stateMu.Unlock()
	var doc string
	err := s.DB.QueryRowContext(ctx,
		"SELECT doc FROM user_state WHERE user_id = ? AND domain = ?", userID, domain).Scan(&doc)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	plain, err := s.Cipher.Decrypt(doc)
	if err != nil {
		return false, fmt.Errorf("decrypt state %s: %w", domain, err)
	}
	s.stateMu.Lock()
	s.stateCache[key] = stateCacheEntry{plain: plain, exp: time.Now().Add(stateCacheTTL)}
	s.stateMu.Unlock()
	if err := json.Unmarshal(plain, out); err != nil {
		return false, fmt.Errorf("decode state %s: %w", domain, err)
	}
	return true, nil
}

// SaveState JSON-encodes and encrypts one state domain of a user.
func (s *Store) SaveState(ctx context.Context, userID int64, domain string, v any) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return err
	}
	enc, err := s.Cipher.Encrypt(plain)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO user_state (user_id, domain, doc) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE doc = VALUES(doc)`, userID, domain, enc)
	if err == nil {
		s.stateMu.Lock()
		s.stateCache[stateCacheKey(userID, domain)] = stateCacheEntry{
			plain: plain, exp: time.Now().Add(stateCacheTTL)}
		s.stateMu.Unlock()
	}
	return err
}
