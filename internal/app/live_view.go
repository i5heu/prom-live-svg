package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/http"
	"strings"
	"time"

	"prom-live-svg/internal/charts"
	"prom-live-svg/internal/config"
)

//go:embed templates/live_chart.go.html
var liveChartTemplateRaw string

var parsedLiveChartTemplate = htmltemplate.Must(htmltemplate.New("live_chart").Parse(liveChartTemplateRaw))

type liveChartPageData struct {
	Title                     string
	InitialDocumentsJSON      htmltemplate.JS
	GenerationIntervalSeconds int64
}

func (a *App) handleLiveChartRequest(w http.ResponseWriter, r *http.Request) { // A
	chartName, timestamp, ok := parseLiveChartPath(r.URL.Path)
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

	intervalSeconds, err := a.generationIntervalSeconds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	initialDocuments, err := a.loadInitialLiveDocuments(r.Context(), chart, requestedAt, intervalSeconds)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		a.logger.Error("load initial live chart documents failed", "chart", chart.Name, "requested_at", requestedAt.Unix(), "err", err)
		http.Error(w, "failed to query chart data", http.StatusBadGateway)
		return
	}

	body, err := renderLiveChartPage(initialDocuments, intervalSeconds)
	if err != nil {
		http.Error(w, "failed to render live chart page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) loadInitialLiveDocuments(ctx context.Context, chart config.ChartConfig, requestedAt time.Time, intervalSeconds int64) ([]charts.Document, error) { // A
	interval := time.Duration(intervalSeconds) * time.Second
	requestedTimes := []time.Time{
		requestedAt.Add(-2 * interval),
		requestedAt.Add(-interval),
		requestedAt,
	}

	documents := make([]charts.Document, 0, len(requestedTimes))
	for _, ts := range requestedTimes {
		document, err := a.loadChartDocument(ctx, chart, ts)
		if err != nil {
			return nil, fmt.Errorf("load chart document at %d: %w", ts.Unix(), err)
		}
		documents = append(documents, document)
	}

	return documents, nil
}

func parseLiveChartPath(path string) (chart string, timestamp string, ok bool) { // A
	trimmed := strings.TrimPrefix(path, "/live/")
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		chart = strings.TrimSpace(parts[0])
		return chart, "", chart != ""
	case 2:
		chart = strings.TrimSpace(parts[0])
		timestamp = strings.TrimSpace(parts[1])
		if chart == "" || timestamp == "" {
			return "", "", false
		}
		return chart, timestamp, true
	default:
		return "", "", false
	}
}

func renderLiveChartPage(docs []charts.Document, generationIntervalSeconds int64) ([]byte, error) { // A
	if len(docs) == 0 {
		return nil, fmt.Errorf("at least one live chart document is required")
	}

	initialDocumentsJSON, err := json.Marshal(docs)
	if err != nil {
		return nil, fmt.Errorf("marshal initial document JSON: %w", err)
	}

	data := liveChartPageData{
		Title:                     docs[len(docs)-1].Title,
		InitialDocumentsJSON:      htmltemplate.JS(initialDocumentsJSON),
		GenerationIntervalSeconds: generationIntervalSeconds,
	}

	var buffer bytes.Buffer
	if err := parsedLiveChartTemplate.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("execute live chart template: %w", err)
	}

	return buffer.Bytes(), nil
}
