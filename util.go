package h3

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
)

// Run 启动 app 并等待本轮运行完成。
//
// Run 是对 [App.Start] 的阻塞式辅助封装，适合开发时直接在 main 中
// 运行 App。它临时包装 [Options.OnStopped]，因此会在 HTTP 服务退出且
// 所有 Servlet 均已停止后返回合并错误。已配置的 OnStopped 仍会在
// Run 返回前执行。
//
// ctx 的语义与 [App.Start] 相同：它只控制启动和初始化阶段。Start
// 成功后取消 ctx 不会停止 App，也不会使 Run 返回；运行期停止仍由
// [App.Stop] 或 HTTP 服务自身退出触发。
//
// Run 与 App 共用相同的顺序生命周期约束：只应在 app 未运行时调用，
// 不应与 Start、Stop 或对 Options 的修改并发执行。Run 不加锁，因为它
// 只是开发入口的便捷组合，不负责协调多个生命周期所有者。
func Run(ctx context.Context, app *App) error {
	stopped := make(chan error, 1)
	onStopped := app.options.OnStopped
	app.options.OnStopped = func(manual bool, err error) {
		if onStopped != nil {
			onStopped(manual, err)
		}
		stopped <- err
	}

	var startErr error
	func() {
		// Start 会在返回前捕获本轮回调。立即恢复 Options，
		// 使 Run 不会永久改变后续运行周期的配置。
		defer func() { app.options.OnStopped = onStopped }()
		startErr = app.Start(ctx)
	}()
	if startErr != nil {
		return startErr
	}
	return <-stopped
}

// safeCall 在 Servlet 生命周期边界将 panic 转换为 error。
//
// 启动回滚和停止流程必须继续处理其他 Servlet，因此不允许单个
// Servlet 的 panic 跳过后续清理。safeCall 只转换错误而不记日志；
// 调用方根据当前是启动回滚还是正常停止，决定返回、合并或记录该错误。
func safeCall(f func() error) (err error) {
	defer func() {
		if panicked := recover(); panicked != nil {
			if recovered, ok := panicked.(error); ok {
				err = recovered
			} else {
				err = fmt.Errorf("%v", panicked)
			}
		}
	}()
	err = f()
	return
}

// loadMux 返回 Router 当前已原子发布的路由表。
//
// 未调用 Build 的 Router 没有已发布的路由表，此时返回只读的空 ServeMux，使
// Handler 和 ServeHTTP 均以标准库的 404 行为安全降级。
// 原子加载只保证已编译快照的发布与读取，不为 Router 的可变配置提供
// 并发安全。
func loadMux(r *Router) *http.ServeMux {
	return cmp.Or(r.mux.Load(), emptyServeMux)
}

// validateTLSConfig 在启动 Listener 前检查 ServeTLS 所需的证书入口。
// 它只验证证书的配置入口存在，动态回调是否能为具体握手返回有效证书
// 仍由 crypto/tls 在运行时判定。
func validateTLSConfig(config *tls.Config) error {
	if config == nil {
		return nil
	}
	if len(config.Certificates) > 0 || config.GetCertificate != nil || config.GetConfigForClient != nil {
		return nil
	}
	return errors.New("h3: TLSConfig does not provide a server certificate")
}
