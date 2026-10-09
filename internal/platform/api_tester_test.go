package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type apiTestResult struct {
	Success  bool                  `json:"success"`
	Duration int64                 `json:"duration"`
	Error    string                `json:"error"`
	Request  requestPreview        `json:"request"`
	Response *upstreamHTTPResponse `json:"response"`
}

func TestAPITestBeforePublicationAndCredentialRedaction(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		username, password, _ := r.BasicAuth()
		if username != "private-user" || password != "private-password" || r.Header.Get("X-Tenant") != "private-tenant" || r.Header.Get("X-Request-ID") != "request-123" {
			t.Error("tester did not share saved header credentials / case-insensitive business headers")
		}
		if r.URL.Query().Get("api_key") != "private-query" || strings.Join(r.URL.Query()["extra"], ",") != "one,two" || !strings.Contains(r.RequestURI, "item%2Fa") {
			t.Error("tester query or escaped path forwarding failed")
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "private-cookie" {
			t.Error("saved cookie missing")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`1234567890123456789`)) || !bytes.Contains(body, []byte(`"token":"private-body"`)) || !bytes.Contains(body, []byte(`"name":"business"`)) {
			t.Error("request body was not merged or lost JSON integer precision")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("X-Result", "first")
		w.Header().Add("X-Result", "second")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("{\"id\":1234567890123456789,\"ok\":true}\n"))
	}))
	defer upstream.Close()
	a, ts, client := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"API tester","version":"1"},"servers":[{"url":%q}],"paths":{"/items/{id}":{"post":{"operationId":"submit","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}},{"name":"api_key","in":"query","required":true,"schema":{"type":"string"}},{"name":"X-Tenant","in":"header","required":true,"schema":{"type":"string"}},{"name":"X-Request-ID","in":"header","required":true,"schema":{"type":"string"}},{"name":"session","in":"cookie","required":true,"schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","additionalProperties":false,"required":["name","auth","count"],"properties":{"name":{"type":"string"},"count":{"type":"integer"},"auth":{"type":"object","required":["token"],"properties":{"token":{"type":"string"}}}}}}}},"responses":{"201":{"description":"ok"}}}}}}`, upstream.URL)
	doc := importSpec(t, client, ts.URL, spec)
	operation := doc.Operations[0]
	credential := map[string]any{"baseUrl": upstream.URL, "kind": "basic", "username": "private-user", "value": "private-password", "entries": []map[string]any{
		{"in": "header", "name": "X-Tenant", "value": "private-tenant"}, {"in": "query", "name": "api_key", "value": "private-query"},
		{"in": "cookie", "name": "session", "value": "private-cookie"}, {"in": "body", "name": "/auth/token", "value": "private-body"},
	}}
	if request(t, client, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", credential, nil) != 200 {
		t.Fatal("save credentials failed")
	}
	var description struct {
		InputSchema map[string]any `json:"inputSchema"`
		BodyFormat  string         `json:"bodyFormat"`
	}
	if request(t, client, ts.URL, "/api/documents/"+doc.ID+"/test?operationId="+url.QueryEscape(operation.ID), "GET", nil, &description) != 200 {
		t.Fatal("describe failed")
	}
	if description.BodyFormat != "json" || obj(description.InputSchema["properties"])["query"] != nil || obj(obj(description.InputSchema["properties"])["body"])["properties"] == nil {
		t.Fatal("tester schema did not hide injected fields")
	}
	before, _ := json.Marshal(a.store.Snapshot().Documents)
	input := map[string]any{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "item/a"}, "header": map[string]any{"x-request-id": "request-123"}, "query": map[string]any{"extra": []string{"one", "two"}}}, "body": `{"name":"business","count":1234567890123456789,"auth":{"token":"caller-ignored"}}`, "bodyFormat": "configured"}
	var result apiTestResult
	if request(t, client, ts.URL, "/api/documents/"+doc.ID+"/test", "POST", input, &result) != 200 || !result.Success || result.Response == nil || result.Response.Status != 201 || calls.Load() != 1 {
		t.Fatalf("direct test failed: %+v", result)
	}
	if result.Response.Body != "{\"id\":1234567890123456789,\"ok\":true}\n" || result.Response.Size != len(result.Response.Body) || len(result.Response.Headers.Values("X-Result")) != 2 || result.Duration < 0 {
		t.Fatal("response body bytes / multi-value headers / metadata not preserved")
	}
	preview, _ := json.Marshal(result.Request)
	described, _ := json.Marshal(description)
	logs, _ := json.Marshal(a.store.Snapshot().Logs)
	for _, secret := range []string{"private-user", "private-password", "private-tenant", "private-query", "private-cookie", "private-body"} {
		if bytes.Contains(preview, []byte(secret)) || bytes.Contains(described, []byte(secret)) || bytes.Contains(logs, []byte(secret)) {
			t.Fatal("credential leaked into request preview, input description or logs")
		}
	}
	if !strings.Contains(result.Request.Body, "1234567890123456789") || !strings.Contains(result.Request.Body, "business") {
		t.Fatal("request preview masked business fields or rounded integers")
	}
	after, _ := json.Marshal(a.store.Snapshot().Documents)
	if !bytes.Equal(before, after) || len(a.store.Snapshot().Servers) != 0 || len(a.store.Snapshot().Logs) != 1 {
		t.Fatal("tester changed configuration or required a published service")
	}
}

func TestAPITestFormAndNonSuccessResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(data))
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || form.Get("token") != "private-form-token" || strings.Join(form["items"], ",") != "1,2" {
			t.Error("form encoding / credential merge failed")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Validation", "missing-name")
		w.WriteHeader(422)
		_, _ = w.Write([]byte("name is required\n"))
	}))
	defer upstream.Close()
	a, ts, client := testApp(t)
	doc := importSpec(t, client, ts.URL, basicSpec(upstream.URL))
	request(t, client, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": upstream.URL, "kind": "none", "entries": []map[string]any{{"in": "body", "name": "token", "value": "private-form-token"}}}, nil)
	before, _ := json.Marshal(a.store.Snapshot().Documents)
	var result apiTestResult
	status := request(t, client, ts.URL, "/api/documents/"+doc.ID+"/test", "POST", map[string]any{"operationId": doc.Operations[0].ID, "arguments": map[string]any{}, "body": `{"items":[1,2]}`, "bodyFormat": "form"}, &result)
	if status != 200 || result.Success || result.Response == nil || result.Response.Status != 422 || result.Response.Body != "name is required\n" || result.Response.Headers.Get("X-Validation") != "missing-name" {
		t.Fatalf("non-success response was lost: %+v", result)
	}
	form, _ := url.ParseQuery(result.Request.Body)
	if form.Get("token") != "••••••" || strings.Join(form["items"], ",") != "1,2" {
		t.Fatal("form request preview leaked credentials or dropped business fields")
	}
	after, _ := json.Marshal(a.store.Snapshot().Documents)
	if !bytes.Equal(before, after) {
		t.Fatal("per-request encoding override modified saved settings")
	}
}

func TestAPITestAuthenticationAndValidation(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer upstream.Close()
	_, ts, client := testApp(t)
	doc := importSpec(t, client, ts.URL, basicSpec(upstream.URL))
	operation := doc.Operations[1]
	for _, candidate := range doc.Operations {
		if strings.Contains(candidate.Path, "{id}") {
			operation = candidate
		}
	}
	path := "/api/documents/" + doc.ID + "/test"
	valid := map[string]any{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}}}
	if request(t, &http.Client{}, ts.URL, path, "POST", valid, nil) != 401 || request(t, &http.Client{}, ts.URL, path, "GET", nil, nil) != 401 {
		t.Fatal("tester was available without login")
	}
	encoded, _ := json.Marshal(valid)
	req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("tester did not require CSRF header")
	}
	for _, input := range []map[string]any{
		{"operationId": operation.ID, "arguments": map[string]any{}},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}, "header": map[string]any{"Host": "other.example"}}},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}, "header": map[string]any{"Authorization": "secret"}}},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}, "header": map[string]any{"X-Extra": "bad\r\nvalue"}}},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}}, "body": "invalid json"},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}}, "body": "[]", "bodyFormat": "form"},
		{"operationId": operation.ID, "arguments": map[string]any{"path": map[string]any{"id": "abc"}}, "bodyFormat": "xml"},
	} {
		if request(t, client, ts.URL, path, "POST", input, nil) != 400 {
			t.Fatalf("invalid test request accepted: %v", input)
		}
	}
	if request(t, client, ts.URL, path, "POST", map[string]any{"operationId": "other-document-operation"}, nil) != 404 || calls.Load() != 0 {
		t.Fatal("invalid request reached upstream or document boundary was ignored")
	}
	var result apiTestResult
	if request(t, client, ts.URL, path, "POST", valid, &result) != 200 || !result.Success || result.Response == nil || result.Response.Status != 204 || result.Response.Body != "" || result.Response.Size != 0 {
		t.Fatal("empty response handling failed")
	}
}

func TestAPITestLocalTargetWithoutConfiguration(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()
	_, ts, client := testApp(t)
	doc := importSpec(t, client, ts.URL, basicSpec(upstream.URL))
	var result apiTestResult
	if request(t, client, ts.URL, "/api/documents/"+doc.ID+"/test", "POST", map[string]any{"operationId": doc.Operations[0].ID}, &result) != 200 || !result.Success || result.Error != "" || result.Response == nil || result.Response.Status != 200 || result.Response.Body != `{"status":"ok"}` || calls.Load() != 1 {
		t.Fatal("API tester required target configuration for a local API")
	}
}

func TestAPITestCancellationReachesUpstream(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) }))
	defer upstream.Close()
	_, ts, client := testApp(t)
	doc := importSpec(t, client, ts.URL, basicSpec(upstream.URL))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body, _ := json.Marshal(map[string]any{"operationId": doc.Operations[0].ID})
	req, _ := http.NewRequestWithContext(ctx, "POST", ts.URL+"/api/documents/"+doc.ID+"/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "API2MCP")
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(req)
		if response != nil {
			response.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("cancel did not reach upstream")
	}
	if <-done == nil {
		t.Fatal("canceled client request unexpectedly succeeded")
	}
}
