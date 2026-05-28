package charts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prom-live-svg/internal/config"
	"prom-live-svg/internal/prometheus"
)

func TestRenderSVGProducesValidSVGWithFixtureData(t *testing.T) { // A
	t.Parallel()

	document := buildChronySourceOffsetDocument(t)

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)

	if !strings.HasPrefix(body, "<svg") {
		t.Fatalf("expected SVG to start with <svg, got %q", body[:min(len(body), 100)])
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "</svg>") {
		t.Fatalf("expected SVG to end with </svg>, got %q", body[max(0, len(body)-20):])
	}
	if !strings.Contains(body, "Chrony source offset by source") {
		t.Fatal("expected SVG to contain chart title")
	}
	if !strings.Contains(body, "polyline") {
		t.Fatal("expected SVG to contain polyline elements for data series")
	}
	if !strings.Contains(body, "role=\"img\"") {
		t.Fatal("expected SVG to have role=\"img\" for accessibility")
	}
	if !strings.Contains(body, "aria-label=") {
		t.Fatal("expected SVG to have aria-label for accessibility")
	}
}

func TestRenderSVGProducesNoDataMessageForEmptySeries(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "empty_chart",
		Title:       "Empty Chart",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series:      []Series{},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if !strings.Contains(body, "no data") {
		t.Fatal("expected SVG to contain 'no data' message for empty series")
	}
	if strings.Contains(body, "polyline") {
		t.Fatal("expected SVG to not contain polyline elements when there is no data")
	}
}

func TestRenderSVGRejectsInvalidDimensions(t *testing.T) { // A
	t.Parallel()

	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "zero width", width: 0, height: 320},
		{name: "zero height", width: 800, height: 0},
		{name: "negative width", width: -1, height: 320},
		{name: "negative height", width: 800, height: -1},
		{name: "too small for plot area", width: 50, height: 50},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := Document{
				Kind:   "prometheus_matrix_chart",
				Chart:  "test",
				Title:  "Test",
				Width:  test.width,
				Height: test.height,
			}

			_, err := RenderSVG(document)
			if err == nil {
				t.Fatal("expected error for invalid dimensions, got nil")
			}
		})
	}
}

func TestRenderSVGHandlesSinglePointSeries(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "single_point",
		Title:       "Single Point",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779895900, Value: 42.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if !strings.Contains(body, "polyline") {
		t.Fatal("expected SVG to contain polyline for single point series")
	}
}

func TestRenderSVGHandlesSameStartEndTimestamps(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "same_time",
		Title:       "Same Time",
		RequestedAt: 1779896355,
		StartAt:     1779896355,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779896355, Value: 100.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if !strings.Contains(body, "polyline") {
		t.Fatal("expected SVG to contain polyline when start and end are the same")
	}
}

func TestRenderSVGHandlesAllSameValues(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "flat_line",
		Title:       "Flat Line",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 50.0},
					{Timestamp: 1779895900, Value: 50.0},
					{Timestamp: 1779896355, Value: 50.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if !strings.Contains(body, "polyline") {
		t.Fatal("expected SVG to contain polyline for flat line series")
	}
}

func TestRenderSVGHandlesMultipleSeries(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "multi_series",
		Title:       "Multi Series",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "a:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 10.0},
					{Timestamp: 1779896355, Value: 20.0},
				},
			},
			{
				Metric: map[string]string{"instance": "b:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 30.0},
					{Timestamp: 1779896355, Value: 40.0},
				},
			},
			{
				Metric: map[string]string{"instance": "c:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 50.0},
					{Timestamp: 1779896355, Value: 60.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	count := strings.Count(body, "<polyline")
	if count != 3 {
		t.Fatalf("expected 3 polyline elements, got %d", count)
	}
}

func TestRenderSVGEscapesSpecialCharactersInTitle(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "xss_test",
		Title:       `Test <script>alert("xss")</script> & Chart`,
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 42.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if strings.Contains(body, "<script>") {
		t.Fatal("expected script tag to be escaped in SVG output")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("expected escaped script tag in SVG output")
	}
	if !strings.Contains(body, "&amp;") {
		t.Fatal("expected escaped ampersand in SVG output")
	}
}

func TestRenderSVGProducesDeterministicOutput(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "deterministic",
		Title:       "Deterministic Test",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 10.0},
					{Timestamp: 1779895900, Value: 50.0},
					{Timestamp: 1779896355, Value: 90.0},
				},
			},
		},
	}

	first, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("first RenderSVG returned error: %v", err)
	}

	second, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("second RenderSVG returned error: %v", err)
	}

	if string(first) != string(second) {
		t.Fatal("expected deterministic output from RenderSVG")
	}
}

func TestRenderSVGIncludesCorrectDimensions(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "dimensions",
		Title:       "Dimensions Test",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       960,
		Height:      400,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "test:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 42.0},
				},
			},
		},
	}

	svg, err := RenderSVG(document)
	if err != nil {
		t.Fatalf("RenderSVG returned error: %v", err)
	}

	body := string(svg)
	if !strings.Contains(body, `width="960"`) {
		t.Fatal("expected SVG to contain width=\"960\"")
	}
	if !strings.Contains(body, `height="400"`) {
		t.Fatal("expected SVG to contain height=\"400\"")
	}
	if !strings.Contains(body, `viewBox="0 0 960 400"`) {
		t.Fatal("expected SVG to contain correct viewBox")
	}
}

func TestBuildSVGDataProducesCorrectStructure(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "structure_test",
		Title:       "Structure Test",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series: []Series{
			{
				Metric: map[string]string{"instance": "a:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 10.0},
					{Timestamp: 1779896355, Value: 20.0},
				},
			},
			{
				Metric: map[string]string{"instance": "b:9090"},
				Values: []Point{
					{Timestamp: 1779895455, Value: 30.0},
					{Timestamp: 1779896355, Value: 40.0},
				},
			},
		},
	}

	data := buildSVGData(document)

	if data.Width != 800 {
		t.Fatalf("expected Width 800, got %d", data.Width)
	}
	if data.Height != 320 {
		t.Fatalf("expected Height 320, got %d", data.Height)
	}
	if data.Title != "Structure Test" {
		t.Fatalf("expected Title 'Structure Test', got %q", data.Title)
	}
	if !data.HasData {
		t.Fatal("expected HasData to be true")
	}
	if len(data.Series) != 2 {
		t.Fatalf("expected 2 series, got %d", len(data.Series))
	}
	if data.StartAt != 1779895455 {
		t.Fatalf("expected StartAt 1779895455, got %d", data.StartAt)
	}
	if data.EndAt != 1779896355 {
		t.Fatalf("expected EndAt 1779896355, got %d", data.EndAt)
	}
}

func TestBuildSVGDataEmptySeriesHasNoData(t *testing.T) { // A
	t.Parallel()

	document := Document{
		Kind:        "prometheus_matrix_chart",
		Chart:       "empty",
		Title:       "Empty",
		RequestedAt: 1779896355,
		StartAt:     1779895455,
		EndAt:       1779896355,
		StepSeconds: 15,
		Width:       800,
		Height:      320,
		Series:      []Series{},
	}

	data := buildSVGData(document)

	if data.HasData {
		t.Fatal("expected HasData to be false for empty series")
	}
	if len(data.Series) != 0 {
		t.Fatalf("expected 0 series, got %d", len(data.Series))
	}
}

func repositoryRoot() string { // A
	return filepath.Join("..", "..")
}

func buildChronySourceOffsetDocument(t *testing.T) Document { // A
	t.Helper()

	responsePath := filepath.Join(
		repositoryRoot(),
		"testdata", "prometheus", "chrony",
		"chrony_source_offset_by_source.range.json",
	)

	responseBytes, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read fixture response: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string              `json:"resultType"`
			Result     []prometheus.Series `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBytes, &payload); err != nil {
		t.Fatalf("decode fixture response: %v", err)
	}

	chart := config.ChartConfig{
		Name:     "chrony_source_offset_by_source",
		Title:    "Chrony source offset by source",
		Width:    960,
		Height:   400,
		Lookback: config.Duration{},
		Step:     config.Duration{},
	}
	chart.Lookback.Duration = 15 * time.Minute
	chart.Step.Duration = 15 * time.Second

	matrix := prometheus.Matrix{Series: payload.Data.Result}
	end := time.Unix(1779896355, 0).UTC()

	return BuildDocument(chart, end, matrix)
}
