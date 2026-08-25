package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/auth"
	"github.com/ai-coding-remote/relay-server/internal/config"
	runtimecore "github.com/ai-coding-remote/relay-server/internal/runtime"
	"github.com/ai-coding-remote/relay-server/internal/server"
)

var version = "0.0.1"

func main() {
	os.Exit(realMain(os.Args[1:]))
}

func realMain(arguments []string) int {
	base, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	flags := flag.NewFlagSet("relay", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	listenAddr := flags.String("listen", base.ListenAddr, "HTTP and WebSocket listen address")
	showVersion := flags.Bool("version", false, "print build version")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println(version)
		return 0
	}
	base.ListenAddr = *listenAddr
	if err := base.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	logger := newLogger(base.LogLevel)
	startupContext, startupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	store, err := runtimecore.OpenPostgres(startupContext, base.DatabaseURL)
	if err != nil {
		startupCancel()
		logger.Error("Open Runtime PostgreSQL", "error", err)
		return 1
	}
	defer store.Close()
	broker, err := runtimecore.OpenRedis(startupContext, base.RedisURL)
	startupCancel()
	if err != nil {
		logger.Error("Open Runtime Redis", "error", err)
		return 1
	}
	defer broker.Close()
	var authStore *auth.PostgresStore
	var authModule server.RuntimeAuthModule
	if base.AuthEnabled {
		startupContext, startupCancel = context.WithTimeout(context.Background(), 15*time.Second)
		authStore, err = auth.OpenPostgres(startupContext, base.DatabaseURL)
		startupCancel()
		if err != nil {
			logger.Error("Open Auth PostgreSQL", "error", err)
			return 1
		}
		defer authStore.Close()
		authService := auth.NewService(authStore, auth.Config{
			AccessTTL: base.AuthAccessTTL, RefreshTTL: base.AuthRefreshTTL, PairingTTL: base.AuthPairingTTL,
		})
		authModule = auth.NewModule(authService, auth.HTTPConfig{
			CookieName: base.AuthCookieName, CookieSecure: base.AuthCookieSecure,
		})
	}
	relay := server.NewWithRuntimeAndAuth(base, logger, store, broker, authModule)
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	httpServer := &http.Server{
		Addr:              base.ListenAddr,
		Handler:           relay.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return requestContext },
	}
	var controlServer *http.Server
	if relay.ControlHandler() != nil {
		controlServer = &http.Server{
			Addr: base.AuthControlAddr, Handler: relay.ControlHandler(),
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsChannel := make(chan error, 2)
	go func() {
		logger.Info("Relay listening", "address", base.ListenAddr, "runtime_auth", map[bool]string{true: "required", false: "disabled"}[base.AuthEnabled])
		errorsChannel <- httpServer.ListenAndServe()
	}()
	if controlServer != nil {
		go func() {
			logger.Info("Auth control listening", "address", base.AuthControlAddr)
			errorsChannel <- controlServer.ListenAndServe()
		}()
	}

	select {
	case err := <-errorsChannel:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Relay stopped", "error", err)
			return 1
		}
		return 0
	case <-ctx.Done():
		logger.Info("Relay shutting down")
	}

	cancelRequests()
	relay.CloseConnections("Relay is shutting down")
	if err := httpServer.Close(); err != nil {
		logger.Warn("Close Relay HTTP server", "error", err)
	}
	if controlServer != nil {
		if err := controlServer.Close(); err != nil {
			logger.Warn("Close Auth control HTTP server", "error", err)
		}
	}
	return 0
}

func newLogger(level string) *slog.Logger {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slogLevel}))
}
