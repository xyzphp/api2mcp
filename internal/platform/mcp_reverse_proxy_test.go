package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func publishProxy(t *testing.T, client *http.Client, origin, slug, target string, headers map[string]string) Server {
	t.Helper()
	var server Server
	input := map[string]any{"mode": "publish", "draft": Draft{Type: "proxy", Name: slug, Slug: slug, Color: "purple", Proxy: &MCPProxyConfig{URL: target, Headers: headers}}}
	if status := request(t, client, origin, "/api/servers", "POST", input, &server); status != 200 {
		t.Fatalf("publish MCP proxy: HTTP %d", status)
	}
	return server
}

type proxyTestTransport struct{ headers http.Header }

func (p proxyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	for name, values := range p.headers {
		out.Header[name] = append([]string(nil), values...)
	}
	return http.DefaultTransport.RoundTrip(out)
}

func TestMCPProxySDKToolsResourcesPromptsAndSessions(t *testing.T) {
	for _, jsonResponse := range []bool{true, false} {
		t.Run(fmt.Sprintf("JSONResponse=%v", jsonResponse), func(t *testing.T) {
			a, ts, admin := testApp(t)
			impl := mcp.NewServer(&mcp.Implementation{Name: "remote-original", Version: "1.2.3"}, nil)
			impl.AddTool(&mcp.Tool{Name: "echo", Description: "Remote tool", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}}}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(r.Params.Arguments)}}}, nil
			})
			impl.AddResource(&mcp.Resource{URI: "demo://info", Name: "Remote info"}, func(ctx context.Context, r *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "demo://info", Text: "remote resource"}}}, nil
			})
			impl.AddPrompt(&mcp.Prompt{Name: "greet"}, func(ctx context.Context, r *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "remote prompt"}}}}, nil
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return impl }, &mcp.StreamableHTTPOptions{JSONResponse: jsonResponse})
			var deletes, sessionRequests, overrides atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.EscapedPath() != "/nested/mcp%2Fservice/" || !strings.HasPrefix(r.URL.RawQuery, "key=a%2Bb&token=upstream-query") {
					t.Errorf("upstream endpoint changed: %s", r.URL.String())
				}
				if strings.Contains(r.URL.RawQuery, "mcp_") || r.Header.Get("base_url") != "" || r.Header.Get("Origin") != "" || r.Header.Get("X-Requested-With") != "" || r.Header.Get("X-Forwarded-For") != "" {
					t.Error("gateway credentials or control headers reached upstream")
				}
				switch r.Header.Get("Authorization") {
				case "Bearer saved-secret":
				case "Bearer client-secret":
					overrides.Add(1)
				default:
					t.Error("upstream authentication missing")
				}
				if r.Header.Get("X-Tenant") != "saved-tenant" {
					t.Error("saved header lost")
				}
				if r.Header.Get("Mcp-Session-Id") != "" {
					sessionRequests.Add(1)
				}
				if r.Method == "DELETE" {
					deletes.Add(1)
				}
				handler.ServeHTTP(w, r)
			}))
			defer upstream.Close()
			target := upstream.URL + "/nested/mcp%2Fservice/?key=a%2Bb&token=upstream-query"
			server := publishProxy(t, admin, ts.URL, "remote-proxy", target, map[string]string{"Authorization": "Bearer saved-secret", "X-Tenant": "saved-tenant"})
			if len(server.OperationIDs) != 0 || server.Type != "proxy" || server.Proxy.Headers != nil || len(server.Proxy.HeaderNames) != 2 {
				t.Fatal("proxy should need no documents and redact saved header values")
			}
			var workspace Workspace
			request(t, admin, ts.URL, "/api/workspace", "GET", nil, &workspace)
			if workspace.Servers[0].Proxy.Headers != nil || a.store.Snapshot().Servers[0].Proxy.Headers["Authorization"] != "Bearer saved-secret" {
				t.Fatal("workspace redaction changed stored credentials")
			}
			var clientHeaders clientHeaderConfig
			if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/client-headers", "GET", nil, &clientHeaders); status != 200 || clientHeaders.Headers["Authorization"] != "Bearer saved-secret" || clientHeaders.Headers["base_url"] != target {
				t.Fatal("client JSON defaults must contain the upstream URL and saved credentials")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			client := mcp.NewClient(&mcp.Implementation{Name: "proxy-test", Version: "1"}, nil)
			session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp/" + server.Slug + "?token=" + server.Token, HTTPClient: &http.Client{Transport: proxyTestTransport{headers: http.Header{"Authorization": []string{"Bearer client-secret"}, "X-Forwarded-For": []string{"spoofed"}}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if session.InitializeResult().ServerInfo.Name != "remote-original" {
				t.Fatal("upstream initialization was replaced")
			}
			tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "echo" {
				t.Fatalf("tools: %v, %v", tools, err)
			}
			called, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"value": "你好"}})
			if err != nil || !strings.Contains(called.Content[0].(*mcp.TextContent).Text, "你好") {
				t.Fatalf("tool call: %v", err)
			}
			resource, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "demo://info"})
			if err != nil || resource.Contents[0].Text != "remote resource" {
				t.Fatalf("resource: %v", err)
			}
			prompt, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "greet"})
			if err != nil || prompt.Messages[0].Content.(*mcp.TextContent).Text != "remote prompt" {
				t.Fatalf("prompt: %v", err)
			}
			if err := session.Close(); err != nil {
				t.Fatal(err)
			}
			if deletes.Load() != 1 || sessionRequests.Load() < 5 || overrides.Load() < 5 {
				t.Fatalf("session/auth forwarding failed: deletes=%d sessions=%d overrides=%d", deletes.Load(), sessionRequests.Load(), overrides.Load())
			}
			var testResult map[string]any
			if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/test", "POST", nil, &testResult); status != 200 || testResult["success"] != true {
				t.Fatalf("admin connection test: %v", testResult)
			}
		})
	}
}

func TestMCPProxyStreamsFlushAndSurviveWriteDeadline(t *testing.T) {
	a, ts, admin := testApp(t)
	release, canceled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Session-Id") != "session-123" || r.Header.Get("Last-Event-Id") != "event-0" || r.Header.Get("Mcp-Protocol-Version") != "2025-11-25" {
			t.Error("MCP stream metadata lost")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Mcp-Session-Id", "session-123")
		_, _ = io.WriteString(w, "id: event-1\ndata: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "id: event-2\ndata: second\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	server := publishProxy(t, admin, ts.URL, "stream-proxy", upstream.URL+"/mcp", nil)
	// Simulate a listener whose normal responses have a short write deadline.
	streamServer := httptest.NewUnstartedServer(a.Handler())
	streamServer.Config.WriteTimeout = 50 * time.Millisecond
	streamServer.Start()
	defer streamServer.Close()
	req, _ := http.NewRequest("GET", streamServer.URL+"/mcp/"+server.Slug+"?token="+server.Token, nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", "session-123")
	req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	req.Header.Set("Last-Event-Id", "event-0")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Mcp-Session-Id") != "session-123" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatal("session header or streaming response headers missing")
	}
	reader := bufio.NewReader(response.Body)
	for _, expected := range []string{"id: event-1\n", "data: first\n", "\n"} {
		line, err := reader.ReadString('\n')
		if err != nil || line != expected {
			t.Fatalf("first event was buffered: %q, %v", line, err)
		}
	}
	<-time.After(150 * time.Millisecond)
	close(release)
	for _, expected := range []string{"id: event-2\n", "data: second\n", "\n"} {
		line, err := reader.ReadString('\n')
		if err != nil || line != expected {
			t.Fatalf("SSE stream did not survive normal write deadline: %q, %v", line, err)
		}
	}
	response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("disconnect did not cancel the upstream stream")
	}
}

func TestMCPProxyDraftsOverridesAuthAndErrors(t *testing.T) {
	a, ts, admin := testApp(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer upstream-secret" || r.URL.Query().Get("token") != "upstream-token" || r.Header.Get("X-Discard") != "" || r.Header.Get("Cookie") != "business=client" {
			t.Error("proxy credentials, cookies or hop-by-hop headers incorrect")
		}
		w.Header().Add("Set-Cookie", "api2mcp_session=untrusted")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1234567890123456789,"error":{"code":-32000,"message":"upstream error"}}`)
	}))
	defer upstream.Close()
	server := publishProxy(t, admin, ts.URL, "auth-proxy", "http://unused.invalid/mcp", map[string]string{"Authorization": "Bearer upstream-secret"})
	path := "/mcp/" + server.Slug
	if status := request(t, http.DefaultClient, ts.URL, path, "POST", nil, nil); status != 401 {
		t.Fatalf("unauthenticated proxy: %d", status)
	}
	if status := request(t, http.DefaultClient, ts.URL, "/api/servers/"+server.ID+"/proxy-config", "GET", nil, nil); status != 401 {
		t.Fatalf("anonymous credential read: %d", status)
	}
	target := upstream.URL + "/remote?token=upstream-token"
	req, _ := http.NewRequest("POST", ts.URL+path+"?token="+server.Token, strings.NewReader(`{"jsonrpc":"2.0","id":1234567890123456789,"method":"tools/list"}`))
	req.Header.Set("base_url", target)
	req.Header.Set("Authorization", "Bearer "+server.Token)
	req.Header.Set("Cookie", "api2mcp_session=private-admin-cookie; business=client")
	req.Header.Set("Connection", "X-Discard")
	req.Header.Set("X-Discard", "hop-by-hop")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 429 || !strings.Contains(string(data), "1234567890123456789") || response.Header.Get("Set-Cookie") != "" || calls.Load() != 1 {
		t.Fatalf("upstream response was not preserved: HTTP %d, %s", response.StatusCode, data)
	}
	// A saved draft must not change the live target or credentials.
	var updated Server
	draft := server.Draft
	draft.Proxy = &MCPProxyConfig{URL: target, Headers: map[string]string{"X-New": "pending-secret"}}
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID, "PUT", map[string]any{"mode": "draft", "draft": draft}, &updated); status != 200 || updated.Pending == nil || updated.Token != server.Token {
		t.Fatal("draft/update lifecycle changed")
	}
	stored := a.store.Snapshot().Servers[0]
	if stored.Proxy.URL != "http://unused.invalid/mcp" || stored.Pending.Proxy.Headers["X-New"] != "pending-secret" || stored.Proxy.Headers["Authorization"] != "Bearer upstream-secret" {
		t.Fatal("pending proxy changed live configuration")
	}
	var config MCPProxyConfig
	request(t, admin, ts.URL, "/api/servers/"+server.ID+"/proxy-config", "GET", nil, &config)
	if config.Headers["X-New"] != "pending-secret" {
		t.Fatal("editor did not retrieve pending credentials")
	}
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/state", "POST", map[string]string{"status": "stopped"}, nil); status != 200 {
		t.Fatal(status)
	}
	if status := request(t, http.DefaultClient, ts.URL, path+"?token="+server.Token, "POST", nil, nil); status != 503 || calls.Load() != 1 {
		t.Fatal("stopped proxy reached upstream")
	}
	request(t, admin, ts.URL, "/api/servers/"+server.ID+"/state", "POST", map[string]string{"status": "running"}, nil)
	// Connection failures must not expose URLs or saved credentials in responses/logs.
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	failed, _ := http.NewRequest("POST", ts.URL+path+"?token="+server.Token, nil)
	failed.Header.Set("base_url", closed.URL+"/mcp?secret=must-not-leak")
	response, err = http.DefaultClient.Do(failed)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(response.Body)
	response.Body.Close()
	logs, _ := json.Marshal(a.store.Snapshot().Logs)
	if response.StatusCode != 502 || strings.Contains(string(data)+string(logs), "must-not-leak") || strings.Contains(string(data)+string(logs), "upstream-secret") {
		t.Fatalf("unsafe proxy error: %s", data)
	}
}

func TestMCPProxyValidationAndLoopDetection(t *testing.T) {
	a, ts, admin := testApp(t)
	for _, config := range []*MCPProxyConfig{
		nil, {URL: ""}, {URL: "file:///tmp/example"}, {URL: "https://user:pass@example.com/mcp"},
		{URL: "https://example.com/mcp", Headers: map[string]string{"Host": "evil"}},
		{URL: "https://example.com/mcp", Headers: map[string]string{"Mcp-Session-Id": "fixed"}},
		{URL: "https://example.com/mcp", Headers: map[string]string{"X-Key": "one", "x-key": "two"}},
		{URL: "https://example.com/mcp", Headers: map[string]string{"X-Key": "one\r\nInjected: value"}},
	} {
		var result map[string]any
		input := map[string]any{"mode": "publish", "draft": Draft{Type: "proxy", Name: "invalid", Slug: "invalid", Color: "blue", Proxy: config}}
		if status := request(t, admin, ts.URL, "/api/servers", "POST", input, &result); status != 400 {
			t.Fatalf("invalid proxy accepted: %#v, HTTP %d", config, status)
		}
	}
	server := publishProxy(t, admin, ts.URL, "loop-proxy", "http://127.0.0.1/mcp", nil)
	draft := server.Draft
	draft.Proxy = &MCPProxyConfig{URL: ts.URL + "/mcp/" + server.Slug + "?token=" + server.Token}
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID, "PUT", map[string]any{"mode": "publish", "draft": draft}, nil); status != 200 {
		t.Fatal(status)
	}
	if status := request(t, http.DefaultClient, ts.URL, "/mcp/"+server.Slug+"?token="+server.Token, "POST", nil, nil); status != 508 {
		t.Fatalf("proxy loop did not terminate: HTTP %d", status)
	}
	if len(a.store.Snapshot().Logs) > 8 {
		t.Fatal("proxy loop forwarded too many requests")
	}
	draft.Type, draft.Proxy = "api", nil
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID, "PUT", map[string]any{"mode": "draft", "draft": draft}, nil); status != 400 {
		t.Fatal("published proxy type changed")
	}
}

func TestMCPProxyConfigPersistsEncrypted(t *testing.T) {
	a, ts, admin := testApp(t)
	server := publishProxy(t, admin, ts.URL, "persisted-proxy", "https://mcp.example.com/mcp?tenant=demo", map[string]string{"Authorization": "Bearer persisted-proxy-secret"})
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	store, fresh, err := OpenStore(a.cfg.DataPath, a.cfg.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored := store.Snapshot().Servers[0]
	if fresh || restored.Type != "proxy" || restored.Token != server.Token || restored.Proxy.Headers["Authorization"] != "Bearer persisted-proxy-secret" || restored.Proxy.URL != "https://mcp.example.com/mcp?tenant=demo" {
		t.Fatal("proxy settings did not survive reopening the existing encrypted database")
	}
}

func TestMCPProxyConnectionTestWithoutTools(t *testing.T) {
	_, ts, admin := testApp(t)
	impl := mcp.NewServer(&mcp.Implementation{Name: "resources-only", Version: "1"}, nil)
	impl.AddResource(&mcp.Resource{URI: "demo://info", Name: "Info"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "demo://info", Text: "ok"}}}, nil
	})
	upstream := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return impl }, nil))
	defer upstream.Close()
	server := publishProxy(t, admin, ts.URL, "resources-proxy", upstream.URL, nil)
	var result map[string]any
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/test", "POST", nil, &result); status != 200 || result["success"] != true || len(result["tools"].([]any)) != 0 {
		t.Fatalf("valid resources-only MCP should pass connection test: %v", result)
	}
}
