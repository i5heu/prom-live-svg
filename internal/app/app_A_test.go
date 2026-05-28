package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prom-live-svg/internal/charts"
	"prom-live-svg/internal/config"
	promapi "prom-live-svg/internal/prometheus"

	"gopkg.in/yaml.v3"
)

type fixtureManifest struct {
	Entries []fixtureEntry `yaml:"entries"`
}

type fixtureEntry struct {
	Name         string `yaml:"name"`
	QueryFile    string `yaml:"query_file"`
	ResponseFile string `yaml:"response_file"`
}

type fixtureQuerier struct {
	matricesByQuery map[string]promapi.Matrix
	callCount       int
}

func TestHandleChartJSONUsesFixtureData(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_packets_accepted/1779896355.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "application/json; charset=utf-8"; got != want {
		t.Fatalf("unexpected content type: got %q want %q", got, want)
	}

	var document charts.Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}

	if got, want := document.Chart, "chrony_packets_accepted"; got != want {
		t.Fatalf("unexpected chart name: got %q want %q", got, want)
	}
	if got, want := document.EndAt, int64(1779896355); got != want {
		t.Fatalf("unexpected requested timestamp: got %d want %d", got, want)
	}
	if got, want := len(document.Series), 1; got != want {
		t.Fatalf("unexpected series count: got %d want %d", got, want)
	}
	if got, want := len(document.Series[0].Values), 61; got != want {
		t.Fatalf("unexpected point count: got %d want %d", got, want)
	}
	if got, want := document.Series[0].Metric["instance"], "chrony.example:9123"; got != want {
		t.Fatalf("unexpected instance label: got %q want %q", got, want)
	}
	if got, want := document.Series[0].Values[0].Value, 286.47407407407405; got != want {
		t.Fatalf("unexpected first value: got %v want %v", got, want)
	}
}

func TestHandleChartSVGUsesFixtureData(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_source_offset_by_source/1779896355.svg", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "image/svg+xml; charset=utf-8"; got != want {
		t.Fatalf("unexpected content type: got %q want %q", got, want)
	}

	body := response.Body.String()
	if !strings.Contains(body, "<svg") {
		t.Fatalf("expected SVG element in response body, got %q", body)
	}
	if !strings.Contains(body, "Chrony source offset by source") {
		t.Fatalf("expected chart title in SVG body, got %q", body)
	}
}

func TestHandleChartJSONWithoutTimestampUsesCurrentAlignedWindow(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)
	application.now = func() time.Time { return time.Unix(1779896361, 0).UTC() }

	waitCalled := false
	application.waitUntil = func(_ context.Context, _ time.Time) error {
		waitCalled = true
		return nil
	}

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_packets_accepted.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if waitCalled {
		t.Fatal("did not expect waitUntil to be called for shorthand latest chart requests")
	}

	var document charts.Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}
	if got, want := document.EndAt, int64(1779896355); got != want {
		t.Fatalf("unexpected aligned timestamp: got %d want %d", got, want)
	}
}

func TestHandleChartWaitsForNextAlignedTimestamp(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)

	now := time.Unix(1779896342, 0).UTC()
	application.now = func() time.Time { return now }

	waitCalled := false
	application.waitUntil = func(_ context.Context, target time.Time) error {
		waitCalled = true
		if got, want := target.Unix(), int64(1779896355); got != want {
			t.Fatalf("unexpected wait target: got %d want %d", got, want)
		}
		return nil
	}

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_packets_accepted/1779896355.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if !waitCalled {
		t.Fatal("expected waitUntil to be called for a valid future quarter-minute")
	}
	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
}

func TestHandleChartRejectsTimestampTooFarInFuture(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)
	application.now = func() time.Time { return time.Unix(1779896330, 0).UTC() }
	application.waitUntil = application.waitForRequestedTimestamp

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_packets_accepted/1779896385.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusBadRequest; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if querier.callCount != 0 {
		t.Fatalf("expected querier not to be called, got %d calls", querier.callCount)
	}
}

func TestHandleChartRejectsUnalignedTimestamp(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_packets_accepted/1779896354.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusBadRequest; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if querier.callCount != 0 {
		t.Fatalf("expected querier not to be called, got %d calls", querier.callCount)
	}
}

func TestHandleChartJSONIncludesStats(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	cfg.Charts = []config.ChartConfig{
		{
			Name:     "chrony_requests_summary",
			Title:    "Chrony requests",
			Query:    "rate(chrony_serverstats_ntp_packets_received_total[5m]) - rate(chrony_serverstats_ntp_packets_dropped_total[5m])",
			Width:    800,
			Height:   320,
			Lookback: config.Duration{Duration: 5 * time.Minute},
			Step:     config.Duration{Duration: 15 * time.Second},
			Stats: []config.ChartStatConfig{
				{
					Name:     "all_time_requests",
					Label:    "All time requests",
					Query:    "chrony_serverstats_ntp_packets_received_total - chrony_serverstats_ntp_packets_dropped_total",
					Lookback: config.Duration{Duration: 15 * time.Second},
					Step:     config.Duration{Duration: 15 * time.Second},
					Decimals: 0,
				},
				{
					Name:     "requests_per_second",
					Label:    "Req/s",
					Query:    "rate(chrony_serverstats_ntp_packets_received_total[5m]) - rate(chrony_serverstats_ntp_packets_dropped_total[5m])",
					Lookback: config.Duration{Duration: 15 * time.Second},
					Step:     config.Duration{Duration: 15 * time.Second},
					Decimals: 2,
					Unit:     "req/s",
				},
			},
		},
	}

	backgroundQuery := cfg.Charts[0].Query
	totalQuery := cfg.Charts[0].Stats[0].Query
	querier := &fixtureQuerier{
		matricesByQuery: map[string]promapi.Matrix{
			backgroundQuery: {
				Series: []promapi.Series{
					{
						Metric: map[string]string{"instance": "chrony.example:9123"},
						Values: []promapi.Sample{
							{Timestamp: time.Unix(1779896065, 0).UTC(), Value: 318.2},
							{Timestamp: time.Unix(1779896215, 0).UTC(), Value: 333.4},
							{Timestamp: time.Unix(1779896355, 0).UTC(), Value: 340.51},
						},
					},
				},
			},
			totalQuery: {
				Series: []promapi.Series{
					{
						Metric: map[string]string{"instance": "chrony.example:9123"},
						Values: []promapi.Sample{
							{Timestamp: time.Unix(1779896340, 0).UTC(), Value: 145670},
							{Timestamp: time.Unix(1779896355, 0).UTC(), Value: 145678},
						},
					},
				},
			},
		},
	}
	application := newTestApp(cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/chrony_requests_summary/1779896355.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}

	var document charts.Document
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode response JSON: %v", err)
	}

	if got, want := len(document.Stats), 2; got != want {
		t.Fatalf("unexpected stat count: got %d want %d", got, want)
	}
	if got, want := document.Stats[0].Formatted, "145678"; got != want {
		t.Fatalf("unexpected formatted total stat: got %q want %q", got, want)
	}
	if got, want := document.Stats[1].Formatted, "340.51 req/s"; got != want {
		t.Fatalf("unexpected formatted rate stat: got %q want %q", got, want)
	}
	if got, want := querier.callCount, 3; got != want {
		t.Fatalf("unexpected querier call count: got %d want %d", got, want)
	}
}

func TestHandleChartReturnsNotFoundForUnknownChart(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/unknown/1779896355.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
}

func loadFixtureConfig(t *testing.T) config.Config { // A
	t.Helper()

	cfg, err := config.Load(filepath.Join(repositoryRootFromPackage(), "testdata", "configs", "chrony-fixtures.yaml"))
	if err != nil {
		t.Fatalf("load fixture config: %v", err)
	}

	return cfg
}

func fixtureDatasetPath() string { // A
	return filepath.Join(repositoryRootFromPackage(), "testdata", "prometheus", "chrony")
}

func repositoryRootFromPackage() string { // A
	return filepath.Join("..", "..")
}

func newTestApp(cfg config.Config, querier *fixtureQuerier) *App { // A
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	application := newApp(cfg, logger, querier)
	application.now = func() time.Time { return time.Unix(1779896355, 0).UTC() }
	application.waitUntil = func(_ context.Context, _ time.Time) error { return nil }
	return application
}

func newFixtureQuerier(t *testing.T, datasetPath string) *fixtureQuerier { // A
	t.Helper()

	manifestBytes, err := os.ReadFile(filepath.Join(datasetPath, "fixtures.yaml"))
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}

	var manifest fixtureManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode fixture manifest: %v", err)
	}

	matricesByQuery := make(map[string]promapi.Matrix, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		queryBytes, err := os.ReadFile(filepath.Join(datasetPath, entry.QueryFile))
		if err != nil {
			t.Fatalf("read query file %q: %v", entry.QueryFile, err)
		}

		responseBytes, err := os.ReadFile(filepath.Join(datasetPath, entry.ResponseFile))
		if err != nil {
			t.Fatalf("read response file %q: %v", entry.ResponseFile, err)
		}

		var payload struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string           `json:"resultType"`
				Result     []promapi.Series `json:"result"`
			} `json:"data"`
		}
		if err := json.Unmarshal(responseBytes, &payload); err != nil {
			t.Fatalf("decode response file %q: %v", entry.ResponseFile, err)
		}
		if payload.Status != "success" {
			t.Fatalf("fixture response %q has unexpected status %q", entry.ResponseFile, payload.Status)
		}
		if payload.Data.ResultType != "matrix" {
			t.Fatalf("fixture response %q has unexpected result type %q", entry.ResponseFile, payload.Data.ResultType)
		}

		matricesByQuery[strings.TrimSpace(string(queryBytes))] = promapi.Matrix{Series: payload.Data.Result}
	}

	return &fixtureQuerier{matricesByQuery: matricesByQuery}
}

func (f *fixtureQuerier) QueryChartRange(_ context.Context, chart config.ChartConfig, _ time.Time) (promapi.Matrix, error) { // A
	return f.QueryRange(context.Background(), promapi.RangeQuery{Query: chart.Query})
}

func (f *fixtureQuerier) QueryRange(_ context.Context, query promapi.RangeQuery) (promapi.Matrix, error) { // A
	f.callCount++

	matrix, ok := f.matricesByQuery[strings.TrimSpace(query.Query)]
	if !ok {
		return promapi.Matrix{}, context.DeadlineExceeded
	}

	return matrix, nil
}
