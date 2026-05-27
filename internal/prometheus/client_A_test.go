package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"prom-live-svg/internal/config"

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

type fixtureRequest struct {
	Query string
	Start string
	End   string
	Step  string
}

type fixtureServer struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []fixtureRequest
}

func TestQueryChartRangeUsesFixtureMock(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	server := newFixtureServer(t, fixtureDatasetPath(t))
	defer server.Close()

	cfg.Prometheus.BaseURL = server.URL()
	client, err := NewFromConfig(cfg.Prometheus)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	end := time.Unix(1779896355, 0).UTC()
	testCases := []struct {
		chartName       string
		wantSeries      int
		wantSamples     int
		wantMetricKey   string
		wantMetricValue string
		wantFirstValue  float64
		wantLastValue   float64
		valueTolerance  float64
	}{
		{
			chartName:       "chrony_packets_accepted",
			wantSeries:      1,
			wantSamples:     61,
			wantMetricKey:   "instance",
			wantMetricValue: "chrony.example:9123",
			wantFirstValue:  286.47407407407405,
			wantLastValue:   340.5111111111112,
			valueTolerance:  1e-12,
		},
		{
			chartName:       "chrony_timestamp_span",
			wantSeries:      1,
			wantSamples:     61,
			wantMetricKey:   "instance",
			wantMetricValue: "chrony.example:9123",
			wantFirstValue:  79750,
			wantLastValue:   79947,
			valueTolerance:  0,
		},
		{
			chartName:       "chrony_source_offset_by_source",
			wantSeries:      12,
			wantSamples:     61,
			wantMetricKey:   "source_name",
			wantMetricValue: "source-01.example",
			wantFirstValue:  0.000003174870244038175,
			wantLastValue:   -0.00016913599392864853,
			valueTolerance:  1e-15,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.chartName, func(t *testing.T) {
			chart := findChart(t, cfg, tc.chartName)

			result, err := client.QueryChartRange(context.Background(), chart, end)
			if err != nil {
				t.Fatalf("query chart range: %v", err)
			}

			if got := len(result.Series); got != tc.wantSeries {
				t.Fatalf("unexpected series count: got %d want %d", got, tc.wantSeries)
			}

			firstSeries := result.Series[0]
			if got := firstSeries.Metric[tc.wantMetricKey]; got != tc.wantMetricValue {
				t.Fatalf("unexpected metric %q: got %q want %q", tc.wantMetricKey, got, tc.wantMetricValue)
			}
			if got := len(firstSeries.Values); got != tc.wantSamples {
				t.Fatalf("unexpected sample count: got %d want %d", got, tc.wantSamples)
			}

			assertFloatApprox(t, firstSeries.Values[0].Value, tc.wantFirstValue, tc.valueTolerance)
			assertFloatApprox(t, firstSeries.Values[len(firstSeries.Values)-1].Value, tc.wantLastValue, tc.valueTolerance)
		})
	}
}

func TestQueryChartRangeBuildsExpectedRequestWindow(t *testing.T) { // A
	t.Parallel()

	cfg := loadFixtureConfig(t)
	server := newFixtureServer(t, fixtureDatasetPath(t))
	defer server.Close()

	cfg.Prometheus.BaseURL = server.URL()
	client, err := NewFromConfig(cfg.Prometheus)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	chart := findChart(t, cfg, "chrony_packets_accepted")
	end := time.Unix(1779896355, 0).UTC()

	if _, err := client.QueryChartRange(context.Background(), chart, end); err != nil {
		t.Fatalf("query chart range: %v", err)
	}

	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("unexpected request count: got %d want 1", len(requests))
	}

	request := requests[0]
	if got, want := request.Query, strings.TrimSpace(chart.Query); got != want {
		t.Fatalf("unexpected query: got %q want %q", got, want)
	}
	if got, want := request.Start, "1779895455"; got != want {
		t.Fatalf("unexpected start: got %q want %q", got, want)
	}
	if got, want := request.End, "1779896355"; got != want {
		t.Fatalf("unexpected end: got %q want %q", got, want)
	}
	if got, want := request.Step, "15s"; got != want {
		t.Fatalf("unexpected step: got %q want %q", got, want)
	}
}

func TestQueryRangeReturnsPrometheusErrors(t *testing.T) { // A
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","errorType":"bad_data","error":"invalid query"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = client.QueryRange(context.Background(), RangeQuery{
		Query: "up",
		Start: time.Unix(100, 0).UTC(),
		End:   time.Unix(115, 0).UTC(),
		Step:  15 * time.Second,
	})
	if err == nil {
		t.Fatal("expected Prometheus error")
	}
	if !strings.Contains(err.Error(), "invalid query") {
		t.Fatalf("expected invalid query error, got %v", err)
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

func fixtureDatasetPath(t *testing.T) string { // A
	t.Helper()
	return filepath.Join(repositoryRootFromPackage(), "testdata", "prometheus", "chrony")
}

func repositoryRootFromPackage() string { // A
	return filepath.Join("..", "..")
}

func findChart(t *testing.T, cfg config.Config, name string) config.ChartConfig { // A
	t.Helper()

	for _, chart := range cfg.Charts {
		if chart.Name == name {
			return chart
		}
	}

	t.Fatalf("chart %q not found", name)
	return config.ChartConfig{}
}

func assertFloatApprox(t *testing.T, got float64, want float64, tolerance float64) { // A
	t.Helper()

	if math.Abs(got-want) > tolerance {
		t.Fatalf("unexpected value: got %.18f want %.18f (tolerance %.18f)", got, want, tolerance)
	}
}

func newFixtureServer(t *testing.T, datasetPath string) *fixtureServer { // A
	t.Helper()

	manifestBytes, err := os.ReadFile(filepath.Join(datasetPath, "fixtures.yaml"))
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}

	var manifest fixtureManifest
	if err := yaml.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("decode fixture manifest: %v", err)
	}

	responsesByQuery := make(map[string][]byte, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		queryBytes, err := os.ReadFile(filepath.Join(datasetPath, entry.QueryFile))
		if err != nil {
			t.Fatalf("read query file %q: %v", entry.QueryFile, err)
		}
		responseBytes, err := os.ReadFile(filepath.Join(datasetPath, entry.ResponseFile))
		if err != nil {
			t.Fatalf("read response file %q: %v", entry.ResponseFile, err)
		}

		responsesByQuery[strings.TrimSpace(string(queryBytes))] = responseBytes
	}

	fixtureServer := &fixtureServer{}
	fixtureServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" {
			http.NotFound(w, r)
			return
		}

		request := fixtureRequest{
			Query: strings.TrimSpace(r.URL.Query().Get("query")),
			Start: r.URL.Query().Get("start"),
			End:   r.URL.Query().Get("end"),
			Step:  r.URL.Query().Get("step"),
		}

		fixtureServer.mu.Lock()
		fixtureServer.requests = append(fixtureServer.requests, request)
		fixtureServer.mu.Unlock()

		response, ok := responsesByQuery[request.Query]
		if !ok {
			http.Error(w, fmt.Sprintf("no fixture response for query %q", request.Query), http.StatusNotFound)
			return
		}

		var payload any
		if err := json.Unmarshal(response, &payload); err != nil {
			http.Error(w, fmt.Sprintf("invalid fixture response for query %q: %v", request.Query, err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))

	return fixtureServer
}

func (s *fixtureServer) URL() string { // A
	return s.server.URL
}

func (s *fixtureServer) Close() { // A
	s.server.Close()
}

func (s *fixtureServer) Requests() []fixtureRequest { // A
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]fixtureRequest, len(s.requests))
	copy(requests, s.requests)
	return requests
}
