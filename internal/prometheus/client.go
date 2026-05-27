package prometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"prom-live-svg/internal/config"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

type RangeQuery struct {
	Query string
	Start time.Time
	End   time.Time
	Step  time.Duration
}

type Matrix struct {
	Series []Series
}

type Series struct {
	Metric map[string]string `json:"metric"`
	Values []Sample          `json:"values"`
}

type Sample struct {
	Timestamp time.Time
	Value     float64
}

type apiResponse struct {
	Status    string  `json:"status"`
	Data      apiData `json:"data"`
	ErrorType string  `json:"errorType"`
	Error     string  `json:"error"`
}

type apiData struct {
	ResultType string   `json:"resultType"`
	Result     []Series `json:"result"`
}

func New(baseURL string, timeout time.Duration) (*Client, error) { // A
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base URL must be absolute: %q", baseURL)
	}
	if timeout <= 0 {
		return nil, errors.New("timeout must be greater than zero")
	}

	return &Client{
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func NewFromConfig(cfg config.PrometheusConfig) (*Client, error) { // A
	return New(cfg.BaseURL, cfg.QueryTimeout.Duration)
}

func (c *Client) QueryRange(ctx context.Context, query RangeQuery) (Matrix, error) { // A
	if err := query.Validate(); err != nil {
		return Matrix{}, err
	}

	endpoint := c.queryRangeURL()
	params := endpoint.Query()
	params.Set("query", query.Query)
	params.Set("start", formatPrometheusTime(query.Start))
	params.Set("end", formatPrometheusTime(query.End))
	params.Set("step", query.Step.String())
	endpoint.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Matrix{}, fmt.Errorf("build query_range request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Matrix{}, fmt.Errorf("execute query_range request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Matrix{}, fmt.Errorf("query_range returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return Matrix{}, fmt.Errorf("decode query_range response: %w", err)
	}

	if payload.Status != "success" {
		if payload.ErrorType != "" || payload.Error != "" {
			return Matrix{}, fmt.Errorf("prometheus query_range error (%s): %s", payload.ErrorType, payload.Error)
		}
		return Matrix{}, fmt.Errorf("prometheus query_range returned status %q", payload.Status)
	}
	if payload.Data.ResultType != "matrix" {
		return Matrix{}, fmt.Errorf("unexpected query_range result type %q", payload.Data.ResultType)
	}

	return Matrix{Series: payload.Data.Result}, nil
}

func (c *Client) QueryChartRange(ctx context.Context, chart config.ChartConfig, end time.Time) (Matrix, error) { // A
	return c.QueryRange(ctx, RangeQuery{
		Query: chart.Query,
		Start: end.Add(-chart.Lookback.Duration),
		End:   end,
		Step:  chart.Step.Duration,
	})
}

func (q RangeQuery) Validate() error { // A
	var errs []error

	if strings.TrimSpace(q.Query) == "" {
		errs = append(errs, errors.New("query must not be empty"))
	}
	if q.Start.IsZero() {
		errs = append(errs, errors.New("start must not be zero"))
	}
	if q.End.IsZero() {
		errs = append(errs, errors.New("end must not be zero"))
	}
	if !q.Start.IsZero() && !q.End.IsZero() && q.End.Before(q.Start) {
		errs = append(errs, errors.New("end must not be before start"))
	}
	if q.Step <= 0 {
		errs = append(errs, errors.New("step must be greater than zero"))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

func (c *Client) queryRangeURL() url.URL { // A
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api/v1/query_range"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint
}

func formatPrometheusTime(t time.Time) string { // A
	seconds := float64(t.UnixNano()) / float64(time.Second)
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}

func (s *Sample) UnmarshalJSON(data []byte) error { // A
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode sample pair: %w", err)
	}
	if len(raw) != 2 {
		return fmt.Errorf("sample must contain exactly two elements, got %d", len(raw))
	}

	var unixSeconds float64
	if err := json.Unmarshal(raw[0], &unixSeconds); err != nil {
		return fmt.Errorf("decode sample timestamp: %w", err)
	}

	var rawValue string
	if err := json.Unmarshal(raw[1], &rawValue); err != nil {
		return fmt.Errorf("decode sample value: %w", err)
	}

	parsedValue, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return fmt.Errorf("parse sample value %q: %w", rawValue, err)
	}

	s.Timestamp = unixSecondsToTime(unixSeconds)
	s.Value = parsedValue
	return nil
}

func unixSecondsToTime(value float64) time.Time { // A
	seconds, fraction := math.Modf(value)
	nanoseconds := int64(math.Round(fraction * float64(time.Second)))
	return time.Unix(int64(seconds), nanoseconds).UTC()
}
