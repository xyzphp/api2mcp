package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	web "api2mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testAdminToken = "test-admin-token-with-at-least-24-chars"

func testApp(t *testing.T) (*App, *httptest.Server, *http.Client) {
	t.Helper()
	cfg := Config{Addr: ":8080", AdminToken: testAdminToken, EncryptionKey: bytes.Repeat([]byte{7}, 32), DataPath: filepath.Join(t.TempDir(), "state.db"), PublicOrigin: "http://localhost:8080", UpstreamTimeout: 2 * time.Second, MockAPIURL: "http://127.0.0.1:9090/v1"}
	a, err := NewApp(cfg, web.Files)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.Handler())
	a.internalOrigin = ts.URL
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	if status := request(t, client, ts.URL, "/api/login", "POST", map[string]string{"token": testAdminToken}, nil); status != 200 {
		t.Fatalf("login: %d", status)
	}
	t.Cleanup(func() { ts.Close(); _ = a.Close() })
	return a, ts, client
}

func TestOriginAndCookieSchemeSupportPublicAndLANAccess(t *testing.T) {
	app := &App{cfg: Config{PublicOrigin: "https://mcp.example.com"}, auth: newAuth(testAdminToken, true)}
	lan := &http.Request{Host: "192.168.1.10:18080", Header: http.Header{"Origin": []string{"http://192.168.1.10:18080"}}}
	if !app.checkOrigin(lan) {
		t.Fatal("same-origin LAN HTTP request was rejected")
	}
	if app.cookieSecure(lan) {
		t.Fatal("LAN HTTP session cookie must not require HTTPS")
	}
	proxied := &http.Request{Host: "mcp.example.com", Header: http.Header{"Origin": []string{"https://mcp.example.com"}, "X-Forwarded-Proto": []string{"https"}}}
	if !app.checkOrigin(proxied) || !app.cookieSecure(proxied) {
		t.Fatal("HTTPS reverse-proxy request must remain same-origin with a secure cookie")
	}
	crossOrigin := &http.Request{Host: "mcp.example.com", Header: http.Header{"Origin": []string{"https://attacker.example"}, "X-Forwarded-Proto": []string{"https"}}}
	if app.checkOrigin(crossOrigin) {
		t.Fatal("cross-origin request was accepted")
	}
}

func request(t *testing.T, client *http.Client, origin, path, method string, input, output any) int {
	t.Helper()
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, origin+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "API2MCP")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response.StatusCode
}

func TestConfiguredBasePathSupportsReverseProxy(t *testing.T) {
	cfg := Config{Addr: ":8080", AdminToken: testAdminToken, EncryptionKey: bytes.Repeat([]byte{9}, 32), DataPath: filepath.Join(t.TempDir(), "state.db"), PublicOrigin: "https://mcp.example.com", BasePath: "/http_mcp", UpstreamTimeout: 2 * time.Second, MockAPIURL: "http://127.0.0.1:9090/v1"}
	a, err := NewApp(cfg, web.Files)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(a.Handler())
	defer func() { ts.Close(); _ = a.Close() }()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	response, err := client.Get(ts.URL + "/http_mcp")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/http_mcp/login" {
		t.Fatalf("prefixed page redirect: status %d, location %q", response.StatusCode, response.Header.Get("Location"))
	}
	response, err = client.Get(ts.URL + "/http_mcp/login")
	if err != nil {
		t.Fatal(err)
	}
	login, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(login), `<base href="/http_mcp/">`) || !strings.Contains(string(login), `src="assets/login.js"`) {
		t.Fatalf("prefixed login page was not rendered correctly: status %d, error %v", response.StatusCode, err)
	}
	response, err = client.Get(ts.URL + "/http_mcp/assets/login.js")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prefixed asset status %d", response.StatusCode)
	}
	if status := request(t, client, ts.URL, "/http_mcp/api/login", "POST", map[string]string{"token": testAdminToken}, nil); status != http.StatusOK {
		t.Fatalf("prefixed login API status %d", status)
	}
	var workspace Workspace
	lanRequest, _ := http.NewRequest("GET", ts.URL+"/http_mcp/api/workspace", nil)
	lanRequest.Host = "192.168.1.10:18080"
	lanRequest.Header.Set("Origin", "http://192.168.1.10:18080")
	response, err = client.Do(lanRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("LAN workspace API status %d", response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&workspace)
	response.Body.Close()
	if err != nil || workspace.Settings.EndpointOrigin != "http://192.168.1.10:18080/http_mcp" {
		t.Fatalf("MCP endpoint origin missing LAN host and proxy prefix: %q, error %v", workspace.Settings.EndpointOrigin, err)
	}
	publicRequest, _ := http.NewRequest("GET", ts.URL+"/http_mcp/api/workspace", nil)
	publicRequest.Host = "mcp.example.com"
	publicRequest.Header.Set("Origin", "https://mcp.example.com")
	publicRequest.Header.Set("X-Forwarded-Proto", "https")
	response, err = client.Do(publicRequest)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("proxied workspace API status %d", response.StatusCode)
	}
	err = json.NewDecoder(response.Body).Decode(&workspace)
	response.Body.Close()
	if err != nil || workspace.Settings.EndpointOrigin != "https://mcp.example.com/http_mcp" {
		t.Fatalf("MCP endpoint origin missing public host and proxy prefix: %q, error %v", workspace.Settings.EndpointOrigin, err)
	}
	for _, route := range []string{"/healthz", "/http_mcp/healthz"} {
		response, err := client.Get(ts.URL + route)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("health route %s returned %d", route, response.StatusCode)
		}
	}
}

func importSpec(t *testing.T, client *http.Client, origin, spec string) Document {
	t.Helper()
	var doc Document
	if status := request(t, client, origin, "/api/documents", "POST", map[string]string{"content": spec, "filename": "test.json"}, &doc); status != 201 {
		t.Fatalf("import status %d", status)
	}
	return doc
}
func publish(t *testing.T, client *http.Client, origin, slug string, ops ...Operation) Server {
	t.Helper()
	ids := []string{}
	for _, op := range ops {
		ids = append(ids, op.ID)
	}
	var server Server
	if status := request(t, client, origin, "/api/servers", "POST", map[string]any{"mode": "publish", "draft": Draft{Name: slug, Slug: slug, Color: "blue", OperationIDs: ids}}, &server); status != 200 {
		t.Fatalf("publish %d", status)
	}
	return server
}
func basicSpec(base string) string {
	return fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Test","version":"1"},"servers":[{"url":%q}],"paths":{"/users":{"get":{"operationId":"listUsers","summary":"List users","responses":{"200":{"description":"ok"}}}},"/users/{id}":{"get":{"operationId":"getUser","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`, base)
}

func TestAdminAuthCSRFAndLogout(t *testing.T) {
	_, ts, client := testApp(t)
	anonymous := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, path := range []string{"/", "/index.html"} {
		response, err := anonymous.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 303 || response.Header.Get("Location") != "/login" {
			t.Fatalf("unprotected HTML: %s", path)
		}
	}
	if status := request(t, anonymous, ts.URL, "/api/workspace", "GET", nil, nil); status != 401 {
		t.Fatalf("anonymous API %d", status)
	}
	u, _ := http.NewRequest("GET", ts.URL, nil)
	cookies := client.Jar.Cookies(u.URL)
	if len(cookies) != 1 {
		t.Fatal("expected session cookie")
	}
	for _, origin := range []string{"https://attacker.example", "null"} {
		req, _ := http.NewRequest("DELETE", ts.URL+"/api/logs", nil)
		req.Header.Set("Origin", origin)
		req.Header.Set("X-Requested-With", "API2MCP")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatalf("cross origin accepted %d", response.StatusCode)
		}
	}
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/logs", nil)
	response, _ := client.Do(req)
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("mutation without CSRF header accepted")
	}
	if status := request(t, client, ts.URL, "/api/logout", "POST", nil, nil); status != 200 {
		t.Fatal(status)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/workspace", nil)
	req.AddCookie(cookies[0])
	response, _ = anonymous.Do(req)
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("logged-out cookie still valid")
	}
}

func TestLoginRateLimitAndCookieFlags(t *testing.T) {
	_, ts, _ := testApp(t)
	client := &http.Client{}
	req, _ := http.NewRequest("POST", ts.URL+"/api/login", strings.NewReader(`{"token":"`+testAdminToken+`"}`))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	cookie := response.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 28800 || cookie.Value == testAdminToken {
		t.Fatal("unsafe cookie")
	}
	for i := 0; i < 11; i++ {
		status := request(t, client, ts.URL, "/api/login", "POST", map[string]string{"token": "wrong"}, nil)
		want := 401
		if i == 10 {
			want = 429
		}
		if status != want {
			t.Fatalf("attempt %d: %d", i, status)
		}
	}
}

func TestMCPForwardingValidationAndTokenIsolation(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "PATCH" || r.URL.EscapedPath() != "/v1/users/a%2Fb" {
			t.Errorf("wrong method or path: %s %s", r.Method, r.URL.EscapedPath())
		}
		if r.Header.Get("Authorization") != "Bearer private-api-secret" || r.Header.Get("X-Trace") != "trace-123" {
			t.Error("credentials/header not forwarded")
		}
		if strings.Join(r.URL.Query()["tags"], ",") != "go,mcp" || r.URL.Query().Get("limit") != "2" {
			t.Error("query serialization failed")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] == "reject" {
			w.WriteHeader(422)
		} else {
			w.WriteHeader(201)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"received": body})
	}))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Proxy","version":"1"},"servers":[{"url":%q}],"paths":{"/users/{id}":{"patch":{"operationId":"updateUser","parameters":[{"in":"path","name":"id","required":true,"schema":{"type":"string"}},{"in":"query","name":"tags","schema":{"type":"array","items":{"type":"string"}}},{"in":"query","name":"limit","schema":{"type":"integer","minimum":1}},{"in":"header","name":"X-Trace","schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1}},"additionalProperties":false}}}},"responses":{"201":{"description":"ok"}}}}}}`, upstream.URL+"/v1")
	doc := importSpec(t, admin, ts.URL, spec)
	var credential Document
	status := request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]string{"baseUrl": doc.BaseURL, "kind": "bearer", "header": "X-API-Key", "value": "private-api-secret"}, &credential)
	if status != 200 || credential.Credential.Value != "" || !credential.Credential.Configured {
		t.Fatal("credential leaked or not saved")
	}
	first := publish(t, admin, ts.URL, "first", doc.Operations...)
	second := publish(t, admin, ts.URL, "second", doc.Operations...)
	if first.Token == second.Token || first.Token == testAdminToken {
		t.Fatal("tokens not independent")
	}
	for _, token := range []string{"", testAdminToken, second.Token} {
		req, _ := http.NewRequest("POST", ts.URL+"/mcp/first", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("MCP auth bypass")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.connect(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Annotations.ReadOnlyHint {
		t.Fatal("wrong tool list or annotation")
	}
	args := map[string]any{"path": map[string]any{"id": "a/b"}, "query": map[string]any{"tags": []string{"go", "mcp"}, "limit": 2}, "header": map[string]any{"X-Trace": "trace-123"}, "body": map[string]any{"name": "张三"}}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("call failed: %v", err)
	}
	delete(args, "body")
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: args})
	if err != nil || !result.IsError || calls.Load() != 1 {
		t.Fatal("missing required body reached upstream")
	}
	args["body"] = map[string]any{"name": "reject"}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: args})
	if err != nil || !result.IsError || calls.Load() != 2 {
		t.Fatal("HTTP error not exposed as tool error")
	}
	var workspace Workspace
	request(t, admin, ts.URL, "/api/workspace", "GET", nil, &workspace)
	if workspace.Documents[0].Credential.Value != "" || len(workspace.Logs) != 3 {
		t.Fatal("redaction/logs failed")
	}
}

func TestDraftPublicationPauseRotationAndReferenceProtection(t *testing.T) {
	a, ts, admin := testApp(t)
	doc := importSpec(t, admin, ts.URL, basicSpec("http://127.0.0.1:9090/v1"))
	server := publish(t, admin, ts.URL, "stable", doc.Operations[0])
	draft := server.Draft
	draft.Name = "Changed"
	draft.OperationIDs = []string{doc.Operations[1].ID}
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID, "PUT", map[string]any{"mode": "draft", "draft": draft}, nil); status != 200 {
		t.Fatal(status)
	}
	ctx := context.Background()
	session, err := a.connect(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, _ := session.ListTools(ctx, &mcp.ListToolsParams{})
	if tools.Tools[0].Name != doc.Operations[0].ToolName {
		t.Fatal("draft affected published tools")
	}
	if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID, "DELETE", nil, nil); status != 409 {
		t.Fatal("referenced document deleted")
	}
	var updated Server
	request(t, admin, ts.URL, "/api/servers/"+server.ID, "PUT", map[string]any{"mode": "publish", "draft": draft}, &updated)
	if updated.Version != 2 || updated.Slug != server.Slug || updated.Token != server.Token || updated.Pending != nil {
		t.Fatal("publication changed identity")
	}
	tools, err = session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || tools.Tools[0].Name != doc.Operations[1].ToolName {
		t.Fatalf("published tool list stale: %v", err)
	}
	request(t, admin, ts.URL, "/api/servers/"+server.ID+"/state", "POST", map[string]string{"status": "stopped"}, nil)
	if _, err := session.ListTools(ctx, &mcp.ListToolsParams{}); err == nil {
		t.Fatal("paused service accessible")
	}
	request(t, admin, ts.URL, "/api/servers/"+server.ID+"/state", "POST", map[string]string{"status": "running"}, nil)
	request(t, admin, ts.URL, "/api/servers/"+server.ID+"/token", "POST", nil, nil)
	if _, err := session.ListTools(ctx, &mcp.ListToolsParams{}); err == nil {
		t.Fatal("rotated old token accessible")
	}
	request(t, admin, ts.URL, "/api/servers/"+server.ID, "DELETE", nil, nil)
	if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID, "DELETE", nil, nil); status != 200 {
		t.Fatal(status)
	}
}

func TestAdminMCPConnectionAndToolCall(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"data":[{"id":"usr_001"}]}`)) }))
	defer upstream.Close()
	_, ts, admin := testApp(t)
	doc := importSpec(t, admin, ts.URL, basicSpec(upstream.URL))
	server := publish(t, admin, ts.URL, "ui", doc.Operations[0])
	var checked struct {
		Success bool       `json:"success"`
		Tools   []mcp.Tool `json:"tools"`
	}
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/test", "POST", nil, &checked); status != 200 || !checked.Success || len(checked.Tools) != 1 {
		t.Fatal("admin connection test failed")
	}
	var result mcp.CallToolResult
	if status := request(t, admin, ts.URL, "/api/servers/"+server.ID+"/call", "POST", map[string]any{"name": doc.Operations[0].ToolName, "arguments": map[string]any{}}, &result); status != 200 || result.IsError {
		t.Fatal("admin call failed")
	}
}

func TestStoreEncryptionRestartSnapshotAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	key := bytes.Repeat([]byte{1}, 32)
	store, fresh, err := OpenStore(path, key)
	if err != nil || !fresh {
		t.Fatal(err)
	}
	secret := "persistent-secret-not-in-plaintext"
	err = store.Update(func(w *Workspace) error {
		w.Servers = append(w.Servers, Server{ID: "one", Token: secret, Draft: Draft{Name: "Original"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	snapshot.Servers[0].Name = "Mutable"
	if store.Snapshot().Servers[0].Name != "Original" {
		t.Fatal("snapshot aliases storage")
	}
	for _, name := range []string{path, path + "-wal"} {
		b, _ := os.ReadFile(name)
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("plaintext secret in database")
		}
	}
	store.Close()
	store, fresh, err = OpenStore(path, key)
	if err != nil || fresh || store.Snapshot().Servers[0].Token != secret {
		t.Fatal("restart lost state", err)
	}
	store.Close()
	if err := store.Update(func(w *Workspace) error { w.Servers = nil; return nil }); err == nil || len(store.Snapshot().Servers) != 1 {
		t.Fatal("failed commit changed memory")
	}
	if wrong, _, err := OpenStore(path, bytes.Repeat([]byte{2}, 32)); err == nil {
		wrong.Close()
		t.Fatal("wrong key accepted")
	}
}

func TestConcurrentStoreMutations(t *testing.T) {
	store, _, err := OpenStore(filepath.Join(t.TempDir(), "state.db"), bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := store.Update(func(w *Workspace) error { w.Logs = append(w.Logs, CallLog{ID: fmt.Sprint(i)}); return nil }); err != nil {
				t.Error(err)
			}
			_ = store.Snapshot()
		}(i)
	}
	wg.Wait()
	if len(store.Snapshot().Logs) != 40 {
		t.Fatal("lost concurrent mutation")
	}
}

func TestRemoteImportLocalAPIAndRedirectResponse(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
		_, _ = w.Write([]byte(basicSpec("https://example.com")))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	_, ts, admin := testApp(t)
	if status := request(t, admin, ts.URL, "/api/documents", "POST", map[string]string{"url": target.URL}, nil); status != 201 || redirected.Load() != 1 {
		t.Fatal("local document import failed without target configuration")
	}
	if status := request(t, admin, ts.URL, "/api/documents", "POST", map[string]string{"url": redirect.URL}, nil); status != 400 || redirected.Load() != 1 {
		t.Fatal("redirect followed")
	}
}

func TestUpstreamLocalIPAndHostnameWithoutProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	a, _, _ := testApp(t)
	for _, address := range []string{upstream.URL, strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)} {
		req, _ := http.NewRequest("GET", address, nil)
		result, err := a.executeUpstream(req)
		if err != nil || result.Status != http.StatusNoContent {
			t.Fatalf("local target required configuration or inherited a proxy: %s, %v", address, err)
		}
	}
}

func TestSeedMockMultipleEndpoints(t *testing.T) {
	cfg := Config{Addr: ":8080", AdminToken: testAdminToken, EncryptionKey: bytes.Repeat([]byte{8}, 32), DataPath: filepath.Join(t.TempDir(), "state.db"), SeedMock: true, MockAPIURL: "http://mock-api:9090/v1", UpstreamTimeout: time.Second}
	a, err := NewApp(cfg, web.Files)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	w := a.store.Snapshot()
	if len(w.Documents) != 1 || len(w.Servers) != 2 || w.Servers[0].Token == w.Servers[1].Token || w.Servers[0].Slug == w.Servers[1].Slug {
		t.Fatal("invalid seed")
	}
}

func TestDocumentUpdatesKeepIDsCredentialsAndProtectRemovedTools(t *testing.T) {
	_, ts, admin := testApp(t)
	doc := importSpec(t, admin, ts.URL, basicSpec("https://example.com/v1"))
	request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]string{"baseUrl": "https://upstream.example/v2", "kind": "apiKey", "header": "X-API-Key", "value": "do-not-return-this-secret"}, nil)
	server := publish(t, admin, ts.URL, "document-update", doc.Operations...)
	var changed Document
	status := request(t, admin, ts.URL, "/api/documents/"+doc.ID, "PUT", map[string]string{"content": strings.Replace(basicSpec("https://ignored.example"), "List users", "New summary", 1), "filename": "new.json"}, &changed)
	if status != 200 || changed.ID != doc.ID || changed.Operations[0].ID != doc.Operations[0].ID || changed.Operations[0].ToolName != doc.Operations[0].ToolName || changed.BaseURL != "https://upstream.example/v2" || changed.Credential.Value != "" || !changed.Credential.Configured {
		t.Fatal("update lost stable configuration or leaked credential")
	}
	var workspace Workspace
	request(t, admin, ts.URL, "/api/workspace", "GET", nil, &workspace)
	if workspace.Servers[0].Version != server.Version+1 {
		t.Fatal("document update did not advance service version")
	}
	removed := `{"openapi":"3.0.3","info":{"title":"Removed","version":"1"},"paths":{"/users":{"get":{"responses":{"200":{"description":"ok"}}}}}}`
	if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID, "PUT", map[string]string{"content": removed}, nil); status != 409 {
		t.Fatal("in-use operation removed")
	}
}

func TestLegacyMCPHTTPProtocol(t *testing.T) {
	_, ts, admin := testApp(t)
	doc := importSpec(t, admin, ts.URL, basicSpec("https://example.com/v1"))
	server := publish(t, admin, ts.URL, "legacy-client", doc.Operations[0])
	post := func(body string) map[string]any {
		r, _ := http.NewRequest("POST", ts.URL+"/mcp/"+server.Slug, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.Header.Set("Mcp-Protocol-Version", "2025-11-25")
		r.Header.Set("Authorization", "Bearer "+server.Token)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatal("legacy HTTP", response.StatusCode)
		}
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	init := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy-test","version":"1"}}}`)
	if obj(init["result"])["protocolVersion"] != "2025-11-25" {
		t.Fatal("legacy negotiation failed")
	}
	list := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if len(arr(obj(list["result"])["tools"])) != 1 {
		t.Fatal("legacy tool list failed")
	}
}

func TestStoreRejectsConcurrentProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	key := bytes.Repeat([]byte{4}, 32)
	first, _, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if other, _, err := OpenStore(path, key); err == nil {
		other.Close()
		t.Fatal("second writer process accepted")
	}
}
