package main

import (
	"bytes"
	"strings"
	"testing"
)

const testBenchmark = `goos: darwin
goarch: arm64
pkg: go-slim.dev/h3/benchmarks/router
cpu: Test CPU
BenchmarkRouterStatic/h3-8          100  50.00 ns/op  0 B/op  0 allocs/op
BenchmarkRouterStatic/h3-8          100  70.00 ns/op  0 B/op  0 allocs/op
BenchmarkRouterStatic/gin-8         100  25.00 ns/op  8 B/op  1 allocs/op
BenchmarkRouterStatic/gin-8         100  35.00 ns/op  8 B/op  1 allocs/op
BenchmarkRouterNotFound/h3-8        100  180.00 ns/op 80 B/op 5 allocs/op
BenchmarkRouterNotFound/h3-8        100  200.00 ns/op 80 B/op 5 allocs/op
BenchmarkRouterNotFound/gin-8       100  30.00 ns/op  0 B/op 0 allocs/op
BenchmarkRouterNotFound/gin-8       100  32.00 ns/op  0 B/op 0 allocs/op
PASS
`

func TestParseBenchmark(t *testing.T) {
	report, err := parseBenchmark([]byte(testBenchmark))
	if err != nil {
		t.Fatal(err)
	}

	if report.SampleCount != "2 次采样，中位数" {
		t.Fatalf("SampleCount = %q", report.SampleCount)
	}
	if len(report.Scenarios) != 2 || report.Scenarios[0].Name != "Static" {
		t.Fatalf("unexpected scenarios: %#v", report.Scenarios)
	}

	static := report.Scenarios[0]
	if got := static.Metrics[0].Rows[0].Value; got != "60.00 ns/op" {
		t.Fatalf("h3 median = %q", got)
	}
	if got := static.Metrics[0].Rows[1].Value; got != "30.00 ns/op" {
		t.Fatalf("gin median = %q", got)
	}
	if !static.Metrics[0].Rows[1].Fastest {
		t.Fatal("gin should be the fastest static result")
	}
}

func TestRenderReport(t *testing.T) {
	report, err := parseBenchmark([]byte(testBenchmark + "<unsafe>\n"))
	if err != nil {
		t.Fatal(err)
	}
	report.Title = "Router <report>"
	report.GeneratedAt = "2026-08-05 16:00:00 CST"

	var output bytes.Buffer
	if err := renderReport(&output, report); err != nil {
		t.Fatal(err)
	}

	html := output.String()
	for _, expected := range []string{
		"Router &lt;report&gt;",
		"静态路由",
		"60.00 ns/op",
		"--width: 100.00%",
		"&lt;unsafe&gt;",
	} {
		if !strings.Contains(html, expected) {
			t.Fatalf("HTML does not contain %q", expected)
		}
	}
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatal("HTML template rejected a generated CSS value")
	}
}

func TestParseBenchmarkRequiresBenchmem(t *testing.T) {
	_, err := parseBenchmark([]byte("BenchmarkRouterStatic/h3-8 100 50 ns/op\n"))
	if err == nil || !strings.Contains(err.Error(), "-benchmem") {
		t.Fatalf("error = %v", err)
	}
}
