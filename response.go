package h3

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
)

var (
	_ http.ResponseWriter = (*response)(nil)
	_ http.Flusher        = (*response)(nil)
	_ http.Hijacker       = (*response)(nil)
	_ http.Pusher         = (*response)(nil)
	_ Response            = (*response)(nil)
)

// Response 扩展了 http.ResponseWriter，添加了状态捕获和连接控制功能
//
// Response 包装 http.ResponseWriter 以捕获响应状态信息。
// 它记录响应的状态码、写入字节数和提交状态，这对于日志记录、
// 指标收集和错误处理中间件特别有用。
//
// 组合接口:
//   - http.ResponseWriter: 基本的响应写入功能
//   - http.Flusher: 支持立即刷新缓冲数据到客户端（SSE、流式响应）
//   - http.Hijacker: 支持接管底层 TCP 连接（WebSocket 升级）
//   - http.Pusher: 支持 HTTP/2 服务器推送
//
// 状态捕获方法:
//   - Status() int: 获取 HTTP 响应状态码
//   - Committed() bool: 检查响应是否已提交
//   - Size() int64: 获取已写入的字节数
//   - Unwrap() http.ResponseWriter: 获取直接被包装的 ResponseWriter
//   - Push(target, opts) error: HTTP/2 服务器推送
//
// 重要特性:
//   - 自动捕获状态码（包括隐式的 200 OK）
//   - 记录写入的字节总数
//   - 跟踪响应是否已提交（WriteHeader 或 Write 被调用）
//   - 防止重复写入响应头
//   - 支持 WebSocket、SSE、HTTP/2 推送等高级特性
//
// Response 只跟踪一个最终响应状态，不建模多个临时 1xx 响应。需要发送
// 100 Continue、103 Early Hints 等临时响应时，调用方应通过 [Response.Unwrap]
// 直接操作底层 ResponseWriter，再使用 Response 写入最终响应。
//
// Response 遵循 net/http 中由单个 handler 拥有 ResponseWriter 的模型，
// 不为并发写入 Header、WriteHeader 或 Write 加锁。锁只能避免字段竞争，
// 不能为多个 goroutine 定义正确的 HTTP 提交顺序；需要并发生成内容时，
// 调用方应在写入 Response 前自行汇总或同步。
//
// 为兼容仍通过类型断言查找可选接口的库，Response 始终实现
// http.Flusher、http.Hijacker 和 http.Pusher。这不表示底层 writer 必然
// 支持对应功能；调用结果仍取决于底层 writer。
//
// 使用场景:
//   - 中间件需要记录响应状态和大小
//   - 需要在响应完成后执行日志记录
//   - 实现流式响应或服务器推送
//   - WebSocket 连接升级
//
// 示例用法:
//
//	func middleware(next http.Handler) http.Handler {
//		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//			rw := h3.NewResponse(w)
//			next.ServeHTTP(rw, r)
//			log.Printf("Status: %d, Size: %d bytes", rw.Status(), rw.Size())
//		})
//	}
type Response interface {
	http.ResponseWriter
	http.Flusher
	http.Hijacker
	http.Pusher

	// Status 返回 HTTP 响应状态码
	//
	// 返回值:
	//   - 如果调用了 WriteHeader，返回传入的状态码
	//   - 如果调用了 Write 但未调用 WriteHeader，返回 200
	//   - 如果都未调用，返回初始默认值 200
	Status() int

	// Size 返回已写入响应体的字节总数
	Size() int64

	// Committed 返回响应是否已提交
	//
	// 响应在以下情况被视为已提交：
	//   - 调用了 WriteHeader
	//   - 调用了 Write（会自动调用 WriteHeader）
	//
	// 一旦响应提交，就无法再修改状态码。
	Committed() bool

	// Unwrap 返回直接被包装的 http.ResponseWriter
	//
	// ResponseController 可以通过这个方法继续向内解包 ResponseWriter。
	// 参见 [https://go.dev/blog/go1.20]
	Unwrap() http.ResponseWriter
}

type response struct {
	http.ResponseWriter       // 嵌入原始 ResponseWriter
	status              int   // 捕获的 HTTP 状态码
	size                int64 // 已写入的字节数
	committed           bool  // 响应是否已开始写入
}

// NewResponse 创建 Response 包装器
//
// 如果传入的 ResponseWriter 已经是 Response 类型，直接返回避免重复包装。
// 默认状态码设置为 200 OK，这是 HTTP 协议的默认状态。
func NewResponse(w http.ResponseWriter) Response {
	if r, ok := w.(Response); ok {
		return r
	}

	return &response{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
}

// Status 返回 HTTP 响应状态码
func (r *response) Status() int {
	return r.status
}

// Size 返回已写入响应体的字节总数
func (r *response) Size() int64 {
	return r.size
}

// Committed 返回响应是否已提交
func (r *response) Committed() bool {
	return r.committed
}

// Unwrap 返回直接被包装的 http.ResponseWriter
func (r *response) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// WriteHeader 拦截并记录状态码
//
// 此方法会记录一个最终状态码并标记响应为已提交。
// 如果响应已经提交（WriteHeader 或 Write 已被调用），
// 再次调用此方法会被忽略并记录错误日志。
//
// Response 将第一次 WriteHeader 调用视为最终响应，因此不表示或统计可重复的
// 1xx 临时响应。需要 100 Continue、103 Early Hints 等细节时，请调用
// [Response.Unwrap] 直接写入临时响应；之后再通过本方法或 Write 写入最终响应。
//
// 注意:
//   - 最终响应头只能发送一次；1xx 临时响应是例外
//   - 对最终响应多次调用 WriteHeader 是编程错误，应该避免
//   - 标准库的行为是忽略后续调用（但可能记录警告）
func (r *response) WriteHeader(code int) {
	if r.committed {
		// 响应已提交，无法修改状态码，只能记录错误
		log.Printf("attempt to write header after response committed")
		return
	}

	r.status = code
	r.committed = true
	r.ResponseWriter.WriteHeader(code)
}

// Write 实现 io.Writer 接口，写入响应体数据
//
// 如果在调用 Write 之前没有调用 WriteHeader，
// 会自动调用 WriteHeader(200) 发送响应头。
//
// 此方法会:
//   - 自动提交响应（如果尚未提交）
//   - 记录写入的字节数
//   - 将数据写入底层的 ResponseWriter
//
// 返回:
//   - n: 成功写入的字节数
//   - err: 写入过程中的错误（如果有）
func (r *response) Write(p []byte) (size int, err error) {
	if !r.committed {
		// 默认状态码为 200 OK（如果处理器不显式调用 WriteHeader）
		if r.status == 0 {
			r.status = http.StatusOK
		}
		r.WriteHeader(r.status)
	}

	size, err = r.ResponseWriter.Write(p)
	r.size += int64(size)

	return
}

// Hijack 实现 http.Hijacker 接口，允许 HTTP 处理器接管底层连接
//
// 此方法用于 WebSocket 连接升级、代理和其他高级用例。
// 参见 [http.Hijacker](https://golang.org/pkg/net/http/#Hijacker)
func (r *response) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// 新代码应该这样进行响应劫持
	// http.NewResponseController(responseWriter).Hijack()
	//
	// 但是一些旧库不知道 `http.NewResponseController` 的存在，会尝试直接劫持
	// `hj, ok := resp.(http.Hijacker)` <-- 如果 Response 不直接实现 Hijack 方法就会失败
	// 所以为此我们需要实现 http.Hijacker 接口
	return http.NewResponseController(r.ResponseWriter).Hijack()
}

// Flush 实现 http.Flusher 接口，允许 HTTP 处理器将缓冲数据刷新到客户端
//
// 参见 [http.Flusher](https://golang.org/pkg/net/http/#Flusher)
func (r *response) Flush() {
	err := http.NewResponseController(r.ResponseWriter).Flush()
	if err != nil && errors.Is(err, http.ErrNotSupported) {
		panic(fmt.Errorf("h3: response writer %T does not support flushing (http.Flusher interface)", r.ResponseWriter))
	}
}

// Push 实现 http.Pusher 接口，用于 HTTP/2 服务器推送
//
// 参见 [http.Pusher](https://golang.org/pkg/net/http/#Pusher)
func (r *response) Push(target string, opts *http.PushOptions) error {
	pusher, ok := r.ResponseWriter.(http.Pusher)
	if !ok {
		return fmt.Errorf("h3: response writer %T does not support pushing (http.Pusher interface)", r.ResponseWriter)
	}
	return pusher.Push(target, opts)
}
