package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"prom-live-svg/internal/config"
)

type App struct {
	cfg    config.Config
	logger *slog.Logger
	server *http.Server
}

func Run(cfg config.Config) error { // H
	application, err := New(cfg)
	if err != nil {
		return err
	}

	return application.Run()
}

func New(cfg config.Config) (*App, error) { // H
	logger, err := newLogger(cfg.Service, cfg.Logging)
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}

	application := &App{
		cfg:    cfg,
		logger: logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", application.handleHealth)
	mux.HandleFunc("/readyz", application.handleReady)

	application.server = &http.Server{
		Addr:              cfg.HTTP.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout.Duration,
		ReadTimeout:       cfg.HTTP.ReadTimeout.Duration,
		WriteTimeout:      cfg.HTTP.WriteTimeout.Duration,
		IdleTimeout:       cfg.HTTP.IdleTimeout.Duration,
	}

	return application, nil
}

func (a *App) Run() error { // H
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	go func() {
		a.logger.Info("starting HTTP server", "listen_addr", a.cfg.HTTP.ListenAddr)
		err := a.server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen and serve: %w", err)
			return
		}

		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.HTTP.ShutdownTimeout.Duration)
		defer cancel()

		a.logger.Info("shutting down HTTP server")
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}

		return nil
	}
}

func newLogger(service config.ServiceConfig, logging config.LoggingConfig) (*slog.Logger, error) { // H
	var level slog.Level

	switch strings.ToLower(logging.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", logging.Level)
	}

	options := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch strings.ToLower(logging.Format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, options)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, options)
	default:
		return nil, fmt.Errorf("unsupported log format %q", logging.Format)
	}

	return slog.New(handler).With(
		"service", service.Name,
		"environment", service.Environment,
	), nil
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) { // H
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) handleReady(w http.ResponseWriter, _ *http.Request) { // H
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}
