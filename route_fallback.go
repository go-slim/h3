package h3

import (
	"bytes"
	"net/http"
	"slices"
)

// fallbackHandler 只在 App 配置了自定义 404 或 405 Handler 时创建。
// 未配置时 newFallbackHandler 直接返回 Router，正常请求因此不会
// 创建 fallbackResponse。Options 在 New 时按值复制，所以该选择可以
// 在 App 创建期间确定，无需在每个请求中重复判断。
type fallbackHandler struct {
	router           *Router
	notFound         http.Handler
	methodNotAllowed http.Handler
}

func newFallbackHandler(router *Router, notFound, methodNotAllowed http.Handler) http.Handler {
	if notFound == nil && methodNotAllowed == nil {
		return router
	}
	return &fallbackHandler{
		router:           router,
		notFound:         notFound,
		methodNotAllowed: methodNotAllowed,
	}
}

func (handler *fallbackHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	response := newFallbackResponse(NewResponse(w), request)
	handler.router.ServeHTTP(response, request)
	response.finish(handler.notFound, handler.methodNotAllowed)
}

// fallbackResponse 在 App 与 Router 之间保留 http.ServeMux 生成的
// 未匹配响应，使 App 能在响应提交前将 404 或 405 替换为
// 用户配置的 Handler。
//
// ServeMux 会在调用已匹配 Handler 前设置 Request.Pattern。因此：
//   - Pattern 非空时，所有操作直接转发给 Response，正常路由不会被缓冲；
//   - Pattern 为空时，状态、响应头和小型标准库响应体会暂存，
//     交给 finish 分辨 404、405 和其他内部响应。
//
// 这种方式只调用一次 ServeMux.ServeHTTP，不会为正常请求重复
// 路由匹配，也不会缓冲流式响应。嵌入 Response 使正常 Handler
// 仍然可以使用 h3 的状态统计、Flush、Hijack 和 Push 能力。
type fallbackResponse struct {
	Response
	request *http.Request

	header http.Header
	status int
	body   bytes.Buffer
}

func newFallbackResponse(response Response, request *http.Request) *fallbackResponse {
	return &fallbackResponse{
		Response: response,
		request:  request,
	}
}

// Header 在未匹配响应中返回隔离的 Header，避免 http.Error
// 预先写入的 text/plain 等字段泄漏到自定义 JSON 响应。
func (response *fallbackResponse) Header() http.Header {
	if response.request.Pattern != "" {
		return response.Response.Header()
	}
	if response.header == nil {
		// 保留 App 外层 Handler 在进入 App 前已设置的响应头。
		response.header = response.Response.Header().Clone()
	}
	return response.header
}

func (response *fallbackResponse) WriteHeader(status int) {
	if response.request.Pattern != "" {
		response.Response.WriteHeader(status)
		return
	}
	if response.status == 0 {
		response.status = status
	}
}

func (response *fallbackResponse) Write(body []byte) (int, error) {
	if response.request.Pattern != "" {
		return response.Response.Write(body)
	}
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(body)
}

// finish 在 Router 返回后提交暂存的未匹配响应，或调用 App
// 配置的替代 Handler。替代 Handler 必须直接使用嵌入的
// Response，否则因 Request.Pattern 仍为空而再次被暂存。
func (response *fallbackResponse) finish(notFound, methodNotAllowed http.Handler) {
	switch response.status {
	case http.StatusNotFound:
		if notFound != nil {
			notFound.ServeHTTP(response.Response, response.request)
			return
		}
	case http.StatusMethodNotAllowed:
		if methodNotAllowed != nil {
			if values := response.header.Values("Allow"); len(values) > 0 {
				response.Response.Header()["Allow"] = slices.Clone(values)
			}
			methodNotAllowed.ServeHTTP(response.Response, response.request)
			return
		}
	}

	response.commit()
}

// commit 原样提交未被自定义 Handler 替换的 ServeMux 响应，
// 包括默认 404/405、路径规范化重定向和 "OPTIONS *" 等特殊请求。
func (response *fallbackResponse) commit() {
	if response.header != nil {
		header := response.Response.Header()
		clear(header)
		for name, values := range response.header {
			header[name] = slices.Clone(values)
		}
	}
	if response.status != 0 {
		response.Response.WriteHeader(response.status)
	}
	if response.body.Len() > 0 {
		_, _ = response.Response.Write(response.body.Bytes())
	}
}
