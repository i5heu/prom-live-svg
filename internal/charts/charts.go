package charts

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"prom-live-svg/internal/config"
	"prom-live-svg/internal/prometheus"
)

type Document struct {
	Kind        string   `json:"kind"`
	Chart       string   `json:"chart"`
	Title       string   `json:"title"`
	RequestedAt int64    `json:"requested_at"`
	StartAt     int64    `json:"start_at"`
	EndAt       int64    `json:"end_at"`
	StepSeconds int64    `json:"step_seconds"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	Series      []Series `json:"series"`
	Stats       []Stat   `json:"stats,omitempty"`
}

type Series struct {
	Metric map[string]string `json:"metric"`
	Values []Point           `json:"values"`
}

type Stat struct {
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Value     float64 `json:"value"`
	Formatted string  `json:"formatted"`
	Decimals  int     `json:"decimals"`
	Unit      string  `json:"unit"`
}

type Point struct {
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

var palette = []string{
	"#0ea5e9",
	"#22c55e",
	"#f97316",
	"#a855f7",
	"#ef4444",
	"#14b8a6",
	"#eab308",
	"#8b5cf6",
}

func BuildDocument(chart config.ChartConfig, end time.Time, matrix prometheus.Matrix) Document { // A
	return BuildDocumentWithStats(chart, end, matrix, nil)
}

func BuildDocumentWithStats(chart config.ChartConfig, end time.Time, matrix prometheus.Matrix, stats []Stat) Document { // A
	start := end.Add(-chart.Lookback.Duration)
	doc := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       chart.Name,
		Title:       chart.Title,
		RequestedAt: end.Unix(),
		StartAt:     start.Unix(),
		EndAt:       end.Unix(),
		StepSeconds: int64(chart.Step.Duration / time.Second),
		Width:       chart.Width,
		Height:      chart.Height,
		Series:      make([]Series, 0, len(matrix.Series)),
		Stats:       cloneStats(stats),
	}

	for _, inputSeries := range matrix.Series {
		series := Series{
			Metric: cloneMetric(inputSeries.Metric),
			Values: make([]Point, 0, len(inputSeries.Values)),
		}

		for _, sample := range inputSeries.Values {
			series.Values = append(series.Values, Point{
				Timestamp: sample.Timestamp.Unix(),
				Value:     sample.Value,
			})
		}

		doc.Series = append(doc.Series, series)
	}

	return doc
}

func BuildStat(cfg config.ChartStatConfig, matrix prometheus.Matrix) (Stat, error) { // A
	value, ok := latestAggregateValue(matrix)
	if !ok {
		return Stat{}, fmt.Errorf("stat query returned no samples")
	}

	return BuildStatFromValue(cfg, value), nil
}

func BuildStatFromValue(cfg config.ChartStatConfig, value float64) Stat { // A
	return Stat{
		Name:      cfg.Name,
		Label:     cfg.Label,
		Value:     value,
		Formatted: formatStatValue(value, cfg.Decimals, cfg.Unit),
		Decimals:  cfg.Decimals,
		Unit:      cfg.Unit,
	}
}

func MarshalJSON(doc Document) ([]byte, error) { // A
	return json.Marshal(doc)
}

// RenderSVG renders a chart Document as an SVG string using a pre-compiled template.
func RenderSVG(doc Document) ([]byte, error) { // A
	if doc.Width <= 0 || doc.Height <= 0 {
		return nil, fmt.Errorf("invalid SVG size %dx%d", doc.Width, doc.Height)
	}

	const (
		leftPad   = 48.0
		rightPad  = 16.0
		topPad    = 32.0
		bottomPad = 28.0
	)

	plotWidth := float64(doc.Width) - leftPad - rightPad
	plotHeight := float64(doc.Height) - topPad - bottomPad
	if plotWidth <= 0 || plotHeight <= 0 {
		return nil, fmt.Errorf("invalid plot area for SVG size %dx%d", doc.Width, doc.Height)
	}

	data := buildSVGData(doc)
	return executeSVGTemplate(data)
}

// RenderMixedSVG renders multiple chart Documents stacked vertically in a single SVG.
func RenderMixedSVG(title string, width, height int, docs []Document) ([]byte, error) { // A
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid SVG size %dx%d", width, height)
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("at least one chart document is required for mixed chart")
	}

	data := buildMixedSVGData(title, width, height, docs)
	return executeMixedSVGTemplate(data)
}

func valueBounds(doc Document) (float64, float64, bool) { // A
	minValue := 0.0
	maxValue := 0.0
	hasData := false

	for _, series := range doc.Series {
		for _, point := range series.Values {
			if !hasData {
				minValue = point.Value
				maxValue = point.Value
				hasData = true
				continue
			}
			minValue = math.Min(minValue, point.Value)
			maxValue = math.Max(maxValue, point.Value)
		}
	}

	return minValue, maxValue, hasData
}

func cloneMetric(metric map[string]string) map[string]string { // A
	cloned := make(map[string]string, len(metric))
	for key, value := range metric {
		cloned[key] = value
	}
	return cloned
}

func cloneStats(stats []Stat) []Stat { // A
	if len(stats) == 0 {
		return nil
	}

	cloned := make([]Stat, len(stats))
	copy(cloned, stats)
	return cloned
}

func latestAggregateValue(matrix prometheus.Matrix) (float64, bool) { // A
	total := 0.0
	hasData := false

	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		total += series.Values[len(series.Values)-1].Value
		hasData = true
	}

	return total, hasData
}

func joinPoints(points []string) string { // A
	if len(points) == 0 {
		return ""
	}

	result := points[0]
	for i := 1; i < len(points); i++ {
		result += " " + points[i]
	}
	return result
}

func formatFloat(value float64) string { // A
	return strconv.FormatFloat(value, 'f', 3, 64)
}

func formatStatValue(value float64, decimals int, unit string) string { // A
	if decimals < 0 {
		decimals = 0
	}

	formatted := strconv.FormatFloat(value, 'f', decimals, 64)
	unit = strings.TrimSpace(unit)
	if unit == "" {
		return formatted
	}

	return formatted + " " + unit
}
