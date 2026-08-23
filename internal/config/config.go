package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddr      = ":18765"
	DefaultMaxMessageBytes = 256 * 1024
	DefaultWriteQueueSize  = 128
	DefaultPingInterval    = 20 * time.Second
	DefaultShutdownTimeout = 10 * time.Second
	DefaultAuthControlAddr = "127.0.0.1:18776"
	DefaultAuthAccessTTL   = 15 * time.Minute
	DefaultAuthRefreshTTL  = 30 * 24 * time.Hour
	DefaultAuthPairingTTL  = 10 * time.Minute
)

type Config struct {
	ListenAddr       string
	MaxMessageBytes  int64
	WriteQueueSize   int
	PingInterval     time.Duration
	ShutdownTimeout  time.Duration
	LogLevel         string
	DatabaseURL      string
	RedisURL         string
	AllowedOrigin    string
	AuthEnabled      bool
	AuthControlAddr  string
	AuthAccessTTL    time.Duration
	AuthRefreshTTL   time.Duration
	AuthPairingTTL   time.Duration
	AuthCookieName   string
	AuthCookieSecure bool
}

func FromEnv() (Config, error) {
	config := Config{
		ListenAddr:      envOrDefault("RELAY_LISTEN_ADDR", DefaultListenAddr),
		MaxMessageBytes: DefaultMaxMessageBytes,
		WriteQueueSize:  DefaultWriteQueueSize,
		PingInterval:    DefaultPingInterval,
		ShutdownTimeout: DefaultShutdownTimeout,
		LogLevel:        envOrDefault("RELAY_LOG_LEVEL", "info"),
		DatabaseURL:     envOrDefault("RUNTIME_DATABASE_URL", "postgres://codexremote:codexremote@127.0.0.1:54329/codexremote?sslmode=disable"),
		RedisURL:        envOrDefault("RUNTIME_REDIS_URL", "redis://default:codexremote@127.0.0.1:63799/0"),
		AllowedOrigin:   envOrDefault("RUNTIME_ALLOWED_ORIGIN", "http://127.0.0.1:4173"),
		AuthEnabled:     true,
		AuthControlAddr: envOrDefault("AUTH_CONTROL_ADDR", DefaultAuthControlAddr),
		AuthAccessTTL:   DefaultAuthAccessTTL,
		AuthRefreshTTL:  DefaultAuthRefreshTTL,
		AuthPairingTTL:  DefaultAuthPairingTTL,
		AuthCookieName:  envOrDefault("AUTH_REFRESH_COOKIE_NAME", "codexremote_refresh"),
	}
	var err error
	if config.MaxMessageBytes, err = int64Env("RELAY_MAX_MESSAGE_BYTES", config.MaxMessageBytes); err != nil {
		return Config{}, err
	}
	if config.WriteQueueSize, err = intEnv("RELAY_WRITE_QUEUE_SIZE", config.WriteQueueSize); err != nil {
		return Config{}, err
	}
	if config.PingInterval, err = durationEnv("RELAY_PING_INTERVAL", config.PingInterval); err != nil {
		return Config{}, err
	}
	if config.ShutdownTimeout, err = durationEnv("RELAY_SHUTDOWN_TIMEOUT", config.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if config.AuthEnabled, err = boolEnv("AUTH_ENABLED", config.AuthEnabled); err != nil {
		return Config{}, err
	}
	if config.AuthCookieSecure, err = boolEnv("AUTH_COOKIE_SECURE", false); err != nil {
		return Config{}, err
	}
	if config.AuthAccessTTL, err = durationEnv("AUTH_ACCESS_TTL", config.AuthAccessTTL); err != nil {
		return Config{}, err
	}
	if config.AuthRefreshTTL, err = durationEnv("AUTH_REFRESH_TTL", config.AuthRefreshTTL); err != nil {
		return Config{}, err
	}
	if config.AuthPairingTTL, err = durationEnv("AUTH_PAIRING_TTL", config.AuthPairingTTL); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.ListenAddr) == "":
		return fmt.Errorf("listen address is required")
	case c.MaxMessageBytes < 1024:
		return fmt.Errorf("max message bytes must be at least 1024")
	case c.WriteQueueSize < 1:
		return fmt.Errorf("write queue size must be positive")
	case c.PingInterval <= 0:
		return fmt.Errorf("ping interval must be positive")
	case c.ShutdownTimeout <= 0:
		return fmt.Errorf("shutdown timeout must be positive")
	case c.AuthEnabled && strings.TrimSpace(c.AuthControlAddr) == "":
		return fmt.Errorf("auth control address is required when auth is enabled")
	case c.AuthEnabled && (c.AuthAccessTTL <= 0 || c.AuthRefreshTTL <= 0 || c.AuthPairingTTL <= 0):
		return fmt.Errorf("auth token lifetimes must be positive")
	case c.AuthEnabled && strings.TrimSpace(c.AuthCookieName) == "":
		return fmt.Errorf("auth refresh cookie name is required")
	}
	if c.AuthEnabled {
		host, _, err := net.SplitHostPort(c.AuthControlAddr)
		if err != nil {
			return fmt.Errorf("invalid auth control address: %w", err)
		}
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("auth control address must be loopback-only")
		}
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("unsupported log level %q", c.LogLevel)
	}
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func intEnv(name string, fallback int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func int64Env(name string, fallback int64) (int64, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func boolEnv(name string, fallback bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}
