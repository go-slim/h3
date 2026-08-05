package routerbench

import (
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v3"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fasthttp"
	"go-slim.dev/h3"
	"gopkg.in/macaron.v1"
)

func newTargets(middlewareCount int) []target {
	return []target{
		newH3Target(middlewareCount),
		newNetHTTPTarget(middlewareCount),
		newEchoTarget(middlewareCount),
		newGinTarget(middlewareCount),
		newChiTarget(middlewareCount),
		newMacaronTarget(middlewareCount),
		newFiberTarget(middlewareCount),
	}
}

func newNetHTTPRunner(handler http.Handler, path string) *runner {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	response := newResponseWriter()

	return &runner{
		serve: func() {
			response.Reset()
			handler.ServeHTTP(response, request)
		},
		status: response.Status,
		body:   response.body.String,
	}
}

func standardMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(w, request)
	})
}

func newH3Target(middlewareCount int) target {
	router := h3.NewRouter()
	for range middlewareCount {
		router.Use(standardMiddleware)
	}
	router.HandleFunc("GET "+staticPath, writeStatic)
	router.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.PathValue("id"))
	})
	router.HandleFunc("/", writeNotFound)
	router.Build()

	return target{
		name:      "h3",
		newRunner: func(path string) *runner { return newNetHTTPRunner(router, path) },
	}
}

func newNetHTTPTarget(middlewareCount int) target {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+staticPath, writeStatic)
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, request.PathValue("id"))
	})
	mux.HandleFunc("/", writeNotFound)

	var handler http.Handler = mux
	for range middlewareCount {
		handler = standardMiddleware(handler)
	}

	return target{
		name:      "net_http",
		newRunner: func(path string) *runner { return newNetHTTPRunner(handler, path) },
	}
}

func newEchoTarget(middlewareCount int) target {
	router := echo.New()
	for range middlewareCount {
		router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(context echo.Context) error {
				return next(context)
			}
		})
	}
	router.GET(staticPath, func(context echo.Context) error {
		_, err := io.WriteString(context.Response().Writer, staticBody)
		return err
	})
	router.GET("/users/:id", func(context echo.Context) error {
		_, err := io.WriteString(context.Response().Writer, context.Param("id"))
		return err
	})
	router.RouteNotFound("/*", func(context echo.Context) error {
		context.Response().WriteHeader(http.StatusNotFound)
		return nil
	})

	return target{
		name:      "echo",
		newRunner: func(path string) *runner { return newNetHTTPRunner(router, path) },
	}
}

func newGinTarget(middlewareCount int) target {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	for range middlewareCount {
		router.Use(func(context *gin.Context) {
			context.Next()
		})
	}
	router.GET(staticPath, func(context *gin.Context) {
		_, _ = context.Writer.WriteString(staticBody)
	})
	router.GET("/users/:id", func(context *gin.Context) {
		_, _ = context.Writer.WriteString(context.Param("id"))
	})
	router.NoRoute(func(context *gin.Context) {
		context.Status(http.StatusNotFound)
		context.Writer.WriteHeaderNow()
	})

	return target{
		name:      "gin",
		newRunner: func(path string) *runner { return newNetHTTPRunner(router, path) },
	}
}

func newChiTarget(middlewareCount int) target {
	router := chi.NewRouter()
	for range middlewareCount {
		router.Use(standardMiddleware)
	}
	router.Get(staticPath, writeStatic)
	router.Get("/users/{id}", func(w http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(w, chi.URLParam(request, "id"))
	})
	router.NotFound(writeNotFound)

	return target{
		name:      "chi",
		newRunner: func(path string) *runner { return newNetHTTPRunner(router, path) },
	}
}

func newMacaronTarget(middlewareCount int) target {
	router := macaron.New()
	for range middlewareCount {
		router.Use(func(context *macaron.Context) {
			context.Next()
		})
	}
	router.Get(staticPath, func(context *macaron.Context) {
		_, _ = io.WriteString(context.Resp, staticBody)
	})
	router.Get("/users/:id", func(context *macaron.Context) {
		_, _ = io.WriteString(context.Resp, context.Params("id"))
	})
	router.NotFound(func(context *macaron.Context) {
		context.Resp.WriteHeader(http.StatusNotFound)
	})

	return target{
		name:      "macaron",
		newRunner: func(path string) *runner { return newNetHTTPRunner(router, path) },
	}
}

func newFiberTarget(middlewareCount int) target {
	router := fiber.New()
	for range middlewareCount {
		router.Use(func(context fiber.Ctx) error {
			return context.Next()
		})
	}
	router.Get(staticPath, func(context fiber.Ctx) error {
		_, err := context.WriteString(staticBody)
		return err
	})
	router.Get("/users/:id", func(context fiber.Ctx) error {
		_, err := context.WriteString(context.Params("id"))
		return err
	})
	router.Use(func(context fiber.Ctx) error {
		context.Status(http.StatusNotFound)
		return nil
	})

	handler := router.Handler()
	return target{
		name: "fiber",
		newRunner: func(path string) *runner {
			requestContext := new(fasthttp.RequestCtx)
			requestContext.Request.Header.SetMethod(http.MethodGet)
			requestContext.Request.SetRequestURI(path)

			return &runner{
				serve: func() {
					requestContext.Response.Reset()
					handler(requestContext)
				},
				status: requestContext.Response.StatusCode,
				body: func() string {
					return string(requestContext.Response.Body())
				},
			}
		},
	}
}

func writeStatic(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, staticBody)
}

func writeNotFound(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
