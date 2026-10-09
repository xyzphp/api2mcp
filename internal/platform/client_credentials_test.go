package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type clientCredentialTransport struct{ headers http.Header }

func (t clientCredentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	for name, values := range t.headers {
		req.Header[name] = append([]string(nil), values...)
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestMCPClientCredentialPrecedenceScopeAndPersistence(t *testing.T) {
	type capture struct {
		auth, key, query, cookie string
		body                     map[string]any
	}
	captured := make(chan capture, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		_ = decoder.Decode(&body)
		cookie, _ := r.Cookie("session")
		value := ""
		if cookie != nil {
			value = cookie.Value
		}
		if r.Header.Get(clientCredentialsHeader) != "" {
			t.Error("MCP transport credential envelope leaked to upstream")
		}
		captured <- capture{r.Header.Get("Authorization"), r.Header.Get("X-API-Key"), r.URL.Query().Get("tenant"), value, body}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	var docs []Document
	for _, path := range []string{"/one", "/two"} {
		spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":%q,"version":"1"},"servers":[{"url":%q}],"paths":{%q:{"post":{"operationId":"send","parameters":[{"in":"header","name":"X-API-Key","required":true,"schema":{"type":"string"}},{"in":"query","name":"tenant","required":true,"schema":{"type":"string"}},{"in":"cookie","name":"session","required":true,"schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["name","auth"],"properties":{"name":{"type":"string"},"auth":{"type":"object","required":["token"],"properties":{"token":{"type":"string"}}}}}}}},"responses":{"200":{"description":"ok"}}}}}}`, path, upstream.URL, path)
		if _, err := ParseDocument([]byte(spec), "test.json", ""); err != nil {
			t.Fatal("invalid test specification", err)
		}
		doc := importSpec(t, admin, ts.URL, spec)
		status := request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{
			"baseUrl": upstream.URL, "kind": "basic", "username": "saved-user", "value": "saved-password",
			"entries": []map[string]any{{"in": "header", "name": "X-API-Key", "value": "saved-key"}, {"in": "query", "name": "tenant", "value": "saved-query"}, {"in": "cookie", "name": "session", "value": "saved-cookie"}, {"in": "body", "name": "/auth/token", "value": "saved-body"}},
		}, nil)
		if status != 200 {
			t.Fatal("credential setup failed")
		}
		docs = append(docs, doc)
	}
	server := publish(t, admin, ts.URL, "client-creds", docs[0].Operations[0], docs[1].Operations[0])
	before, _ := json.Marshal(a.store.Snapshot().Documents)
	config := fmt.Sprintf(`{"auth":{"type":"bearer","token":"global-auth"},"headers":{"x-api-key":"global-key"},"query":{"tenant":"global-query"},"cookies":{"session":"global-cookie"},"body":{"auth":{"token":"global-body"},"exact":1234567890123456789},"documents":{%q:{"auth":{"type":"basic","username":"client-user","password":"client-password"},"headers":{"X-API-Key":"document-key"},"query":{"tenant":"document-query"},"cookies":{"session":"document-cookie"},"body":{"auth":{"token":"document-body"}}}}}`, docs[0].ID)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "json-client", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp/" + server.Slug, HTTPClient: &http.Client{Transport: clientCredentialTransport{http.Header{"Authorization": {"Bearer " + server.Token}, clientCredentialsHeader: {config}}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 2 {
		t.Fatal("runtime tool schema unavailable", err)
	}
	listed, _ := json.Marshal(tools)
	for _, secret := range []string{"global-auth", "client-password", "document-key", "global-body", "saved-password"} {
		if bytes.Contains(listed, []byte(secret)) {
			t.Fatal("runtime credential leaked into tool listing")
		}
	}
	for i, doc := range docs {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: map[string]any{"body": map[string]any{"name": "business"}}})
		if err != nil || result.IsError {
			t.Fatalf("runtime credential forwarding failed: %v, %+v", err, result)
		}
		got := <-captured
		if got.body["name"] != "business" || got.body["exact"] != json.Number("1234567890123456789") {
			t.Fatal("business body or precise runtime JSON number lost")
		}
		if i == 0 {
			expected := &http.Request{Header: http.Header{}}
			expected.SetBasicAuth("client-user", "client-password")
			if got.auth != expected.Header.Get("Authorization") || got.key != "document-key" || got.query != "document-query" || got.cookie != "document-cookie" || obj(got.body["auth"])["token"] != "document-body" {
				t.Fatal("document credentials did not override global and saved values")
			}
		} else if got.auth != "Bearer global-auth" || got.key != "global-key" || got.query != "global-query" || got.cookie != "global-cookie" || obj(got.body["auth"])["token"] != "global-body" {
			t.Fatal("global credentials did not override saved values or document scope leaked")
		}
	}
	// A separate caller with no client configuration still gets the saved values.
	defaultSession, err := a.connect(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	defer defaultSession.Close()
	result, err := defaultSession.CallTool(ctx, &mcp.CallToolParams{Name: docs[0].Operations[0].ToolName, Arguments: map[string]any{"body": map[string]any{"name": "business"}}})
	if err != nil || result.IsError {
		t.Fatal("saved credential fallback failed", err)
	}
	got := <-captured
	if got.key != "saved-key" || got.query != "saved-query" || got.cookie != "saved-cookie" || obj(got.body["auth"])["token"] != "saved-body" {
		t.Fatal("runtime values persisted across callers")
	}
	after, _ := json.Marshal(a.store.Snapshot().Documents)
	logs, _ := json.Marshal(a.store.Snapshot().Logs)
	if !bytes.Equal(before, after) || bytes.Contains(logs, []byte("client-password")) || bytes.Contains(logs, []byte("document-body")) {
		t.Fatal("runtime credentials changed storage or leaked to logs")
	}
}

func TestMCPClientCredentialHeaderValidationAndAuthentication(t *testing.T) {
	_, ts, admin := testApp(t)
	doc := importSpec(t, admin, ts.URL, basicSpec("https://example.test"))
	server := publish(t, admin, ts.URL, "credential-validation", doc.Operations...)
	for _, config := range []string{
		`invalid`, `[]`, `null`, `{"other":"private-secret"}`, `{"query":{"token":42}}`, `{"body":[]}`, `{"body":null}`,
		`{"auth":{"type":"unknown"}}`, `{"auth":{"type":"basic","username":"","password":"private-secret"}}`,
		`{"headers":{"Host":"private-secret"}}`, `{"headers":{"X-Key":"private-secret\r\nextra"}}`,
		`{"headers":{"X-Key":"a","x-key":"b"}}`, `{"documents":{"unrelated-document":{}}}`, `{"bodyFormat":"xml"}`,
		`{"headers":{"X-Key":"` + strings.Repeat("a", 16385) + `"}}`,
	} {
		req, _ := http.NewRequest("POST", ts.URL+"/mcp/"+server.Slug, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+server.Token)
		req.Header.Set(clientCredentialsHeader, config)
		response, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		_ = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if response.StatusCode != 400 || strings.Contains(fmt.Sprint(result), "private-secret") {
			t.Fatalf("invalid runtime credentials accepted or echoed: %d", response.StatusCode)
		}
	}
	for _, token := range []string{"", testAdminToken} {
		req, _ := http.NewRequest("POST", ts.URL+"/mcp/"+server.Slug, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(clientCredentialsHeader, `{"auth":{"type":"bearer","token":"upstream-token"}}`)
		response, err := admin.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("upstream credential bypassed MCP access token")
		}
	}
}

func TestClientAuthorizationHeaderOverridesSavedPreset(t *testing.T) {
	for _, saved := range []Credential{{Kind: "basic", Username: "old-user", Value: "old-password"}, {Kind: "bearer", Value: "old-token"}, {Kind: "apiKey", Header: "X-API-Key", Value: "old-key"}} {
		name := "Authorization"
		if saved.Kind == "apiKey" {
			name = "x-api-key"
		}
		merged, err := applyClientCredentialLayer(saved, clientCredentialLayer{Headers: map[string]string{name: "runtime-value"}})
		if err != nil {
			t.Fatal(err)
		}
		req, err := buildUpstreamRequest(context.Background(), Document{BaseURL: "https://example.test", Credential: merged}, Operation{Method: "GET", Path: "/"}, nil)
		if err != nil || req.Header.Get(name) != "runtime-value" {
			t.Fatal("saved preset overrode an explicit client header")
		}
	}
}

func TestClientCredentialValuesOverrideToolArguments(t *testing.T) {
	doc := Document{ID: "doc", BaseURL: "https://example.test", Credential: Credential{Kind: "bearer", Value: "saved-auth", Entries: []CredentialEntry{
		{Location: "query", Name: "token", Value: "saved-query", Enabled: true},
		{Location: "cookie", Name: "session", Value: "saved-cookie", Enabled: true},
		{Location: "body", Name: "/auth/token", Value: "saved-body", Enabled: true},
	}}}
	config := clientCredentialConfig{clientCredentialLayer: clientCredentialLayer{
		Headers: map[string]string{"authorization": "client-auth"}, Query: map[string]string{"token": "client-query"},
		Cookies: map[string]string{"session": "client-cookie"}, Body: json.RawMessage(`{"auth":{"token":"client-body"}}`), BodyFormat: "form",
	}}
	credential, err := config.credentialFor(doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Credential = credential
	op := Operation{Method: "POST", Path: "/", Parameters: []Parameter{{Location: "header", Name: "Authorization"}, {Location: "query", Name: "token"}, {Location: "cookie", Name: "session"}}}
	args := map[string]any{"header": map[string]any{"Authorization": "caller-auth"}, "query": map[string]any{"token": "caller-query"}, "cookie": map[string]any{"session": "caller-cookie"}, "body": map[string]any{"name": "business", "auth": map[string]any{"token": "caller-body"}}}
	req, err := buildUpstreamRequest(context.Background(), doc, op, args)
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := req.Cookie("session")
	body, _ := io.ReadAll(req.Body)
	if req.Header.Get("Authorization") != "client-auth" || req.URL.Query().Get("token") != "client-query" || cookie == nil || cookie.Value != "client-cookie" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || bytes.Contains(body, []byte("caller-body")) || !bytes.Contains(body, []byte("client-body")) || !bytes.Contains(body, []byte("business")) {
		t.Fatal("tool arguments overrode client values, encoding or business fields were lost")
	}
}

func TestClientCredentialCookieAliasesAndBodyFormatPrecedence(t *testing.T) {
	saved := Credential{Kind: "none", Entries: []CredentialEntry{
		{Location: "cookie", Name: "session", Value: "saved-session", Enabled: true},
		{Location: "cookie", Name: "keep", Value: "saved-keep", Enabled: true},
		{Location: "header", Name: "Content-Type", Value: "application/json", Enabled: true},
	}}
	for _, layer := range []clientCredentialLayer{
		{Headers: map[string]string{"Cookie": "session=client-session"}, BodyFormat: "form"},
		{Auth: &clientAuth{Type: "apiKey", Name: "Cookie", Value: "session=client-session"}, BodyFormat: "form"},
	} {
		merged, err := applyClientCredentialLayer(saved, layer)
		if err != nil {
			t.Fatal(err)
		}
		req, err := buildUpstreamRequest(context.Background(), Document{BaseURL: "https://example.test", Credential: merged}, Operation{Method: "POST", Path: "/"}, map[string]any{"body": map[string]any{"name": "business"}})
		if err != nil {
			t.Fatal(err)
		}
		session, _ := req.Cookie("session")
		keep, _ := req.Cookie("keep")
		if session == nil || session.Value != "client-session" || keep == nil || keep.Value != "saved-keep" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatal("saved Cookie / Content-Type header overrode higher-layer client settings")
		}
	}
}
