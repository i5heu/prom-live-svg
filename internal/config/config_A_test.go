package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadYAMLAppliesDefaults(t *testing.T) { // A
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := `service:
  environment: production
prometheus:
  base_url: https://prometheus.example.com
charts:
  - name: cpu_usage
    query: |-
      rate(container_cpu_usage_seconds_total[5m])
      /
      rate(container_cpu_cfs_periods_total[5m])
`

	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Service.Name != "prom-live-svg" {
		t.Fatalf("expected default service name, got %q", cfg.Service.Name)
	}
	if cfg.Service.Environment != "production" {
		t.Fatalf("expected environment from file, got %q", cfg.Service.Environment)
	}
	if cfg.HTTP.ListenAddr != ":8080" {
		t.Fatalf("expected default listen address, got %q", cfg.HTTP.ListenAddr)
	}
	if cfg.Charts[0].Title != "cpu_usage" {
		t.Fatalf("expected title to default to chart name, got %q", cfg.Charts[0].Title)
	}
	if cfg.Charts[0].Width != 800 || cfg.Charts[0].Height != 320 {
		t.Fatalf("expected default dimensions 800x320, got %dx%d", cfg.Charts[0].Width, cfg.Charts[0].Height)
	}
	if cfg.Charts[0].Step.Duration != 15*time.Second {
		t.Fatalf("expected default chart step to match generation interval, got %s", cfg.Charts[0].Step.Duration)
	}
	if got, want := cfg.Charts[0].Query, "rate(container_cpu_usage_seconds_total[5m])\n/\nrate(container_cpu_cfs_periods_total[5m])"; got != want {
		t.Fatalf("expected multiline query to be preserved, got %q", got)
	}
}

func TestLoadAppliesEnvOverrides(t *testing.T) { // A
	t.Setenv("PROM_LIVE_SVG_HTTP_LISTEN_ADDR", ":9000")
	t.Setenv("PROM_LIVE_SVG_LOG_LEVEL", "debug")
	t.Setenv("PROM_LIVE_SVG_GENERATION_INTERVAL", "30s")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.HTTP.ListenAddr != ":9000" {
		t.Fatalf("expected listen address override, got %q", cfg.HTTP.ListenAddr)
	}
	if cfg.Logging.Level != "debug" {
		t.Fatalf("expected log level override, got %q", cfg.Logging.Level)
	}
	if cfg.Generation.Interval.Duration != 30*time.Second {
		t.Fatalf("expected generation interval override, got %s", cfg.Generation.Interval.Duration)
	}
}

func TestLoadReadsQueryFileRelativeToConfig(t *testing.T) { // A
	t.Parallel()

	tempDir := t.TempDir()
	queriesDir := filepath.Join(tempDir, "queries")
	if err := os.MkdirAll(queriesDir, 0o755); err != nil {
		t.Fatalf("create queries dir: %v", err)
	}

	queryPath := filepath.Join(queriesDir, "accepted.promql")
	queryBody := "rate(chrony_serverstats_ntp_packets_received_total[5m])\n-\nrate(chrony_serverstats_ntp_packets_dropped_total[5m])\n"
	if err := os.WriteFile(queryPath, []byte(queryBody), 0o644); err != nil {
		t.Fatalf("write query file: %v", err)
	}

	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := `prometheus:
  base_url: https://prometheus.example.com
charts:
  - name: chrony_packets_accepted
    query_file: queries/accepted.promql
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.Charts[0].Query, strings.TrimSpace(queryBody); got != want {
		t.Fatalf("expected query file contents, got %q want %q", got, want)
	}
	if got, want := cfg.Charts[0].QueryFile, "queries/accepted.promql"; got != want {
		t.Fatalf("expected query_file to stay as configured, got %q want %q", got, want)
	}
}

func TestLoadRejectsInlineAndFileQueryCombination(t *testing.T) { // A
	t.Parallel()

	tempDir := t.TempDir()
	queryPath := filepath.Join(tempDir, "query.promql")
	if err := os.WriteFile(queryPath, []byte("up\n"), 0o644); err != nil {
		t.Fatalf("write query file: %v", err)
	}

	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := `charts:
  - name: invalid_chart
    query: up
    query_file: query.promql
`

	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected chart with query and query_file to fail")
	}
	if !strings.Contains(err.Error(), "only one of query or query_file") {
		t.Fatalf("expected query/query_file conflict error, got %v", err)
	}
}

func TestLoadReadsChartStatQueryFilesRelativeToConfig(t *testing.T) { // A
	t.Parallel()

	tempDir := t.TempDir()
	queriesDir := filepath.Join(tempDir, "queries")
	if err := os.MkdirAll(queriesDir, 0o755); err != nil {
		t.Fatalf("create queries dir: %v", err)
	}

	historyQueryPath := filepath.Join(queriesDir, "history.promql")
	if err := os.WriteFile(historyQueryPath, []byte("rate(up[5m])\n"), 0o644); err != nil {
		t.Fatalf("write history query file: %v", err)
	}

	statQueryPath := filepath.Join(queriesDir, "total.promql")
	if err := os.WriteFile(statQueryPath, []byte("sum(up)\n"), 0o644); err != nil {
		t.Fatalf("write stat query file: %v", err)
	}

	seedQueryPath := filepath.Join(queriesDir, "seed.promql")
	if err := os.WriteFile(seedQueryPath, []byte("increase(up[365d])\n"), 0o644); err != nil {
		t.Fatalf("write seed query file: %v", err)
	}

	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := `prometheus:
  base_url: https://prometheus.example.com
charts:
  - name: req_summary
    query_file: queries/history.promql
    step: 15s
    stats:
      - name: total_requests
        query_file: queries/total.promql
        seed_query_file: queries/seed.promql
        persist: true
`
	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.Charts[0].Query, "rate(up[5m])"; got != want {
		t.Fatalf("unexpected chart query: got %q want %q", got, want)
	}
	if got, want := cfg.Charts[0].Stats[0].Query, "sum(up)"; got != want {
		t.Fatalf("unexpected stat query: got %q want %q", got, want)
	}
	if got, want := cfg.Charts[0].Stats[0].Label, "total_requests"; got != want {
		t.Fatalf("unexpected default stat label: got %q want %q", got, want)
	}
	if got, want := cfg.Charts[0].Stats[0].SeedQuery, "increase(up[365d])"; got != want {
		t.Fatalf("unexpected stat seed query: got %q want %q", got, want)
	}
	if got, want := cfg.Charts[0].Stats[0].Lookback.Duration, 15*time.Second; got != want {
		t.Fatalf("unexpected default stat lookback: got %s want %s", got, want)
	}
	if got, want := cfg.Charts[0].Stats[0].Step.Duration, 15*time.Second; got != want {
		t.Fatalf("unexpected default stat step: got %s want %s", got, want)
	}
	if !cfg.Charts[0].Stats[0].Persist {
		t.Fatal("expected stat persist flag to be loaded from config")
	}
}

func TestLoadRejectsDuplicateCharts(t *testing.T) { // A
	t.Parallel()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	configBody := `charts:
  - name: cpu_usage
    query: up
  - name: cpu_usage
    query: process_cpu_seconds_total
`

	if err := os.WriteFile(configPath, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected duplicate chart names to fail validation")
	}
	if !strings.Contains(err.Error(), "duplicate chart name") {
		t.Fatalf("expected duplicate chart error, got %v", err)
	}
}
