package platform

import (
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const proxyHopHeader = "X-API2MCP-Proxy-Hop"

func serverType(d Draft) string {
	if d.Type == "" {
		return "api"
	}
	return d.Type
}

func proxyCredentialHeaderAllowed(name string) bool {
	lower := strings.ToLower(name)
	return clientConfigHeaderAllowed(name) && !slices.Contains(nonForwardedMCPHeaders, lower) &&
		!slices.Contains([]string{"base_url", "x-api2mcp-base-url", "x-api2mcp-proxy-hop", "last-event-id", "forwarded", "via", "x-real-ip", "remote-host"}, lower) &&
		!strings.HasPrefix(lower, "x-forwarded-")
}

func validateMCPProxy(config *MCPProxyConfig, publish bool) error {
	if config == nil {
		return bad("请填写上游 MCP 地址")
	}
	config.URL = strings.TrimSpace(config.URL)
	if config.URL != "" || publish {
		if len(config.URL) > 4096 {
			return bad("上游 MCP 地址最多 4096 字节")
		}
		if _, err := validURL(config.URL, false); err != nil {
			return bad("上游 MCP 地址应为完整 HTTP / HTTPS 地址，可包含查询参数")
		}
	}
	if len(config.Headers) > 100 {
		return bad("上游 MCP Header 最多 100 项")
	}
	headers := map[string]string{}
	seen := map[string]bool{}
	total := 0
	for name, value := range config.Headers {
		lower := strings.ToLower(name)
		if len(name) > 256 || !proxyCredentialHeaderAllowed(name) || !safeHeaderValue(value) || seen[lower] {
			return bad("上游 MCP Header 无效、重复或包含受保护的传输字段")
		}
		total += len(name) + len(value)
		if total > 16384 {
			return bad("上游 MCP Header 总长度最多 16 KB")
		}
		seen[lower] = true
		headers[http.CanonicalHeaderKey(name)] = value
	}
	config.Headers, config.HeaderNames = headers, nil
	return nil
}

// Keep authentication values out of the workspace snapshot. The editor and
// client JSON retrieve them explicitly through authenticated admin endpoints.
func publicServer(server Server) Server {
	redact := func(config *MCPProxyConfig) *MCPProxyConfig {
		if config == nil {
			return nil
		}
		names := make([]string, 0, len(config.Headers))
		for name := range config.Headers {
			names = append(names, name)
		}
		sort.Strings(names)
		return &MCPProxyConfig{URL: config.URL, HeaderNames: names}
	}
	server.Proxy = redact(server.Proxy)
	if server.Pending != nil {
		pending := *server.Pending
		pending.Proxy = redact(pending.Proxy)
		server.Pending = &pending
	}
	return server
}

func (a *App) serverProxyConfig(w http.ResponseWriter, r *http.Request) error {
	server, err := a.serverByID(r.PathValue("id"))
	if err != nil {
		return err
	}
	config := server.Proxy
	if server.Pending != nil && r.URL.Query().Get("published") != "true" {
		config = server.Pending.Proxy
	}
	if config == nil {
		return bad("此服务没有 MCP 代理配置")
	}
	return sendJSON(w, 200, config)
}

// A target is an exact MCP endpoint, rather than an API base URL: keep its path,
// trailing slash, escaped components and query credentials intact.
func mcpProxyTarget(r *http.Request, config *MCPProxyConfig) (*url.URL, error) {
	if config == nil {
		return nil, bad("尚未配置上游 MCP 地址")
	}
	raw := config.URL
	values := append(r.Header.Values(proxyBaseURLHeader), r.Header.Values(proxyBaseURLHeaderAlias)...)
	if len(values) > 0 {
		if len(values) != 1 {
			return nil, bad("base_url Header 只能配置一个上游 MCP 地址")
		}
		raw = strings.TrimSpace(values[0])
	}
	u, err := validURL(raw, false)
	if err != nil || len(raw) > 4096 {
		return nil, bad("上游 MCP 地址应为完整 HTTP / HTTPS 地址，可包含查询参数")
	}
	// Consume only the gateway's query token. Never remove a token that belongs
	// to the configured upstream URL, and never decode/re-encode signed queries.
	query := []string{}
	for _, item := range strings.Split(r.URL.RawQuery, "&") {
		name, _, _ := strings.Cut(item, "=")
		decoded, err := url.QueryUnescape(name)
		if err == nil && decoded != "token" && item != "" {
			query = append(query, item)
		}
	}
	if len(query) > 0 {
		if u.RawQuery != "" {
			u.RawQuery += "&"
		}
		u.RawQuery += strings.Join(query, "&")
	}
	return u, nil
}

func (a *App) serveMCPProxy(w http.ResponseWriter, r *http.Request, server Server) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		w.Header().Set("Allow", "GET, POST, DELETE")
		_ = sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "MCP 代理支持 GET、POST 和 DELETE"})
		return
	}
	target, err := mcpProxyTarget(r, server.Proxy)
	if err != nil {
		_ = sendJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	headers, err := mcpProxyHeaders(r, server.Token)
	if err != nil {
		_ = sendJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	hops, _ := strconv.Atoi(r.Header.Get(proxyHopHeader))
	if hops < 0 || hops >= 8 {
		_ = sendJSON(w, http.StatusLoopDetected, map[string]string{"error": "MCP 代理转发层数过多，请检查是否形成循环代理"})
		return
	}
	// The API timeout bounds connection/response headers, not an established SSE
	// stream. Cancellation remains tied to the downstream connection context.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	start := time.Now()
	action := "MCP 代理 / " + r.Method
	proxy := &httputil.ReverseProxy{
		Transport:     a.upstream.Transport,
		FlushInterval: -1,
		ErrorLog:      log.New(io.Discard, "", 0),
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL = target
			p.Out.Host = target.Host
			// Origin is checked at this gateway; it must not be mistaken for an
			// upstream browser origin. Hop-by-hop headers are removed by net/http.
			for name := range p.Out.Header {
				lower := strings.ToLower(name)
				if lower == "authorization" && equalToken(bearerValue(p.Out.Header.Get(name)), server.Token) ||
					lower == "origin" || lower == "x-requested-with" || lower == "cookie" ||
					lower == "base_url" || lower == "x-api2mcp-base-url" || lower == "x-api2mcp-credentials" ||
					lower == "forwarded" || lower == "x-real-ip" || lower == "remote-host" || strings.HasPrefix(lower, "x-forwarded-") {
					p.Out.Header.Del(name)
				}
			}
			for name, value := range server.Proxy.Headers {
				p.Out.Header.Set(name, value)
			}
			connectionHeaders := strings.Split(r.Header.Get("Connection"), ",")
			for name, value := range headers {
				if !proxyCredentialHeaderAllowed(name) || strings.EqualFold(name, "X-Requested-With") || slices.ContainsFunc(connectionHeaders, func(hop string) bool { return strings.EqualFold(strings.TrimSpace(hop), name) }) {
					continue
				}
				if strings.EqualFold(name, "Cookie") {
					cookies := (&http.Request{Header: http.Header{"Cookie": []string{value}}}).Cookies()
					parts := []string{}
					for _, cookie := range cookies {
						if cookie.Name != cookieName {
							parts = append(parts, cookie.String())
						}
					}
					if len(parts) == 0 {
						continue
					}
					value = strings.Join(parts, "; ")
				}
				p.Out.Header.Set(name, value)
			}
			p.Out.Header.Set(proxyHopHeader, strconv.Itoa(hops+1))
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Set("X-Accel-Buffering", "no")
			response.Header.Set("Cache-Control", "no-store")
			// An upstream cookie must not replace the gateway's admin session.
			response.Header.Del("Set-Cookie")
			a.record(server, action, response.StatusCode >= 200 && response.StatusCode < 300, time.Since(start), response.StatusCode, "")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			message := "MCP 代理失败：" + upstreamRequestError(err).Error()
			a.record(server, action, false, time.Since(start), http.StatusBadGateway, message)
			_ = sendJSON(w, http.StatusBadGateway, map[string]string{"error": message})
		},
	}
	proxy.ServeHTTP(w, r)
}
