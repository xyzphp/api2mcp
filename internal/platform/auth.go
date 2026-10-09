package platform

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const cookieName = "api2mcp_session"

type attempt struct {
	Count int
	Until time.Time
}
type authenticator struct {
	mu        sync.Mutex
	tokenHash [32]byte
	sessions  map[string]time.Time
	attempts  map[string]attempt
	secure    bool
}

func newAuth(token string, secure bool) *authenticator {
	return &authenticator{tokenHash: sha256.Sum256([]byte(token)), sessions: map[string]time.Time{}, attempts: map[string]attempt{}, secure: secure}
}
func equalToken(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (a *authenticator) valid(r *http.Request) bool {
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[cookie.Value]
	if ok && time.Now().Before(expires) {
		return true
	}
	delete(a.sessions, cookie.Value)
	return false
}

func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Token string `json:"token"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	a.auth.mu.Lock()
	defer a.auth.mu.Unlock()
	now := time.Now()
	for key, value := range a.auth.attempts {
		if now.After(value.Until) {
			delete(a.auth.attempts, key)
		}
	}
	for key, expires := range a.auth.sessions {
		if now.After(expires) {
			delete(a.auth.sessions, key)
		}
	}
	try := a.auth.attempts[ip]
	if try.Count >= 10 && now.Before(try.Until) {
		return httpErr(429, "尝试次数过多，请 5 分钟后重试")
	}
	provided := sha256.Sum256([]byte(input.Token))
	if subtle.ConstantTimeCompare(provided[:], a.auth.tokenHash[:]) != 1 {
		if try.Count == 0 || now.After(try.Until) {
			try = attempt{Until: now.Add(5 * time.Minute)}
		}
		try.Count++
		if len(a.auth.attempts) < 10000 {
			a.auth.attempts[ip] = try
		}
		return httpErr(401, "登录 Token 不正确")
	}
	delete(a.auth.attempts, ip)
	// Bound memory even when a valid administrator repeatedly signs in.
	if len(a.auth.sessions) >= 100 {
		for key := range a.auth.sessions {
			delete(a.auth.sessions, key)
			break
		}
	}
	session, expires := newID()+newID(), now.Add(8*time.Hour)
	a.auth.sessions[session] = expires
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: session, Path: "/", HttpOnly: true, Secure: a.cookieSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: 8 * 60 * 60, Expires: expires})
	return sendJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(cookieName); err == nil {
		a.auth.mu.Lock()
		delete(a.auth.sessions, cookie.Value)
		a.auth.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.cookieSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	return sendJSON(w, 200, map[string]bool{"ok": true})
}

func forwardedProto(r *http.Request) string {
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	if strings.EqualFold(proto, "http") || strings.EqualFold(proto, "https") {
		return strings.ToLower(proto)
	}
	return ""
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if proto := forwardedProto(r); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}

func (a *App) cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if proto := forwardedProto(r); proto != "" {
		return proto == "https"
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != a.cfg.PublicOrigin {
		return false
	}
	return a.auth.secure
}

func (a *App) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	return origin == a.cfg.PublicOrigin || origin == requestOrigin(r)
}

func (a *App) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.auth.valid(r) {
			_ = sendJSON(w, 401, map[string]string{"error": "请先登录管理后台"})
			return
		}
		if !a.checkOrigin(r) || (r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-Requested-With") != "API2MCP") {
			_ = sendJSON(w, 403, map[string]string{"error": "请求来源无效"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return parts[1]
}
