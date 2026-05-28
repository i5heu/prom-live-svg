package charts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"strconv"
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
}

type Series struct {
	Metric map[string]string `json:"metric"`
	Values []Point           `json:"values"`
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

func MarshalJSON(doc Document) ([]byte, error) { // A
	return json.Marshal(doc)
}

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

	minValue, maxValue, hasData := valueBounds(doc)
	if !hasData {
		minValue = 0
		maxValue = 1
	}
	if minValue == maxValue {
		minValue -= 1
		maxValue += 1
	}

	start := float64(doc.StartAt)
	end := float64(doc.EndAt)
	if start == end {
		end = start + 1
	}

	var buffer bytes.Buffer
	buffer.WriteString(fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\" role=\"img\" aria-label=\"%s\">", doc.Width, doc.Height, doc.Width, doc.Height, html.EscapeString(doc.Title)))
	buffer.WriteString("<rect width=\"100%\" height=\"100%\" fill=\"#ffffff\"/>")
	buffer.WriteString(fmt.Sprintf("<text x=\"%.0f\" y=\"20\" font-family=\"sans-serif\" font-size=\"16\" fill=\"#111827\">%s</text>", leftPad, html.EscapeString(doc.Title)))
	buffer.WriteString(fmt.Sprintf("<line x1=\"%.2f\" y1=\"%.2f\" x2=\"%.2f\" y2=\"%.2f\" stroke=\"#d1d5db\" stroke-width=\"1\"/>", leftPad, topPad, leftPad, topPad+plotHeight))
	buffer.WriteString(fmt.Sprintf("<line x1=\"%.2f\" y1=\"%.2f\" x2=\"%.2f\" y2=\"%.2f\" stroke=\"#d1d5db\" stroke-width=\"1\"/>", leftPad, topPad+plotHeight, leftPad+plotWidth, topPad+plotHeight))

	buffer.WriteString(fmt.Sprintf("<text x=\"8\" y=\"%.2f\" font-family=\"sans-serif\" font-size=\"11\" fill=\"#6b7280\">%s</text>", topPad+8, formatFloat(maxValue)))
	buffer.WriteString(fmt.Sprintf("<text x=\"8\" y=\"%.2f\" font-family=\"sans-serif\" font-size=\"11\" fill=\"#6b7280\">%s</text>", topPad+plotHeight, formatFloat(minValue)))
	buffer.WriteString(fmt.Sprintf("<text x=\"%.2f\" y=\"%.2f\" font-family=\"sans-serif\" font-size=\"11\" fill=\"#6b7280\">%d</text>", leftPad, float64(doc.Height)-8, doc.StartAt))
	buffer.WriteString(fmt.Sprintf("<text x=\"%.2f\" y=\"%.2f\" text-anchor=\"end\" font-family=\"sans-serif\" font-size=\"11\" fill=\"#6b7280\">%d</text>", leftPad+plotWidth, float64(doc.Height)-8, doc.EndAt))

	if !hasData {
		buffer.WriteString(fmt.Sprintf("<text x=\"%.2f\" y=\"%.2f\" text-anchor=\"middle\" font-family=\"sans-serif\" font-size=\"14\" fill=\"#6b7280\">no data</text>", leftPad+plotWidth/2, topPad+plotHeight/2))
		buffer.WriteString("</svg>")
		return buffer.Bytes(), nil
	}

	for i, series := range doc.Series {
		if len(series.Values) == 0 {
			continue
		}

		color := palette[i%len(palette)]
		points := make([]string, 0, len(series.Values))
		for _, point := range series.Values {
			x := leftPad + ((float64(point.Timestamp)-start)/(end-start))*plotWidth
			y := topPad + ((maxValue-point.Value)/(maxValue-minValue))*plotHeight
			points = append(points, formatFloat(x)+","+formatFloat(y))
		}

		buffer.WriteString(fmt.Sprintf("<polyline fill=\"none\" stroke=\"%s\" stroke-width=\"2\" points=\"%s\"/>", color, html.EscapeString(joinPoints(points))))
	}

	buffer.WriteString("</svg>")
	return buffer.Bytes(), nil
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
