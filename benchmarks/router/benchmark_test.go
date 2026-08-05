package routerbench

import (
	"bytes"
	"net/http"
	"testing"
)

const (
	staticPath    = "/users"
	parameterPath = "/users/42"
	missingPath   = "/missing"
	staticBody    = "ok"
	parameterBody = "42"
)

// target 表示一个已经完成路由注册、可以反复处理请求的框架实例。
// newRunner 只在计时开始前创建请求和响应记录器。
type target struct {
	name      string
	newRunner func(path string) *runner
}

// runner 复用同一个请求，并在每次调用前重置响应。
// 基准只执行 serve；status 和 body 仅供正确性测试读取。
type runner struct {
	serve  func()
	status func() int
	body   func() string
}

type responseWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func newResponseWriter() *responseWriter {
	return &responseWriter{header: make(http.Header)}
}

func (w *responseWriter) Header() http.Header {
	return w.header
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *responseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(value)
}

func (w *responseWriter) WriteString(value string) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.WriteString(value)
}

func (w *responseWriter) Reset() {
	clear(w.header)
	w.body.Reset()
	w.status = 0
}

func (w *responseWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func TestRouterTargets(t *testing.T) {
	tests := []struct {
		name            string
		middlewareCount int
		path            string
		status          int
		body            string
	}{
		{name: "static", path: staticPath, status: http.StatusOK, body: staticBody},
		{name: "parameter", path: parameterPath, status: http.StatusOK, body: parameterBody},
		{name: "not found", path: missingPath, status: http.StatusNotFound},
		{name: "three middleware", middlewareCount: 3, path: staticPath, status: http.StatusOK, body: staticBody},
	}

	for _, test := range tests {
		for _, target := range newTargets(test.middlewareCount) {
			t.Run(test.name+"/"+target.name, func(t *testing.T) {
				runner := target.newRunner(test.path)
				runner.serve()

				if status := runner.status(); status != test.status {
					t.Fatalf("status = %d, want %d", status, test.status)
				}
				if body := runner.body(); body != test.body {
					t.Fatalf("body = %q, want %q", body, test.body)
				}
			})
		}
	}
}

func BenchmarkRouterStatic(b *testing.B) {
	benchmarkTargets(b, 0, staticPath)
}

func BenchmarkRouterParameter(b *testing.B) {
	benchmarkTargets(b, 0, parameterPath)
}

func BenchmarkRouterNotFound(b *testing.B) {
	benchmarkTargets(b, 0, missingPath)
}

func BenchmarkRouterThreeMiddleware(b *testing.B) {
	benchmarkTargets(b, 3, staticPath)
}

func benchmarkTargets(b *testing.B, middlewareCount int, path string) {
	b.Helper()

	for _, target := range newTargets(middlewareCount) {
		b.Run(target.name, func(b *testing.B) {
			runner := target.newRunner(path)

			// 预热框架内部的 context pool，并将一次性初始化排除在计时外。
			runner.serve()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				runner.serve()
			}
		})
	}
}
