package h3

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFallbackDisabledUsesRouterDirectly(t *testing.T) {
	router := NewRouter()
	if handler := newFallbackHandler(router, nil, nil); handler != router {
		t.Errorf("handler = %T, want *Router", handler)
	}
}

func TestAppCustomNotFound(t *testing.T) {
	router := NewRouter()
	router.Build()

	called := false
	app := New(Options{
		Router: router,
		NotFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Pattern != "" {
				t.Errorf("Pattern = %q, want empty", r.Pattern)
			}
			if _, ok := w.(Response); !ok {
				t.Error("NotFound should receive h3.Response")
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"NotFound"}`))
		}),
	})

	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if !called {
		t.Fatal("NotFound was not called")
	}
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "" {
		t.Errorf("X-Content-Type-Options = %q, want empty", got)
	}
	if got := recorder.Body.String(); got != `{"code":"NotFound"}` {
		t.Errorf("body = %q, want JSON error", got)
	}
}

func TestAppCustomMethodNotAllowedPreservesAllow(t *testing.T) {
	router := NewRouter()
	router.HandleFunc("GET /users/{id}", func(http.ResponseWriter, *http.Request) {})
	router.Build()

	called := false
	app := New(Options{
		Router: router,
		MethodNotAllowed: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Pattern != "" {
				t.Errorf("Pattern = %q, want empty", r.Pattern)
			}
			if got := w.Header().Get("Allow"); got != "GET, HEAD" {
				t.Errorf("Allow visible to handler = %q, want %q", got, "GET, HEAD")
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"code":"MethodNotAllowed"}`))
		}),
	})

	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/users/42", nil))

	if !called {
		t.Fatal("MethodNotAllowed was not called")
	}
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	if got := recorder.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
	if got := recorder.Body.String(); got != `{"code":"MethodNotAllowed"}` {
		t.Errorf("body = %q, want JSON error", got)
	}
}

func TestAppRouteStatusIsNotReplaced(t *testing.T) {
	router := NewRouter()
	router.HandleFunc("GET /resource", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("route response"))
	})
	router.Build()

	called := false
	app := New(Options{
		Router: router,
		NotFound: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
		}),
	})

	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/resource", nil))

	if called {
		t.Error("NotFound must not replace a status written by a matched route")
	}
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
	if got := recorder.Body.String(); got != "route response" {
		t.Errorf("body = %q, want %q", got, "route response")
	}
}

func TestAppPreservesServeMuxFallbacks(t *testing.T) {
	router := NewRouter()
	router.Build()

	t.Run("default not found", func(t *testing.T) {
		app := New(Options{Router: router})
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))

		if recorder.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", recorder.Code, http.StatusNotFound)
		}
		if got := recorder.Body.String(); got != "404 page not found\n" {
			t.Errorf("body = %q, want standard library response", got)
		}
	})

	t.Run("path cleaning redirect", func(t *testing.T) {
		called := false
		app := New(Options{
			Router: router,
			NotFound: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}),
		})
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing//path", nil))
		standard := httptest.NewRecorder()
		http.NewServeMux().ServeHTTP(
			standard,
			httptest.NewRequest(http.MethodGet, "/missing//path", nil),
		)

		if called {
			t.Error("NotFound must not replace a ServeMux path redirect")
		}
		if recorder.Code != standard.Code {
			t.Errorf("status = %d, want standard library status %d", recorder.Code, standard.Code)
		}
		if got, want := recorder.Header().Get("Location"), standard.Header().Get("Location"); got != want {
			t.Errorf("Location = %q, want standard library location %q", got, want)
		}
		if got, want := recorder.Body.String(), standard.Body.String(); got != want {
			t.Errorf("body = %q, want standard library body %q", got, want)
		}
	})
}
