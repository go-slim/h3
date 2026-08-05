# Router benchmarks

该模块比较 h3、标准库 `http.ServeMux`、Echo、Gin、Chi、Macaron 和 Fiber 的
进程内路由分发开销。第三方框架保存在独立 Go 模块中，避免改变 h3 主模块仅依赖
标准库的边界。

```sh
cd benchmarks/router
go test ./...
go test -run '^$' -bench . -benchmem -count 5
```

## HTML 报告

先保存至少 5 次采样，再使用内置报告工具生成自包含 HTML：

```sh
go test -run '^$' -bench . -benchmem -count 5 > benchmark.txt
go run ./cmd/report -input benchmark.txt -output benchmark.html
open benchmark.html
```

报告按场景展示 `ns/op`、`B/op` 和 `allocs/op` 的中位数、相对最快结果的倍数，
并高亮 h3。CSS、原始 benchmark 输出及运行环境都嵌入同一个 HTML 文件，不依赖
JavaScript、CDN 或网络连接。`benchmark.txt` 和 `benchmark.html` 是本机产物，已被
`.gitignore` 忽略。

测试场景包括：

- 静态 GET 路由；
- 读取一个路径参数的 GET 路由；
- 统一为空响应体的 404；
- 三层空操作中间件包裹的静态路由。

每个框架在计时前完成路由注册和请求构造，并先执行一次请求预热内部对象池。计时
只包含响应重置、路由匹配、中间件和最终 Handler，不包含监听端口、网络传输以及
请求解析。

Fiber 基于 `fasthttp`，测试直接调用其 `fasthttp.RequestHandler`；其他框架直接调用
`http.Handler`。因此结果适合观察当前版本在相同业务动作下的进程内开销，不代表完整
HTTP 服务器在真实网络、并发、请求体和响应体负载下的绝对性能。
