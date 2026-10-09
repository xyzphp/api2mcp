package platform

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
)

const clientCredentialsHeader = "X-API2MCP-Credentials"

type clientAuth struct {
	Type     string `json:"type"`
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
}

type clientCredentialLayer struct {
	Auth       *clientAuth       `json:"auth,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Query      map[string]string `json:"query,omitempty"`
	Cookies    map[string]string `json:"cookies,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	BodyFormat string            `json:"bodyFormat,omitempty"`
}

type clientCredentialConfig struct {
	clientCredentialLayer
	Documents map[string]clientCredentialLayer `json:"documents,omitempty"`
}

func removeCredentialCookies(c *Credential, value string) {
	req := &http.Request{Header: http.Header{"Cookie": {value}}}
	names := map[string]bool{}
	for _, cookie := range req.Cookies() {
		names[cookie.Name] = true
	}
	c.Entries = slices.DeleteFunc(c.Entries, func(entry CredentialEntry) bool {
		return entry.Location == "cookie" && names[entry.Name]
	})
}

func parseClientCredentials(r *http.Request, documents map[string]bool) (clientCredentialConfig, error) {
	var config clientCredentialConfig
	values := r.Header.Values(clientCredentialsHeader)
	if len(values) == 0 {
		return config, nil
	}
	if len(values) != 1 || len(values[0]) > 16384 || !strings.HasPrefix(strings.TrimSpace(values[0]), "{") {
		return config, bad("客户端凭证 Header 必须是单个 JSON 对象，最多 16 KB")
	}
	decoder := json.NewDecoder(strings.NewReader(values[0]))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF {
		return config, bad("客户端凭证 JSON 无效，请检查 auth / headers / query / cookies / body / documents 字段")
	}
	if _, err := applyClientCredentialLayer(Credential{Kind: "none"}, config.clientCredentialLayer); err != nil {
		return config, err
	}
	for id, layer := range config.Documents {
		if !documents[id] {
			return config, bad("客户端凭证引用了当前 MCP 服务未使用的 API 文档")
		}
		if _, err := applyClientCredentialLayer(Credential{Kind: "none"}, layer); err != nil {
			return config, err
		}
	}
	return config, nil
}

func applyClientCredentialLayer(saved Credential, layer clientCredentialLayer) (Credential, error) {
	c := saved
	c.Entries = append([]CredentialEntry(nil), saved.Entries...)
	if layer.Auth != nil {
		auth := layer.Auth
		input := credentialInput{Kind: auth.Type, ReplaceValue: true, BodyFormat: c.BodyFormat}
		switch auth.Type {
		case "none":
		case "basic":
			input.Username, input.Value = auth.Username, auth.Password
		case "bearer":
			input.Value = auth.Token
		case "apiKey":
			input.Header, input.Value = auth.Name, auth.Value
		default:
			return Credential{}, bad("客户端 auth.type 应为 none、basic、bearer 或 apiKey")
		}
		// An empty username must not inherit the saved account implicitly.
		if auth.Type == "basic" && input.Username == "" {
			return Credential{}, bad("客户端 Basic Auth 需要填写 username")
		}
		merged, err := mergeCredential(Credential{Kind: "none", Entries: c.Entries, BodyFormat: c.BodyFormat}, input)
		if err != nil {
			return Credential{}, bad("客户端认证配置无效，请检查认证类型、Header 名称及必填凭证")
		}
		c = merged
		if c.Kind == "apiKey" && strings.EqualFold(c.Header, "Cookie") {
			removeCredentialCookies(&c, c.Value)
		}
	}
	if layer.BodyFormat != "" {
		if !slices.Contains([]string{"document", "json", "form"}, layer.BodyFormat) {
			return Credential{}, bad("客户端 bodyFormat 应为 document、json 或 form")
		}
		c.BodyFormat = layer.BodyFormat
		// An explicit format at this layer also replaces a lower-layer static
		// Content-Type. Headers supplied below at the same layer may override it.
		c.Entries = slices.DeleteFunc(c.Entries, func(entry CredentialEntry) bool {
			return entry.Location == "header" && strings.EqualFold(entry.Name, "Content-Type")
		})
		if c.Kind == "apiKey" && strings.EqualFold(c.Header, "Content-Type") {
			c.Kind, c.Header, c.Username, c.Value = "none", "", "", ""
		}
	}
	count := len(layer.Headers) + len(layer.Query) + len(layer.Cookies)
	if count > 100 {
		return Credential{}, bad("每层客户端凭证最多 100 条 Header / Query / Cookie")
	}
	for _, group := range []struct {
		location string
		fields   map[string]string
	}{{"header", layer.Headers}, {"query", layer.Query}, {"cookie", layer.Cookies}} {
		seen := map[string]bool{}
		for name, value := range group.fields {
			key := name
			if group.location == "header" {
				key = strings.ToLower(name)
			}
			if seen[key] {
				return Credential{}, bad("客户端 Header 名称不能重复，名称不区分大小写")
			}
			seen[key] = true
			entry := CredentialEntry{Location: group.location, Name: name, Value: value, ValueType: "string", Enabled: true}
			if validateCredentialEntry(entry) != nil {
				return Credential{}, bad("客户端请求参数无效，请检查名称、类型和控制字符")
			}
			c.Entries = slices.DeleteFunc(c.Entries, func(previous CredentialEntry) bool {
				return previous.Location == group.location && (previous.Name == name || group.location == "header" && strings.EqualFold(previous.Name, name))
			})
			if group.location == "header" && (strings.EqualFold(name, "Authorization") && (c.Kind == "basic" || c.Kind == "bearer") || c.Kind == "apiKey" && strings.EqualFold(c.Header, name)) {
				c.Kind, c.Header, c.Username, c.Value = "none", "", "", ""
			}
			if group.location == "header" && strings.EqualFold(name, "Cookie") {
				removeCredentialCookies(&c, value)
			}
			c.Entries = append(c.Entries, entry)
		}
	}
	if len(layer.Body) > 0 {
		var body map[string]any
		decoder := json.NewDecoder(strings.NewReader(string(layer.Body)))
		decoder.UseNumber()
		if decoder.Decode(&body) != nil || body == nil {
			return Credential{}, bad("客户端 body 必须为 JSON 对象")
		}
		merged := credentialBody(c)
		mergeObject(merged, body)
		encoded, _ := json.Marshal(merged)
		if len(encoded) > 256000 {
			return Credential{}, bad("合并后的客户端 Body 凭证过大")
		}
		c.Entries = slices.DeleteFunc(c.Entries, func(entry CredentialEntry) bool { return entry.Location == "body" })
		c.Entries = append(c.Entries, CredentialEntry{Location: "body", Name: "$", Value: string(encoded), ValueType: "json", Enabled: true})
	}
	return c, nil
}

func (c clientCredentialConfig) credentialFor(doc Document) (Credential, error) {
	merged, err := applyClientCredentialLayer(doc.Credential, c.clientCredentialLayer)
	if err != nil {
		return Credential{}, err
	}
	return applyClientCredentialLayer(merged, c.Documents[doc.ID])
}
