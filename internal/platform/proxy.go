package platform

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"
)

var headerName = regexp.MustCompile("^[!#$%&'*+.^_`|~a-zA-Z0-9-]+$")

func reservedHeader(name string) bool {
	return slices.Contains([]string{"host", "connection", "content-length", "content-type", "transfer-encoding", "trailer", "te", "upgrade", "proxy-authorization", "proxy-authenticate", "authorization", "cookie"}, strings.ToLower(name))
}

func upstreamClient(cfg Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// API requests may target any reachable HTTP/HTTPS host, including LAN and
	// loopback addresses. Build/download proxies do not affect application traffic.
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxConnsPerHost = 32
	transport.ResponseHeaderTimeout = cfg.UpstreamTimeout
	return &http.Client{Transport: transport, Timeout: cfg.UpstreamTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func textValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func simpleValue(v any, explode bool) string {
	switch value := v.(type) {
	case []any:
		parts := []string{}
		for _, item := range value {
			parts = append(parts, textValue(item))
		}
		return strings.Join(parts, ",")
	case map[string]any:
		parts := []string{}
		for _, key := range sortedKeys(value) {
			if explode {
				parts = append(parts, key+"="+textValue(value[key]))
			} else {
				parts = append(parts, key, textValue(value[key]))
			}
		}
		return strings.Join(parts, ",")
	default:
		return textValue(v)
	}
}

func queryValue(q url.Values, p Parameter, v any) {
	switch value := v.(type) {
	case []any:
		parts := []string{}
		for _, item := range value {
			parts = append(parts, textValue(item))
		}
		if p.CollectionFormat == "multi" || p.CollectionFormat == "" && p.Explode {
			for _, part := range parts {
				q.Add(p.Name, part)
			}
			return
		}
		sep := ","
		if p.Style == "spaceDelimited" || p.CollectionFormat == "ssv" {
			sep = " "
		}
		if p.Style == "pipeDelimited" || p.CollectionFormat == "pipes" {
			sep = "|"
		}
		if p.CollectionFormat == "tsv" {
			sep = "\t"
		}
		q.Add(p.Name, strings.Join(parts, sep))
	case map[string]any:
		if p.Style == "deepObject" {
			for _, key := range sortedKeys(value) {
				q.Add(p.Name+"["+key+"]", textValue(value[key]))
			}
			return
		}
		if p.Explode {
			for _, key := range sortedKeys(value) {
				q.Add(key, textValue(value[key]))
			}
			return
		}
		q.Add(p.Name, simpleValue(value, false))
	default:
		q.Add(p.Name, textValue(v))
	}
}

type upstreamResult struct {
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	Data        any    `json:"data"`
}

func (a *App) forward(ctx context.Context, doc Document, op Operation, args map[string]any) (upstreamResult, error) {
	req, err := buildUpstreamRequest(ctx, doc, op, args)
	if err != nil {
		return upstreamResult{}, err
	}
	response, err := a.executeUpstream(req)
	return upstreamResult{Status: response.Status, ContentType: response.ContentType, Data: response.Data}, err
}

type upstreamHTTPResponse struct {
	Status      int         `json:"status"`
	StatusText  string      `json:"statusText"`
	ContentType string      `json:"contentType"`
	Headers     http.Header `json:"headers"`
	Body        string      `json:"body"`
	Size        int         `json:"size"`
	Data        any         `json:"-"`
}

func buildUpstreamRequest(ctx context.Context, doc Document, op Operation, args map[string]any) (*http.Request, error) {
	if _, err := validURL(doc.BaseURL, true); err != nil {
		return nil, fmt.Errorf("文档尚未配置有效的 API 基础地址")
	}
	requestPath := op.Path
	query := url.Values{}
	headers := http.Header{}
	cookies := []*http.Cookie{}
	for _, p := range op.Parameters {
		value, exists := obj(args[p.Location])[p.Name]
		if !exists {
			continue
		}
		switch p.Location {
		case "path":
			encoded := url.PathEscape(simpleValue(value, p.Explode))
			if encoded == "." || encoded == ".." {
				encoded = strings.ReplaceAll(encoded, ".", "%2E")
			}
			requestPath = strings.ReplaceAll(requestPath, "{"+p.Name+"}", encoded)
		case "query":
			queryValue(query, p, value)
		case "header":
			v := simpleValue(value, p.Explode)
			if !safeHeaderValue(v) {
				return nil, fmt.Errorf("Header 参数含有无效字符")
			}
			if !reservedHeader(p.Name) {
				headers.Set(p.Name, v)
			}
		case "cookie":
			cookie := &http.Cookie{Name: p.Name, Value: simpleValue(value, p.Explode)}
			if err := cookie.Valid(); err != nil {
				return nil, fmt.Errorf("Cookie 参数无效")
			}
			cookies = append(cookies, cookie)
		}
	}
	if strings.ContainsAny(requestPath, "{}") {
		return nil, fmt.Errorf("缺少必需的路径参数")
	}
	for _, entry := range doc.Credential.Entries {
		if entry.Enabled && entry.Location == "query" {
			query.Set(entry.Name, entry.Value)
		}
	}
	u, err := url.Parse(strings.TrimRight(doc.BaseURL, "/") + requestPath)
	if err != nil {
		return nil, fmt.Errorf("API 地址无效")
	}
	u.RawQuery = query.Encode()
	var body io.Reader
	contentType := op.ContentType
	if doc.Credential.BodyFormat == "json" {
		contentType = "application/json"
	} else if doc.Credential.BodyFormat == "form" {
		contentType = "application/x-www-form-urlencoded"
	}
	bodyValue, hasBody := args["body"]
	if hasCredentialBody(doc.Credential) {
		if hasBody && bodyValue != nil {
			if _, ok := bodyValue.(map[string]any); !ok {
				return nil, fmt.Errorf("Body 请求配置需要 JSON 对象或表单字段，无法与数组或文本请求体合并")
			}
		}
		merged, fixed := map[string]any{}, credentialBody(doc.Credential)
		mergeObject(merged, obj(bodyValue))
		mergeObject(merged, fixed)
		bodyValue, hasBody = merged, true
		if op.RequestBody != nil {
			validation := copySchema(op.RequestBody)
			bodyCredentialSchema(validation, fixed, false)
			schema, err := compileSchema(validation)
			encoded, _ := json.Marshal(merged)
			var plain any
			decodeErr := json.Unmarshal(encoded, &plain)
			if err != nil || decodeErr != nil || schema.Validate(plain) != nil {
				// Validator errors can contain injected credentials. Return only a
				// fixed explanation, never the secret or the merged request body.
				return nil, fmt.Errorf("合并 Body 配置后的请求不符合文档 Schema，请检查字段类型和必填业务参数")
			}
		}
	}
	if hasBody {
		if contentType == "application/x-www-form-urlencoded" {
			if _, ok := bodyValue.(map[string]any); !ok {
				return nil, fmt.Errorf("表单请求体需要对象字段，请检查 Body 格式配置")
			}
			form := url.Values{}
			for key, value := range obj(bodyValue) {
				if _, object := value.(map[string]any); object {
					encoded, _ := json.Marshal(value)
					form.Set(key, string(encoded))
				} else {
					queryValue(form, Parameter{Name: key, Explode: true}, value)
				}
			}
			body = strings.NewReader(form.Encode())
		} else {
			b, err := json.Marshal(bodyValue)
			if err != nil {
				return nil, fmt.Errorf("请求体无法编码")
			}
			body = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("请求无法构建")
	}
	req.Header = headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "api2mcp/0.2.0")
	if body != nil {
		req.Header.Set("Content-Type", fallback(contentType, "application/json"))
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	for _, entry := range doc.Credential.Entries {
		if entry.Enabled && entry.Location == "header" {
			setCredentialHeader(req, entry.Name, entry.Value)
		}
	}
	if doc.Credential.Kind == "basic" {
		req.SetBasicAuth(doc.Credential.Username, doc.Credential.Value)
	}
	if doc.Credential.Kind == "bearer" {
		req.Header.Set("Authorization", "Bearer "+doc.Credential.Value)
	}
	if doc.Credential.Kind == "apiKey" {
		setCredentialHeader(req, doc.Credential.Header, doc.Credential.Value)
	}
	configuredCookies := []*http.Cookie{}
	for _, entry := range doc.Credential.Entries {
		if entry.Enabled && entry.Location == "cookie" {
			configuredCookies = append(configuredCookies, &http.Cookie{Name: entry.Name, Value: entry.Value})
		}
	}
	if len(configuredCookies) > 0 {
		mergeRequestCookies(req, configuredCookies)
	}
	return req, nil
}

// net/http errors include the full request URL, which may contain saved Query
// credentials. Classify the cause without exposing the underlying error text.
func upstreamRequestError(err error) error {
	var networkError net.Error
	var dnsError *net.DNSError
	var certificateError *tls.CertificateVerificationError
	switch {
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("上游请求已取消")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &networkError) && networkError.Timeout():
		return fmt.Errorf("上游请求超时，请检查 API 响应速度、网络或 UPSTREAM_TIMEOUT_SECONDS 配置")
	case errors.As(err, &dnsError):
		return fmt.Errorf("上游域名解析失败，请检查 API 主机名及 Docker DNS 配置")
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("上游拒绝连接，请检查 API 服务是否启动、端口是否正确及监听地址")
	case errors.As(err, &certificateError):
		return fmt.Errorf("上游 HTTPS 证书校验失败，请检查证书有效期、主机名和证书链")
	default:
		return fmt.Errorf("上游连接失败，请检查 API 地址、协议、端口及网络连通性")
	}
}

func (a *App) executeUpstream(req *http.Request) (upstreamHTTPResponse, error) {
	response, err := a.upstream.Do(req)
	if err != nil {
		return upstreamHTTPResponse{}, upstreamRequestError(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4_000_001))
	if err != nil {
		return upstreamHTTPResponse{}, fmt.Errorf("上游响应读取失败")
	}
	if len(data) > 4_000_000 {
		return upstreamHTTPResponse{}, fmt.Errorf("上游响应超过 4 MB 限制")
	}
	var value any
	if len(data) == 0 {
		value = nil
	} else if json.Unmarshal(data, &value) != nil {
		value = string(data)
	}
	return upstreamHTTPResponse{Status: response.StatusCode, StatusText: http.StatusText(response.StatusCode), ContentType: response.Header.Get("Content-Type"), Headers: response.Header.Clone(), Body: string(data), Size: len(data), Data: value}, nil
}

func setCredentialHeader(req *http.Request, name, value string) {
	if strings.EqualFold(name, "Cookie") {
		source := &http.Request{Header: http.Header{"Cookie": []string{value}}}
		if cookies := source.Cookies(); len(cookies) > 0 {
			mergeRequestCookies(req, cookies)
			return
		}
	}
	req.Header.Set(name, value)
}

func mergeRequestCookies(req *http.Request, configured []*http.Cookie) {
	merged := req.Cookies()
	for _, cookie := range configured {
		merged = slices.DeleteFunc(merged, func(previous *http.Cookie) bool { return previous.Name == cookie.Name })
		merged = append(merged, cookie)
	}
	req.Header.Del("Cookie")
	for _, cookie := range merged {
		req.AddCookie(cookie)
	}
}
