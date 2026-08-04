# h3

[简体中文](README_CN.md)

`go-slim.dev/h3` is a small HTTP application foundation built on the Go
standard library. It retains `http.ServeMux` route syntax and matching
behavior while adding a declarative route tree, middleware composition,
component lifecycle management, HTTP/HTTPS startup, and final response
metadata.

h3 intentionally leaves custom request contexts, binding, content
negotiation, rendering, validation, business error mapping, and tracing to
focused packages, standard `net/http` middleware, and `context.Context`.

## Design principles

- **Standard library first:** route patterns, requests, response writers, and
  contexts retain their `net/http` semantics. h3 adds composition and
  lifecycle management instead of introducing a second HTTP abstraction.
- **Configuration is separate from serving:** one writer configures a Router;
  `Build` creates an immutable snapshot, and requests atomically read that
  snapshot. Locking one map could remove a local race, but cannot define an
  atomic mutation across recursive mounts, shared child Routers, and user
  middleware factories. Full support would require tree-wide snapshots,
  ownership, and lock ordering disproportionate to startup-oriented
  configuration, so mutable configuration is intentionally not
  concurrency-safe while the request path remains lock-free.
- **One lifecycle owner:** App `Start` and `Stop` are sequential control-plane
  operations. Concurrent lifecycle transitions have no single correct order,
  and a mutex cannot define that order for Servlets or user callbacks. One
  application control flow should own lifecycle transitions; HTTP request
  handling remains concurrent.
- **Context does not implicitly own runtime:** the Context passed to `Start`
  controls startup checks and rollback. A running App is finalized by `Stop`
  or by the HTTP service exiting, avoiding accidental cancellation of every
  request when an initialization Context is cancelled.
- **Boundary wrappers stay small:** Response records one final response status
  and size. Interim responses, hijacked connections, and advanced protocol
  details remain accessible through `Unwrap` and `http.ResponseController`.

## Requirements and installation

h3 requires Go 1.25.5 or later. Route patterns use the `http.ServeMux` syntax
introduced in Go 1.22.

```sh
go get go-slim.dev/h3
```

## Quick start

`App.Start` starts the HTTP server in the background. Keep the process alive
in application code and call `App.Stop` explicitly when it should shut down.

```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"go-slim.dev/h3"
)

func main() {
	app := h3.New(h3.Options{Addr: ":8080"})
	app.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("user=" + r.PathValue("id")))
	})

	if err := app.Start(context.Background()); err != nil {
		log.Fatal(err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	<-signals

	if err := app.Stop(); err != nil {
		log.Fatal(err)
	}
}
```

## Router

`Router` records routes, middleware, and child routers. `Build` compiles the
complete declaration into a new `http.ServeMux` and publishes it atomically.

```go
router := h3.NewRouter()
router.HandleFunc("GET /users", listUsers)
router.HandleFunc("GET /users/{id}", getUser)
router.HandleFunc("POST /users", createUser)
router.HandleFunc("GET /files/{path...}", serveFile)
router.Build()
```

The standard library populates path values:

```go
func getUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, _ = w.Write([]byte(id))
}
```

Before its first `Build`, a Router behaves like an empty `http.ServeMux` and
returns 404. Configuration changes require another `Build`; requests that
already selected the previous routing table continue using that table.

Router configuration is not concurrency-safe. Do not concurrently call
`Use`, `Handle`, `HandleFunc`, `Mount`, or `Build`. A published, read-only
routing table can serve requests concurrently. `Build` may also run alongside
requests when no configuration writes occur.

### Duplicate configuration policy

- A later `Handle` or `HandleFunc` with the same pattern string replaces the
  previous handler.
- A later `Mount` with the same normalized prefix replaces the previous child;
  `/api` and `/api/` are the same prefix.
- Different strings that ultimately conflict under `http.ServeMux` are still
  detected by `Build`, which panics. A failed build does not replace the
  routing table that is already published.

### Mounting and complete patterns

`Mount` expands child patterns during compilation. It does not use
`http.StripPrefix`, so handlers see the original request URL and `r.Pattern`
contains every mount prefix.

```go
api := h3.NewRouter()
api.HandleFunc("GET /health", health)

root := h3.NewRouter()
root.Mount("/api", api)
root.Build()
// Final pattern: GET /api/health
```

Host-qualified standard-library patterns can also be mounted. Only the path
receives the prefix; the method and host remain unchanged:

```go
tenant := h3.NewRouter()
tenant.HandleFunc("GET example.com/users", users)

root.Mount("/api", tenant)
root.Build()
// Final pattern: GET example.com/api/users
```

A mount prefix is always a path: `"api"` is normalized to `"/api"`. Child
route patterns follow ServeMux grammar: in `"GET api/users"`, `api` is a host;
a path must be written as `"GET /api/users"`.

Mounting a nil Router, a direct cycle, or an indirect cycle panics immediately.
Building a parent publishes only the parent's complete routing table; it does
not publish the child Router's standalone table. Call `Build` on a child
explicitly when it must also serve independently.

### Middleware

Middleware uses the standard `func(http.Handler) http.Handler` shape. Earlier
middleware is outermost, and parent Router middleware wraps child Router
middleware.

```go
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("started method=%s path=%s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("finished method=%s path=%s", r.Method, r.URL.Path)
	})
}

router.Use(requestLog)
```

`Use` records middleware. `Build` and `CompileRoutes` construct a fixed
handler chain for every final route, so middleware factories are not called
during requests. A nil middleware panics in `Use`; a factory that returns a
nil handler panics during compilation. Middleware itself must synchronize any
state captured and shared across requests; request-local state belongs inside
`ServeHTTP`.

### CompileRoutes

`CompileRoutes` returns `[]h3.Route` with mount prefixes expanded and
middleware already composed. It is useful when registering h3 declarations
with another facility compatible with `http.ServeMux`. Result order is
undefined because declarations are stored in maps. Most applications should
simply call `Build`.

### 404 and 405 responses

A standalone `Router` retains the default text responses from
`http.ServeMux`. When serving through `App`, use `Options.NotFound` and
`Options.MethodNotAllowed` to provide JSON or another application protocol:

```go
func jsonError(status int, code string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   false,
			"code": code,
		})
	})
}

app := h3.New(h3.Options{
	Router:           router,
	NotFound:         jsonError(http.StatusNotFound, "NotFound"),
	MethodNotAllowed: jsonError(http.StatusMethodNotAllowed, "MethodNotAllowed"),
})
```

`http.ServeMux` still decides whether a request is a 404 or 405; h3 does not
reimplement host, wildcard, or GET/HEAD matching. A custom 405 handler receives
the `Allow` header calculated by ServeMux. Only routing-layer 404/405 responses
are replaced: a matched handler that deliberately writes either status is left
unchanged. A nil handler preserves the standard-library response. When both
handlers are nil, App selects Router directly during `New`, so requests do not
create the additional route-error response wrapper.

Route-error handlers live at the App boundary and do not belong to a registered
Route, so `Router.Use` middleware is not applied to them automatically. Wrap
these handlers with the same standard middleware when route misses also need
logging or tracing.

## App lifecycle

`App` combines a Router, `http.Server`, and Servlets, and itself implements
`Servlet`.

- `Start(ctx)` builds routes, synchronously creates the TCP listener, starts
  Servlets, and then runs HTTP or HTTPS in the background. It is non-blocking.
- Because the listener is created before `Start` returns, invalid addresses,
  permission failures, and occupied ports are reported reliably.
- `Stop()` first calls `http.Server.Shutdown`, waits for in-flight requests,
  then calls every `Servlet.Stop` in reverse registration order, returning all
  shutdown errors as one error.
- After `Stop` completes, the same App may be started again.
- `Options.OnStopped` is called once after the HTTP service exits and every
  Servlet has stopped. Its `manual` argument is true only when `Stop`
  initiated shutdown. Its error joins serving, shutdown, and Servlet stop
  errors.

The Context passed to `Start` controls Servlet startup and initialization, such
as a database connectivity check. App checks cancellation before startup and
before and after each Servlet starts. If cancellation is observed before
startup completes, App closes the listener and stops every successfully
started Servlet in reverse order. Cancelling that Context after `Start`
succeeds does not stop the running App; runtime shutdown remains explicit
through `Stop`.

An unexpected error after the HTTP serving loop has started is written to
`Options.ErrorLog` (or the standard logger). App then shuts down the HTTP
server, stops every Servlet in reverse order, and invokes `OnStopped` with
`manual=false` and the combined error. Startup failures reported directly by
`Start` do not invoke `OnStopped`.

Lifecycle operations are intentionally sequential and lock-free: do not call
`Start` and `Stop` concurrently. Calling `Start` while the App is running
returns an error; calling `Stop` while it is not running returns nil. This
constraint does not limit concurrent HTTP handlers; it only requires one
application control flow to own App lifecycle transitions.

`OnStopped` runs after every Servlet has stopped, but a manual `Stop` does not
wait for the callback to finish. It is a completion notification rather than
a second resource-cleanup hook. Because the App can be started again after
`Stop` returns, the callback may overlap the next run. Use `Run`, or wait for
the callback explicitly, when runs must be strictly separated.

```go
if err := app.Start(context.Background()); err != nil {
	return err
}
// ...
return app.Stop()
```

For development and other blocking entry points, `Run` combines `Start` with
waiting for `OnStopped` and returns the same final error. It preserves and
invokes an already configured callback. Its Context still controls only
startup; cancelling it after startup does not stop the App.
Call `Run` only for an App that is not running, and do not run it concurrently
with other lifecycle operations.

```go
if err := h3.Run(context.Background(), app); err != nil {
	log.Fatal(err)
}
```

## Component and Servlet

A `Component` consists of a mount prefix and child Router. `App.Register`
mounts its routes. If the component also implements `Servlet`, App manages its
startup and shutdown.

Register components only while the App is stopped, and do not reuse a mount
prefix. Repeated `Router.Mount` calls can mean "replace this route declaration
before building." A Component may also own a Servlet; replacing only its
routes would leave the old resource in the lifecycle list. `Register`
therefore panics on duplicate prefixes to keep route ownership and resource
ownership aligned.

```go
type workerComponent struct {
	h3.Component
}

func newWorkerComponent() *workerComponent {
	component := &workerComponent{Component: h3.NewComponent("/workers")}
	component.Router().HandleFunc("GET /status", workerStatus)
	return component
}

func (c *workerComponent) Start(ctx context.Context) error {
	// Initialize resources or perform startup checks.
	return nil
}

func (c *workerComponent) Stop() error {
	// Stop background work and release resources.
	return nil
}

app.Register(newWorkerComponent())
```

Embed `h3.Component`, not `*h3.Component`: `Component` is an interface and
`NewComponent` returns an implementation of it.

When a resource has lifecycle but no routes, register it directly with
`RegisterServlet`; it does not need an empty Component:

```go
type databaseServlet struct {
	db *sql.DB
}

func (s *databaseServlet) Start(ctx context.Context) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return err
	}
	s.db = db
	return nil
}

func (s *databaseServlet) Stop() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

app.RegisterServlet(&databaseServlet{})
```

`Register` automatically enrolls a Component that also implements `Servlet`.
Do not pass the same object to `RegisterServlet` as well, or its lifecycle will
be enrolled twice. All Servlets start in registration order and stop in reverse
order.

## TLS

A non-nil `Options.TLSConfig` makes App serve HTTPS with
`http.Server.ServeTLS`. The configuration must provide a server certificate
through `Certificates`, `GetCertificate`, or `GetConfigForClient`; otherwise
`Start` returns an error before creating the listener.

```go
certificate, err := tls.LoadX509KeyPair("server.crt", "server.key")
if err != nil {
	return err
}

app := h3.New(h3.Options{
	Addr: ":8443",
	TLSConfig: &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{certificate},
	},
})
```

With an empty `Addr`, plain HTTP uses `:http` (port 80) and TLS uses `:https`
(port 443). An explicit address always wins, so ports such as `:8443` remain
available.

## Response

App wraps the `http.ResponseWriter` passed to handlers as `h3.Response`, which
records the final status, response body bytes, and commit state. A standalone
Router does not wrap the writer; callers can use `h3.NewResponse` explicitly
when metadata is needed.

```go
func responseLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		response := w.(h3.Response)
		log.Printf(
			"status=%d size=%d committed=%t",
			response.Status(), response.Size(), response.Committed(),
		)
	})
}
```

`NewResponse` is idempotent. `Response` also provides `Flush`, `Hijack`,
`Push`, and `Unwrap`:

- `Hijack` and `Push` return an error when the underlying writer does not
  support them.
- `Flush` retains the `http.Flusher` signature and panics when the underlying
  writer cannot flush.
- Response records one final status. Write interim responses such as
  `100 Continue` or `103 Early Hints` through `Unwrap`, then write the final
  response through Response.

```go
response := w.(h3.Response)
response.Unwrap().WriteHeader(http.StatusEarlyHints)
response.WriteHeader(http.StatusOK)
```

h3 does not attempt to emulate every optional `http.ResponseWriter` capability
or protocol detail. Advanced code can use the underlying writer through
`Unwrap` or `http.ResponseController`.

Like the standard `http.ResponseWriter`, Response does not support
unsynchronized concurrent writes. Locking only its counters would not define
correct Header and body commit ordering across goroutines; callers generating
content concurrently should aggregate or synchronize before writing.

## Server options

`Options` maps supported `http.Server` settings, including the listen address,
read/write/idle timeouts, TLS, connection-state hooks, serving completion,
error logging, HTTP/2 configuration, and protocols.
When `PassOptionsStar` is true, the special `OPTIONS *` request is passed to
the application Handler; by default `net/http` answers it directly. This does
not affect OPTIONS routes for ordinary paths.

```go
app := h3.New(h3.Options{
	Router:            router,
	Addr:              ":8080",
	ReadHeaderTimeout: 5 * time.Second,
	IdleTimeout:       60 * time.Second,
	OnStopped: func(manual bool, err error) {
		if !manual {
			log.Printf("HTTP server exited: %v", err)
		}
	},
})
```

## What h3 does not provide

h3 has no custom request Context, binder, renderer, validator, central error
pipeline, or tracing implementation. Keep request-scoped data in
`r.Context()`. For example, `go-slim.dev/binding` can bind request input and
`go-slim.dev/nego` can handle media-type matching and Accept negotiation.

## Verification

```sh
go test ./...
go vet ./...
go test -race ./...
```

## License

MIT. See [LICENSE](LICENSE).
