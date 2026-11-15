// Package config loads runtime configuration from the environment and
// the root-only files under /etc/fo3 (db.env, master.key).
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

// Config is the process-wide configuration.
type Config struct {
	Addr        string // game API listen address, default 127.0.0.1:8080
	ShadowAddr  string // pre-cutover test address, default 127.0.0.1:18080
	MessengerAddr string // TCP messenger listen address, default :28002
	DBDSN       string // MariaDB DSN (from /etc/fo3/db.env)
	RedisAddr   string // default 127.0.0.1:6379
	RedisPass   string // aaPanel Redis requirepass (from /etc/fo3/redis.env)
	MasterKey   []byte // 32-byte AES-256 master key (from /etc/fo3/master.key)
	DebugToken  string // token for /debug/* (from /etc/fo3/debug.token, may be empty = debug off)
	PythonBase  string // fallback proxy target for unported domains, e.g. http://127.0.0.1:8081
}

// Load reads /etc/fo3 files plus FO3_* environment overrides.
func Load() (*Config, error) {
	c := &Config{
		Addr:          env("FO3_ADDR", "127.0.0.1:8080"),
		ShadowAddr:    env("FO3_SHADOW_ADDR", "127.0.0.1:18080"),
		MessengerAddr: env("FO3_MSG_ADDR", ":28002"),
		RedisAddr:     env("FO3_REDIS_ADDR", "127.0.0.1:6379"),
		PythonBase:    os.Getenv("FO3_PYTHON_BASE"),
	}
	if c.DBDSN = os.Getenv("FO3_DB_DSN"); c.DBDSN == "" {
		b, err := os.ReadFile("/etc/fo3/db.env")
		if err != nil {
			return nil, fmt.Errorf("read db.env: %w", err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(line, "FO3_DB_DSN="); ok {
				c.DBDSN = strings.TrimSpace(v)
			}
		}
	}
	kb, err := os.ReadFile("/etc/fo3/master.key")
	if err != nil {
		return nil, fmt.Errorf("read master.key: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(kb)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("master.key: want base64 of 32 bytes")
	}
	c.MasterKey = key
	if c.RedisPass = os.Getenv("FO3_REDIS_PASS"); c.RedisPass == "" {
		if b, err := os.ReadFile("/etc/fo3/redis.env"); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if v, ok := strings.CutPrefix(line, "FO3_REDIS_PASS="); ok {
					c.RedisPass = strings.TrimSpace(v)
				}
			}
		}
	}
	if b, err := os.ReadFile("/etc/fo3/debug.token"); err == nil {
		c.DebugToken = strings.TrimSpace(string(b))
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
