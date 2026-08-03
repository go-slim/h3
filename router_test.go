package h3

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func buildRouter(t *testing.T, router *Router) {
	t.Helper()
	router.Build()
}

func TestNewRouter(t *testing.T) {
	mux := NewRouter()
	if mux == nil {
		t.Fatal("NewRouter returned nil")
	}
}

func TestRouterHandle(t *testing.T) {
	mux := NewRouter()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	mux.Handle("GET /test", handler)
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
	}
}

func TestRouterHandleFunc(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello")
	}
}

func TestRouterHandler(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("test"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	h, pattern := mux.Handler(req)

	if h == nil {
		t.Fatal("Handler returned nil handler")
	}

	if pattern != "GET /test" {
		t.Errorf("pattern = %q, want %q", pattern, "GET /test")
	}
}

func TestRouterUse(t *testing.T) {
	mux := NewRouter()

	order := []string{}

	// First middleware
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "first-before")
			next.ServeHTTP(w, r)
			order = append(order, "first-after")
		})
	})

	// Second middleware
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "second-before")
			next.ServeHTTP(w, r)
			order = append(order, "second-after")
		})
	})

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.Write([]byte("ok"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	expected := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if len(order) != len(expected) {
		t.Fatalf("execution order length = %d, want %d", len(order), len(expected))
	}

	for i, got := range order {
		if got != expected[i] {
			t.Errorf("order[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestRouterMiddlewareWithHeader(t *testing.T) {
	mux := NewRouter()

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Custom", "middleware")
			next.ServeHTTP(w, r)
		})
	})

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Custom"); got != "middleware" {
		t.Errorf("X-Custom header = %q, want %q", got, "middleware")
	}
}

func TestRouterUseAfterBuildRequiresRebuild(t *testing.T) {
	mux := NewRouter()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {})
	buildRouter(t, mux)

	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rebuilt", "true")
			next.ServeHTTP(w, r)
		})
	})

	// 已发布的路由表仍保持可用；配置变更只有在 Build 后才会生效。
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
	if got := rec.Header().Get("X-Rebuilt"); got != "" {
		t.Errorf("X-Rebuilt before rebuild = %q, want empty", got)
	}

	buildRouter(t, mux)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/test", nil))
	if got := rec.Header().Get("X-Rebuilt"); got != "true" {
		t.Errorf("X-Rebuilt header = %q, want true", got)
	}
}

func TestRouterBuildAtomicallyReplacesActiveMux(t *testing.T) {
	router := NewRouter()
	router.HandleFunc("GET /first", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	})

	router.Build()
	previous := router.mux.Load()
	if previous == nil {
		t.Fatal("Build did not publish its ServeMux")
	}

	router.HandleFunc("GET /second", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	})
	router.Build()

	if current := router.mux.Load(); current == previous {
		t.Fatal("Build did not replace the active ServeMux")
	}

	// 在重建时已经取得旧路由表的请求仍使用旧表。
	oldResponse := httptest.NewRecorder()
	previous.ServeHTTP(oldResponse, httptest.NewRequest(http.MethodGet, "/second", nil))
	if oldResponse.Code != http.StatusNotFound {
		t.Errorf("old ServeMux status = %d, want %d", oldResponse.Code, http.StatusNotFound)
	}

	// 新请求从 Router 原子读取当前路由表。
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/second", nil))
	if response.Code != http.StatusOK {
		t.Errorf("current Router status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Body.String(); got != "second" {
		t.Errorf("current Router body = %q, want %q", got, "second")
	}
}

func TestRouterCompileRoutesReturnsExpandedRoutes(t *testing.T) {
	child := NewRouter()
	child.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("healthy"))
	})

	router := NewRouter()
	router.Mount("/api", child)

	routes := router.CompileRoutes()
	if len(routes) != 1 {
		t.Fatalf("CompileRoutes count = %d, want 1", len(routes))
	}

	route := routes[0]
	if route.Pattern != "GET /api/health" {
		t.Errorf("Route.Pattern = %q, want %q", route.Pattern, "GET /api/health")
	}
	if route.Handler == nil {
		t.Fatal("Route.Handler is nil")
	}
}

func TestRouterCompileRoutesPrefixesHostPatterns(t *testing.T) {
	child := NewRouter()
	child.HandleFunc("GET\t\texample.com/users", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("users"))
	})
	child.HandleFunc("admin.example.com/status", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("status"))
	})

	router := NewRouter()
	router.Mount("/api", child)

	routes := router.CompileRoutes()
	patterns := make(map[string]bool, len(routes))
	for _, route := range routes {
		patterns[route.Pattern] = true
	}
	if !patterns["GET example.com/api/users"] {
		t.Errorf("compiled patterns = %v, missing host and method pattern", patterns)
	}
	if !patterns["admin.example.com/api/status"] {
		t.Errorf("compiled patterns = %v, missing host pattern", patterns)
	}

	router.Build()
	tests := []struct {
		url  string
		want string
	}{
		{"http://example.com/api/users", "users"},
		{"http://admin.example.com/api/status", "status"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.url, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", test.url, recorder.Code, http.StatusOK)
		}
		if got := recorder.Body.String(); got != test.want {
			t.Errorf("GET %s body = %q, want %q", test.url, got, test.want)
		}
	}
}

func TestRouterDuplicateConfigurationReplacesPreviousValue(t *testing.T) {
	router := NewRouter()
	router.HandleFunc("GET /value", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("first"))
	})
	router.HandleFunc("GET /value", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("second"))
	})

	firstChild := NewRouter()
	firstChild.HandleFunc("GET /first", func(w http.ResponseWriter, r *http.Request) {})
	secondChild := NewRouter()
	secondChild.HandleFunc("GET /second", func(w http.ResponseWriter, r *http.Request) {})
	router.Mount("/api/", firstChild)
	router.Mount("/api", secondChild)
	router.Build()

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/value", nil))
	if got := recorder.Body.String(); got != "second" {
		t.Errorf("duplicate handler body = %q, want second", got)
	}

	firstRecorder := httptest.NewRecorder()
	router.ServeHTTP(firstRecorder, httptest.NewRequest(http.MethodGet, "/api/first", nil))
	if firstRecorder.Code != http.StatusNotFound {
		t.Errorf("replaced mount status = %d, want %d", firstRecorder.Code, http.StatusNotFound)
	}
	secondRecorder := httptest.NewRecorder()
	router.ServeHTTP(secondRecorder, httptest.NewRequest(http.MethodGet, "/api/second", nil))
	if secondRecorder.Code != http.StatusOK {
		t.Errorf("replacement mount status = %d, want %d", secondRecorder.Code, http.StatusOK)
	}
}

func TestRouterRejectsInvalidMiddleware(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("Use(nil) should panic")
			}
		}()
		NewRouter().Use(nil)
	})

	t.Run("nil handler", func(t *testing.T) {
		router := NewRouter()
		router.Use(func(http.Handler) http.Handler { return nil })
		router.HandleFunc("GET /", func(http.ResponseWriter, *http.Request) {})

		defer func() {
			if recover() == nil {
				t.Fatal("Build should panic when middleware returns nil")
			}
		}()
		router.Build()
	})
}

func TestRouterMountRejectsNilAndCycles(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("Mount with nil router should panic")
			}
		}()
		NewRouter().Mount("/api", nil)
	})

	t.Run("self", func(t *testing.T) {
		router := NewRouter()
		defer func() {
			if recover() == nil {
				t.Fatal("mounting a router into itself should panic")
			}
		}()
		router.Mount("/self", router)
	})

	t.Run("indirect", func(t *testing.T) {
		root := NewRouter()
		child := NewRouter()
		grandchild := NewRouter()
		root.Mount("/child", child)
		child.Mount("/grandchild", grandchild)

		defer func() {
			if recover() == nil {
				t.Fatal("indirect cyclic mount should panic")
			}
		}()
		grandchild.Mount("/root", root)
	})
}

func TestRouterMount(t *testing.T) {
	// Create sub-mux
	apiMux := NewRouter()
	apiMux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users"))
	})
	apiMux.HandleFunc("GET /posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("posts"))
	})

	// Mount to main mux
	mux := NewRouter()
	mux.Mount("/api", apiMux)
	buildRouter(t, mux)

	tests := []struct {
		path string
		want string
	}{
		{"/api/users", "users"},
		{"/api/posts", "posts"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}

			if rec.Body.String() != tt.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestRouterBuildDoesNotPublishMountedRouter(t *testing.T) {
	child := NewRouter()
	child.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("healthy"))
	})

	parent := NewRouter()
	parent.Mount("/api", child)
	parent.Build()

	// 父 Router 会展开子 Router 的配置，但不发布子 Router 自己的路由表。
	childResponse := httptest.NewRecorder()
	child.ServeHTTP(childResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if childResponse.Code != http.StatusNotFound {
		t.Errorf("unbuilt child status = %d, want %d", childResponse.Code, http.StatusNotFound)
	}

	parentResponse := httptest.NewRecorder()
	parent.ServeHTTP(parentResponse, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if parentResponse.Code != http.StatusOK {
		t.Errorf("parent status = %d, want %d", parentResponse.Code, http.StatusOK)
	}
	if got := parentResponse.Body.String(); got != "healthy" {
		t.Errorf("parent body = %q, want healthy", got)
	}
}

func TestRouterMountRoot(t *testing.T) {
	subMux := NewRouter()
	subMux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("root mount"))
	})

	mux := NewRouter()
	mux.Mount("/", subMux)
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "root mount" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "root mount")
	}
}

func TestRouterMountWithTrailingSlash(t *testing.T) {
	subMux := NewRouter()
	subMux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	mux := NewRouter()
	mux.Mount("/api/", subMux) // trailing slash
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRouterMountPanic(t *testing.T) {
	mux := NewRouter()
	subMux := NewRouter()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Mount with empty pattern should panic")
		}
	}()

	mux.Mount("", subMux)
}

func TestRouterHandlePanic(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		handler http.Handler
	}{
		{"empty pattern", "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})},
		{"nil handler", "GET /test", nil},
		{"nil HandlerFunc", "GET /test", http.HandlerFunc(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewRouter()

			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Handle(%q, %v) should panic", tt.pattern, tt.handler)
				}
			}()

			mux.Handle(tt.pattern, tt.handler)
		})
	}
}

func TestRouterMethodMatching(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("GET"))
	})

	mux.HandleFunc("POST /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("POST"))
	})
	buildRouter(t, mux)

	tests := []struct {
		method string
		want   string
		status int
	}{
		{"GET", "GET", http.StatusOK},
		{"POST", "POST", http.StatusOK},
		{"PUT", "", http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			rec := httptest.NewRecorder()

			mux.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Errorf("status = %d, want %d", rec.Code, tt.status)
			}

			if tt.status == http.StatusOK && rec.Body.String() != tt.want {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestRouterPathParameters(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		w.Write([]byte("user-" + id))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/users/123", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "user-123" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "user-123")
	}
}

func TestRouterWildcard(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /files/{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		w.Write([]byte("path:" + path))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/files/a/b/c.txt", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if !strings.Contains(rec.Body.String(), "a/b/c.txt") {
		t.Errorf("body = %q, should contain %q", rec.Body.String(), "a/b/c.txt")
	}
}

func TestRouterNestedMount(t *testing.T) {
	// Create nested muxes
	usersMux := NewRouter()
	usersMux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("users"))
	})

	apiMux := NewRouter()
	apiMux.Mount("/users", usersMux)

	mainMux := NewRouter()
	mainMux.Mount("/api", apiMux)
	buildRouter(t, mainMux)

	req := httptest.NewRequest("GET", "/api/users/", nil)
	rec := httptest.NewRecorder()

	mainMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "users" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "users")
	}
}

func TestRouterWithoutMiddleware(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("no middleware"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "no middleware" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "no middleware")
	}
}

func TestRouterDoesNotWrapResponseWriter(t *testing.T) {
	mux := NewRouter()

	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(Response); ok {
			t.Error("Router should not wrap ResponseWriter")
		}
		w.Write([]byte("ok"))
	})
	buildRouter(t, mux)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)
}
