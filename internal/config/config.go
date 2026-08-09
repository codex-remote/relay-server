package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddr      = ":8080"
	DefaultMaxMessageBytes = 256 * 1024
	DefaultWriteQueueSize  = 128
	DefaultPingInterval    = 20 * time.Second
	DefaultShutdownTimeout = 10 * time.Second
)

type Config struct {
	ListenAddr      string
	MaxMessageBytes int64
	WriteQueueSize  int
	PingInterval    time.Duration
	ShutdownTimeout time.Duration
	LogLevel        string
}

func FromEnv() (Config, error) {
	config := Config{
		ListenAddr:      envOrDefault("RELAY_LISTEN_ADDR", DefaultListenAddr),
		MaxMessageBytes: DefaultMaxMessageBytes,
		WriteQueueSize:  DefaultWriteQueueSize,
		PingInterval:    DefaultPingInterval,
		ShutdownTimeout: DefaultShutdownTimeout,
		LogLevel:        envOrDefault("RELAY_LOG_LEVEL", "info"),
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
