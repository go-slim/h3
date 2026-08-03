package h3

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

// Router 是声明式 HTTP 路由树。
//
// Handle、HandleFunc、Use 和 Mount 只记录路由配置；调用 Build 后才会将
// 当前配置编译为标准库的 http.ServeMux。Build 使用原子替换发布新路由表：
// 正在处理的请求继续使用旧表，之后开始的请求使用新表。
//
// Router 的可变配置不是并发安全的。不要让 Use、Handle、HandleFunc 或 Mount
// 与彼此或 Build 并发执行；请求处理可以与一次不修改配置的 Build 并发。
//
// 这是有意的“单写者配置、多读者运行”设计。路由声明是启动期或管理面操作，
// 而 ServeHTTP 是高频数据面操作。仅为当前 Router 的 map 加锁只能消除局部
// 数据竞争，不能定义递归挂载、共享子 Router 和用户中间件工厂的原子修改语义。
// 完整支持需要跨路由树的快照、所有权和锁顺序，与以配置期为主的用途不成比例。
// Router 因此只在发布边界使用 atomic.Pointer：Build 先离线构造一份
// 不可变 ServeMux，再一次性切换快照，请求路径无需加锁。
// 未调用 Build 时，Router 等同于一个返回 404 的空路由器。
type Router struct {
	mounts     map[string]*Router
	handlers   map[string]http.Handler
	middleware []func(http.Handler) http.Handler
	mux        atomic.Pointer[http.ServeMux] // 当前已发布的只读路由表
}

// NewRouter 创建一个空的 Router。
//
// 在注册完路由和中间件后调用 Build，或将 Router 交给 App 由 App.Start 调用
// Build。创建后直接处理请求会得到 404。
func NewRouter() *Router {
	return &Router{
		mounts:     make(map[string]*Router),
		handlers:   make(map[string]http.Handler),
		middleware: make([]func(http.Handler) http.Handler, 0),
	}
}

// Use 将中间件添加到当前 Router 的中间件链。
//
// 中间件会作用于当前 Router 直接声明的路由及其挂载的子 Router；它们在下一次
// Build 时与每个最终处理器组合。中间件按注册顺序形成洋葱模型：
//   - 先注册的中间件在外层（先执行 before，后执行 after）
//   - 后注册的中间件在内层（后执行 before，先执行 after）
//
// 示例：
//
//	mux.Use(loggingMiddleware)  // 外层
//	mux.Use(authMiddleware)     // 内层
//	// 执行顺序：logging before -> auth before -> handler -> auth after -> logging after
func (r *Router) Use(middleware func(http.Handler) http.Handler) {
	if middleware == nil {
		panic("h3: nil middleware")
	}
	r.middleware = append(r.middleware, middleware)
}

// Handler 返回当前已发布路由表中与请求匹配的处理器和模式。
//
// 返回的处理器已包含 Router 编译时组合的中间件。调用 Build 前，或在配置修改后
// 尚未重新 Build 时，它基于上一次已发布的路由表；从未 Build 时基于空路由表。
func (r *Router) Handler(req *http.Request) (h http.Handler, pattern string) {
	return loadMux(r).Handler(req)
}

// Handle 注册处理器到指定路由模式
//
// pattern 支持 Go 1.22+ ServeMux 的所有特性：
//   - 方法匹配：GET /path, POST /path
//   - 路径参数：/users/{id}, /files/{path...}
//   - 主机匹配：example.com/path
//
// 配置将在下一次 Build 时发布。pattern 为空或 handler 为 nil 时会触发 panic。
// 对同一个 pattern 重复调用 Handle 或 HandleFunc 时，后注册的处理器替换先前的
// 处理器；这使配置可以在下一次 Build 前显式修正，而不会保留两份含义相同的声明。
func (r *Router) Handle(pattern string, handler http.Handler) {
	r.register(pattern, handler)
}

// HandleFunc 注册处理函数到指定路由模式
//
// 这是 Handle 方法的便捷包装，自动将函数转换为 http.HandlerFunc。
func (r *Router) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	r.register(pattern, http.HandlerFunc(handler))
}

// register 注册路由；参数无效时 panic。
func (r *Router) register(pattern string, handler http.Handler) {
	if err := r.registerErr(pattern, handler); err != nil {
		panic(err)
	}
}

// registerErr 记录路由并返回基础参数错误而不是 panic。
//
// 路由模式的完整语法和冲突校验由 Build 中的 http.ServeMux.Handle 完成。
// 此处只检查：
//   - pattern 不能为空
//   - handler 不能为 nil
//   - http.HandlerFunc 类型的 handler 不能为 nil 函数
func (r *Router) registerErr(pattern string, handler http.Handler) error {
	if pattern == "" {
		return errors.New("h3: invalid pattern")
	}
	if handler == nil {
		return errors.New("h3: nil handler")
	}
	if f, ok := handler.(http.HandlerFunc); ok && f == nil {
		return errors.New("h3: nil handler")
	}
	r.handlers[pattern] = handler
	return nil
}

// Mount 将子 Router 挂载到指定路径。
//
// 构建时，子 Router 的每个模式都会加上 pattern 前缀。例如，将 apiRouter 挂载到
// "/api" 后，子 Router 中的 "GET /users" 会成为 "GET /api/users"。父 Router
// 的中间件包裹子 Router 的中间件和处理器。
//
// 特殊情况：
//   - pattern == "/" : 直接挂载到根路径
//   - pattern 带尾部斜杠（如 "/api/"）: 自动规范化为 "/api"
//   - pattern == "" : 触发 panic
//
// 子 Router 的模式可以包含方法和主机，例如 "GET example.com/users" 挂载到
// "/api" 后会编译为 "GET example.com/api/users"。对同一个规范化 prefix 重复
// Mount 时，后挂载的 Router 替换先前的 Router。
// prefix 始终表示路径而不是 ServeMux 的完整模式；"api" 会规范化为 "/api"，
// 不会被解释为 host。
func (r *Router) Mount(prefix string, child *Router) {
	if child == nil {
		panic("h3: nil router")
	}
	if child == r || child.contains(r, make(map[*Router]struct{})) {
		panic("h3: cyclic router mount")
	}

	prefix = normalizeMountPrefix(prefix)
	r.mounts[prefix] = child
}

// normalizeMountPrefix 将挂载前缀规范化为绝对路径。
// Mount 的 prefix 不是 ServeMux 完整模式，因此这里不解析 method 或 host。
func normalizeMountPrefix(prefix string) string {
	if prefix == "" {
		panic(errors.New("h3: invalid pattern"))
	}

	// 去掉尾部斜杠。
	if len(prefix) > 0 && prefix != "/" && prefix[len(prefix)-1] == '/' {
		prefix = prefix[:len(prefix)-1]
	}

	if prefix == "" {
		prefix = "/"
	} else if prefix != "/" && prefix[0] != '/' {
		prefix = "/" + prefix
	}
	return prefix
}

// contains 报告当前挂载树中是否包含 target。Router 配置不支持并发修改，
// 因此 Mount 可以在配置阶段直接完成循环检查而不需要锁。
func (r *Router) contains(target *Router, visited map[*Router]struct{}) bool {
	if r == target {
		return true
	}
	if _, ok := visited[r]; ok {
		return false
	}
	visited[r] = struct{}{}
	for _, child := range r.mounts {
		if child != nil && child.contains(target, visited) {
			return true
		}
	}
	return false
}

// ServeHTTP 使用当前已发布的路由表处理请求。
//
// Router 只负责路由和中间件组合，不包装 http.ResponseWriter。需要记录响应状态或
// 大小的应用应由 App 处理，或在调用 Router 前自行包装 ResponseWriter。
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	loadMux(r).ServeHTTP(w, req)
}

// Route 是 Router 构建时使用的完整路由定义。
//
// Pattern 使用标准库 http.ServeMux 的模式语法。Handler 已组合 Route 所属 Router
// 及其祖先 Router 的中间件。
type Route struct {
	// Pattern 是已展开所有挂载前缀的 http.ServeMux 模式。
	Pattern string
	// Handler 是已组合当前 Router 及所有祖先中间件的处理器。
	Handler http.Handler
}

// CompileRoutes 展开当前 Router 与其子 Router，返回带完整模式和已组合中间件的路由。
//
// 返回结果可用于构建其他兼容 http.ServeMux 的路由器。映射的遍历顺序未定义，调用方
// 不应依赖 Route 的顺序。每次编译都会为每条最终路由执行中间件工厂，
// 从而在请求之前固定处理器链；工厂不应存放单个请求的可变状态。
// 工厂捕获并由多个请求共享的状态，应由中间件自身保证并发安全。
// 工厂返回 nil 表示配置不完整并会触发 panic。
func (r *Router) CompileRoutes() []Route {
	var routes = make([]Route, 0, len(r.handlers))
	for pattern, handler := range r.handlers {
		routes = append(routes, Route{
			Pattern: pattern,
			Handler: applyMiddleware(r.middleware, handler),
		})
	}

	for prefix, child := range r.mounts {
		for _, route := range child.CompileRoutes() {
			routes = append(routes, Route{
				Pattern: prefixRoutePattern(prefix, route.Pattern),
				Handler: applyMiddleware(r.middleware, route.Handler),
			})
		}
	}

	return routes
}

// prefixRoutePattern 为标准库路由模式添加挂载路径前缀。
//
// pattern 使用 http.ServeMux 的 "[METHOD] [HOST]/[PATH]" 语法。方法和主机保持
// 不变，只向路径部分添加 prefix；方法与目标之间可以使用空格或制表符。
// 例如 "GET api/users" 中的 api 按 ServeMux 语法是 host；路径必须写为
// "GET /api/users"。
func prefixRoutePattern(prefix, pattern string) string {
	method, host, path, err := splitRoutePattern(pattern)
	if err != nil {
		panic(fmt.Errorf("h3: invalid mounted route pattern %q: %w", pattern, err))
	}

	target := host + joinPatternPath(prefix, path)
	if method == "" {
		return target
	}
	return method + " " + target
}

// splitRoutePattern 按照 http.ServeMux 的模式边界分离方法、主机和路径。
// 路径内部的语法校验仍交给 Build 中的 http.ServeMux.Handle。
func splitRoutePattern(pattern string) (method, host, path string, err error) {
	rest := pattern
	if index := strings.IndexAny(pattern, " \t"); index >= 0 {
		method = pattern[:index]
		rest = strings.TrimLeft(pattern[index+1:], " \t")
	}

	index := strings.IndexByte(rest, '/')
	if index < 0 {
		return "", "", "", errors.New("host/path missing /")
	}
	return method, rest[:index], rest[index:], nil
}

func joinPatternPath(prefix, path string) string {
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

// applyMiddleware 按注册顺序从外到内组合中间件。
func applyMiddleware(middleware []func(http.Handler) http.Handler, handler http.Handler) http.Handler {
	h := handler
	for _, middleware := range slices.Backward(middleware) {
		if middleware == nil {
			panic("h3: nil middleware")
		}
		h = middleware(h)
		if h == nil {
			panic("h3: middleware returned nil handler")
		}
	}
	return h
}

// Build 将当前路由树编译为新的 http.ServeMux，并原子发布到 Router。
//
// 它不修改已经发布的路由表，因此正在执行的请求不会受影响。调用方应继续通过
// Router 或 App 处理请求，以便后续 Build 能自动切换到新路由表。
//
// Build 只发布当前 Router 的完整路由表，不会发布挂载子 Router 自己的路由表。
// 子 Router 若需独立作为 http.Handler 使用，调用方应显式调用它的 Build。路由模式
// 冲突或无效时，http.ServeMux 会 panic；此时当前已发布的路由表保持不变。
// Build 可与 ServeHTTP 并发，但前提是此时没有任何配置写入；它不是把
// Router 变成通用的并发可变容器。
func (r *Router) Build() {
	var mux = http.NewServeMux()
	for _, route := range r.CompileRoutes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	r.mux.Store(mux)
}
