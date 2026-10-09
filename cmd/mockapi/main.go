package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"api2mcp/examples"
	"api2mcp/internal/envfile"
)

type user struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}
type order struct {
	ID     string  `json:"id"`
	UserID string  `json:"userId"`
	Total  float64 `json:"total"`
	Status string  `json:"status"`
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != 204 {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func uid(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}
func read(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1_000_000)).Decode(v); err != nil {
		respond(w, 400, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

func main() {
	envfile.Load(".env")
	var mu sync.Mutex
	users := map[string]user{"usr_001": {ID: "usr_001", Name: "示例用户", Email: "demo@example.com"}}
	orders := map[string]order{"ord_001": {ID: "ord_001", UserID: "usr_001", Total: 199, Status: "paid"}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		b := examples.MockSpec
		base := os.Getenv("MOCK_API_URL")
		if base == "" {
			base = "http://127.0.0.1:9090/v1"
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(strings.ReplaceAll(string(b), "http://mock-api:9090/v1", base)))
	})
	mux.HandleFunc("GET /v1/users", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		values := []user{}
		for _, u := range users {
			if q := r.URL.Query().Get("q"); q == "" || strings.Contains(strings.ToLower(u.Name), strings.ToLower(q)) {
				values = append(values, u)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 100 {
			limit = 20
		}
		total := len(values)
		start := min((page-1)*limit, total)
		end := min(start+limit, total)
		respond(w, 200, map[string]any{"data": values[start:end], "total": total, "page": page})
	})
	mux.HandleFunc("POST /v1/users", func(w http.ResponseWriter, r *http.Request) {
		var u user
		if !read(w, r, &u) {
			return
		}
		if strings.TrimSpace(u.Name) == "" {
			respond(w, 422, map[string]string{"error": "name is required"})
			return
		}
		u.ID = uid("usr_")
		mu.Lock()
		users[u.ID] = u
		mu.Unlock()
		respond(w, 201, u)
	})
	mux.HandleFunc("/v1/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.PathValue("id")
		u, ok := users[id]
		if !ok {
			respond(w, 404, map[string]string{"error": "user not found"})
			return
		}
		switch r.Method {
		case "GET":
			respond(w, 200, u)
		case "PATCH":
			var input user
			if !read(w, r, &input) {
				return
			}
			if strings.TrimSpace(input.Name) == "" {
				respond(w, 422, map[string]string{"error": "name is required"})
				return
			}
			u.Name, u.Email = input.Name, input.Email
			users[id] = u
			respond(w, 200, u)
		case "DELETE":
			delete(users, id)
			respond(w, 204, nil)
		default:
			respond(w, 405, map[string]string{"error": "method not allowed"})
		}
	})
	mux.HandleFunc("GET /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		values := []order{}
		for _, o := range orders {
			if id := r.URL.Query().Get("userId"); id == "" || o.UserID == id {
				values = append(values, o)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
		respond(w, 200, map[string]any{"data": values, "total": len(values)})
	})
	mux.HandleFunc("POST /v1/orders", func(w http.ResponseWriter, r *http.Request) {
		var input order
		if !read(w, r, &input) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if _, ok := users[input.UserID]; !ok || input.Total < 0 {
			respond(w, 422, map[string]string{"error": "invalid userId or total"})
			return
		}
		input.ID, input.Status = uid("ord_"), "pending"
		orders[input.ID] = input
		respond(w, 201, input)
	})
	mux.HandleFunc("GET /v1/orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		value, ok := orders[r.PathValue("id")]
		if !ok {
			respond(w, 404, map[string]string{"error": "order not found"})
			return
		}
		respond(w, 200, value)
	})
	addr := os.Getenv("MOCK_ADDR")
	if addr == "" {
		addr = ":9090"
	}
	slog.Info("Mock API 已启动", "listen", addr)
	server := http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Mock 服务失败", "error", err)
		os.Exit(1)
	}
}
