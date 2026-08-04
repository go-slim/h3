package h3

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8080"})

	if app == nil {
		t.Fatal("New returned nil")
	}

	if app.options.Addr != ":8080" {
		t.Errorf("addr = %q, want %q", app.options.Addr, ":8080")
	}

	if app.router == nil {
		t.Error("mux should not be nil")
	}
}

func TestListenAddress(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		want    string
	}{
		{
			name: "default HTTP",
			want: ":http",
		},
		{
			name:    "default HTTPS",
			options: Options{TLSConfig: &tls.Config{}},
			want:    ":https",
		},
		{
			name:    "explicit HTTP address",
			options: Options{Addr: "127.0.0.1:8080"},
			want:    "127.0.0.1:8080",
		},
		{
			name: "explicit TLS address",
			options: Options{
				Addr:      "127.0.0.1:8443",
				TLSConfig: &tls.Config{},
			},
			want: "127.0.0.1:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenAddress(&tt.options); got != tt.want {
				t.Fatalf("listenAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppWrapsResponseWriter(t *testing.T) {
	router := NewRouter()
	router.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(Response); !ok {
			t.Error("App should wrap ResponseWriter")
		}
	})

	app := New(Options{Router: router})
	router.Build()
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test", nil))
}

func TestAppUse(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8080"})

	called := false
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			next.ServeHTTP(w, r)
		})
	})

	// Add a handler to test middleware
	app.router.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Verify middleware was added by making a test request
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = app.Stop() }()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:8080/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if !called {
		t.Error("middleware was not called")
	}
}

func TestAppRegister(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8081"})

	// Create a component
	c := NewComponent("/api")
	c.Router().HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Register component
	app.Register(c)

	// Start server
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = app.Stop() }()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test component route
	resp, err := http.Get("http://localhost:8081/api/status")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", string(body), "ok")
	}
}

func TestAppRegisterRejectsAmbiguousLifecycleOwnership(t *testing.T) {
	t.Run("nil component", func(t *testing.T) {
		app := New()
		defer func() {
			if recovered := recover(); recovered != "h3: nil component" {
				t.Fatalf("Register panic = %v, want h3: nil component", recovered)
			}
		}()
		app.Register(nil)
	})

	t.Run("duplicate prefix", func(t *testing.T) {
		app := New()
		app.Register(NewComponent("/api/"))

		defer func() {
			if recover() == nil {
				t.Fatal("Register should panic for a duplicate normalized prefix")
			}
		}()
		app.Register(NewComponent("/api"))
	})

	t.Run("started app", func(t *testing.T) {
		app := New(Options{Addr: "127.0.0.1:0"})
		if err := app.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = app.Stop() }()

		defer func() {
			if recover() == nil {
				t.Fatal("Register should panic while App is started")
			}
		}()
		app.Register(NewComponent("/api"))
	})
}

func TestAppRegisterServlet(t *testing.T) {
	addr := unusedTestAddress(t)
	app := New(Options{Addr: addr})
	servlet := newMockServlet()

	app.RegisterServlet(servlet)
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !servlet.wasStartCalled() {
		t.Fatal("Servlet.Start was not called")
	}

	if err := app.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !servlet.wasStopCalled() {
		t.Fatal("Servlet.Stop was not called")
	}
}

func TestAppRegisterServletRejectsInvalidRegistration(t *testing.T) {
	t.Run("nil servlet", func(t *testing.T) {
		app := New()
		defer func() {
			if recovered := recover(); recovered != "h3: nil servlet" {
				t.Fatalf("RegisterServlet panic = %v, want h3: nil servlet", recovered)
			}
		}()
		app.RegisterServlet(nil)
	})

	t.Run("started app", func(t *testing.T) {
		app := New(Options{Addr: "127.0.0.1:0"})
		if err := app.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = app.Stop() }()

		defer func() {
			if recovered := recover(); recovered != "h3: cannot register servlet while app is started" {
				t.Fatalf("RegisterServlet panic = %v, want started-app panic", recovered)
			}
		}()
		app.RegisterServlet(newMockServlet())
	})
}

func TestAppStartStop(t *testing.T) {
	mux := NewRouter()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("running"))
	})

	app := New(Options{Router: mux, Addr: ":8082"})
	ctx := context.Background()

	// Start server
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is running
	resp, err := http.Get("http://localhost:8082/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Stop server
	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Give server time to stop
	time.Sleep(100 * time.Millisecond)

	// Verify server is stopped
	_, err = http.Get("http://localhost:8082/test")
	if err == nil {
		t.Error("expected error when connecting to stopped server")
	}
}

func TestAppInvalidAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
	}{
		{"no port", "localhost"},
		{"invalid format", ":::"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewRouter()
			app := New(Options{Router: mux, Addr: tt.addr})
			ctx := context.Background()

			err := app.Start(ctx)
			if err == nil {
				_ = app.Stop()
				t.Error("Start should fail with invalid address")
			}
		})
	}
}

func TestAppStartReturnsListenErrorSynchronously(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	app := New(Options{Addr: listener.Addr().String()})
	servlet := newMockServletComponent("/servlet")
	app.Register(servlet)

	err = app.Start(context.Background())
	if err == nil {
		t.Fatal("Start should return an error when the address is already in use")
	}
	if servlet.wasStartCalled() {
		t.Fatal("Servlet.Start should not run when the listener cannot be created")
	}
}

func TestAppStartRejectsTLSConfigWithoutCertificate(t *testing.T) {
	app := New(Options{
		Addr:      "127.0.0.1:0",
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	})

	err := app.Start(context.Background())
	if err == nil {
		t.Fatal("Start should reject TLSConfig without a server certificate")
	}
}

func TestAppServesTLSWhenConfigured(t *testing.T) {
	certificateSource := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := certificateSource.Client()
	config := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: certificateSource.TLS.Certificates,
	}
	certificateSource.Close()
	defer client.CloseIdleConnections()

	addr := unusedTestAddress(t)
	router := NewRouter()
	router.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secure"))
	})
	app := New(Options{Router: router, Addr: addr, TLSConfig: config})
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start TLS app: %v", err)
	}
	defer func() { _ = app.Stop() }()

	response, err := client.Get("https://" + addr + "/")
	if err != nil {
		t.Fatalf("GET TLS app: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(body); got != "secure" {
		t.Errorf("body = %q, want secure", got)
	}
}

func TestAppCancelingStartupContextDoesNotStopStartedApp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	addr := unusedTestAddress(t)
	app := New(Options{Addr: addr})
	app.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("running"))
	})
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = app.Stop() }()

	cancel()
	response, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET after startup Context cancellation: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(body); got != "running" {
		t.Errorf("body = %q, want running", got)
	}
}

func unusedTestAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return addr
}

func TestAppValidAddress(t *testing.T) {
	tests := []struct {
		name string
		addr string
		port int
	}{
		{"localhost with port", ":8083", 8083},
		{"explicit localhost", "localhost:8084", 8084},
		{"ipv4", "127.0.0.1:8085", 8085},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewRouter()
			app := New(Options{Router: mux, Addr: tt.addr})
			ctx := context.Background()

			if err := app.Start(ctx); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			defer func() { _ = app.Stop() }()

			time.Sleep(100 * time.Millisecond)

			// Try to connect
			url := fmt.Sprintf("http://localhost:%d/", tt.port)
			resp, err := http.Get(url)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()
		})
	}
}

func TestAppMultipleComponents(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8086"})

	// Register multiple components
	apiComponent := NewComponent("/api")
	apiComponent.Router().HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("api"))
	})

	adminComponent := NewComponent("/admin")
	adminComponent.Router().HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("admin"))
	})

	app.Register(apiComponent)
	app.Register(adminComponent)

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = app.Stop() }()

	time.Sleep(100 * time.Millisecond)

	// Test both components
	tests := []struct {
		path string
		want string
	}{
		{"/api/status", "api"},
		{"/admin/status", "admin"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := http.Get("http://localhost:8086" + tt.path)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer func() { resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("ReadAll failed: %v", err)
			}
			if string(body) != tt.want {
				t.Errorf("body = %q, want %q", string(body), tt.want)
			}
		})
	}
}

func TestAppGracefulShutdown(t *testing.T) {
	mux := NewRouter()

	// Add a slow handler
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("done"))
	})

	app := New(Options{Router: mux, Addr: ":8087"})
	ctx := context.Background()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Start a slow request
	done := make(chan bool)
	go func() {
		resp, err := http.Get("http://localhost:8087/slow")
		if err != nil {
			t.Errorf("slow request failed: %v", err)
		} else {
			resp.Body.Close()
		}
		done <- true
	}()

	// Give the request time to start
	time.Sleep(50 * time.Millisecond)

	// Stop server (should wait for slow request)
	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify the slow request completed
	select {
	case <-done:
		// Request completed successfully
	case <-time.After(1 * time.Second):
		t.Error("slow request did not complete")
	}
}

func TestAppContextPropagation(t *testing.T) {
	mux := NewRouter()

	contextReceived := false
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		if r.Context() != nil {
			contextReceived = true
		}
		w.Write([]byte("ok"))
	})

	app := New(Options{Router: mux, Addr: ":8088"})
	ctx := context.Background()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = app.Stop() }()

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:8088/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()

	if !contextReceived {
		t.Error("handler did not receive context")
	}
}

func TestAppWithMiddlewareAndComponents(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8089"})

	// Add global middleware
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Global", "true")
			next.ServeHTTP(w, r)
		})
	})

	// Create component with its own middleware
	c := NewComponent("/api")
	c.Router().Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Component", "true")
			next.ServeHTTP(w, r)
		})
	})

	c.Router().HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	app.Register(c)

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = app.Stop() }()

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:8089/api/test")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Global") != "true" {
		t.Error("global middleware not executed")
	}

	if resp.Header.Get("X-Component") != "true" {
		t.Error("component middleware not executed")
	}
}

func TestAppStartMultipleTimes(t *testing.T) {
	addr := unusedTestAddress(t)
	router := NewRouter()
	router.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	serveDone := make(chan struct {
		manual bool
		err    error
	}, 2)

	app := New(Options{
		Router: router,
		Addr:   addr,
		OnStopped: func(manual bool, err error) {
			serveDone <- struct {
				manual bool
				err    error
			}{manual: manual, err: err}
		},
	})
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()

	request := func(cycle int) {
		response, err := client.Get("http://" + addr + "/test")
		if err != nil {
			t.Fatalf("cycle %d GET: %v", cycle, err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("cycle %d ReadAll: %v", cycle, err)
		}
		if got := string(body); got != "ok" {
			t.Errorf("cycle %d body = %q, want ok", cycle, got)
		}
	}

	for cycle := 1; cycle <= 2; cycle++ {
		if err := app.Start(context.Background()); err != nil {
			t.Fatalf("cycle %d Start: %v", cycle, err)
		}
		request(cycle)
		if err := app.Stop(); err != nil {
			t.Fatalf("cycle %d Stop: %v", cycle, err)
		}
		select {
		case result := <-serveDone:
			if !result.manual {
				t.Errorf("cycle %d OnStopped manual = false", cycle)
			}
			if result.err != nil {
				t.Errorf("cycle %d OnStopped error = %v", cycle, result.err)
			}
		case <-time.After(time.Second):
			t.Fatalf("cycle %d OnStopped was not called", cycle)
		}
	}
}

func TestAppOnStoppedRunsAfterServletStop(t *testing.T) {
	type stoppedResult struct {
		manual         bool
		err            error
		servletStopped bool
	}

	servlet := newMockServletComponent("/servlet")
	stopErr := errors.New("servlet stop failed")
	servlet.stopError = stopErr
	stopped := make(chan stoppedResult, 1)
	app := New(Options{
		Addr: "127.0.0.1:0",
		OnStopped: func(manual bool, err error) {
			stopped <- stoppedResult{
				manual:         manual,
				err:            err,
				servletStopped: servlet.wasStopCalled(),
			}
		},
	})
	app.Register(servlet)

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(); !errors.Is(err, stopErr) {
		t.Fatalf("Stop error = %v, want %v", err, stopErr)
	}

	select {
	case result := <-stopped:
		if !result.manual {
			t.Error("OnStopped manual = false, want true")
		}
		if !errors.Is(result.err, stopErr) {
			t.Errorf("OnStopped error = %v, want %v", result.err, stopErr)
		}
		if !result.servletStopped {
			t.Error("OnStopped ran before Servlet.Stop")
		}
	case <-time.After(time.Second):
		t.Fatal("OnStopped was not called")
	}
}

func TestAppStopDoesNotWaitForOnStopped(t *testing.T) {
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	app := New(Options{
		Addr: "127.0.0.1:0",
		OnStopped: func(bool, error) {
			close(callbackStarted)
			<-releaseCallback
		},
	})
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stopDone := make(chan error, 1)
	go func() { stopDone <- app.Stop() }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(time.Second):
		close(releaseCallback)
		t.Fatal("Stop waited for OnStopped")
	}
	select {
	case <-callbackStarted:
		close(releaseCallback)
	case <-time.After(time.Second):
		close(releaseCallback)
		t.Fatal("OnStopped was not called")
	}
}

func TestAppStartRejectsAlreadyStartedApp(t *testing.T) {
	app := New(Options{Addr: "127.0.0.1:0"})
	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer func() { _ = app.Stop() }()

	err := app.Start(context.Background())
	if err == nil || err.Error() != "h3: app is already started" {
		t.Fatalf("second Start error = %v", err)
	}
}

func TestAppStopWithoutStart(t *testing.T) {
	if err := New().Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// mockServletComponent 实现了 Component 和 Servlet 接口的测试组件
type mockServletComponent struct {
	*component
	startCalled bool
	stopCalled  bool
	startError  error
	stopError   error
	mu          sync.Mutex
}

type cancelingServletComponent struct {
	*mockServletComponent
	cancel context.CancelFunc
}

func (c *cancelingServletComponent) Start(ctx context.Context) error {
	if err := c.mockServletComponent.Start(ctx); err != nil {
		return err
	}
	c.cancel()
	return nil
}

func newMockServletComponent(prefix string) *mockServletComponent {
	return &mockServletComponent{
		component: NewComponent(prefix).(*component),
	}
}

func (c *mockServletComponent) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.startCalled = true
	return c.startError
}

func (c *mockServletComponent) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopCalled = true
	return c.stopError
}

func (c *mockServletComponent) wasStartCalled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.startCalled
}

func (c *mockServletComponent) wasStopCalled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopCalled
}

func TestAppServletLifecycle(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8091"})

	// 创建实现了 Servlet 接口的组件
	servlet := newMockServletComponent("/servlet")
	servlet.Router().HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	app.Register(servlet)

	ctx := context.Background()

	// 启动服务器应该调用 Servlet.Start
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if !servlet.wasStartCalled() {
		t.Error("Servlet.Start was not called")
	}

	// 停止服务器应该调用 Servlet.Stop
	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !servlet.wasStopCalled() {
		t.Error("Servlet.Stop was not called")
	}
}

func TestAppStartRollsBackWhenContextIsCanceledDuringStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := &cancelingServletComponent{
		mockServletComponent: newMockServletComponent("/first"),
		cancel:               cancel,
	}
	second := newMockServletComponent("/second")
	app := New(Options{Addr: "127.0.0.1:0"})
	app.Register(first)
	app.Register(second)

	err := app.Start(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want %v", err, context.Canceled)
	}
	if !first.wasStartCalled() {
		t.Fatal("first Servlet.Start was not called")
	}
	if !first.wasStopCalled() {
		t.Fatal("started Servlet was not stopped during rollback")
	}
	if second.wasStartCalled() {
		t.Fatal("next Servlet.Start ran after startup context cancellation")
	}
}

func TestAppServletStartError(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8092"})

	// 创建会在 Start 时返回错误的组件
	servlet := newMockServletComponent("/servlet")
	servlet.startError = errors.New("start failed")

	app.Register(servlet)

	ctx := context.Background()

	// 启动服务器应该失败
	err := app.Start(ctx)
	if err == nil {
		_ = app.Stop()
		t.Fatal("Start should fail when Servlet.Start returns error")
	}

	if err.Error() != "start failed" {
		t.Errorf("error = %q, want %q", err.Error(), "start failed")
	}
}

func TestAppMultipleServlets(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8093"})

	// 创建多个 Servlet 组件
	servlet1 := newMockServletComponent("/servlet1")
	servlet2 := newMockServletComponent("/servlet2")
	servlet3 := newMockServletComponent("/servlet3")

	app.Register(servlet1)
	app.Register(servlet2)
	app.Register(servlet3)

	ctx := context.Background()

	// 启动服务器
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 验证所有 Servlet 都被启动
	if !servlet1.wasStartCalled() {
		t.Error("servlet1.Start was not called")
	}
	if !servlet2.wasStartCalled() {
		t.Error("servlet2.Start was not called")
	}
	if !servlet3.wasStartCalled() {
		t.Error("servlet3.Start was not called")
	}

	// 停止服务器
	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// 验证所有 Servlet 都被停止（应该按逆序）
	if !servlet1.wasStopCalled() {
		t.Error("servlet1.Stop was not called")
	}
	if !servlet2.wasStopCalled() {
		t.Error("servlet2.Stop was not called")
	}
	if !servlet3.wasStopCalled() {
		t.Error("servlet3.Stop was not called")
	}
}

func TestAppStopJoinsServletErrors(t *testing.T) {
	app := New(Options{Addr: "127.0.0.1:0"})
	firstError := errors.New("first stop failed")
	secondError := errors.New("second stop failed")
	first := newMockServletComponent("/first")
	first.stopError = firstError
	second := newMockServletComponent("/second")
	second.stopError = secondError
	app.Register(first)
	app.Register(second)

	if err := app.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err := app.Stop()
	if !errors.Is(err, firstError) {
		t.Errorf("Stop error %v does not contain %v", err, firstError)
	}
	if !errors.Is(err, secondError) {
		t.Errorf("Stop error %v does not contain %v", err, secondError)
	}
}

// servletWithOrder 用于测试 Stop 调用顺序
type servletWithOrder struct {
	*mockServletComponent
	id        int
	stopOrder *[]int
	mu        *sync.Mutex
}

func (s *servletWithOrder) Stop() error {
	s.mu.Lock()
	*s.stopOrder = append(*s.stopOrder, s.id)
	s.mu.Unlock()
	return s.mockServletComponent.Stop()
}

func TestAppServletStopOrder(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8094"})

	// 记录 Stop 调用顺序
	var stopOrder []int
	var mu sync.Mutex

	createServlet := func(id int, prefix string) *servletWithOrder {
		return &servletWithOrder{
			mockServletComponent: newMockServletComponent(prefix),
			id:                   id,
			stopOrder:            &stopOrder,
			mu:                   &mu,
		}
	}

	servlet1 := createServlet(1, "/s1")
	servlet2 := createServlet(2, "/s2")
	servlet3 := createServlet(3, "/s3")

	app.Register(servlet1)
	app.Register(servlet2)
	app.Register(servlet3)

	ctx := context.Background()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// 验证 Stop 按逆序调用：3, 2, 1
	mu.Lock()
	defer mu.Unlock()

	if len(stopOrder) != 3 {
		t.Fatalf("stopOrder length = %d, want 3", len(stopOrder))
	}

	expectedOrder := []int{3, 2, 1}
	for i, id := range stopOrder {
		if id != expectedOrder[i] {
			t.Errorf("stopOrder[%d] = %d, want %d", i, id, expectedOrder[i])
		}
	}
}

func TestAppMixedComponents(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8095"})

	// 注册普通组件（不实现 Servlet）
	normalComponent := NewComponent("/normal")
	normalComponent.Router().HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("normal"))
	})

	// 注册 Servlet 组件
	servlet := newMockServletComponent("/servlet")
	servlet.Router().HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("servlet"))
	})

	app.Register(normalComponent)
	app.Register(servlet)

	ctx := context.Background()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 只有 Servlet 组件应该被启动
	if !servlet.wasStartCalled() {
		t.Error("servlet.Start was not called")
	}

	// 测试两个组件的路由都正常工作
	resp, err := http.Get("http://localhost:8095/normal/test")
	if err != nil {
		t.Fatalf("GET /normal/test failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "normal" {
		t.Errorf("normal component body = %q, want %q", string(body), "normal")
	}

	resp, err = http.Get("http://localhost:8095/servlet/test")
	if err != nil {
		t.Fatalf("GET /servlet/test failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "servlet" {
		t.Errorf("servlet component body = %q, want %q", string(body), "servlet")
	}

	if err := app.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if !servlet.wasStopCalled() {
		t.Error("servlet.Stop was not called")
	}
}

// servletWithContextCapture 用于捕获传入的 context
type servletWithContextCapture struct {
	*mockServletComponent
	receivedCtx *context.Context
}

func (s *servletWithContextCapture) Start(ctx context.Context) error {
	*s.receivedCtx = ctx
	return s.mockServletComponent.Start(ctx)
}

func TestAppServletWithContext(t *testing.T) {
	mux := NewRouter()
	app := New(Options{Router: mux, Addr: ":8096"})

	var receivedCtx context.Context
	servlet := &servletWithContextCapture{
		mockServletComponent: newMockServletComponent("/servlet"),
		receivedCtx:          &receivedCtx,
	}

	app.Register(servlet)

	// 创建带值的 context
	ctx := context.WithValue(context.Background(), "test", "value") //nolint:staticcheck // SA1029: test code

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 验证 Servlet.Start 收到了正确的 context
	if receivedCtx == nil {
		t.Fatal("Servlet.Start did not receive context")
	}

	if receivedCtx.Value("test") != "value" {
		t.Error("Servlet.Start received wrong context")
	}

	err := app.Stop()
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}
