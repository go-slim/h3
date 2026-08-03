package h3

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestRunWaitsForOnStopped(t *testing.T) {
	addr := runTestAddress(t)
	callback := make(chan struct {
		manual bool
		err    error
	}, 1)
	app := New(Options{
		Addr: addr,
		OnStopped: func(manual bool, err error) {
			callback <- struct {
				manual bool
				err    error
			}{manual: manual, err: err}
		},
	})
	app.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("running"))
	})

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), app)
	}()

	waitForApp(t, addr)
	select {
	case err := <-done:
		t.Fatalf("Run returned before Stop: %v", err)
	default:
	}

	if err := app.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Stop")
	}

	select {
	case result := <-callback:
		if !result.manual {
			t.Error("OnStopped manual = false, want true")
		}
		if result.err != nil {
			t.Errorf("OnStopped error = %v, want nil", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("configured OnStopped was not called")
	}
}

func TestRunReturnsStartError(t *testing.T) {
	app := New(Options{Addr: "invalid"})

	err := Run(context.Background(), app)
	if err == nil {
		t.Fatal("Run error = nil, want address error")
	}
	if app.options.OnStopped != nil {
		t.Fatal("Run did not restore OnStopped after Start error")
	}
}

func TestRunReturnsStopError(t *testing.T) {
	addr := runTestAddress(t)
	stopErr := errors.New("stop failed")
	servlet := &runTestServlet{
		Component: NewComponent("/servlet"),
		stopErr:   stopErr,
	}
	app := New(Options{Addr: addr})
	app.Register(servlet)

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), app)
	}()

	waitForApp(t, addr)
	if err := app.Stop(); !errors.Is(err, stopErr) {
		t.Fatalf("Stop error = %v, want %v", err, stopErr)
	}
	select {
	case err := <-done:
		if !errors.Is(err, stopErr) {
			t.Fatalf("Run error = %v, want %v", err, stopErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

type runTestServlet struct {
	Component
	stopErr error
}

func (*runTestServlet) Start(context.Context) error {
	return nil
}

func (servlet *runTestServlet) Stop() error {
	return servlet.stopErr
}

func TestSafeCall(t *testing.T) {
	t.Run("return error", func(t *testing.T) {
		want := errors.New("failed")
		if got := safeCall(func() error { return want }); !errors.Is(got, want) {
			t.Fatalf("safeCall error = %v, want %v", got, want)
		}
	})

	t.Run("panic error", func(t *testing.T) {
		want := errors.New("panicked")
		if got := safeCall(func() error { panic(want) }); !errors.Is(got, want) {
			t.Fatalf("safeCall error = %v, want %v", got, want)
		}
	})

	t.Run("panic value", func(t *testing.T) {
		if got := safeCall(func() error { panic("panicked") }); got == nil || got.Error() != "panicked" {
			t.Fatalf("safeCall error = %v, want panicked", got)
		}
	})
}

func runTestAddress(t *testing.T) string {
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

func waitForApp(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(time.Second)
	for {
		response, err := client.Get("http://" + addr + "/")
		if err == nil {
			_ = response.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("app did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
