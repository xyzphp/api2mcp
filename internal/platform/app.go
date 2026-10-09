package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"strings"
)

type App struct {
	cfg            Config
	store          *Store
	auth           *authenticator
	files          fs.FS
	upstream       *http.Client
	internalOrigin string
}

func NewApp(cfg Config, files fs.FS) (*App, error) {
	store, fresh, err := OpenStore(cfg.DataPath, cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("HTTP_ADDR 格式错误")
	}
	a := &App{cfg: cfg, store: store, auth: newAuth(cfg.AdminToken, cfg.CookieSecure), files: files, upstream: upstreamClient(cfg), internalOrigin: "http://127.0.0.1:" + port}
	if fresh && cfg.SeedMock {
		if err := a.seedMock(); err != nil {
			store.Close()
			return nil, fmt.Errorf("初始化 Mock 文档: %w", err)
		}
	}
	return a, nil
}
func (a *App) Close() error { a.upstream.CloseIdleConnections(); return a.store.Close() }

type apiHandler func(http.ResponseWriter, *http.Request) error
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string              { return e.Message }
func httpErr(status int, message string) error { return &apiError{Status: status, Message: message} }
func bad(message string) error                 { return httpErr(400, message) }
func route(fn apiHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			var public *apiError
			if errors.As(err, &public) {
				_ = sendJSON(w, public.Status, map[string]string{"error": public.Message})
				return
			}
			slog.Error("请求失败", "path", r.URL.Path, "error", err)
			_ = sendJSON(w, 500, map[string]string{"error": "服务内部错误，请查看服务端日志"})
		}
	})
}

func readJSON(w http.ResponseWriter, r *http.Request, value any) error {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		return httpErr(415, "请使用 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3_000_000)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return bad("请求 JSON 无效或内容过大")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return bad("请求只能包含一个 JSON 对象")
	}
	return nil
}
func sendJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(value)
}

func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _ = sendJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.Handle("POST /api/login", route(func(w http.ResponseWriter, r *http.Request) error {
		if !a.checkOrigin(r) {
			return httpErr(403, "请求来源无效")
		}
		return a.login(w, r)
	}))
	admin := func(pattern string, fn apiHandler) { mux.Handle(pattern, a.admin(route(fn))) }
	admin("POST /api/logout", a.logout)
	admin("GET /api/workspace", a.workspace)
	admin("GET /api/example", a.example)
	admin("POST /api/documents", a.importDocument)
	admin("PUT /api/documents/{id}", a.importDocument)
	admin("PUT /api/documents/{id}/credentials", a.saveCredential)
	admin("GET /api/documents/{id}/test", a.describeDocumentTest)
	admin("POST /api/documents/{id}/test", a.testDocument)
	admin("DELETE /api/documents/{id}", a.deleteDocument)
	admin("POST /api/servers", a.saveServer)
	admin("PUT /api/servers/{id}", a.saveServer)
	admin("GET /api/servers/{id}/client-headers", a.serverClientHeaders)
	admin("POST /api/servers/{id}/state", a.serverState)
	admin("POST /api/servers/{id}/token", a.rotateToken)
	admin("DELETE /api/servers/{id}", a.deleteServer)
	admin("POST /api/servers/{id}/test", a.testServer)
	admin("POST /api/servers/{id}/call", a.callServer)
	admin("GET /api/logs", func(w http.ResponseWriter, r *http.Request) error { return sendJSON(w, 200, a.store.Snapshot().Logs) })
	admin("DELETE /api/logs", func(w http.ResponseWriter, r *http.Request) error {
		if err := a.store.Update(func(s *Workspace) error { s.Logs = []CallLog{}; return nil }); err != nil {
			return err
		}
		return sendJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/mcp/{slug}", a.serveMCP)
	mux.HandleFunc("GET /assets/{rest...}", func(w http.ResponseWriter, r *http.Request) { http.FileServer(http.FS(a.files)).ServeHTTP(w, r) })
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if a.auth.valid(r) {
			http.Redirect(w, r, a.appPath("/"), http.StatusSeeOther)
			return
		}
		a.serveFile(w, r, "login.html")
	})
	mux.HandleFunc("GET /{$}", a.index)
	mux.HandleFunc("GET /index.html", a.index)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		defer func() {
			if v := recover(); v != nil {
				slog.Error("请求异常", "path", r.URL.Path)
				http.Error(w, "服务内部错误", 500)
			}
		}()
		mux.ServeHTTP(w, r)
	})
	if a.cfg.BasePath == "" {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != a.cfg.BasePath && !strings.HasPrefix(r.URL.Path, a.cfg.BasePath+"/") {
			handler.ServeHTTP(w, r)
			return
		}
		request := r.Clone(r.Context())
		requestURL := *r.URL
		requestURL.Path = strings.TrimPrefix(requestURL.Path, a.cfg.BasePath)
		if requestURL.Path == "" {
			requestURL.Path = "/"
		}
		requestURL.RawPath = ""
		request.URL = &requestURL
		handler.ServeHTTP(w, request)
	})
}

func (a *App) appPath(route string) string {
	if !strings.HasPrefix(route, "/") {
		route = "/" + route
	}
	return a.cfg.BasePath + route
}

func (a *App) index(w http.ResponseWriter, r *http.Request) {
	if !a.auth.valid(r) {
		http.Redirect(w, r, a.appPath("/login"), http.StatusSeeOther)
		return
	}
	a.serveFile(w, r, "index.html")
}
func (a *App) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	b, err := fs.ReadFile(a.files, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, strings.ReplaceAll(string(b), "{{APP_BASE_HREF}}", a.appPath("/")))
}

func (a *App) workspace(w http.ResponseWriter, r *http.Request) error {
	workspace := a.store.Snapshot()
	workspace.Settings.EndpointOrigin = requestOrigin(r) + a.cfg.BasePath
	for i := range workspace.Documents {
		workspace.Documents[i].Credential = publicCredential(workspace.Documents[i].Credential)
	}
	return sendJSON(w, 200, workspace)
}

func (a *App) example(w http.ResponseWriter, r *http.Request) error {
	b, err := fs.ReadFile(a.files, "examples/mock-api.yaml")
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]string{"content": strings.ReplaceAll(string(b), "http://mock-api:9090/v1", a.cfg.MockAPIURL), "filename": path.Base("mock-api.yaml")})
}

func (a *App) seedMock() error {
	b, err := fs.ReadFile(a.files, "examples/mock-api.yaml")
	if err != nil {
		return err
	}
	d, err := ParseDocument(b, "mock-api.yaml", "内置 Mock 示例")
	if err != nil {
		return err
	}
	d.BaseURL = a.cfg.MockAPIURL
	return a.store.Update(func(w *Workspace) error {
		w.Documents = append(w.Documents, d)
		for i, tag := range []string{"用户", "订单"} {
			ids := []string{}
			for _, op := range d.Operations {
				if op.Tag == tag {
					ids = append(ids, op.ID)
				}
			}
			w.Servers = append(w.Servers, Server{ID: newID(), Draft: Draft{Name: tag + "服务 MCP", Slug: []string{"mock-users", "mock-orders"}[i], Description: "连接独立 Mock HTTP API 的示例服务", Color: []string{"green", "purple"}[i], OperationIDs: ids}, Status: "running", Token: newToken(), Version: 1, UpdatedAt: now()})
		}
		return nil
	})
}
