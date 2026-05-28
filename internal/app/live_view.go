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

//go:embed templates/live_mixed_chart.go.html
var liveMixedChartTemplateRaw string

var parsedLiveChartTemplate = htmltemplate.Must(htmltemplate.New("live_chart").Parse(liveChartTemplateRaw))
var parsedLiveMixedChartTemplate = htmltemplate.Must(htmltemplate.New("live_mixed_chart").Parse(liveMixedChartTemplateRaw))

type liveChartPageData struct {
	Title                     string
	InitialDocumentsJSON      htmltemplate.JS
	GenerationIntervalSeconds int64
	StaticSVGSnippet          string
	LiveIFrameSnippet         string
	LiveScriptSnippet         string
}

type liveMixedSnapshot struct {
	RequestedAt int64             `json:"requested_at"`
	Documents   []charts.Document `json:"documents"`
}

type liveMixedChartPageData struct {
	Title                     string
	MixedChartName            string
	MixedWidth                int
	MixedHeight               int
	InitialSnapshotsJSON      htmltemplate.JS
	GenerationIntervalSeconds int64
	StaticSVGSnippet          string
	LiveIFrameSnippet         string
	LiveScriptSnippet         string
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

	baseURL := requestBaseURL(r)
	body, err := renderLiveChartPage(chart.Name, initialDocuments, intervalSeconds, buildStaticSVGSnippet(baseURL, chart.Title, "/charts/"+chart.Name+".svg"), buildLiveIFrameSnippet(baseURL, chart.Title, "/live/"+chart.Name+"?embed=1", initialDocuments[len(initialDocuments)-1].Height+24), buildLiveScriptSnippet(baseURL, chart.Title, "/live/"+chart.Name+"?embed=1", initialDocuments[len(initialDocuments)-1].Height+24))
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

func (a *App) handleLiveMixedChartRequest(w http.ResponseWriter, r *http.Request) { // A
	mixedChartName, timestamp, ok := parseLiveMixedChartPath(r.URL.Path)
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

	intervalSeconds, err := a.generationIntervalSeconds()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	initialSnapshots, err := a.loadInitialLiveMixedSnapshots(r.Context(), mc, requestedAt, intervalSeconds)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		a.logger.Error("load initial live mixed chart snapshots failed", "mixed_chart", mc.Name, "requested_at", requestedAt.Unix(), "err", err)
		http.Error(w, "failed to query mixed chart data", http.StatusBadGateway)
		return
	}

	baseURL := requestBaseURL(r)
	body, err := renderLiveMixedChartPage(mc, initialSnapshots, intervalSeconds, buildStaticSVGSnippet(baseURL, mc.Title, "/mixed/"+mc.Name+".svg"), buildLiveIFrameSnippet(baseURL, mc.Title, "/live/mixed/"+mc.Name+"?embed=1", mc.Height+24), buildLiveScriptSnippet(baseURL, mc.Title, "/live/mixed/"+mc.Name+"?embed=1", mc.Height+24))
	if err != nil {
		http.Error(w, "failed to render live mixed chart page", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (a *App) loadInitialLiveMixedSnapshots(ctx context.Context, mc config.MixedChartConfig, requestedAt time.Time, intervalSeconds int64) ([]liveMixedSnapshot, error) { // A
	interval := time.Duration(intervalSeconds) * time.Second
	requestedTimes := []time.Time{
		requestedAt.Add(-2 * interval),
		requestedAt.Add(-interval),
		requestedAt,
	}

	snapshots := make([]liveMixedSnapshot, 0, len(requestedTimes))
	for _, ts := range requestedTimes {
		documents, err := a.loadMixedChartDocuments(ctx, mc, ts)
		if err != nil {
			return nil, fmt.Errorf("load mixed chart documents at %d: %w", ts.Unix(), err)
		}
		snapshots = append(snapshots, liveMixedSnapshot{
			RequestedAt: ts.Unix(),
			Documents:   documents,
		})
	}

	return snapshots, nil
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

func parseLiveMixedChartPath(path string) (name string, timestamp string, ok bool) { // A
	trimmed := strings.TrimPrefix(path, "/live/mixed/")
	parts := strings.Split(trimmed, "/")
	switch len(parts) {
	case 1:
		name = strings.TrimSpace(parts[0])
		return name, "", name != ""
	case 2:
		name = strings.TrimSpace(parts[0])
		timestamp = strings.TrimSpace(parts[1])
		if name == "" || timestamp == "" {
			return "", "", false
		}
		return name, timestamp, true
	default:
		return "", "", false
	}
}

func renderLiveChartPage(chartName string, docs []charts.Document, generationIntervalSeconds int64, staticSVGSnippet string, liveIFrameSnippet string, liveScriptSnippet string) ([]byte, error) { // A
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
		StaticSVGSnippet:          staticSVGSnippet,
		LiveIFrameSnippet:         liveIFrameSnippet,
		LiveScriptSnippet:         liveScriptSnippet,
	}

	var buffer bytes.Buffer
	if err := parsedLiveChartTemplate.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("execute live chart template for %q: %w", chartName, err)
	}

	return buffer.Bytes(), nil
}

func renderLiveMixedChartPage(mc config.MixedChartConfig, snapshots []liveMixedSnapshot, generationIntervalSeconds int64, staticSVGSnippet string, liveIFrameSnippet string, liveScriptSnippet string) ([]byte, error) { // A
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("at least one live mixed chart snapshot is required")
	}

	initialSnapshotsJSON, err := json.Marshal(snapshots)
	if err != nil {
		return nil, fmt.Errorf("marshal initial mixed snapshot JSON: %w", err)
	}

	data := liveMixedChartPageData{
		Title:                     mc.Title,
		MixedChartName:            mc.Name,
		MixedWidth:                mc.Width,
		MixedHeight:               mc.Height,
		InitialSnapshotsJSON:      htmltemplate.JS(initialSnapshotsJSON),
		GenerationIntervalSeconds: generationIntervalSeconds,
		StaticSVGSnippet:          staticSVGSnippet,
		LiveIFrameSnippet:         liveIFrameSnippet,
		LiveScriptSnippet:         liveScriptSnippet,
	}

	var buffer bytes.Buffer
	if err := parsedLiveMixedChartTemplate.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("execute live mixed chart template for %q: %w", mc.Name, err)
	}

	return buffer.Bytes(), nil
}

func requestBaseURL(r *http.Request) string { // A
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host
}

func buildStaticSVGSnippet(baseURL string, title string, path string) string { // A
	return fmt.Sprintf("<img\n  src=%q\n  alt=%q\n  loading=%q\n  style=%q\n/>", baseURL+path, title, "lazy", "display:block;max-width:100%;height:auto;")
}

func buildLiveIFrameSnippet(baseURL string, title string, path string, height int) string { // A
	return fmt.Sprintf("<iframe\n  src=%q\n  title=%q\n  loading=%q\n  referrerpolicy=%q\n  style=%q\n></iframe>", baseURL+path, title, "lazy", "no-referrer", fmt.Sprintf("display:block;width:100%%;max-width:100%%;height:%dpx;border:0;overflow:hidden;", height))
}

func buildLiveScriptSnippet(baseURL string, title string, path string, height int) string { // A
	widgetID := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(title), " ", "-"), "/", "-"))
	return fmt.Sprintf("<div id=%q></div>\n<script>\n(() => {\n  const root = document.getElementById(%q);\n  if (!root) return;\n  const iframe = document.createElement(\"iframe\");\n  iframe.src = %q;\n  iframe.title = %q;\n  iframe.loading = \"lazy\";\n  iframe.referrerPolicy = \"no-referrer\";\n  iframe.style.display = \"block\";\n  iframe.style.width = \"100%%\";\n  iframe.style.maxWidth = \"100%%\";\n  iframe.style.height = %q;\n  iframe.style.border = \"0\";\n  iframe.style.overflow = \"hidden\";\n  root.appendChild(iframe);\n})();\n</script>", "prom-live-svg-"+widgetID, "prom-live-svg-"+widgetID, baseURL+path, title, fmt.Sprintf("%dpx", height))
}
