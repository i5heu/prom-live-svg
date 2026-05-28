package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"prom-live-svg/internal/charts"
	"prom-live-svg/internal/config"
	"prom-live-svg/internal/prometheus"
)

type chartQuerier interface {
	QueryChartRange(ctx context.Context, chart config.ChartConfig, end time.Time) (prometheus.Matrix, error)
	QueryRange(ctx context.Context, query prometheus.RangeQuery) (prometheus.Matrix, error)
}

type App struct {
	cfg               config.Config
	logger            *slog.Logger
	server            *http.Server
	querier           chartQuerier
	chartsByName      map[string]config.ChartConfig
	mixedChartsByName map[string]config.MixedChartConfig
	now               func() time.Time
	waitUntil         func(ctx context.Context, target time.Time) error
}

func Run(cfg config.Config) error { // H
	application, err := New(cfg)
	if err != nil {
		return err
	}

	return application.Run()
}

func New(cfg config.Config) (*App, error) { // A
	logger, err := newLogger(cfg.Service, cfg.Logging)
	if err != nil {
		return nil, fmt.Errorf("create logger: %w", err)
	}

	querier, err := prometheus.NewFromConfig(cfg.Prometheus)
	if err != nil {
		return nil, fmt.Errorf("create Prometheus client: %w", err)
	}

	return newApp(cfg, logger, querier), nil
}

func newApp(cfg config.Config, logger *slog.Logger, querier chartQuerier) *App { // A
	application := &App{
		cfg:               cfg,
		logger:            logger,
		querier:           querier,
		chartsByName:      indexCharts(cfg.Charts),
		mixedChartsByName: indexMixedCharts(cfg.MixedCharts),
		now: func() time.Time {
			return time.Now().UTC()
		},
	}
	application.waitUntil = application.waitForRequestedTimestamp

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", application.handleHealth)
	mux.HandleFunc("GET /readyz", application.handleReady)
	mux.HandleFunc("GET /charts/", application.handleChartRequest)
	mux.HandleFunc("GET /mixed/", application.handleMixedChartRequest)

	application.server = &http.Server{
		Addr:              cfg.HTTP.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout.Duration,
		ReadTimeout:       cfg.HTTP.ReadTimeout.Duration,
		WriteTimeout:      cfg.HTTP.WriteTimeout.Duration,
		IdleTimeout:       cfg.HTTP.IdleTimeout.Duration,
	}

	return application
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

func (a *App) handleChartRequest(w http.ResponseWriter, r *http.Request) { // A
	chartName, timestamp, format, ok := parseChartPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	chart, ok := a.chartsByName[chartName]
	if !ok {
		http.NotFound(w, r)
		return
	}

	requestedAt, err := a.resolveRequestedTimestamp(r.Context(), timestamp)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		statusCode := http.StatusBadRequest
		if errors.Is(err, errInvalidGenerationInterval) {
			statusCode = http.StatusInternalServerError
		}
		http.Error(w, err.Error(), statusCode)
		return
	}

	document, err := a.loadChartDocument(r.Context(), chart, requestedAt)
	if err != nil {
		a.logger.Error("load chart document failed", "chart", chart.Name, "requested_at", requestedAt.Unix(), "err", err)
		http.Error(w, "failed to query chart data", http.StatusBadGateway)
		return
	}

	switch format {
	case "json":
		body, err := charts.MarshalJSON(document)
		if err != nil {
			http.Error(w, "failed to encode chart JSON", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case "svg":
		body, err := charts.RenderSVG(document)
		if err != nil {
			http.Error(w, "failed to render chart SVG", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	default:
		http.Error(w, "unsupported chart format", http.StatusInternalServerError)
	}
}

var errInvalidGenerationInterval = errors.New("generation interval must be a positive whole number of seconds")

func (a *App) resolveRequestedTimestamp(ctx context.Context, raw string) (time.Time, error) { // A
	if strings.TrimSpace(raw) == "" {
		return a.currentAlignedTimestamp()
	}

	requestedAt, err := a.parseRequestedTimestamp(raw)
	if err != nil {
		return time.Time{}, err
	}

	if err := a.waitUntil(ctx, requestedAt); err != nil {
		return time.Time{}, err
	}

	return requestedAt, nil
}

func (a *App) parseRequestedTimestamp(raw string) (time.Time, error) { // A
	timestamp, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp must be a unix time in seconds")
	}

	intervalSeconds, err := a.generationIntervalSeconds()
	if err != nil {
		return time.Time{}, err
	}

	if timestamp%intervalSeconds != 0 {
		return time.Time{}, fmt.Errorf("timestamp must align to the %d-second generation interval", intervalSeconds)
	}

	return time.Unix(timestamp, 0).UTC(), nil
}

func (a *App) currentAlignedTimestamp() (time.Time, error) { // A
	intervalSeconds, err := a.generationIntervalSeconds()
	if err != nil {
		return time.Time{}, err
	}

	now := a.now().UTC().Unix()
	aligned := now - (now % intervalSeconds)
	return time.Unix(aligned, 0).UTC(), nil
}

func (a *App) generationIntervalSeconds() (int64, error) { // A
	interval := a.cfg.Generation.Interval.Duration
	if interval <= 0 || interval%time.Second != 0 {
		return 0, errInvalidGenerationInterval
	}
	return int64(interval / time.Second), nil
}

func parseChartPath(path string) (chart string, timestamp string, format string, ok bool) { // A
	trimmed := strings.TrimPrefix(path, "/charts/")
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		chart, format, ok = parseChartNameAndFormat(parts[0])
		if !ok {
			return "", "", "", false
		}
		return chart, "", format, true
	case 2:
		chart = strings.TrimSpace(parts[0])
		if chart == "" {
			return "", "", "", false
		}
		timestamp, format, ok = parseChartNameAndFormat(parts[1])
		if !ok || timestamp == "" {
			return "", "", "", false
		}
		return chart, timestamp, format, true
	default:
		return "", "", "", false
	}
}

func parseChartNameAndFormat(raw string) (name string, format string, ok bool) { // A
	trimmed := strings.TrimSpace(raw)
	switch {
	case strings.HasSuffix(trimmed, ".json"):
		name = strings.TrimSuffix(trimmed, ".json")
		return strings.TrimSpace(name), "json", strings.TrimSpace(name) != ""
	case strings.HasSuffix(trimmed, ".svg"):
		name = strings.TrimSuffix(trimmed, ".svg")
		return strings.TrimSpace(name), "svg", strings.TrimSpace(name) != ""
	default:
		return "", "", false
	}
}

func (a *App) waitForRequestedTimestamp(ctx context.Context, target time.Time) error { // A
	now := a.now().UTC()
	if !target.After(now) {
		return nil
	}

	waitDuration := target.Sub(now)
	if waitDuration > a.cfg.Generation.Interval.Duration {
		return fmt.Errorf("timestamp is more than one generation interval in the future")
	}

	timer := time.NewTimer(waitDuration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) loadChartDocument(ctx context.Context, chart config.ChartConfig, requestedAt time.Time) (charts.Document, error) { // A
	matrix, err := a.querier.QueryChartRange(ctx, chart, requestedAt)
	if err != nil {
		return charts.Document{}, fmt.Errorf("query chart range: %w", err)
	}

	stats, err := a.queryChartStats(ctx, chart, requestedAt)
	if err != nil {
		return charts.Document{}, err
	}

	return charts.BuildDocumentWithStats(chart, requestedAt, matrix, stats), nil
}

func (a *App) queryChartStats(ctx context.Context, chart config.ChartConfig, requestedAt time.Time) ([]charts.Stat, error) { // A
	if len(chart.Stats) == 0 {
		return nil, nil
	}

	stats := make([]charts.Stat, 0, len(chart.Stats))
	for _, stat := range chart.Stats {
		matrix, err := a.querier.QueryRange(ctx, prometheus.RangeQuery{
			Query: stat.Query,
			Start: requestedAt.Add(-stat.Lookback.Duration),
			End:   requestedAt,
			Step:  stat.Step.Duration,
		})
		if err != nil {
			return nil, fmt.Errorf("query chart stat %q: %w", stat.Name, err)
		}

		resolvedStat, err := charts.BuildStat(stat, matrix)
		if err != nil {
			return nil, fmt.Errorf("build chart stat %q: %w", stat.Name, err)
		}
		stats = append(stats, resolvedStat)
	}

	return stats, nil
}

func indexCharts(charts []config.ChartConfig) map[string]config.ChartConfig { // A
	indexed := make(map[string]config.ChartConfig, len(charts))
	for _, chart := range charts {
		indexed[chart.Name] = chart
	}
	return indexed
}

func indexMixedCharts(mixedCharts []config.MixedChartConfig) map[string]config.MixedChartConfig { // A
	indexed := make(map[string]config.MixedChartConfig, len(mixedCharts))
	for _, mc := range mixedCharts {
		indexed[mc.Name] = mc
	}
	return indexed
}

func (a *App) handleMixedChartRequest(w http.ResponseWriter, r *http.Request) { // A
	mixedChartName, timestamp, format, ok := parseMixedChartPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	mc, ok := a.mixedChartsByName[mixedChartName]
	if !ok {
		http.NotFound(w, r)
		return
	}

	requestedAt, err := a.resolveRequestedTimestamp(r.Context(), timestamp)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		statusCode := http.StatusBadRequest
		if errors.Is(err, errInvalidGenerationInterval) {
			statusCode = http.StatusInternalServerError
		}
		http.Error(w, err.Error(), statusCode)
		return
	}

	docs := make([]charts.Document, 0, len(mc.Charts))
	for _, chartName := range mc.Charts {
		chartName = strings.TrimSpace(chartName)
		chart, ok := a.chartsByName[chartName]
		if !ok {
			a.logger.Error("mixed chart references unknown chart", "mixed_chart", mc.Name, "chart", chartName)
			http.Error(w, "mixed chart references unknown chart", http.StatusInternalServerError)
			return
		}

		matrix, qErr := a.querier.QueryChartRange(r.Context(), chart, requestedAt)
		if qErr != nil {
			a.logger.Error("query chart range failed", "mixed_chart", mc.Name, "chart", chartName, "requested_at", requestedAt.Unix(), "err", qErr)
			http.Error(w, "failed to query chart data", http.StatusBadGateway)
			return
		}

		stats, statsErr := a.queryChartStats(r.Context(), chart, requestedAt)
		if statsErr != nil {
			a.logger.Error("query chart stats failed", "mixed_chart", mc.Name, "chart", chartName, "requested_at", requestedAt.Unix(), "err", statsErr)
			http.Error(w, "failed to query chart data", http.StatusBadGateway)
			return
		}

		docs = append(docs, charts.BuildDocumentWithStats(chart, requestedAt, matrix, stats))
	}

	switch format {
	case "json":
		body, err := json.Marshal(docs)
		if err != nil {
			http.Error(w, "failed to encode mixed chart JSON", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	case "svg":
		body, err := charts.RenderMixedSVG(mc.Title, mc.Width, mc.Height, docs)
		if err != nil {
			http.Error(w, "failed to render mixed chart SVG", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	default:
		http.Error(w, "unsupported chart format", http.StatusInternalServerError)
	}
}

func parseMixedChartPath(path string) (name string, timestamp string, format string, ok bool) { // A
	trimmed := strings.TrimPrefix(path, "/mixed/")
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		name, format, ok = parseChartNameAndFormat(parts[0])
		if !ok {
			return "", "", "", false
		}
		return name, "", format, true
	case 2:
		name = strings.TrimSpace(parts[0])
		if name == "" {
			return "", "", "", false
		}
		timestamp, format, ok = parseChartNameAndFormat(parts[1])
		if !ok || timestamp == "" {
			return "", "", "", false
		}
		return name, timestamp, format, true
	default:
		return "", "", "", false
	}
}
