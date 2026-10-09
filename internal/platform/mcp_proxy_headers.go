package platform

import (
	"net/http"
	"slices"
	"strings"
)

const (
	proxyBaseURLHeader      = "base_url"
	proxyBaseURLHeaderAlias = "X-API2MCP-Base-URL"
)

var nonForwardedMCPHeaders = []string{
	"accept", "accept-encoding", "connection", "content-length", "content-type", "host",
	"mcp-protocol-version", "mcp-session-id", "mcp-method", "origin", "te", "trailer",
	"transfer-encoding", "upgrade", "user-agent", "x-api2mcp-credentials",
}

// proxyBaseURL reads the optional per-request upstream URL. The default remains
// the URL imported from the API document when neither supported header exists.
func proxyBaseURL(r *http.Request) (string, bool, error) {
	values := append(r.Header.Values(proxyBaseURLHeader), r.Header.Values(proxyBaseURLHeaderAlias)...)
	if len(values) == 0 {
		return "", false, nil
	}
	if len(values) != 1 || len(values[0]) > 4096 {
		return "", false, bad("base_url Header 必须是单个 HTTP / HTTPS 地址")
	}
	u, err := validURL(strings.TrimSpace(values[0]), true)
	if err != nil {
		return "", false, bad("base_url Header 必须是有效的 HTTP / HTTPS API 基础地址")
	}
	return strings.TrimRight(u.String(), "/"), true, nil
}

// mcpProxyHeaders treats custom MCP request headers as per-call upstream
// credentials. MCP transport metadata and the routing-only base_url value are
// consumed here and are never sent to the API.
func mcpProxyHeaders(r *http.Request, mcpToken string) (map[string]string, error) {
	result := map[string]string{}
	total, count := 0, 0
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if lower == proxyBaseURLHeader || strings.EqualFold(name, proxyBaseURLHeaderAlias) || lower == "x-api2mcp-credentials" {
			for _, value := range values {
				total += len(name) + len(value)
			}
			count++
			continue
		}
		if slices.Contains(nonForwardedMCPHeaders, lower) {
			continue
		}
		if lower == "authorization" && len(values) == 1 && equalToken(bearerValue(values[0]), mcpToken) {
			// Do not leak a legacy MCP Bearer token to the upstream API.
			continue
		}
		if !headerName.MatchString(name) || len(values) == 0 {
			return nil, bad("客户端请求 Header 无效")
		}
		value := strings.Join(values, ", ")
		if lower == "cookie" {
			value = strings.Join(values, "; ")
		}
		entry := CredentialEntry{Location: "header", Name: name, Value: value, ValueType: "string", Enabled: true}
		if err := validateCredentialEntry(entry); err != nil {
			return nil, bad("客户端请求 Header 无效，请检查名称、值和控制字符")
		}
		total += len(name) + len(value)
		count++
		if total > 16384 || count > 100 {
			return nil, bad("客户端请求 Header 总长度最多 16 KB，且最多 100 项")
		}
		result[name] = value
	}
	return result, nil
}

func bearerValue(value string) string {
	const prefix = "Bearer "
	if len(value) >= len(prefix) && strings.EqualFold(value[:len(prefix)], prefix) {
		return strings.TrimSpace(value[len(prefix):])
	}
	return ""
}
