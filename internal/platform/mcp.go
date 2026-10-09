package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (a *App) serveMCP(w http.ResponseWriter, r *http.Request) {
	workspace := a.store.Snapshot()
	var server *Server
	for i := range workspace.Servers {
		if workspace.Servers[i].Slug == r.PathValue("slug") {
			server = &workspace.Servers[i]
			break
		}
	}
	if server == nil || server.Status == "draft" {
		http.NotFound(w, r)
		return
	}
	requestToken := r.URL.Query().Get("token")
	if requestToken == "" {
		requestToken = bearer(r)
	}
	if requestToken == "" || !equalToken(requestToken, server.Token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api2mcp"`)
		_ = sendJSON(w, 401, map[string]string{"error": "MCP 调用 Token 无效"})
		return
	}
	if !a.checkOrigin(r) {
		_ = sendJSON(w, 403, map[string]string{"error": "请求来源无效"})
		return
	}
	if server.Status != "running" {
		_ = sendJSON(w, 503, map[string]string{"error": "服务已停用"})
		return
	}
	baseURL, overrideBaseURL, err := proxyBaseURL(r)
	if err != nil {
		_ = sendJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	proxyHeaders, err := mcpProxyHeaders(r, server.Token)
	if err != nil {
		_ = sendJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	documents := map[string]bool{}
	for _, doc := range workspace.Documents {
		for _, op := range doc.Operations {
			if slices.Contains(server.OperationIDs, op.ID) {
				documents[doc.ID] = true
			}
		}
	}
	clientCredentials, err := parseClientCredentials(r, documents)
	if err != nil {
		_ = sendJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	impl := mcp.NewServer(&mcp.Implementation{Name: server.Slug, Version: fmt.Sprintf("1.%d", server.Version-1)}, &mcp.ServerOptions{
		Instructions: server.Description,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: false}},
	})
	for _, doc := range workspace.Documents {
		if !documents[doc.ID] {
			continue
		}
		credential, err := clientCredentials.credentialFor(doc)
		if err != nil {
			_ = sendJSON(w, 400, map[string]string{"error": "客户端凭证与文档配置无法合并"})
			return
		}
		if len(proxyHeaders) > 0 {
			credential, err = applyClientCredentialLayer(credential, clientCredentialLayer{Headers: proxyHeaders})
			if err != nil {
				_ = sendJSON(w, 400, map[string]string{"error": "客户端请求 Header 无法应用于上游 API"})
				return
			}
		}
		doc.Credential = credential
		if overrideBaseURL {
			doc.BaseURL = baseURL
		}
		for _, op := range doc.Operations {
			if !slices.Contains(server.OperationIDs, op.ID) {
				continue
			}
			input := credentialInputSchema(op, doc.Credential)
			schema, err := compileSchema(input)
			if err != nil {
				http.Error(w, "工具参数 Schema 无效", 500)
				return
			}
			readOnly := op.Method == "GET" || op.Method == "HEAD" || op.Method == "OPTIONS"
			impl.AddTool(&mcp.Tool{Name: op.ToolName, Description: op.Name + "\n" + op.Description + "\n" + op.Method + " " + op.Path, InputSchema: input,
				Annotations: &mcp.ToolAnnotations{Title: op.Name, ReadOnlyHint: readOnly, IdempotentHint: readOnly || op.Method == "PUT" || op.Method == "DELETE"}},
				func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					start := time.Now()
					var args map[string]any
					raw := request.Params.Arguments
					if len(raw) == 0 {
						raw = json.RawMessage(`{}`)
					}
					if err := json.Unmarshal(raw, &args); err != nil || args == nil {
						return a.toolError(*server, op, start, "工具参数必须为 JSON 对象"), nil
					}
					if err := schema.Validate(args); err != nil {
						return a.toolError(*server, op, start, "工具参数不符合 Schema: "+err.Error()), nil
					}
					ctx, cancel := context.WithTimeout(ctx, a.cfg.UpstreamTimeout)
					defer cancel()
					result, err := a.forward(ctx, doc, op, args)
					if err != nil {
						return a.toolError(*server, op, start, err.Error()), nil
					}
					success := result.Status >= 200 && result.Status < 300
					a.record(*server, op.Method+" "+op.Path, success, time.Since(start), result.Status, "")
					data, _ := json.Marshal(result)
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, StructuredContent: result, IsError: !success}, nil
				})
		}
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return impl }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 2_000_000, PropagateRequestCancellation: true})
	handler.ServeHTTP(w, r)
}

func (a *App) toolError(server Server, op Operation, start time.Time, message string) *mcp.CallToolResult {
	// Schema errors may contain supplied values: keep those out of persisted logs.
	a.record(server, op.Method+" "+op.Path, false, time.Since(start), 0, "参数校验或上游请求失败")
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

func (a *App) record(server Server, action string, success bool, duration time.Duration, status int, message string) {
	if err := a.store.Update(func(w *Workspace) error {
		log := CallLog{ID: newID(), ServerID: server.ID, ServerName: server.Name, Action: action, Success: success, Duration: duration.Milliseconds(), CreatedAt: now(), Status: status, Error: message}
		w.Logs = append([]CallLog{log}, w.Logs...)
		if len(w.Logs) > 1000 {
			w.Logs = w.Logs[:1000]
		}
		return nil
	}); err != nil {
		slog.Error("保存调用日志失败", "error", err)
	}
}

type tokenTransport struct{ token string }

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	req := r.Clone(r.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(req)
}

func (a *App) serverByID(id string) (Server, error) {
	for _, server := range a.store.Snapshot().Servers {
		if server.ID == id {
			return server, nil
		}
	}
	return Server{}, httpErr(404, "服务不存在")
}
func (a *App) connect(ctx context.Context, server Server) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "api2mcp-admin", Version: "0.2.0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: a.internalOrigin + "/mcp/" + server.Slug, HTTPClient: &http.Client{Transport: tokenTransport{token: server.Token}, Timeout: a.cfg.UpstreamTimeout + 5*time.Second}}, nil)
}

func (a *App) testServer(w http.ResponseWriter, r *http.Request) error {
	server, err := a.serverByID(r.PathValue("id"))
	if err != nil {
		return err
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.UpstreamTimeout+5*time.Second)
	defer cancel()
	result := map[string]any{"success": false, "tools": []*mcp.Tool{}}
	session, err := a.connect(ctx, server)
	if err == nil {
		defer session.Close()
		var tools []*mcp.Tool
		listed, listErr := session.ListTools(ctx, &mcp.ListToolsParams{})
		err = listErr
		if err == nil {
			tools = listed.Tools
			for listed.NextCursor != "" {
				listed, err = session.ListTools(ctx, &mcp.ListToolsParams{Cursor: listed.NextCursor})
				if err != nil {
					break
				}
				tools = append(tools, listed.Tools...)
			}
			result["tools"] = tools
		}
	}
	message := ""
	if err != nil {
		message = "连接失败，请确认服务已发布并启用，且本机 HTTP 服务可访问"
	} else {
		result["success"] = true
	}
	result["error"], result["duration"] = message, time.Since(start).Milliseconds()
	a.record(server, "连接测试 / tools.list", err == nil, time.Since(start), 0, message)
	return sendJSON(w, 200, result)
}

func (a *App) callServer(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	server, err := a.serverByID(r.PathValue("id"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.UpstreamTimeout+5*time.Second)
	defer cancel()
	session, err := a.connect(ctx, server)
	if err != nil {
		return bad("无法连接 MCP 服务，请先启用并测试连接")
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: input.Name, Arguments: input.Arguments})
	if err != nil {
		return bad("工具调用失败，请检查工具名称及参数")
	}
	return sendJSON(w, 200, result)
}
