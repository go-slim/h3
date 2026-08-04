# h3

[English](README.md)

`go-slim.dev/h3` 是建立在 Go 标准库之上的小型 HTTP 应用基础库。它以
`http.ServeMux` 的路由语法和匹配行为为基础，补充声明式路由树、中间件组合、
组件生命周期、HTTP/HTTPS 启停以及最终响应元数据记录。

h3 有意不包含自定义请求 Context、请求绑定、内容协商、渲染、校验、业务错误映射
和 tracing。这些能力适合通过独立包、标准 `net/http` 中间件和
`context.Context` 组合。

## 设计原则

- **标准库优先**：路由语法、Request、ResponseWriter 和 Context 均保持
  `net/http` 的语义。h3 只补充组合与生命周期，不建立第二套 HTTP 抽象。
- **配置与运行分离**：Router 由单一写者配置，`Build` 生成不可变快照，
  请求只原子读取快照。给单个 map 加锁只能消除局部 race，不能定义
  递归挂载、共享子 Router 和用户中间件工厂的原子修改语义。完整支持
  需要跨路由树的快照、所有权和锁顺序，与以配置期为主的用途不成比例，
  因此可变配置刻意不提供并发安全；请求路径仍然无锁。
- **生命周期只有一个所有者**：App 的 `Start`/`Stop` 是顺序控制面操作。
  并发启停并没有唯一正确的顺序，加锁也无法替 Servlet 和用户回调定义
  该语义，所以由应用的一个控制流负责启停。HTTP 请求处理仍然可以并发。
- **Context 不隐式接管运行期**：传给 `Start` 的 Context 用于启动检查和回滚；
  已启动的 App 通过 `Stop` 或 HTTP 服务退出收尾，避免初始化 Context 的意外
  取消中断所有正在处理的请求。
- **边界能力保持最小**：Response 只记录一个最终响应的状态和大小。
  1xx、劫持连接及其他高级协议细节仍可通过 `Unwrap` 或
  `http.ResponseController` 交还标准库处理。

## 要求与安装

h3 要求 Go 1.25.5 或更高版本。路由模式使用 Go 1.22 起
`http.ServeMux` 支持的语法。

```sh
go get go-slim.dev/h3
```

## 快速开始

`App.Start` 在后台启动 HTTP 服务。应用代码自行保持进程运行，并在需要关闭时显式
调用 `App.Stop`。

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

`Router` 记录路由、中间件和子 Router，调用 `Build` 后才将完整配置编译为一个新的
`http.ServeMux` 并原子发布。

```go
router := h3.NewRouter()
router.HandleFunc("GET /users", listUsers)
router.HandleFunc("GET /users/{id}", getUser)
router.HandleFunc("POST /users", createUser)
router.HandleFunc("GET /files/{path...}", serveFile)
router.Build()
```

路径参数由标准库填充：

```go
func getUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_, _ = w.Write([]byte(id))
}
```

`Router` 在第一次 `Build` 前等同于一个空 `http.ServeMux`，请求会得到 404。修改
配置后，必须再次调用 `Build` 才会发布新配置；已经进入旧路由表的请求不受影响。

Router 的配置不是并发安全的。不要并发调用 `Use`、`Handle`、`HandleFunc`、
`Mount` 或 `Build`。这项限制只适用于可变配置；已发布的只读路由表
可以安全地并发处理请求，`Build` 也可以在没有配置写入时与请求并发。

### 重复配置策略

- 相同字符串 pattern 的后一次 `Handle` 或 `HandleFunc` 替换前一次配置。
- 相同规范化挂载前缀的后一次 `Mount` 替换前一次配置，例如 `/api` 与 `/api/`
  视为同一个前缀。
- 不同字符串最终产生冲突的 `http.ServeMux` 模式仍由 `Build` 检出并 panic；失败的
  Build 不会替换已经发布的路由表。

### 挂载与完整模式

`Mount` 在编译阶段展开子 Router 的模式，不使用 `http.StripPrefix`，因此处理器看到的
请求 URL 保持不变，`r.Pattern` 是包含所有挂载前缀的最终模式。

```go
api := h3.NewRouter()
api.HandleFunc("GET /health", health)

root := h3.NewRouter()
root.Mount("/api", api)
root.Build()
// 最终模式：GET /api/health
```

带 host 的标准库模式同样可以挂载。只有路径部分会添加前缀，方法和 host 保持不变：

```go
tenant := h3.NewRouter()
tenant.HandleFunc("GET example.com/users", users)

root.Mount("/api", tenant)
root.Build()
// 最终模式：GET example.com/api/users
```

`Mount` 的 prefix 始终是路径，`"api"` 会被规范化为 `"/api"`。子路由
模式则严格遵循 ServeMux 语法：`"GET api/users"` 中的 `api` 是 host，
路径必须写成 `"GET /api/users"`。

挂载 nil Router、直接循环挂载或间接循环挂载会立即 panic。父 Router 的 `Build` 只发布
父 Router 的完整路由表，不会顺便发布子 Router 自己的路由表；需要独立使用子 Router
时，应显式调用子 Router 的 `Build`。

### 中间件

中间件使用标准的 `func(http.Handler) http.Handler` 形式。越早注册的中间件越在
外层；父 Router 的中间件包裹子 Router 的中间件。

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

`Use` 只记录中间件。`Build`/`CompileRoutes` 会为每条最终路由构造固定的处理器链，
请求期间不会重复构造。nil 中间件会在 `Use` 时 panic；中间件工厂返回 nil handler
会在编译时 panic。工厂捕获并由多个请求共享的状态，应由中间件自身
保证并发安全；单个请求的状态应在 `ServeHTTP` 内部创建。

### CompileRoutes

`CompileRoutes` 返回展开挂载前缀并组合好中间件的 `[]h3.Route`。它适合需要把 h3
路由声明注册到其他兼容 `http.ServeMux` 的设施中。结果顺序未定义，调用方不应依赖
映射遍历顺序。通常直接调用 `Build` 即可。

### 404 与 405 响应

`Router` 单独使用时保留 `http.ServeMux` 的默认文本响应。通过 `App`
服务时，可以用 `Options.NotFound` 和 `Options.MethodNotAllowed` 替换为
JSON 或其他应用协议：

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

h3 仍由 `http.ServeMux` 判断 404 和 405，不重新实现 host、通配符、
GET/HEAD 等匹配规则。405 Handler 能够读取 ServeMux 计算的 `Allow`
响应头。仅路由层生成的 404/405 会被替换；已匹配 Handler 自己写入
的 404/405 保持不变。nil Handler 保留标准库默认响应。
两个 Handler 均为 nil 时，App 在 `New` 期间直接选用 Router，请求路径
不会创建额外的路由错误响应包装。

路由错误 Handler 位于 App 边界，不属于任何已注册 Route，因此不会
自动经过 `Router.Use` 中间件。需要为路由错误记录日志或 tracing 时，
可用同一个标准库中间件包装这两个 Handler。

## App 生命周期

`App` 组合 Router、`http.Server` 和 Servlet，并且自身也实现 `Servlet`。

- `Start(ctx)` 编译路由、同步创建 TCP Listener、启动 Servlet，然后在后台运行
  HTTP/HTTPS 服务；它是非阻塞方法。
- Listener 在 `Start` 返回前创建，因此地址错误、权限不足和端口占用可以可靠返回。
- `Stop()` 先执行 `http.Server.Shutdown`，等待在途请求完成，再按注册逆序调用所有
  `Servlet.Stop`，并组合返回全部关闭错误。
- `Stop` 完成后，同一个 App 可以再次启动。
- HTTP 服务退出且所有 Servlet 已停止后，会调用一次
  `Options.OnStopped`。只有由 `Stop` 触发时 `manual` 才为 true；
  err 会合并 Serve、Shutdown 和 Servlet.Stop 的错误。

传给 `Start` 的 Context 控制 Servlet 的启动和初始化，例如数据库连通性检查。App
在启动前以及每个 Servlet 启动前后检查取消状态；启动完成前观察到取消时，会关闭
Listener，并逆序停止已经成功启动的 Servlet。`Start` 成功返回后再取消该 Context，
不会停止已经运行的 App；运行期关闭仍由 `Stop` 显式控制。

HTTP 服务循环启动后发生的异常会写入 `Options.ErrorLog`（未配置时使用标准 logger）。
App 随后会关闭 HTTP 服务、逆序停止所有 Servlet，最后以 `manual=false`
及合并后的错误调用 `OnStopped`。`Start` 直接返回的启动失败不会触发该回调。

生命周期操作刻意保持为顺序、无锁模型：不要并发调用 `Start` 和 `Stop`。App 运行时
再次调用 `Start` 会返回错误；App 未运行时调用 `Stop` 返回 nil。
这个约束不会限制 HTTP handler 的并发执行；它只要求应用为 App 指定一个
生命周期控制流。

`OnStopped` 在 Servlet 全部停止后调用，但手动 `Stop` 不等待回调执行完成。
它是完成通知，不是第二套资源清理钩子。需要阻塞到回调完成时，使用
`Run`。由于 `Stop` 返回后 App 已可再次启动，回调也可能与下一轮运行重叠；
需要严格分隔运行周期的入口应使用 `Run` 或自行等待回调。

```go
if err := app.Start(context.Background()); err != nil {
	return err
}
// ...
return app.Stop()
```

开发环境或其他需要阻塞入口的场景可使用 `Run`。它组合 `Start` 与
对 `OnStopped` 的等待，并返回相同的最终错误；已配置的回调仍会正常执行。
`Run` 的 Context 仍只控制启动阶段，启动后取消它不会停止 App。
`Run` 只应在 App 未运行时调用，不应与其他生命周期操作并发。

```go
if err := h3.Run(context.Background(), app); err != nil {
	log.Fatal(err)
}
```

## Component 与 Servlet

`Component` 由挂载前缀和子 Router 组成。`App.Register` 挂载其路由；如果组件同时
实现 `Servlet`，App 还会管理它的启动和停止。

Component 必须在 App 未运行时注册，且挂载前缀不能重复。Router 的重复
`Mount` 可以表示“构建前替换一份路由声明”；Component 还可能拥有 Servlet，
如果只替换路由而保留旧 Servlet，会使路由所有权与资源所有权分离，
因此 `Register` 对重复前缀直接 panic。

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
	// 初始化资源或执行启动检查。
	return nil
}

func (c *workerComponent) Stop() error {
	// 停止后台工作并释放资源。
	return nil
}

app.Register(newWorkerComponent())
```

应嵌入 `h3.Component`，而不是 `*h3.Component`：`Component` 是接口，
`NewComponent` 返回该接口的实现。

如果资源只有生命周期而没有路由，直接使用 `RegisterServlet`，不需要创建空的
Component：

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

`Register` 自动注册同时实现 `Servlet` 的 Component；不要再把同一对象传给
`RegisterServlet`，否则其生命周期会被登记两次。所有 Servlet 按登记顺序启动，
并按相反顺序停止。

## TLS

`Options.TLSConfig` 非 nil 时，App 使用 `http.Server.ServeTLS` 提供 HTTPS。配置必须
通过 `Certificates`、`GetCertificate` 或 `GetConfigForClient` 提供服务端证书；否则
`Start` 会在创建 Listener 前返回错误。

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

`Addr` 为空时，普通 HTTP 使用 `:http`（端口 80），启用 TLS 后使用
`:https`（端口 443）。显式设置的地址始终优先，因此仍可使用 `:8443` 等端口。

## Response

App 会把传给处理器的 `http.ResponseWriter` 包装为 `h3.Response`，记录最终状态码、
响应体字节数和提交状态。独立使用 Router 时不会自动包装；需要这些信息时可由调用方
显式调用 `h3.NewResponse`。

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

`NewResponse` 是幂等的。`Response` 还提供 `Flush`、`Hijack`、`Push` 和 `Unwrap`：

- `Hijack` 和 `Push` 在底层 writer 不支持时返回错误。
- `Flush` 保持 `http.Flusher` 的签名；底层 writer 不支持时会 panic。
- Response 只记录一个最终响应状态。需要发送 `100 Continue`、`103 Early Hints`
  等临时响应时，应通过 `Unwrap` 写入临时响应，再通过 Response 写入最终响应。

```go
response := w.(h3.Response)
response.Unwrap().WriteHeader(http.StatusEarlyHints)
response.WriteHeader(http.StatusOK)
```

h3 不尝试模拟 `http.ResponseWriter` 的全部可选能力与所有协议细节；复杂场景可以通过
`Unwrap` 或 `http.ResponseController` 直接使用底层 writer。

Response 与标准 `http.ResponseWriter` 一样不支持无协调的并发写入。为它的计数字段
加锁不能定义多个 goroutine 之间正确的 Header 和 body 提交顺序，因此需要
并发生成内容时，应在写入 Response 前由调用方汇总或同步。

## 服务配置

`Options` 映射 h3 支持的 `http.Server` 配置，包括监听地址、读/写/空闲超时、TLS、
连接状态回调、Serve 完成回调、错误日志、HTTP/2 配置与协议集。
`PassOptionsStar` 为 true 时会将特殊的 `OPTIONS *` 请求传递给应用
Handler；默认由 `net/http` 直接响应。该选项不影响普通路径的 OPTIONS 路由。

```go
app := h3.New(h3.Options{
	Router:            router,
	Addr:              ":8080",
	ReadHeaderTimeout: 5 * time.Second,
	IdleTimeout:       60 * time.Second,
	OnStopped: func(manual bool, err error) {
		if !manual {
			log.Printf("HTTP 服务异常退出：%v", err)
		}
	},
})
```

## h3 不提供的能力

h3 没有自定义请求 Context、binder、renderer、validator、集中式错误处理管线或
tracing 实现。请求范围的数据应存入 `r.Context()`。例如，可以使用
`go-slim.dev/binding` 绑定请求输入，使用 `go-slim.dev/nego` 进行媒体类型判断和
Accept 协商。

## 验证

```sh
go test ./...
go vet ./...
go test -race ./...
```

## 许可证

MIT，见 [LICENSE](LICENSE)。
