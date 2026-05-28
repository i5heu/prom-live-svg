package charts

import (
	"bytes"
	_ "embed"
	"fmt"
	"html"
	"text/template"
)

//go:embed templates/chart.go.svg
var svgTemplateRaw string

//go:embed templates/mixed_chart.go.svg
var mixedSVGTemplateRaw string

// parsedSVGTemplate is the compiled SVG template with the "escape" function registered.
var parsedSVGTemplate = template.Must(
	template.New("chart").Funcs(template.FuncMap{
		"escape": html.EscapeString,
	}).Parse(svgTemplateRaw),
)

// parsedMixedSVGTemplate is the compiled mixed chart SVG template.
var parsedMixedSVGTemplate = template.Must(
	template.New("mixed_chart").Funcs(template.FuncMap{
		"escape": html.EscapeString,
	}).Parse(mixedSVGTemplateRaw),
)

// svgTemplateData holds all data needed to render a single chart as SVG.
type svgTemplateData struct {
	Width           int
	Height          int
	Title           string
	LeftPad         string
	TopPad          string
	AxisBottom      string
	AxisRight       string
	TopPadLabelY    string
	BottomPadLabelY string
	XLabelY         string
	CenterX         string
	CenterY         string
	MaxLabel        string
	MinLabel        string
	StartAt         int64
	EndAt           int64
	HasData         bool
	Series          []svgSeriesData
}

// svgSeriesData holds the rendering data for a single data series in the SVG.
type svgSeriesData struct {
	Color  string
	Points string
}

// buildSVGData converts a Document into svgTemplateData suitable for template rendering.
func buildSVGData(doc Document) svgTemplateData { // A
	const (
		leftPad   = 48.0
		rightPad  = 16.0
		topPad    = 32.0
		bottomPad = 28.0
	)

	plotWidth := float64(doc.Width) - leftPad - rightPad
	plotHeight := float64(doc.Height) - topPad - bottomPad

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

	data := svgTemplateData{
		Width:           doc.Width,
		Height:          doc.Height,
		Title:           doc.Title,
		LeftPad:         formatFloat(leftPad),
		TopPad:          formatFloat(topPad),
		AxisBottom:      formatFloat(topPad + plotHeight),
		AxisRight:       formatFloat(leftPad + plotWidth),
		TopPadLabelY:    formatFloat(topPad + 8),
		BottomPadLabelY: formatFloat(topPad + plotHeight),
		XLabelY:         formatFloat(float64(doc.Height) - 8),
		CenterX:         formatFloat(leftPad + plotWidth/2),
		CenterY:         formatFloat(topPad + plotHeight/2),
		MaxLabel:        formatFloat(maxValue),
		MinLabel:        formatFloat(minValue),
		StartAt:         doc.StartAt,
		EndAt:           doc.EndAt,
		HasData:         hasData,
	}

	if hasData {
		data.Series = make([]svgSeriesData, 0, len(doc.Series))
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

			data.Series = append(data.Series, svgSeriesData{
				Color:  color,
				Points: joinPoints(points),
			})
		}
	}

	return data
}

// executeSVGTemplate renders the SVG template with the given data and returns the result.
func executeSVGTemplate(data svgTemplateData) ([]byte, error) { // A
	var buffer bytes.Buffer
	if err := parsedSVGTemplate.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("execute SVG template: %w", err)
	}
	return buffer.Bytes(), nil
}

// mixedChartFrameData holds the rendering data for a single chart frame within a mixed chart SVG.
type mixedChartFrameData struct {
	Title           string
	ChartHeight     string
	ChartHeightNum  float64
	AxisBottom      string
	AxisRight       string
	TopPadLabelY    string
	BottomPadLabelY string
	XLabelY         string
	CenterX         string
	CenterY         string
	MaxLabel        string
	MinLabel        string
	StartAt         int64
	EndAt           int64
	HasData         bool
	Series          []svgSeriesData
}

// mixedChartTemplateData holds all data needed to render a mixed (multi-chart) SVG.
type mixedChartTemplateData struct {
	Width        int
	Height       int
	Title        string
	LeftPad      string
	TopPad       string
	RightPad     string
	BottomPad    string
	PlotWidth    string
	Gap          string
	Charts       []mixedChartFrameData
	ChartOffsets []string
}

// buildMixedSVGData converts a list of chart Documents into mixed chart template data.
func buildMixedSVGData(title string, width, height int, docs []Document) mixedChartTemplateData { // A
	const (
		leftPad   = 48.0
		rightPad  = 16.0
		topPad    = 44.0
		bottomPad = 28.0
		gap       = 8.0
	)

	plotWidth := float64(width) - leftPad - rightPad
	if plotWidth <= 0 {
		plotWidth = 1
	}

	totalGaps := gap * float64(len(docs)-1)
	availablePlotHeight := float64(height) - topPad*float64(len(docs)) - totalGaps - bottomPad
	if availablePlotHeight <= 0 {
		availablePlotHeight = 1
	}
	perChartPlotHeight := availablePlotHeight / float64(len(docs))
	perChartFrameHeight := perChartPlotHeight + topPad

	data := mixedChartTemplateData{
		Width:     width,
		Height:    height,
		Title:     title,
		LeftPad:   formatFloat(leftPad),
		TopPad:    formatFloat(topPad),
		RightPad:  formatFloat(rightPad),
		BottomPad: formatFloat(bottomPad),
		PlotWidth: formatFloat(plotWidth),
		Gap:       formatFloat(gap),
	}

	charts := make([]mixedChartFrameData, 0, len(docs))
	offsets := make([]string, 0, len(docs))

	for i, doc := range docs {
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

		frame := mixedChartFrameData{
			Title:           doc.Title,
			ChartHeight:     formatFloat(perChartFrameHeight),
			ChartHeightNum:  perChartFrameHeight,
			AxisBottom:      formatFloat(topPad + perChartPlotHeight),
			AxisRight:       formatFloat(leftPad + plotWidth),
			TopPadLabelY:    formatFloat(topPad + 8),
			BottomPadLabelY: formatFloat(topPad + perChartPlotHeight),
			XLabelY:         formatFloat(perChartFrameHeight - 4),
			CenterX:         formatFloat(leftPad + plotWidth/2),
			CenterY:         formatFloat(topPad + perChartPlotHeight/2),
			MaxLabel:        formatFloat(maxValue),
			MinLabel:        formatFloat(minValue),
			StartAt:         doc.StartAt,
			EndAt:           doc.EndAt,
			HasData:         hasData,
		}

		if hasData {
			frame.Series = make([]svgSeriesData, 0, len(doc.Series))
			for i, series := range doc.Series {
				if len(series.Values) == 0 {
					continue
				}

				color := palette[i%len(palette)]
				points := make([]string, 0, len(series.Values))
				for _, point := range series.Values {
					x := leftPad + ((float64(point.Timestamp)-start)/(end-start))*plotWidth
					y := topPad + ((maxValue-point.Value)/(maxValue-minValue))*perChartPlotHeight
					points = append(points, formatFloat(x)+","+formatFloat(y))
				}

				frame.Series = append(frame.Series, svgSeriesData{
					Color:  color,
					Points: joinPoints(points),
				})
			}
		}

		charts = append(charts, frame)
		offsets = append(offsets, formatFloat(float64(i)*(perChartFrameHeight+gap)))
	}

	data.Charts = charts
	data.ChartOffsets = offsets

	return data
}

// executeMixedSVGTemplate renders the mixed chart SVG template with the given data.
func executeMixedSVGTemplate(data mixedChartTemplateData) ([]byte, error) { // A
	var buffer bytes.Buffer
	if err := parsedMixedSVGTemplate.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("execute mixed SVG template: %w", err)
	}
	return buffer.Bytes(), nil
}
