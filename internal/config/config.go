package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var chartNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type Duration struct {
	time.Duration
}

type Config struct {
	Service    ServiceConfig    `json:"service" yaml:"service"`
	HTTP       HTTPConfig       `json:"http" yaml:"http"`
	Prometheus PrometheusConfig `json:"prometheus" yaml:"prometheus"`
	Generation GenerationConfig `json:"generation" yaml:"generation"`
	Cache      CacheConfig      `json:"cache" yaml:"cache"`
	Storage    StorageConfig    `json:"storage" yaml:"storage"`
	Logging    LoggingConfig    `json:"logging" yaml:"logging"`
	Charts     []ChartConfig    `json:"charts" yaml:"charts"`
}

type ServiceConfig struct {
	Name        string `json:"name" yaml:"name"`
	Environment string `json:"environment" yaml:"environment"`
}

type HTTPConfig struct {
	ListenAddr        string   `json:"listen_addr" yaml:"listen_addr"`
	ReadHeaderTimeout Duration `json:"read_header_timeout" yaml:"read_header_timeout"`
	ReadTimeout       Duration `json:"read_timeout" yaml:"read_timeout"`
	WriteTimeout      Duration `json:"write_timeout" yaml:"write_timeout"`
	IdleTimeout       Duration `json:"idle_timeout" yaml:"idle_timeout"`
	ShutdownTimeout   Duration `json:"shutdown_timeout" yaml:"shutdown_timeout"`
}

type PrometheusConfig struct {
	BaseURL      string   `json:"base_url" yaml:"base_url"`
	QueryTimeout Duration `json:"query_timeout" yaml:"query_timeout"`
}

type GenerationConfig struct {
	Interval Duration `json:"interval" yaml:"interval"`
}

type CacheConfig struct {
	Retention Duration `json:"retention" yaml:"retention"`
}

type StorageConfig struct {
	DataDir string `json:"data_dir" yaml:"data_dir"`
}

type LoggingConfig struct {
	Level  string `json:"level" yaml:"level"`
	Format string `json:"format" yaml:"format"`
}

type ChartConfig struct {
	Name      string   `json:"name" yaml:"name"`
	Title     string   `json:"title" yaml:"title"`
	Query     string   `json:"query" yaml:"query"`
	QueryFile string   `json:"query_file" yaml:"query_file"`
	Width     int      `json:"width" yaml:"width"`
	Height    int      `json:"height" yaml:"height"`
	Lookback  Duration `json:"lookback" yaml:"lookback"`
	Step      Duration `json:"step" yaml:"step"`
}

func Default() Config { // H
	return Config{
		Service: ServiceConfig{
			Name:        "prom-live-svg",
			Environment: "development",
		},
		HTTP: HTTPConfig{
			ListenAddr:        ":8080",
			ReadHeaderTimeout: Duration{Duration: 5 * time.Second},
			ReadTimeout:       Duration{Duration: 10 * time.Second},
			WriteTimeout:      Duration{Duration: 30 * time.Second},
			IdleTimeout:       Duration{Duration: time.Minute},
			ShutdownTimeout:   Duration{Duration: 10 * time.Second},
		},
		Prometheus: PrometheusConfig{
			BaseURL:      "http://localhost:9090",
			QueryTimeout: Duration{Duration: 10 * time.Second},
		},
		Generation: GenerationConfig{
			Interval: Duration{Duration: 15 * time.Second},
		},
		Cache: CacheConfig{
			Retention: Duration{Duration: 15 * time.Minute},
		},
		Storage: StorageConfig{
			DataDir: "data",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "text",
		},
	}
}

func Load(path string) (Config, error) { // H
	cfg := Default()

	if path != "" {
		if err := loadFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return Config{}, err
	}

	normalize(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadFile(path string, cfg *Config) error { // A
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file %q: %w", path, err)
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		if err := json.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("decode JSON config %q: %w", path, err)
		}
	default:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("decode YAML config %q: %w", path, err)
		}
	}

	if err := resolveChartQueries(filepath.Dir(path), cfg); err != nil {
		return err
	}

	return nil
}

func resolveChartQueries(baseDir string, cfg *Config) error { // A
	var errs []error

	for i := range cfg.Charts {
		chart := &cfg.Charts[i]
		chart.Query = strings.TrimSpace(chart.Query)
		chart.QueryFile = strings.TrimSpace(chart.QueryFile)

		prefix := fmt.Sprintf("charts[%d]", i)
		switch {
		case chart.Query != "" && chart.QueryFile != "":
			errs = append(errs, fmt.Errorf("%s must set only one of query or query_file", prefix))
			continue
		case chart.QueryFile == "":
			continue
		}

		resolvedPath := chart.QueryFile
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(baseDir, resolvedPath)
		}

		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s.query_file %q: %w", prefix, chart.QueryFile, err))
			continue
		}

		chart.Query = string(data)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func normalize(cfg *Config) { // A
	defaults := Default()

	cfg.Service.Name = strings.TrimSpace(cfg.Service.Name)
	if cfg.Service.Name == "" {
		cfg.Service.Name = defaults.Service.Name
	}

	cfg.Service.Environment = strings.TrimSpace(cfg.Service.Environment)
	if cfg.Service.Environment == "" {
		cfg.Service.Environment = defaults.Service.Environment
	}

	cfg.HTTP.ListenAddr = strings.TrimSpace(cfg.HTTP.ListenAddr)
	if cfg.HTTP.ListenAddr == "" {
		cfg.HTTP.ListenAddr = defaults.HTTP.ListenAddr
	}
	if cfg.HTTP.ReadHeaderTimeout.Duration == 0 {
		cfg.HTTP.ReadHeaderTimeout = defaults.HTTP.ReadHeaderTimeout
	}
	if cfg.HTTP.ReadTimeout.Duration == 0 {
		cfg.HTTP.ReadTimeout = defaults.HTTP.ReadTimeout
	}
	if cfg.HTTP.WriteTimeout.Duration == 0 {
		cfg.HTTP.WriteTimeout = defaults.HTTP.WriteTimeout
	}
	if cfg.HTTP.IdleTimeout.Duration == 0 {
		cfg.HTTP.IdleTimeout = defaults.HTTP.IdleTimeout
	}
	if cfg.HTTP.ShutdownTimeout.Duration == 0 {
		cfg.HTTP.ShutdownTimeout = defaults.HTTP.ShutdownTimeout
	}

	cfg.Prometheus.BaseURL = strings.TrimSpace(cfg.Prometheus.BaseURL)
	if cfg.Prometheus.BaseURL == "" {
		cfg.Prometheus.BaseURL = defaults.Prometheus.BaseURL
	}
	if cfg.Prometheus.QueryTimeout.Duration == 0 {
		cfg.Prometheus.QueryTimeout = defaults.Prometheus.QueryTimeout
	}

	if cfg.Generation.Interval.Duration == 0 {
		cfg.Generation.Interval = defaults.Generation.Interval
	}

	if cfg.Cache.Retention.Duration == 0 {
		cfg.Cache.Retention = defaults.Cache.Retention
	}

	cfg.Storage.DataDir = strings.TrimSpace(cfg.Storage.DataDir)
	if cfg.Storage.DataDir == "" {
		cfg.Storage.DataDir = defaults.Storage.DataDir
	}

	cfg.Logging.Level = strings.ToLower(strings.TrimSpace(cfg.Logging.Level))
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = defaults.Logging.Level
	}

	cfg.Logging.Format = strings.ToLower(strings.TrimSpace(cfg.Logging.Format))
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = defaults.Logging.Format
	}

	for i := range cfg.Charts {
		cfg.Charts[i].Name = strings.TrimSpace(cfg.Charts[i].Name)
		cfg.Charts[i].Title = strings.TrimSpace(cfg.Charts[i].Title)
		cfg.Charts[i].Query = strings.TrimSpace(cfg.Charts[i].Query)
		cfg.Charts[i].QueryFile = strings.TrimSpace(cfg.Charts[i].QueryFile)
		if cfg.Charts[i].Title == "" {
			cfg.Charts[i].Title = cfg.Charts[i].Name
		}
		if cfg.Charts[i].Width == 0 {
			cfg.Charts[i].Width = 800
		}
		if cfg.Charts[i].Height == 0 {
			cfg.Charts[i].Height = 320
		}
		if cfg.Charts[i].Lookback.Duration == 0 {
			cfg.Charts[i].Lookback = Duration{Duration: 15 * time.Minute}
		}
		if cfg.Charts[i].Step.Duration == 0 {
			cfg.Charts[i].Step = defaults.Generation.Interval
		}
	}
}

func validate(cfg Config) error { // A
	var errs []error

	if cfg.Service.Name == "" {
		errs = append(errs, errors.New("service.name must not be empty"))
	}

	if cfg.HTTP.ListenAddr == "" {
		errs = append(errs, errors.New("http.listen_addr must not be empty"))
	}
	if cfg.HTTP.ReadHeaderTimeout.Duration <= 0 {
		errs = append(errs, errors.New("http.read_header_timeout must be greater than zero"))
	}
	if cfg.HTTP.ReadTimeout.Duration <= 0 {
		errs = append(errs, errors.New("http.read_timeout must be greater than zero"))
	}
	if cfg.HTTP.WriteTimeout.Duration <= 0 {
		errs = append(errs, errors.New("http.write_timeout must be greater than zero"))
	}
	if cfg.HTTP.IdleTimeout.Duration <= 0 {
		errs = append(errs, errors.New("http.idle_timeout must be greater than zero"))
	}
	if cfg.HTTP.ShutdownTimeout.Duration <= 0 {
		errs = append(errs, errors.New("http.shutdown_timeout must be greater than zero"))
	}

	promURL, err := url.Parse(cfg.Prometheus.BaseURL)
	if err != nil || promURL == nil || promURL.Scheme == "" || promURL.Host == "" {
		errs = append(errs, errors.New("prometheus.base_url must be a valid absolute URL"))
	} else if promURL.Scheme != "http" && promURL.Scheme != "https" {
		errs = append(errs, errors.New("prometheus.base_url must use http or https"))
	}
	if cfg.Prometheus.QueryTimeout.Duration <= 0 {
		errs = append(errs, errors.New("prometheus.query_timeout must be greater than zero"))
	}

	if cfg.Generation.Interval.Duration <= 0 {
		errs = append(errs, errors.New("generation.interval must be greater than zero"))
	}
	if cfg.Cache.Retention.Duration <= 0 {
		errs = append(errs, errors.New("cache.retention must be greater than zero"))
	}
	if cfg.Cache.Retention.Duration < cfg.Generation.Interval.Duration {
		errs = append(errs, errors.New("cache.retention must be greater than or equal to generation.interval"))
	}
	if cfg.Storage.DataDir == "" {
		errs = append(errs, errors.New("storage.data_dir must not be empty"))
	}

	switch cfg.Logging.Level {
	case "debug", "info", "warn", "warning", "error":
	default:
		errs = append(errs, fmt.Errorf("logging.level must be one of debug, info, warn, warning, error: %q", cfg.Logging.Level))
	}

	switch cfg.Logging.Format {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("logging.format must be one of text or json: %q", cfg.Logging.Format))
	}

	seenCharts := make(map[string]struct{}, len(cfg.Charts))
	for i, chart := range cfg.Charts {
		prefix := fmt.Sprintf("charts[%d]", i)
		if chart.Name == "" {
			errs = append(errs, fmt.Errorf("%s.name must not be empty", prefix))
		} else {
			if !chartNamePattern.MatchString(chart.Name) {
				errs = append(errs, fmt.Errorf("%s.name must match %s", prefix, chartNamePattern.String()))
			}
			if _, exists := seenCharts[chart.Name]; exists {
				errs = append(errs, fmt.Errorf("duplicate chart name %q", chart.Name))
			}
			seenCharts[chart.Name] = struct{}{}
		}
		if chart.Query == "" {
			errs = append(errs, fmt.Errorf("%s must define a non-empty query or query_file", prefix))
		}
		if chart.Width <= 0 {
			errs = append(errs, fmt.Errorf("%s.width must be greater than zero", prefix))
		}
		if chart.Height <= 0 {
			errs = append(errs, fmt.Errorf("%s.height must be greater than zero", prefix))
		}
		if chart.Lookback.Duration <= 0 {
			errs = append(errs, fmt.Errorf("%s.lookback must be greater than zero", prefix))
		}
		if chart.Step.Duration <= 0 {
			errs = append(errs, fmt.Errorf("%s.step must be greater than zero", prefix))
		}
		if chart.Step.Duration > chart.Lookback.Duration {
			errs = append(errs, fmt.Errorf("%s.step must be less than or equal to %s.lookback", prefix, prefix))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func applyEnvOverrides(cfg *Config) error { // A
	var errs []error

	applyStringEnv("PROM_LIVE_SVG_SERVICE_NAME", &cfg.Service.Name)
	applyStringEnv("PROM_LIVE_SVG_SERVICE_ENVIRONMENT", &cfg.Service.Environment)
	applyStringEnv("PROM_LIVE_SVG_HTTP_LISTEN_ADDR", &cfg.HTTP.ListenAddr)
	applyStringEnv("PROM_LIVE_SVG_PROMETHEUS_BASE_URL", &cfg.Prometheus.BaseURL)
	applyStringEnv("PROM_LIVE_SVG_STORAGE_DATA_DIR", &cfg.Storage.DataDir)
	applyStringEnv("PROM_LIVE_SVG_LOG_LEVEL", &cfg.Logging.Level)
	applyStringEnv("PROM_LIVE_SVG_LOG_FORMAT", &cfg.Logging.Format)

	applyDurationEnv("PROM_LIVE_SVG_HTTP_READ_HEADER_TIMEOUT", &cfg.HTTP.ReadHeaderTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_HTTP_READ_TIMEOUT", &cfg.HTTP.ReadTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_HTTP_WRITE_TIMEOUT", &cfg.HTTP.WriteTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_HTTP_IDLE_TIMEOUT", &cfg.HTTP.IdleTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_HTTP_SHUTDOWN_TIMEOUT", &cfg.HTTP.ShutdownTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_PROMETHEUS_QUERY_TIMEOUT", &cfg.Prometheus.QueryTimeout, &errs)
	applyDurationEnv("PROM_LIVE_SVG_GENERATION_INTERVAL", &cfg.Generation.Interval, &errs)
	applyDurationEnv("PROM_LIVE_SVG_CACHE_RETENTION", &cfg.Cache.Retention, &errs)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func applyStringEnv(key string, target *string) { // A
	value, ok := os.LookupEnv(key)
	if !ok {
		return
	}

	*target = strings.TrimSpace(value)
}

func applyDurationEnv(key string, target *Duration, errs *[]error) { // A
	value, ok := os.LookupEnv(key)
	if !ok {
		return
	}

	value = strings.TrimSpace(value)
	if value == "" {
		*errs = append(*errs, fmt.Errorf("%s must not be empty", key))
		return
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("parse %s: %w", key, err))
		return
	}

	target.Duration = parsed
}

func (d *Duration) UnmarshalJSON(data []byte) error { // A
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("duration must be a JSON string: %w", err)
	}

	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", raw, err)
	}

	d.Duration = parsed
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) { // A
	return json.Marshal(d.Duration.String())
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error { // A
	if node.Kind != yaml.ScalarNode {
		return errors.New("duration must be a YAML scalar")
	}

	parsed, err := time.ParseDuration(strings.TrimSpace(node.Value))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", node.Value, err)
	}

	d.Duration = parsed
	return nil
}

func (d Duration) MarshalYAML() (any, error) { // A
	return d.Duration.String(), nil
}

func (d Duration) String() string { // A
	return d.Duration.String()
}
