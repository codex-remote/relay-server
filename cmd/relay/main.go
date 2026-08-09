package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ai-coding-remote/relay-server/internal/config"
	"github.com/ai-coding-remote/relay-server/internal/server"
)

var version = "dev"

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
	relay := server.New(base, logger)
	httpServer := &http.Server{
		Addr:              base.ListenAddr,
		Handler:           relay.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errorsChannel := make(chan error, 1)
	go func() {
		logger.Info("Relay listening", "address", base.ListenAddr, "auth", "disabled")
		errorsChannel <- httpServer.ListenAndServe()
	}()

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

	relay.CloseConnections("Relay is shutting down")
	shutdownContext, cancel := context.WithTimeout(context.Background(), base.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		logger.Error("Relay shutdown failed", "error", err)
		return 1
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
