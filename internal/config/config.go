// Package config loads server configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr     string
	DataDir        string
	DBPath         string
	DBMaxReadConns int
	BaseURL        string
	AllowSignup    bool
	CookieSecure   bool
	SessionTTL     time.Duration
	ResetTTL       time.Duration

	SMTPHost     string
	SMTPPort     string
	SMTPUser     string
	SMTPPass     string
	SMTPFrom     string
	SMTPStartTLS bool

	TrustProxyHeaders bool
}

func getEnv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func Load() Config {
	dataDir := getEnv("DATA_DIR", "/data")
	cfg := Config{
		ListenAddr:        getEnv("LISTEN_ADDR", ":8080"),
		DataDir:           dataDir,
		DBPath:            getEnv("DB_PATH", strings.TrimRight(dataDir, "/")+"/koserver.db"),
		DBMaxReadConns:    getInt("DB_MAX_READ_CONNS", 10),
		BaseURL:           strings.TrimRight(getEnv("BASE_URL", "http://localhost:8080"), "/"),
		AllowSignup:       getBool("ALLOW_SIGNUP", true),
		CookieSecure:      getBool("COOKIE_SECURE", false),
		SessionTTL:        30 * 24 * time.Hour,
		ResetTTL:          1 * time.Hour,
		SMTPHost:          getEnv("SMTP_HOST", ""),
		SMTPPort:          getEnv("SMTP_PORT", "587"),
		SMTPUser:          getEnv("SMTP_USER", ""),
		SMTPPass:          getEnv("SMTP_PASS", ""),
		SMTPFrom:          getEnv("SMTP_FROM", ""),
		SMTPStartTLS:      getBool("SMTP_STARTTLS", true),
		TrustProxyHeaders: getBool("TRUST_PROXY_HEADERS", false),
	}
	return cfg
}
