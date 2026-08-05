package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var benchmarkNamePattern = regexp.MustCompile(`^BenchmarkRouter([^/]+)/(.+)-[0-9]+$`)

var (
	scenarioOrder  = []string{"Static", "Parameter", "NotFound", "ThreeMiddleware"}
	frameworkOrder = []string{"h3", "net_http", "echo", "gin", "chi", "macaron", "fiber"}
)

type reportData struct {
	Title       string
	GeneratedAt string
	Environment []environmentValue
	SampleCount string
	Scenarios   []scenarioData
	Raw         string
}

type environmentValue struct {
	Name  string
	Value string
}

type scenarioData struct {
	Name    string
	Title   string
	Metrics []metricData
}

type metricData struct {
	Name string
	Rows []metricRow
}

type metricRow struct {
	Framework string
	Value     string
	Relative  string
	Width     float64
	H3        bool
	Fastest   bool
}

type benchmarkSeries struct {
	nanoseconds []float64
	bytes       []float64
	allocations []float64
}

type benchmarkMedian struct {
	framework   string
	nanoseconds float64
	bytes       float64
	allocations float64
}

func parseBenchmark(input []byte) (reportData, error) {
	environment := make(map[string]string)
	series := make(map[string]map[string]*benchmarkSeries)

	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if key, value, ok := parseEnvironment(line); ok {
			environment[key] = value
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name := benchmarkNamePattern.FindStringSubmatch(fields[0])
		if name == nil {
			continue
		}

		metrics, err := parseMetrics(fields[2:])
		if err != nil {
			return reportData{}, fmt.Errorf("parse %s: %w", fields[0], err)
		}
		nanoseconds, nsOK := metrics["ns/op"]
		bytesPerOperation, bytesOK := metrics["B/op"]
		allocations, allocsOK := metrics["allocs/op"]
		if !nsOK || !bytesOK || !allocsOK {
			return reportData{}, fmt.Errorf(
				"benchmark %s must contain ns/op, B/op, and allocs/op; run go test with -benchmem",
				fields[0],
			)
		}

		scenario, framework := name[1], name[2]
		if series[scenario] == nil {
			series[scenario] = make(map[string]*benchmarkSeries)
		}
		values := series[scenario][framework]
		if values == nil {
			values = new(benchmarkSeries)
			series[scenario][framework] = values
		}
		values.nanoseconds = append(values.nanoseconds, nanoseconds)
		values.bytes = append(values.bytes, bytesPerOperation)
		values.allocations = append(values.allocations, allocations)
	}
	if err := scanner.Err(); err != nil {
		return reportData{}, fmt.Errorf("scan benchmark output: %w", err)
	}
	if len(series) == 0 {
		return reportData{}, errors.New("no BenchmarkRouter results found")
	}

	return reportData{
		Environment: orderedEnvironment(environment),
		SampleCount: sampleCountLabel(series),
		Scenarios:   buildScenarios(series),
		Raw:         string(input),
	}, nil
}

func parseEnvironment(line string) (key, value string, ok bool) {
	key, value, ok = strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	switch key {
	case "goos", "goarch", "pkg", "cpu":
		return key, strings.TrimSpace(value), true
	default:
		return "", "", false
	}
}

func parseMetrics(fields []string) (map[string]float64, error) {
	metrics := make(map[string]float64)
	for index := 0; index+1 < len(fields); index += 2 {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid metric %q: %w", fields[index], err)
		}
		metrics[fields[index+1]] = value
	}
	return metrics, nil
}

func orderedEnvironment(values map[string]string) []environmentValue {
	names := []string{"goos", "goarch", "cpu", "pkg"}
	result := make([]environmentValue, 0, len(names))
	for _, name := range names {
		if value := values[name]; value != "" {
			result = append(result, environmentValue{Name: name, Value: value})
		}
	}
	return result
}

func sampleCountLabel(series map[string]map[string]*benchmarkSeries) string {
	counts := make(map[int]struct{})
	for _, frameworks := range series {
		for _, values := range frameworks {
			counts[len(values.nanoseconds)] = struct{}{}
		}
	}
	if len(counts) != 1 {
		return "各项样本数不同"
	}
	for count := range counts {
		return strconv.Itoa(count) + " 次采样，中位数"
	}
	return ""
}

func buildScenarios(series map[string]map[string]*benchmarkSeries) []scenarioData {
	names := orderedNames(series, scenarioOrder)
	result := make([]scenarioData, 0, len(names))
	for _, name := range names {
		medians := buildMedians(series[name])
		result = append(result, scenarioData{
			Name:  name,
			Title: scenarioTitle(name),
			Metrics: []metricData{
				buildMetric("耗时 · 越低越好", medians, func(value benchmarkMedian) float64 {
					return value.nanoseconds
				}, formatDuration),
				buildMetric("内存 · 越低越好", medians, func(value benchmarkMedian) float64 {
					return value.bytes
				}, formatBytes),
				buildMetric("分配 · 越低越好", medians, func(value benchmarkMedian) float64 {
					return value.allocations
				}, formatAllocations),
			},
		})
	}
	return result
}

func buildMedians(series map[string]*benchmarkSeries) []benchmarkMedian {
	names := orderedNames(series, frameworkOrder)
	result := make([]benchmarkMedian, 0, len(names))
	for _, name := range names {
		values := series[name]
		result = append(result, benchmarkMedian{
			framework:   name,
			nanoseconds: median(values.nanoseconds),
			bytes:       median(values.bytes),
			allocations: median(values.allocations),
		})
	}
	return result
}

func buildMetric(
	name string,
	medians []benchmarkMedian,
	valueOf func(benchmarkMedian) float64,
	format func(float64) string,
) metricData {
	minimum, maximum := valueOf(medians[0]), valueOf(medians[0])
	for _, value := range medians[1:] {
		minimum = min(minimum, valueOf(value))
		maximum = max(maximum, valueOf(value))
	}

	rows := make([]metricRow, 0, len(medians))
	for _, median := range medians {
		value := valueOf(median)
		width := 0.0
		if maximum > 0 {
			width = value / maximum * 100
		}
		rows = append(rows, metricRow{
			Framework: frameworkTitle(median.framework),
			Value:     format(value),
			Relative:  relativeValue(value, minimum),
			Width:     width,
			H3:        median.framework == "h3",
			Fastest:   value == minimum,
		})
	}

	return metricData{Name: name, Rows: rows}
}

func orderedNames[V any](values map[string]V, preferred []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, name := range preferred {
		if _, ok := values[name]; ok {
			result = append(result, name)
			seen[name] = struct{}{}
		}
	}

	var remaining []string
	for name := range values {
		if _, ok := seen[name]; !ok {
			remaining = append(remaining, name)
		}
	}
	slices.Sort(remaining)
	return append(result, remaining...)
}

func median(values []float64) float64 {
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func relativeValue(value, minimum float64) string {
	if value == minimum {
		return "最佳"
	}
	if minimum == 0 {
		return "产生分配"
	}
	return fmt.Sprintf("%.2f×", value/minimum)
}

func formatDuration(value float64) string {
	if value >= 1000 {
		return fmt.Sprintf("%.2f µs/op", value/1000)
	}
	return fmt.Sprintf("%.2f ns/op", value)
}

func formatBytes(value float64) string {
	return fmt.Sprintf("%.0f B/op", value)
}

func formatAllocations(value float64) string {
	return fmt.Sprintf("%.0f allocs/op", value)
}

func scenarioTitle(name string) string {
	switch name {
	case "Static":
		return "静态路由"
	case "Parameter":
		return "路径参数"
	case "NotFound":
		return "404 未匹配"
	case "ThreeMiddleware":
		return "三层中间件"
	default:
		return name
	}
}

func frameworkTitle(name string) string {
	if name == "net_http" {
		return "net/http"
	}
	return name
}
