package h3

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"slices"
	"time"
)

// Options 提供了对 HTTP 应用行为的细粒度控制，包括超时、TLS 配置、
// 协议支持等。所有字段都是可选的，未设置的字段将使用 Go 标准库的默认值。
type Options struct {
	// Router 是应用使用的路由树。nil 时 New 会创建一个空 Router。
	// App.Start 会在启动 HTTP 服务前调用 Router.Build。
	Router *Router

	// Addr 可选地指定应用监听的 TCP 地址，格式为 "host:port"。
	// 如果为空，使用 ":http"（端口 80）。
	// 服务名称在 RFC 6335 中定义并由 IANA 分配。
	// 地址格式的详细信息请参见 net.Dial。
	Addr string

	// DisableGeneralOptionsHandler 如果为 true，将 "OPTIONS *" 请求传递给 Handler，
	// 否则响应 200 OK 和 Content-Length: 0。
	DisableGeneralOptionsHandler bool

	// TLSConfig 非 nil 时启用 TLS，并将配置交给 http.Server.ServeTLS。
	// 配置必须通过 Certificates、GetCertificate 或 GetConfigForClient 提供
	// 服务端证书，否则 Start 返回错误。此值会被 ServeTLS 克隆，因此无法使用
	// tls.Config.SetSessionTicketKeys 等方法修改配置。
	// 要使用 SetSessionTicketKeys，请改用 Server.Serve 配合 TLS Listener。
	TLSConfig *tls.Config

	// ReadTimeout 是读取整个请求（包括请求体）的最大持续时间。
	// 零值或负值表示没有超时。
	//
	// 因为 ReadTimeout 不允许 Handler 对每个请求体的可接受截止时间或
	// 上传速率做出单独决策，大多数用户会更倾向于使用 ReadHeaderTimeout。
	// 同时使用两者也是有效的。
	ReadTimeout time.Duration

	// ReadHeaderTimeout 是允许读取请求头的时间量。
	// 读取请求头后，连接的读取截止时间会被重置，Handler 可以决定
	// 请求体的读取速度是否太慢。如果为零，使用 ReadTimeout 的值。
	// 如果为负值，或者为零且 ReadTimeout 为零或负值，则没有超时。
	ReadHeaderTimeout time.Duration

	// WriteTimeout 是响应写入超时前的最大持续时间。
	// 每当读取新请求的头部时，它会被重置。与 ReadTimeout 类似，
	// 它不允许 Handler 基于每个请求做出决策。
	// 零值或负值表示没有超时。
	WriteTimeout time.Duration

	// IdleTimeout 是启用 keep-alive 时等待下一个请求的最大时间量。
	// 如果为零，使用 ReadTimeout 的值。如果为负值，或者为零且
	// ReadTimeout 为零或负值，则没有超时。
	IdleTimeout time.Duration

	// MaxHeaderBytes 控制服务器在解析请求头的键和值时读取的最大字节数，
	// 包括请求行。它不限制请求体的大小。
	// 如果为零，使用 DefaultMaxHeaderBytes。
	MaxHeaderBytes int

	// TLSNextProto 可选地指定一个函数，当 ALPN 协议升级发生时接管
	// 提供的 TLS 连接的所有权。map 的键是协商的协议名称。
	// Handler 参数应该用于处理 HTTP 请求，如果尚未设置，
	// 它将初始化 Request 的 TLS 和 RemoteAddr。
	// 函数返回时连接会自动关闭。
	// 如果 TLSNextProto 不为 nil，HTTP/2 支持不会自动启用。
	TLSNextProto map[string]func(*http.Server, *tls.Conn, http.Handler)

	// ConnState 指定一个可选的回调函数，当客户端连接状态改变时调用。
	// 详情请参见 ConnState 类型和相关常量。
	ConnState func(net.Conn, http.ConnState)

	// ErrorLog 指定 HTTP Server 内部诊断和 Serve 异常退出使用的日志记录器。
	// 如果为 nil，使用 log 包的标准日志记录器。ErrorLog 只用于进程诊断；
	// 可编程的停止结果仍通过 Stop、OnStopped 或 Run 返回。
	ErrorLog *log.Logger

	// OnStopped 在一轮已成功启动的 HTTP 服务退出且所有 Servlet
	// 均已停止后调用一次。manual 表示停止是否由 App.Stop 触发；
	// err 汇总 HTTP 服务退出、Shutdown 和 Servlet.Stop 产生的错误。
	// Start 成功返回前发生的启动失败不会触发此回调。Start
	// 会捕获当前回调供本轮使用，运行期修改此字段不会影响已启动的一轮。
	// OnStopped 是完成通知，不应承担 Servlet 清理；Stop 不等待回调返回，
	// 需要等待回调的阻塞式入口应使用 [Run]。
	OnStopped func(manual bool, err error)

	// HTTP2 配置 HTTP/2 连接。
	//
	// 此字段目前尚未生效。
	// 详见 https://go.dev/issue/67813。
	HTTP2 *http.HTTP2Config

	// Protocols 是服务器接受的协议集。
	//
	// 如果 Protocols 包含 UnencryptedHTTP2，服务器将接受未加密的 HTTP/2 连接。
	// 服务器可以在同一地址和端口上同时提供 HTTP/1 和未加密的 HTTP/2。
	//
	// 如果 Protocols 为 nil，默认通常是 HTTP/1 和 HTTP/2。
	// 如果 TLSNextProto 不为 nil 且不包含 "h2" 条目，默认仅为 HTTP/1。
	Protocols *http.Protocols
}

// emptyServeMux 是尚未发布路由表时的只读回退处理器，所有请求均返回 404。
var emptyServeMux = http.NewServeMux()

var _ Servlet = (*App)(nil)

// App 将 Router、HTTP 服务器和 Servlet 生命周期组合为一个 HTTP 应用。
//
// App 负责包装响应写入器；Router 本身仅负责路由和中间件。通过 Router 重新
// Build 发布的路由表会被后续 App 请求自动读取。
//
// App 的请求处理可以并发，但配置与 Start/Stop 采用单一所有者、顺序、
// 无锁的生命周期模型。这是有意的边界：并发 Start、Stop、Register 或回调
// 重入本身没有唯一合理的先后语义，仅增加互斥锁也无法使 Servlet 和用户回调
// 自动变得并发安全。应由应用的一个控制流负责生命周期操作。
// 本轮停止完成后可以再次 Start 同一个 App。
type App struct {
	options  *Options        // 应用配置参数
	router   *Router         // 路由复用器
	servlets []Servlet       // 服务组件列表
	stop     chan chan error // 当前运行周期的停止请求；未运行时为 nil
}

// New 创建 HTTP 应用实例
//
// 参数:
//   - options: 可选的应用配置参数；仅使用第一个值
//
// 返回:
//   - *App: 应用实例
//
// 示例:
//
//	// 使用默认配置
//	app := h3.New()
//
//	// 使用自定义配置
//	app := h3.New(h3.Options{
//		Addr:         ":8080",
//		ReadTimeout:  10 * time.Second,
//		WriteTimeout: 10 * time.Second,
//	})
func New(options ...Options) *App {
	var opts Options
	if len(options) > 0 {
		opts = options[0]
	}

	router := opts.Router
	if router == nil {
		router = NewRouter()
	}

	return &App{
		options: &opts,
		router:  router,
	}
}

// Use 为应用根 Router 添加中间件。
//
// 中间件将在下一次 Router.Build 时发布，并包裹 App 直接注册的路由及所有组件
// 路由。配置并发约束与 Router 相同。
func (a *App) Use(middleware func(http.Handler) http.Handler) {
	a.router.Use(middleware)
}

// Register 注册应用组件
//
// 此方法会将应用组件的 Router 挂载到应用根 Router。
// 如果应用组件实现了 Servlet 接口，还会将其添加到服务组件列表中，
// 以便在应用启动和关闭时自动调用其 Start 和 Stop 方法。
//
// Register 必须在 App 未运行时调用。与 Router.Mount 的纯路由声明不同，
// Component 还可能拥有 Servlet 生命周期，因此重复前缀不能只替换路由而
// 保留旧 Servlet。为避免路由所有权与资源所有权分离，重复前缀会 panic。
// nil Component 也会 panic。
//
// 参数:
//   - c: 要注册的应用组件
func (a *App) Register(c Component) {
	if c == nil {
		panic("h3: nil component")
	}
	if a.stop != nil {
		panic("h3: cannot register component while app is started")
	}
	prefix := normalizeMountPrefix(c.Prefix())
	if _, exists := a.router.mounts[prefix]; exists {
		panic("h3: component prefix already registered")
	}

	// 挂载组件路由
	a.router.Mount(prefix, c.Router())

	// 如果组件实现了 Servlet 接口，添加到服务组件列表
	if serv, ok := c.(Servlet); ok {
		a.servlets = append(a.servlets, serv)
	}
}

// Handle 注册路由模式和对应的处理器
//
// 此方法委托给内部路由器，将指定的处理器绑定到路由模式。
//
// 参数:
//   - pattern: 路由模式（例如 "GET /users/{id}"）
//   - handler: 处理该路由的 http.Handler
func (a *App) Handle(pattern string, handler http.Handler) {
	a.router.Handle(pattern, handler)
}

// HandleFunc 注册路由模式和对应的处理函数
//
// 此方法委托给内部路由器，将指定的处理函数绑定到路由模式。
// 这是 Handle 方法的便捷版本，接受函数而不是 http.Handler。
//
// 参数:
//   - pattern: 路由模式（例如 "GET /users/{id}"）
//   - handler: 处理该路由的函数
func (a *App) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	a.router.HandleFunc(pattern, handler)
}

// Handler 根据当前已发布的路由表查找匹配的处理器和模式。
//
// 与 Router.Handler 一样，调用 Build 前基于空路由表；配置变更后需重新 Build
// 才会反映到结果中。
func (a *App) Handler(r *http.Request) (h http.Handler, pattern string) {
	return loadMux(a.router).Handler(r)
}

// ServeHTTP 实现 http.Handler 接口，将请求交给 Router 处理。
//
// App 在这里将 ResponseWriter 包装为 Response，以便中间件和处理器观察响应状态、
// 已写入字节数和提交状态；Router 不承担这项职责。
//
// 参数:
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.router.ServeHTTP(NewResponse(w), r)
}

// Start 启动 HTTP 应用（非阻塞）。
//
// 此方法会按顺序执行以下操作:
//  1. 验证监听地址格式
//  2. 编译并发布 Router 的当前配置
//  3. 同步创建 TCP Listener，因此端口占用等监听错误会直接返回
//  4. 启动所有注册的 Servlet 组件（调用 Start 方法）
//  5. 启动 HTTP 或 HTTPS 服务（在后台 goroutine 中）
//  6. 设置统一关闭处理（在后台 goroutine 中等待 Stop 或 Serve 退出）
//
// 如果任何 Servlet 的 Start 方法返回错误，Listener 会关闭，已经启动的 Servlet
// 会按逆序停止，整个启动过程返回该错误。无效或冲突的路由配置遵循
// http.ServeMux.Handle 的约定，在 Build 时触发 panic。
//
// 参数:
//   - ctx: 控制 Servlet 的启动和初始化。Start 成功返回前观察到取消时，启动会
//     中止并回滚；Start 成功返回后再取消，不会停止已经启动完成的 App。HTTP
//     服务仍正常运行时，关闭必须显式调用 [App.Stop]。
//
// 返回:
//   - error: 地址、监听、TLS 配置或 Servlet 启动失败时返回错误
//
// HTTP 服务开始运行后发生的异常会写入 Options.ErrorLog（未配置时使用标准
// logger）。无论 HTTP 服务因 Stop 还是异常而退出，App 都会停止所有 Servlet，
// 然后通过 Options.OnStopped 通知调用方。
func (a *App) Start(ctx context.Context) error {
	if a.stop != nil {
		return errors.New("h3: app is already started")
	}

	opts := a.options
	if err := ctx.Err(); err != nil {
		return err
	}

	// 验证监听地址格式
	addr := cmp.Or(opts.Addr, ":http")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return err
	}

	logger := opts.ErrorLog
	if logger == nil {
		logger = log.Default()
	}

	if err := validateTLSConfig(opts.TLSConfig); err != nil {
		return err
	}

	// 先完成路由配置校验；Build 只有成功时才会原子发布新路由表。
	a.router.Build()
	if err := ctx.Err(); err != nil {
		return err
	}

	// Listener 在当前 goroutine 中创建，使地址无效、权限不足、端口占用等
	// 错误能够由 Start 可靠返回，而不是出现在后台 goroutine 中。
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	rollback := func(started int) {
		if closeErr := listener.Close(); closeErr != nil {
			logger.Println(closeErr)
		}
		// Start 的原始失败是返回给调用方的主错误。回滚错误只作
		// 诊断输出，并继续停止其余 Servlet，避免一个清理失败
		// 遮蔽启动原因或中断后续回滚。
		for index := started - 1; index >= 0; index-- {
			if stopErr := safeCall(a.servlets[index].Stop); stopErr != nil {
				logger.Println(stopErr)
			}
		}
	}

	if err := ctx.Err(); err != nil {
		rollback(0)
		return err
	}

	// 启动所有 Servlet 组件
	for i, serv := range a.servlets {
		if err := ctx.Err(); err != nil {
			rollback(i)
			return err
		}
		err = safeCall(func() error {
			return serv.Start(ctx)
		})
		if err != nil {
			rollback(i)
			return err
		}
		if err := ctx.Err(); err != nil {
			rollback(i + 1)
			return err
		}
	}

	// 请求基础 Context 由 App 自己持有，刻意不从 ctx 派生。Start 的
	// Context 只属于初始化阶段；否则调用方在 Start 返回后取消 ctx
	// 会意外终止所有请求。运行期由 Stop 或 HTTP Serve 退出显式收尾。
	lctx, cancel := context.WithCancel(context.Background())

	server := &http.Server{
		Addr:                         addr,
		Handler:                      a,
		DisableGeneralOptionsHandler: opts.DisableGeneralOptionsHandler,
		TLSConfig:                    opts.TLSConfig,
		ReadTimeout:                  opts.ReadTimeout,
		ReadHeaderTimeout:            opts.ReadHeaderTimeout,
		WriteTimeout:                 opts.WriteTimeout,
		IdleTimeout:                  opts.IdleTimeout,
		MaxHeaderBytes:               opts.MaxHeaderBytes,
		TLSNextProto:                 opts.TLSNextProto,
		ConnState:                    opts.ConnState,
		ErrorLog:                     opts.ErrorLog,
		BaseContext:                  func(net.Listener) context.Context { return lctx },
		HTTP2:                        opts.HTTP2,
		Protocols:                    opts.Protocols,
	}

	stop := make(chan chan error)
	serveDone := make(chan error, 1)
	onStopped := opts.OnStopped
	a.stop = stop

	// Stop 请求和 HTTP Serve 退出共用同一条收尾路径。触发来源
	// 在这里确定，不从 Context 的取消结果反推 manual。
	go func() {
		defer cancel()

		var manual bool
		var result chan error
		var serveErr error

		select {
		case result = <-stop:
			manual = true
		case serveErr = <-serveDone:
		}

		var shutdownErrors []error
		// 先停止接收新请求，并等待已经进入的请求完成。
		if err := server.Shutdown(lctx); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
		if manual {
			serveErr = <-serveDone
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			shutdownErrors = append(shutdownErrors, serveErr)
		}

		// 逆序停止所有 Servlet 组件
		for _, servlet := range slices.Backward(a.servlets) {
			if err := safeCall(servlet.Stop); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}

		err := errors.Join(shutdownErrors...)
		a.stop = nil
		if result != nil {
			// Stop 只等待 HTTP 与 Servlet 清理，回调是随后的完成通知。
			// 这避免慢回调延长 Stop，也避免把回调变成必须完成的
			// 第二套资源清理机制。
			result <- err
		}
		if onStopped != nil {
			// 此时 a.stop 已清空，因此回调可能与下一轮 Start
			// 重叠。需要严格等待通知的入口由 Run 提供同步。
			onStopped(manual, err)
		}
	}()

	go func() {
		defer listener.Close()
		var serveErr error
		if opts.TLSConfig == nil {
			serveErr = server.Serve(listener)
		} else {
			serveErr = server.ServeTLS(listener, "", "")
		}

		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Println(serveErr)
		}
		serveDone <- serveErr
	}()

	return nil
}

// Stop 优雅停止 HTTP 应用
//
// 此方法会先停止 HTTP 服务器接收新请求并等待现有请求完成，再逆序停止所有
// Servlet 组件。应用生命周期不由 Context 取消控制。HTTP Serve 异常退出时
// 也会走同一条收尾路径，停止 Servlet 并触发 OnStopped。
// Stop 完成后可以再次调用 Start，开始一个新的运行周期。
//
// Stop 没有独立的超时参数：它遵循 Servlet 的显式生命周期约定，等待 HTTP
// Shutdown 和所有 Servlet.Stop 完成，并返回期间产生的全部错误。
// 框架不猜测通用的停止超时；长连接、被劫持连接和 Servlet 自身的停止上限
// 应由拥有这些资源的应用实现。
// Start 和 Stop 是顺序生命周期操作，不应并发调用。尚未启动或已经停止时调用
// Stop 会直接返回 nil。
//
// 返回:
//   - error: 关闭过程中的错误
func (a *App) Stop() error {
	stop := a.stop
	if stop == nil {
		return nil
	}

	result := make(chan error)
	stop <- result
	return <-result
}
