package platform

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr            string
	AdminToken      string
	EncryptionKey   []byte
	DataPath        string
	PublicOrigin    string
	BasePath        string
	CookieSecure    bool
	SeedMock        bool
	MockAPIURL      string
	UpstreamTimeout time.Duration
}

func ConfigFromEnv() (Config, error) {
	key, err := base64.StdEncoding.DecodeString(os.Getenv("ENCRYPTION_KEY"))
	if err != nil || len(key) != 32 {
		return Config{}, fmt.Errorf("ENCRYPTION_KEY 必须是 32 字节随机密钥的 Base64 编码，请先执行 make init")
	}
	c := Config{Addr: env("HTTP_ADDR", ":8080"), AdminToken: os.Getenv("ADMIN_TOKEN"), EncryptionKey: key,
		DataPath: env("DATA_PATH", "data/api2mcp.db"), PublicOrigin: strings.TrimRight(env("PUBLIC_ORIGIN", "http://localhost:8080"), "/"),
		BasePath:     strings.TrimSpace(os.Getenv("BASE_PATH")),
		CookieSecure: os.Getenv("COOKIE_SECURE") == "true", SeedMock: os.Getenv("SEED_MOCK") == "true",
		MockAPIURL: env("MOCK_API_URL", "http://127.0.0.1:9090/v1"), UpstreamTimeout: 20 * time.Second}
	if len(c.AdminToken) < 24 {
		return c, fmt.Errorf("ADMIN_TOKEN 至少需要 24 个字符，请先执行 make init")
	}
	if _, err := validURL(c.PublicOrigin, true); err != nil {
		return c, fmt.Errorf("PUBLIC_ORIGIN 无效: %w", err)
	}
	u, _ := url.Parse(c.PublicOrigin)
	if u.Path != "" {
		return c, fmt.Errorf("PUBLIC_ORIGIN 只包含协议、域名及端口")
	}
	if u.Scheme == "https" {
		c.CookieSecure = true
	}
	if c.BasePath == "/" {
		c.BasePath = ""
	}
	if c.BasePath != "" && (c.BasePath[0] != '/' || strings.HasSuffix(c.BasePath, "/") || path.Clean(c.BasePath) != c.BasePath || strings.Trim(c.BasePath, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~/") != "") {
		return c, fmt.Errorf("BASE_PATH 必须是规范的绝对路径前缀，例如 /http_mcp，且不能以 / 结尾")
	}
	if value := os.Getenv("UPSTREAM_TIMEOUT_SECONDS"); value != "" {
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds < 1 || seconds > 120 {
			return c, fmt.Errorf("UPSTREAM_TIMEOUT_SECONDS 取值为 1–120")
		}
		c.UpstreamTimeout = time.Duration(seconds) * time.Second
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func validURL(raw string, base bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("请填写不含访问凭证的 HTTP / HTTPS 地址")
	}
	if base && u.RawQuery != "" {
		return nil, fmt.Errorf("API 基础地址不能包含查询参数")
	}
	return u, nil
}
