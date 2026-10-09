package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func credentialRows(entries []CredentialEntry) []map[string]any {
	rows := []map[string]any{}
	for _, entry := range entries {
		rows = append(rows, map[string]any{"id": entry.ID, "in": entry.Location, "name": entry.Name, "value": "", "valueType": entry.ValueType, "enabled": entry.Enabled})
	}
	return rows
}

func TestCombinedCredentialHTTPForwarding(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Token raw-header-secret" || r.Header.Get("X-Tenant") != "tenant-header-secret" || r.Header.Get("X-Disabled") != "" {
			t.Error("custom headers or disabled row incorrect")
		}
		if r.URL.Query().Get("api_key") != "query-private-secret" || r.URL.Query().Get("limit") != "2" {
			t.Error("query credentials not combined with business parameters")
		}
		if values, ok := r.URL.Query()["empty"]; !ok || len(values) != 1 || values[0] != "" {
			t.Error("explicit empty query value was lost")
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "cookie-private-secret" || len(r.Cookies()) != 3 {
			t.Error("cookie override duplicated or discarded other cookies")
		}
		if dynamic, err := r.Cookie("business-cookie"); err != nil || dynamic.Value != "keep-business-cookie" {
			t.Error("raw Cookie header discarded dynamic cookies")
		}
		var body map[string]any
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		_ = decoder.Decode(&body)
		if body["name"] != "business" || body["count"] != json.Number("1234567890123456789") || body["client_id"] != "body-client-secret" {
			t.Error("body value types or exact JSON number not preserved")
		}
		auth := obj(body["auth"])
		if auth["token"] != "body-token-secret" || auth["tenant"] != "root-object-secret" || auth["note"] != "keep-business-value" {
			t.Error("nested body credentials did not merge")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Combination","version":"1"},"servers":[{"url":%q}],"paths":{"/submit":{"post":{"operationId":"submit","parameters":[{"in":"query","name":"api_key","required":true,"schema":{"type":"string"}},{"in":"query","name":"limit","schema":{"type":"integer"}},{"in":"header","name":"X-Tenant","required":true,"schema":{"type":"string"}},{"in":"cookie","name":"session","required":true,"schema":{"type":"string"}},{"in":"cookie","name":"business-cookie","required":true,"schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["name","auth","count"],"additionalProperties":false,"properties":{"name":{"type":"string"},"count":{"type":"integer"},"auth":{"type":"object","required":["token"],"additionalProperties":false,"properties":{"token":{"type":"string"},"note":{"type":"string"}}}}}}}},"responses":{"200":{"description":"ok"}}}}}}`, upstream.URL)
	doc := importSpec(t, admin, ts.URL, spec)
	rows := []map[string]any{
		{"in": "header", "name": "Authorization", "value": "Token raw-header-secret"},
		{"in": "header", "name": "X-Tenant", "value": "tenant-header-secret"},
		{"in": "header", "name": "X-Disabled", "value": "disabled-private-secret", "enabled": false},
		{"in": "query", "name": "api_key", "value": "query-private-secret"},
		{"in": "query", "name": "empty", "value": ""},
		{"in": "header", "name": "Cookie", "value": "other=keep; session=wrong"},
		{"in": "cookie", "name": "session", "value": "cookie-private-secret"},
		{"in": "body", "name": "/auth/token", "value": "body-token-secret"},
		{"in": "body", "name": "count", "valueType": "json", "value": "1234567890123456789"},
		{"in": "body", "name": "client_id", "value": "body-client-secret"},
		{"in": "body", "name": "$", "valueType": "json", "value": `{"auth":{"tenant":"root-object-secret"}}`},
	}
	var saved Document
	if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": rows}, &saved); status != 200 {
		t.Fatalf("save combination: HTTP %d", status)
	}
	assertCredentialRedacted(t, saved.Credential)
	if len(saved.Credential.Entries) != len(rows) || !saved.Credential.Configured {
		t.Fatal("credential metadata missing")
	}
	// Redacted responses must not mutate the stored secrets.
	if a.store.Snapshot().Documents[0].Credential.Entries[0].Value != "Token raw-header-secret" {
		t.Fatal("redaction changed stored credential")
	}
	server := publish(t, admin, ts.URL, "combined", doc.Operations...)
	session, err := a.connect(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	public := copySchema(obj(tools.Tools[0].InputSchema))
	properties := obj(public["properties"])
	if _, exists := properties["header"]; exists {
		t.Fatal("fixed header still exposed as caller input")
	}
	if _, exists := obj(obj(properties["query"])["properties"])["api_key"]; exists {
		t.Fatal("fixed query still exposed as caller input")
	}
	args := map[string]any{"query": map[string]any{"limit": 2}, "cookie": map[string]any{"business-cookie": "keep-business-cookie"}, "body": map[string]any{"name": "business", "auth": map[string]any{"note": "keep-business-value"}}}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: args})
	if err != nil || result.IsError || calls.Load() != 1 {
		t.Fatalf("combined call failed: %v, %#v", err, result)
	}
	delete(obj(args["body"]), "name")
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: args})
	if err != nil || !result.IsError || calls.Load() != 1 {
		t.Fatal("missing business field reached upstream")
	}
	// Blank values preserve all existing fields; a disabled item is kept but no
	// longer supplies its parameter in the discovered input schema.
	rows = credentialRows(saved.Credential.Entries)
	rows[3]["enabled"] = false
	if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": rows}, &saved); status != 200 {
		t.Fatal("blank-value update failed")
	}
	stored := a.store.Snapshot().Documents[0].Credential
	if stored.Entries[3].Value != "query-private-secret" || stored.Entries[3].Enabled {
		t.Fatal("disabling lost secret or did not disable")
	}
	tools, err = session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := obj(obj(obj(obj(tools.Tools[0].InputSchema)["properties"])["query"])["properties"])["api_key"]; !exists {
		t.Fatal("disabled query did not return to caller schema")
	}
	var workspace Workspace
	request(t, admin, ts.URL, "/api/workspace", "GET", nil, &workspace)
	assertCredentialRedacted(t, workspace.Documents[0].Credential)
	updated := strings.Replace(spec, "Combination", "Combination updated", 1)
	if request(t, admin, ts.URL, "/api/documents/"+doc.ID, "PUT", map[string]string{"content": updated, "filename": "changed.json"}, &saved) != 200 {
		t.Fatal("document update failed")
	}
	assertCredentialRedacted(t, saved.Credential)
	if a.store.Snapshot().Documents[0].Credential.Entries[7].Value != "body-token-secret" {
		t.Fatal("document update lost body credential")
	}
}

func assertCredentialRedacted(t *testing.T, c Credential) {
	t.Helper()
	if c.Value != "" || c.Username != "" {
		t.Fatal("preset credential leaked")
	}
	for _, entry := range c.Entries {
		if entry.ID == "" || entry.Value != "" || !entry.Configured {
			t.Fatal("custom credential leaked or metadata missing")
		}
	}
}

func TestBasicAndFormCredentialForwarding(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != "private-user" || calls.Load() == 1 && password != " password with spaces " || calls.Load() == 2 && password != "" {
			t.Error("Basic Auth value not preserved")
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("app_id") != "form-secret" || r.PostForm.Get("business") != "keep" || strings.Join(r.PostForm["roles"], ",") != "read,write" {
			t.Error("form fields were not combined")
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Form","version":"1"},"servers":[{"url":%q}],"paths":{"/submit":{"post":{"operationId":"formSubmit","requestBody":{"required":true,"content":{"application/x-www-form-urlencoded":{"schema":{"type":"object","required":["business","app_id"],"properties":{"business":{"type":"string"},"app_id":{"type":"string"},"roles":{"type":"array","items":{"type":"string"}}}}}}},"responses":{"200":{"description":"ok"}}}}}}`, upstream.URL)
	doc := importSpec(t, admin, ts.URL, spec)
	var saved Document
	input := map[string]any{"baseUrl": doc.BaseURL, "kind": "basic", "username": "private-user", "value": " password with spaces ", "entries": []map[string]any{
		{"in": "body", "name": "app_id", "value": "form-secret"},
		{"in": "body", "name": "roles", "valueType": "json", "value": `["read","write"]`},
		{"in": "header", "name": "Authorization", "value": "preset-should-win"},
	}}
	if request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", input, &saved) != 200 {
		t.Fatal("could not save Basic and form configuration")
	}
	assertCredentialRedacted(t, saved.Credential)
	if !saved.Credential.ValueConfigured || !saved.Credential.UsernameConfigured {
		t.Fatal("saved Basic metadata missing")
	}
	server := publish(t, admin, ts.URL, "form-basic", doc.Operations...)
	session, err := a.connect(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: map[string]any{"body": map[string]any{"business": "keep"}}})
		if err != nil || result.IsError {
			t.Fatalf("form call failed: %v %#v", err, result)
		}
	}
	call()
	// Omitted entries preserve custom fields for legacy clients; explicit blank
	// replacement permits an empty Basic password while keeping the username.
	if request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "basic", "replaceValue": true}, &saved) != 200 {
		t.Fatal("explicit empty Basic password failed")
	}
	call()
	if len(a.store.Snapshot().Documents[0].Credential.Entries) != 3 {
		t.Fatal("legacy update erased custom fields")
	}
}

func TestCredentialValidationAndSecretErrors(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.0.3","info":{"title":"Validation","version":"1"},"servers":[{"url":%q}],"paths":{"/submit":{"post":{"operationId":"submit","requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object","required":["count"],"properties":{"count":{"type":"integer"}}}}}},"responses":{"200":{"description":"ok"}}}}}}`, upstream.URL)
	doc := importSpec(t, admin, ts.URL, spec)
	for name, entries := range map[string][]map[string]any{
		"transport header": {{"in": "header", "name": "Host", "value": "other-host"}},
		"header injection": {{"in": "header", "name": "X-API-Key", "value": "key\r\nX-Evil: value"}},
		"duplicate header": {{"in": "header", "name": "X-Key", "value": "1"}, {"in": "header", "name": "x-key", "value": "2"}},
		"JSON":             {{"in": "body", "name": "key", "valueType": "json", "value": "{"}},
		"root scalar":      {{"in": "body", "name": "$", "valueType": "json", "value": "42"}},
		"pointer":          {{"in": "body", "name": "/a~2b", "value": "x"}},
		"unknown row ID":   {{"id": "not-a-saved-row", "in": "query", "name": "key", "value": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if status := request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": entries}, nil); status != 400 {
				t.Fatalf("expected rejection, HTTP %d", status)
			}
		})
	}
	var saved Document
	request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": []map[string]any{{"in": "body", "name": "count", "value": "secret-must-not-appear"}}}, &saved)
	server := publish(t, admin, ts.URL, "bad-body", doc.Operations...)
	session, err := a.connect(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: map[string]any{}})
	encoded, _ := json.Marshal(result)
	if err != nil || !result.IsError || calls.Load() != 0 || bytes.Contains(encoded, []byte("secret-must-not-appear")) {
		t.Fatal("invalid merged body reached upstream or leaked its credential")
	}
	rows := credentialRows(saved.Credential.Entries)
	rows[0]["value"], rows[0]["valueType"] = "7", "json"
	request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": rows}, &saved)
	rows = credentialRows(saved.Credential.Entries)
	rows[0]["value"], rows[0]["valueType"], rows[0]["replaceValue"] = "", "string", true
	request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": rows}, nil)
	if a.store.Snapshot().Documents[0].Credential.Entries[0].Value != "" {
		t.Fatal("explicit empty value retained the old secret")
	}
	request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": []any{}}, nil)
	if len(a.store.Snapshot().Documents[0].Credential.Entries) != 0 {
		t.Fatal("explicit empty list did not remove credentials")
	}
}

func TestFlexibleCredentialEncryptedPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.db")
	key := bytes.Repeat([]byte{9}, 32)
	store, _, err := OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	secret := "custom-body-secret-not-plaintext"
	if err = store.Update(func(w *Workspace) error {
		w.Documents = []Document{{ID: "doc", Credential: Credential{Kind: "basic", Username: "private-user", Value: "private-password", Entries: []CredentialEntry{{ID: "row", Location: "body", Name: "auth.token", Value: secret, ValueType: "string", Enabled: true}}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte("private-password")) {
		t.Fatal("credentials written as plaintext")
	}
	store, _, err = OpenStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	credential := store.Snapshot().Documents[0].Credential
	if credential.Entries[0].Value != secret || credential.Username != "private-user" || credential.Value != "private-password" {
		t.Fatal("credential values lost on restart")
	}
}

func TestCredentialBodyFormatOverride(t *testing.T) {
	for _, format := range []string{"json", "form"} {
		t.Run(format, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if format == "form" {
					_ = r.ParseForm()
					if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.PostForm.Get("token") != "fixed" || r.PostForm.Get("name") != "business" {
						t.Error("form override did not encode the body")
					}
				} else {
					var data map[string]any
					_ = json.NewDecoder(r.Body).Decode(&data)
					if r.Header.Get("Content-Type") != "application/json" || data["token"] != "fixed" || data["name"] != "business" {
						t.Error("JSON override did not encode the body")
					}
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			a, _, _ := testApp(t)
			original := "application/json"
			if format == "json" {
				original = "application/x-www-form-urlencoded"
			}
			doc := Document{BaseURL: upstream.URL, Credential: Credential{BodyFormat: format, Entries: []CredentialEntry{{Location: "body", Name: "token", Value: "fixed", Enabled: true}}}}
			result, err := a.forward(context.Background(), doc, Operation{Method: "POST", Path: "/", ContentType: original}, map[string]any{"body": map[string]any{"name": "business"}})
			if err != nil || result.Status != 200 {
				t.Fatalf("format override failed: %v", err)
			}
		})
	}
}

func TestBodyCredentialUnionKeepsBusinessValidation(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	a, ts, admin := testApp(t)
	spec := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"Union","version":"1"},"servers":[{"url":%q}],"paths":{"/submit":{"post":{"operationId":"submit","requestBody":{"required":true,"content":{"application/json":{"schema":{"oneOf":[{"type":"object","required":["kind","name"],"additionalProperties":false,"properties":{"kind":{"const":"user"},"name":{"type":"string"}}},{"type":"object","required":["kind","order_id"],"additionalProperties":false,"properties":{"kind":{"const":"order"},"order_id":{"type":"string"}}}]}}}},"responses":{"200":{"description":"ok"}}}}}}`, upstream.URL)
	doc := importSpec(t, admin, ts.URL, spec)
	if request(t, admin, ts.URL, "/api/documents/"+doc.ID+"/credentials", "PUT", map[string]any{"baseUrl": doc.BaseURL, "kind": "none", "entries": []map[string]string{{"in": "body", "name": "kind", "value": "user"}}}, nil) != 200 {
		t.Fatal("union credential save failed")
	}
	server := publish(t, admin, ts.URL, "union", doc.Operations...)
	session, err := a.connect(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, valid := range []bool{true, false} {
		body := map[string]any{"name": "demo"}
		if !valid {
			body = map[string]any{"order_id": "wrong-branch"}
		}
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: doc.Operations[0].ToolName, Arguments: map[string]any{"body": body}})
		if err != nil || result.IsError == valid {
			t.Fatalf("union business validation incorrect: %v %#v", err, result)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("invalid union body reached upstream")
	}
}
