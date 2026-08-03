package h3

// Component 是可独立注册到 App 的路由模块。
//
// Component 刻意只组合挂载前缀和 Router，不承诺资源生命周期。需要连接、
// 后台任务等资源的组件可以同时实现 [Servlet]，由 App 在路由挂载之外
// 管理其 Start/Stop。这使纯路由组件不需要承担空的生命周期方法。
type Component interface {
	// Prefix 返回组件在 App 中的挂载路径。
	Prefix() string
	// Router 返回组件的路由配置。其路由会在 App.Register 后挂载至 Prefix。
	Router() *Router
}

// NewComponent 创建一个在 prefix 下挂载的基础组件。
//
// 返回的组件不含路由；通过 Router 配置路由与中间件，再将其注册到 App。
func NewComponent(prefix string) Component {
	return &component{
		router: NewRouter(),
		prefix: prefix,
	}
}

// component 是 Component 的默认实现。
type component struct {
	router *Router // 组件路由器
	prefix string  // 路径前缀
}

// Router 返回应用组件的路由器。
func (c *component) Router() *Router {
	return c.router
}

// Prefix 返回应用组件的路径前缀。
func (c *component) Prefix() string {
	return c.prefix
}
