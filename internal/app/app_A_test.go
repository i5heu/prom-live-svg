package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
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
	queries         []promapi.RangeQuery
	callCount       int
}

func TestHandleChartJSONUsesFixtureData(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)

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
	application := newTestApp(t, cfg, querier)

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

func TestHandleLiveChartPageUsesFixtureData(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/live/chrony_source_offset_by_source/1779896355", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if got, want := response.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
		t.Fatalf("unexpected content type: got %q want %q", got, want)
	}

	body := response.Body.String()
	if !strings.Contains(body, "Chrony source offset by source") {
		t.Fatalf("expected chart title in live view body, got %q", body)
	}
	if !strings.Contains(body, "const INITIAL_DOCUMENTS = ") {
		t.Fatalf("expected embedded initial documents in live view body, got %q", body)
	}
	if !strings.Contains(body, `"chart":"chrony_source_offset_by_source"`) {
		t.Fatalf("expected embedded chart name in live view body, got %q", body)
	}
	if !strings.Contains(body, `"end_at":1779896325`) {
		t.Fatalf("expected embedded oldest aligned timestamp in live view body, got %q", body)
	}
	if !strings.Contains(body, `"end_at":1779896340`) {
		t.Fatalf("expected embedded previous aligned timestamp in live view body, got %q", body)
	}
	if !strings.Contains(body, `"end_at":1779896355`) {
		t.Fatalf("expected embedded current aligned timestamp in live view body, got %q", body)
	}
	if !strings.Contains(body, "/charts/${encodeURIComponent(chart)}/${timestamp}.json") {
		t.Fatalf("expected live view to fetch timestamped JSON snapshots, got %q", body)
	}
	if !strings.Contains(body, "const DEBUG_LIVE_CHART = new URLSearchParams(window.location.search).get(\"debug\") === \"1\";") {
		t.Fatalf("expected live view to include debug toggle support, got %q", body)
	}
}

func TestHandleLiveChartPageWithoutTimestampUsesCurrentAlignedWindow(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)
	application.now = func() time.Time { return time.Unix(1779896361, 0).UTC() }

	waitCalled := false
	application.waitUntil = func(_ context.Context, _ time.Time) error {
		waitCalled = true
		return nil
	}

	request := httptest.NewRequest(http.MethodGet, "/live/chrony_packets_accepted", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
	if waitCalled {
		t.Fatal("did not expect waitUntil to be called for shorthand latest live chart requests")
	}

	body := response.Body.String()
	if !strings.Contains(body, `"end_at":1779896325`) {
		t.Fatalf("expected embedded oldest aligned timestamp in live view body, got %q", body)
	}
	if !strings.Contains(body, `"end_at":1779896340`) {
		t.Fatalf("expected embedded previous aligned timestamp in live view body, got %q", body)
	}
	if !strings.Contains(body, `"end_at":1779896355`) {
		t.Fatalf("expected embedded aligned timestamp in live view body, got %q", body)
	}
}

func TestHandleChartJSONWithoutTimestampUsesCurrentAlignedWindow(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)
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
	application := newTestApp(t, cfg, querier)

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
	application := newTestApp(t, cfg, querier)
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
	application := newTestApp(t, cfg, querier)

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
					Name:      "all_time_requests",
					Label:     "All time requests",
					Query:     "chrony_serverstats_ntp_packets_received_total - chrony_serverstats_ntp_packets_dropped_total",
					SeedQuery: "increase(chrony_serverstats_ntp_packets_received_total[365d]) - increase(chrony_serverstats_ntp_packets_dropped_total[365d])",
					Lookback:  config.Duration{Duration: 15 * time.Second},
					Step:      config.Duration{Duration: 15 * time.Second},
					Decimals:  0,
					Persist:   true,
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
	seedQuery := cfg.Charts[0].Stats[0].SeedQuery
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
			seedQuery: {
				Series: []promapi.Series{
					{
						Metric: map[string]string{"instance": "chrony.example:9123"},
						Values: []promapi.Sample{
							{Timestamp: time.Unix(1779896340, 0).UTC(), Value: 140000},
							{Timestamp: time.Unix(1779896355, 0).UTC(), Value: 140000},
						},
					},
				},
			},
		},
	}
	application := newTestApp(t, cfg, querier)

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
	if got, want := document.Stats[0].Formatted, "140000"; got != want {
		t.Fatalf("unexpected formatted total stat on first seeded response: got %q want %q", got, want)
	}
	if got, want := document.Stats[0].Decimals, 0; got != want {
		t.Fatalf("unexpected total stat decimals: got %d want %d", got, want)
	}
	if got, want := document.Stats[0].Unit, ""; got != want {
		t.Fatalf("unexpected total stat unit: got %q want %q", got, want)
	}
	if got, want := document.Stats[1].Formatted, "340.51 req/s"; got != want {
		t.Fatalf("unexpected formatted rate stat: got %q want %q", got, want)
	}
	if got, want := document.Stats[1].Decimals, 2; got != want {
		t.Fatalf("unexpected rate stat decimals: got %d want %d", got, want)
	}
	if got, want := document.Stats[1].Unit, "req/s"; got != want {
		t.Fatalf("unexpected rate stat unit: got %q want %q", got, want)
	}
	if got, want := querier.callCount, 4; got != want {
		t.Fatalf("unexpected querier call count after first response: got %d want %d", got, want)
	}

	querier.matricesByQuery[totalQuery] = promapi.Matrix{Series: []promapi.Series{{
		Metric: map[string]string{"instance": "chrony.example:9123"},
		Values: []promapi.Sample{
			{Timestamp: time.Unix(1779896355, 0).UTC(), Value: 145678},
			{Timestamp: time.Unix(1779896370, 0).UTC(), Value: 145686},
		},
	}}}
	querier.matricesByQuery[backgroundQuery] = promapi.Matrix{Series: []promapi.Series{{
		Metric: map[string]string{"instance": "chrony.example:9123"},
		Values: []promapi.Sample{
			{Timestamp: time.Unix(1779896070, 0).UTC(), Value: 320.1},
			{Timestamp: time.Unix(1779896220, 0).UTC(), Value: 336.2},
			{Timestamp: time.Unix(1779896370, 0).UTC(), Value: 341.75},
		},
	}}}

	secondRequest := httptest.NewRequest(http.MethodGet, "/charts/chrony_requests_summary/1779896370.json", nil)
	secondResponse := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(secondResponse, secondRequest)

	if got, want := secondResponse.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected second status: got %d want %d", got, want)
	}

	var secondDocument charts.Document
	if err := json.Unmarshal(secondResponse.Body.Bytes(), &secondDocument); err != nil {
		t.Fatalf("decode second response JSON: %v", err)
	}
	if got, want := secondDocument.Stats[0].Formatted, "140008"; got != want {
		t.Fatalf("unexpected formatted total stat on second response: got %q want %q", got, want)
	}
	if got, want := querier.callCount, 7; got != want {
		t.Fatalf("unexpected querier call count after second response: got %d want %d", got, want)
	}
	if got, want := querier.queries[5].Start.Unix(), int64(1779896355); got != want {
		t.Fatalf("unexpected persistent delta query start: got %d want %d", got, want)
	}
	if got, want := querier.queries[5].End.Unix(), int64(1779896370); got != want {
		t.Fatalf("unexpected persistent delta query end: got %d want %d", got, want)
	}

	thirdRequest := httptest.NewRequest(http.MethodGet, "/charts/chrony_requests_summary/1779896355.json", nil)
	thirdResponse := httptest.NewRecorder()
	application.server.Handler.ServeHTTP(thirdResponse, thirdRequest)

	if got, want := thirdResponse.Code, http.StatusOK; got != want {
		t.Fatalf("unexpected third status: got %d want %d", got, want)
	}

	var thirdDocument charts.Document
	if err := json.Unmarshal(thirdResponse.Body.Bytes(), &thirdDocument); err != nil {
		t.Fatalf("decode third response JSON: %v", err)
	}
	if got, want := thirdDocument.Stats[0].Formatted, "140000"; got != want {
		t.Fatalf("unexpected formatted total stat on reconstructed historical response: got %q want %q", got, want)
	}
	if got, want := querier.callCount, 10; got != want {
		t.Fatalf("unexpected querier call count after third response: got %d want %d", got, want)
	}
	if got, want := querier.queries[8].Start.Unix(), int64(1779896355); got != want {
		t.Fatalf("unexpected persistent history query start: got %d want %d", got, want)
	}
	if got, want := querier.queries[8].End.Unix(), int64(1779896370); got != want {
		t.Fatalf("unexpected persistent history query end: got %d want %d", got, want)
	}
}

func TestHandleChartReturnsNotFoundForUnknownChart(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)

	request := httptest.NewRequest(http.MethodGet, "/charts/unknown/1779896355.json", nil)
	response := httptest.NewRecorder()

	application.server.Handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusNotFound; got != want {
		t.Fatalf("unexpected status: got %d want %d", got, want)
	}
}

func TestNewAppServerBaseContextUsesServeContext(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	querier := newFixtureQuerier(t, fixtureDatasetPath())
	application := newTestApp(t, cfg, querier)

	expectedCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	application.serveContext = expectedCtx

	baseContext := application.server.BaseContext(testListener{})
	if baseContext != expectedCtx {
		t.Fatal("expected server BaseContext to return the application's serve context")
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

func newTestApp(t *testing.T, cfg config.Config, querier *fixtureQuerier) *App { // A
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	application := newApp(cfg, logger, querier)
	if hasPersistentStats(cfg) {
		cfg.Storage.DataDir = t.TempDir()
		store, err := newRequestStatStore(cfg.Storage.DataDir)
		if err != nil {
			t.Fatalf("create request stat store: %v", err)
		}
		application.requestStats = store
	}
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
	f.queries = append(f.queries, query)

	matrix, ok := f.matricesByQuery[strings.TrimSpace(query.Query)]
	if !ok {
		return promapi.Matrix{}, context.DeadlineExceeded
	}

	return matrix, nil
}

type testListener struct{}

func (testListener) Accept() (net.Conn, error) { // A
	return nil, io.EOF
}

func (testListener) Close() error { // A
	return nil
}

func (testListener) Addr() net.Addr { // A
	return testAddr("test-listener")
}

type testAddr string

func (a testAddr) Network() string { // A
	return string(a)
}

func (a testAddr) String() string { // A
	return string(a)
}
