package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	"api2mcp/internal/envfile"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type authTransport struct{ token string }

func (t authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(copy)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Smoke 验证失败:", err)
		os.Exit(1)
	}
}
func run() error {
	envfile.Load(".env")
	origin := strings.TrimRight(os.Getenv("SMOKE_ORIGIN"), "/")
	if origin == "" {
		origin = strings.TrimRight(os.Getenv("PUBLIC_ORIGIN"), "/")
	}
	if origin == "" {
		origin = "http://localhost:8080"
	}
	base := origin + strings.TrimRight(os.Getenv("BASE_PATH"), "/")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	body, _ := json.Marshal(map[string]string{"token": os.Getenv("ADMIN_TOKEN")})
	request, err := http.NewRequest("POST", base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("登录 HTTP %d", response.StatusCode)
	}
	response, err = client.Get(base + "/api/workspace")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("读取工作空间 HTTP %d", response.StatusCode)
	}
	var workspace struct {
		Servers   []struct{ Slug, Token, Status string }
		Documents []struct {
			Credential struct {
				Value, Username string
				Entries         []struct{ Value string }
			}
		}
	}
	if err = json.NewDecoder(response.Body).Decode(&workspace); err != nil {
		return err
	}
	for _, doc := range workspace.Documents {
		if doc.Credential.Value != "" || doc.Credential.Username != "" {
			return fmt.Errorf("凭证未脱敏")
		}
		for _, entry := range doc.Credential.Entries {
			if entry.Value != "" {
				return fmt.Errorf("自定义请求参数未脱敏")
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	verified := 0
	for _, server := range workspace.Servers {
		if server.Status != "running" {
			continue
		}
		mcpClient := mcp.NewClient(&mcp.Implementation{Name: "api2mcp-smoke", Version: "1"}, nil)
		session, err := mcpClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp/" + server.Slug, HTTPClient: &http.Client{Transport: authTransport{server.Token}}}, nil)
		if err != nil {
			return err
		}
		tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			session.Close()
			return err
		}
		for _, tool := range tools.Tools {
			schemaBytes, _ := json.Marshal(tool.InputSchema)
			var schema struct{ Required []string }
			_ = json.Unmarshal(schemaBytes, &schema)
			if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || len(schema.Required) > 0 {
				continue
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: map[string]any{}})
			if err != nil {
				session.Close()
				return err
			}
			if result.IsError {
				session.Close()
				return fmt.Errorf("%s 工具返回错误", server.Slug)
			}
			fmt.Printf("✓ %s: %d 个工具，真实只读 API 调用成功\n", server.Slug, len(tools.Tools))
			verified++
			break
		}
		session.Close()
	}
	// Authenticated session cookies must never authorize an MCP client by themselves.
	if len(workspace.Servers) > 0 {
		r, _ := http.NewRequest("POST", base+"/mcp/"+workspace.Servers[0].Slug, strings.NewReader(`{}`))
		res, err := client.Do(r)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 401 {
			return fmt.Errorf("MCP 调用缺少独立 Token 仍可访问")
		}
	}
	if verified == 0 {
		return fmt.Errorf("没有可以验证的无必填参数只读工具，请先启动内置 Mock 示例")
	}
	fmt.Println("✓ 登录、凭证脱敏、独立 MCP Token 验证通过（未打印任何密钥）")
	return nil
}
