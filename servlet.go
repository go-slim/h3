package h3

import "context"

// Servlet 服务组件接口，表示可以启动和停止的服务
//
// 实现此接口的组件在服务器启动时会自动调用 Start 方法，
// 在服务器关闭时会自动调用 Stop 方法。这对于需要独立生命周期
// 管理的组件（如数据库连接、消息队列、后台任务等）特别有用。
//
// 生命周期:
//   - Start: 在 HTTP 服务器启动之前被调用
//   - Stop: 在 HTTP 服务器关闭时被调用（逆序执行）
//
// 注意:
//   - 如果 Start 返回错误，服务器启动会失败
//   - 生命周期管理方应为一次成功的 Start 调用一次 Stop
//   - 需要被多个所有者独立清理的实现可以自行保证 Stop 幂等
//   - 多个 Servlet 的 Stop 方法按注册顺序的逆序执行
//   - App 顺序调用生命周期方法，不会并发调用同一 Servlet 的 Start 和 Stop
//
// Servlet 只定义资源的顺序生命周期，不引入状态查询或内部锁。是否
// 支持其他所有者并发访问，由具体资源实现决定，而不是由此接口隐式承诺。
//
// 使用场景:
//   - 数据库连接池的初始化和关闭
//   - 消息队列的连接管理
//   - 后台任务的启动和停止
//   - 定时任务的调度管理
//
// 示例:
//
//	type DatabaseComponent struct {
//		h3.Component
//		db *sql.DB
//	}
//
//	func (c *DatabaseComponent) Start(ctx context.Context) error {
//		db, err := sql.Open("postgres", "connection-string")
//		if err != nil {
//			return err
//		}
//		c.db = db
//		return db.PingContext(ctx)
//	}
//
//	func (c *DatabaseComponent) Stop() error {
//		if c.db != nil {
//			return c.db.Close()
//		}
//		return nil
//	}
type Servlet interface {
	// Start 启动服务组件
	//
	// 参数:
	//   - ctx: 控制启动和初始化操作，例如数据库连通性检查。实现应在取消后
	//     尽快返回；取消可以使 App 中止并回滚尚未完成的启动，但不会停止已经
	//     启动完成的 Servlet 或 App。运行期关闭由 [App.Stop] 或 HTTP 服务退出
	//     触发的 App 统一收尾流程负责，而不由此 ctx 控制。
	//
	// 返回:
	//   - error: 启动失败时返回错误，会导致整个服务器启动失败
	Start(ctx context.Context) error

	// Stop 停止服务组件
	//
	// 此方法在服务器关闭时被调用，应该清理所有资源。
	// App 按一次 Start 对应一次 Stop 的生命周期调用它，不额外维护状态机。
	//
	// 返回:
	//   - error: 停止失败时返回错误；App 会与 HTTP 服务器的关闭错误一起
	//     汇总，并通过 App.Stop、Options.OnStopped 或 Run 交付结果。
	Stop() error
}
