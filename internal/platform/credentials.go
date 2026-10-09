package platform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

type credentialInput struct {
	BaseURL      string                  `json:"baseUrl"`
	Kind         string                  `json:"kind"`
	Header       string                  `json:"header"`
	Username     string                  `json:"username"`
	Value        string                  `json:"value"`
	ReplaceValue bool                    `json:"replaceValue"`
	BodyFormat   string                  `json:"bodyFormat"`
	Entries      *[]credentialEntryInput `json:"entries"`
}

type credentialEntryInput struct {
	ID           string `json:"id"`
	Location     string `json:"in"`
	Name         string `json:"name"`
	Value        string `json:"value"`
	ValueType    string `json:"valueType"`
	Enabled      *bool  `json:"enabled"`
	ReplaceValue bool   `json:"replaceValue"`
}

// Values are persisted only in the encrypted workspace. All responses share
// this redaction path, including saves and document updates.
func publicCredential(c Credential) Credential {
	c.ValueConfigured = c.Kind == "basic" || (c.Kind != "none" && c.Value != "")
	c.UsernameConfigured = c.Kind == "basic" && c.Username != ""
	c.EmptyValue = c.Kind == "basic" && c.Value == ""
	c.Configured = c.ValueConfigured
	c.Value, c.Username = "", ""
	c.Entries = append([]CredentialEntry(nil), c.Entries...)
	for i := range c.Entries {
		c.Entries[i].EmptyValue = c.Entries[i].Value == ""
		c.Entries[i].Value = ""
		c.Entries[i].Configured = true
		c.Configured = c.Configured || c.Entries[i].Enabled
	}
	return c
}

func safeHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < 32 && value[i] != '\t' || value[i] == 127 {
			return false
		}
	}
	return true
}

func credentialHeader(name string) bool {
	// Administrators may supply Authorization, Cookie and Content-Type. HTTP
	// routing and framing still belong to the configured upstream and transport.
	return len(name) <= 256 && headerName.MatchString(name) && !slices.Contains([]string{
		"host", "connection", "content-length", "transfer-encoding", "trailer", "te", "upgrade", "proxy-authorization", "proxy-authenticate",
	}, strings.ToLower(name))
}

func mergeCredential(previous Credential, input credentialInput) (Credential, error) {
	if !slices.Contains([]string{"none", "bearer", "apiKey", "basic"}, input.Kind) {
		return Credential{}, bad("不支持的认证方式")
	}
	c := Credential{Kind: input.Kind, Header: strings.TrimSpace(input.Header), Username: input.Username, Value: input.Value}
	c.BodyFormat = fallback(input.BodyFormat, fallback(previous.BodyFormat, "document"))
	if !slices.Contains([]string{"document", "json", "form"}, c.BodyFormat) {
		return Credential{}, bad("Body 格式应为按文档、JSON 或 URL 编码表单")
	}
	if c.Kind == previous.Kind {
		if c.Value == "" && !input.ReplaceValue {
			c.Value = previous.Value
		}
		if c.Kind == "basic" && c.Username == "" {
			c.Username = previous.Username
		}
	}
	if c.Kind == "none" {
		c.Value, c.Username, c.Header = "", "", ""
	} else {
		if len(c.Value) > 16384 || len(c.Username) > 16384 {
			return Credential{}, bad("单个凭证值最多 16 KB")
		}
		if c.Kind == "basic" {
			if c.Username == "" || strings.ContainsAny(c.Username, ":\r\n") {
				return Credential{}, bad("请填写 Basic Auth 用户名，用户名不能包含冒号或换行")
			}
		} else if c.Value == "" || !safeHeaderValue(c.Value) {
			return Credential{}, bad("请填写有效凭证，Header 值不能包含换行或控制字符")
		}
		if c.Kind == "apiKey" && !credentialHeader(c.Header) {
			return Credential{}, bad("Header 名称无效，不能覆盖 Host 或 HTTP 传输控制字段")
		}
		if c.Kind != "basic" {
			c.Username = ""
		}
		if c.Kind != "apiKey" {
			c.Header = ""
		}
	}
	// Older clients update only the preset. Omitted entries preserve the list;
	// an explicit empty array removes it.
	c.Entries = append([]CredentialEntry(nil), previous.Entries...)
	if input.Entries != nil {
		if len(*input.Entries) > 100 {
			return Credential{}, bad("最多配置 100 条请求参数")
		}
		known := map[string]CredentialEntry{}
		for _, entry := range previous.Entries {
			known[entry.ID] = entry
		}
		seenIDs, active := map[string]bool{}, map[string]bool{}
		c.Entries = []CredentialEntry{}
		for i, item := range *input.Entries {
			entry := CredentialEntry{ID: item.ID, Location: item.Location, Name: strings.TrimSpace(item.Name), Value: item.Value, ValueType: item.ValueType, Enabled: item.Enabled == nil || *item.Enabled, Configured: true}
			if entry.ID == "" {
				entry.ID = newID()
			} else {
				old, exists := known[entry.ID]
				if !exists || seenIDs[entry.ID] {
					return Credential{}, bad("请求参数已变更，请关闭弹窗后重新打开")
				}
				if entry.Value == "" && !item.ReplaceValue {
					entry.Value = old.Value
				}
			}
			seenIDs[entry.ID] = true
			if entry.ValueType == "" {
				entry.ValueType = "string"
			}
			if err := validateCredentialEntry(entry); err != nil {
				return Credential{}, bad(fmt.Sprintf("第 %d 条参数：%s", i+1, err))
			}
			key := entry.Location + ":" + entry.Name
			if entry.Location == "header" {
				key = strings.ToLower(key)
			}
			if entry.Enabled && active[key] {
				return Credential{}, bad("同一位置不能启用多个同名参数，请停用或删除重复项")
			}
			active[key] = active[key] || entry.Enabled
			c.Entries = append(c.Entries, entry)
		}
	}
	size := len(c.Value) + len(c.Username)
	for _, entry := range c.Entries {
		size += len(entry.Name) + len(entry.Value)
	}
	if size > 256000 {
		return Credential{}, bad("请求配置总大小不能超过 256 KB")
	}
	c.Configured = publicCredential(c).Configured
	return c, nil
}

func validateCredentialEntry(entry CredentialEntry) error {
	if !slices.Contains([]string{"header", "query", "cookie", "body"}, entry.Location) {
		return fmt.Errorf("位置应为 Header、Query、Cookie 或 Body")
	}
	if entry.Name == "" || len(entry.Name) > 256 || !utf8.ValidString(entry.Name) || !safeHeaderValue(entry.Name) || strings.ContainsRune(entry.Name, '\t') {
		return fmt.Errorf("参数名应为 1–256 字节，不能包含控制字符")
	}
	if len(entry.Value) > 16384 || !utf8.ValidString(entry.Value) {
		return fmt.Errorf("单个参数值最多 16 KB，需为有效文本")
	}
	if entry.ValueType != "string" && (entry.Location != "body" || entry.ValueType != "json") {
		return fmt.Errorf("只有 Body 支持 JSON 类型，其余位置请使用字符串")
	}
	switch entry.Location {
	case "header":
		if !credentialHeader(entry.Name) || !safeHeaderValue(entry.Value) {
			return fmt.Errorf("Header 名称或值无效，不能覆盖 Host 或 HTTP 传输控制字段")
		}
	case "cookie":
		if err := (&http.Cookie{Name: entry.Name, Value: entry.Value}).Valid(); err != nil {
			return fmt.Errorf("Cookie 名称或值无效；整段 Cookie 可放入自定义 Header")
		}
	case "body":
		if _, err := bodyFieldPath(entry.Name); err != nil {
			return err
		}
		value, err := bodyEntryValue(entry)
		if err != nil {
			return err
		}
		if entry.Name == "$" {
			if _, ok := value.(map[string]any); !ok {
				return fmt.Errorf("Body 的 $ 配置需使用 JSON 对象")
			}
		}
	}
	return nil
}

func bodyFieldPath(name string) ([]string, error) {
	if name == "$" {
		return nil, nil
	}
	var parts []string
	if strings.HasPrefix(name, "/") {
		parts = strings.Split(name[1:], "/")
		for i, part := range parts {
			for j := 0; j < len(part); j++ {
				if part[j] == '~' {
					if j+1 >= len(part) || part[j+1] != '0' && part[j+1] != '1' {
						return nil, fmt.Errorf("Body 路径中的 ~ 需要写为 ~0，/ 需要写为 ~1")
					}
					j++
				}
			}
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		}
	} else {
		parts = strings.Split(name, ".")
	}
	if len(parts) > 16 {
		return nil, fmt.Errorf("Body 嵌套路径最多 16 层")
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("Body 字段或嵌套路径不能为空")
		}
	}
	return parts, nil
}

func bodyEntryValue(entry CredentialEntry) (any, error) {
	if entry.ValueType != "json" {
		return entry.Value, nil
	}
	if !json.Valid([]byte(entry.Value)) {
		return nil, fmt.Errorf("Body 的 JSON 值无效，请输入字符串、数字、布尔值、对象、数组或 null")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(entry.Value))
	decoder.UseNumber()
	_ = decoder.Decode(&value)
	return value, nil
}

func mergeObject(target, source map[string]any) {
	for key, value := range source {
		if object, ok := value.(map[string]any); ok {
			child, _ := target[key].(map[string]any)
			if child == nil {
				child = map[string]any{}
				target[key] = child
			}
			mergeObject(child, object)
		} else {
			target[key] = value
		}
	}
}

func credentialBody(c Credential) map[string]any {
	body := map[string]any{}
	for _, entry := range c.Entries {
		if !entry.Enabled || entry.Location != "body" {
			continue
		}
		parts, _ := bodyFieldPath(entry.Name)
		value, _ := bodyEntryValue(entry)
		if len(parts) == 0 {
			mergeObject(body, value.(map[string]any))
			continue
		}
		cursor := body
		for _, part := range parts[:len(parts)-1] {
			child, _ := cursor[part].(map[string]any)
			if child == nil {
				child = map[string]any{}
				cursor[part] = child
			}
			cursor = child
		}
		mergeObject(cursor, map[string]any{parts[len(parts)-1]: value})
	}
	return body
}

func hasCredentialBody(c Credential) bool {
	for _, entry := range c.Entries {
		if entry.Enabled && entry.Location == "body" {
			return true
		}
	}
	return false
}

func removeRequired(schema map[string]any, name string) {
	remaining := []any{}
	for _, value := range arr(schema["required"]) {
		if value != name {
			remaining = append(remaining, value)
		}
	}
	schema["required"] = remaining
}

// Hide fixed inputs from the caller. oneOf can become ambiguous once its fixed
// discriminator is hidden, so the public schema uses anyOf and the merged body
// is checked against the original schema before it reaches the upstream.
func bodyCredentialSchema(schema map[string]any, provided map[string]any, hide bool) {
	properties, exists := schema["properties"].(map[string]any)
	if !exists {
		properties = map[string]any{}
		schema["properties"] = properties
	}
	if hide {
		// These constraints describe the merged object, rather than the caller's
		// partial object. The original constraints are validated after merging.
		for _, keyword := range []string{"minProperties", "maxProperties", "dependentRequired", "dependentSchemas", "if", "then", "else", "not", "const", "enum"} {
			delete(schema, keyword)
		}
	}
	for name, value := range provided {
		child, exists := properties[name].(map[string]any)
		if object, ok := value.(map[string]any); ok && exists {
			bodyCredentialSchema(child, object, hide)
			if hide && !schemaNeedsInput(child) {
				removeRequired(schema, name)
			}
		} else if hide {
			delete(properties, name)
			removeRequired(schema, name)
		} else if !exists {
			properties[name] = map[string]any{}
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf"} {
		branches := arr(schema[keyword])
		for _, branch := range branches {
			bodyCredentialSchema(obj(branch), provided, hide)
		}
		if hide && keyword == "oneOf" && len(branches) > 0 {
			delete(schema, "oneOf")
			if _, exists := schema["anyOf"]; exists {
				schema["allOf"] = append(arr(schema["allOf"]), map[string]any{"anyOf": branches})
			} else {
				schema["anyOf"] = branches
			}
		}
	}
}

func schemaNeedsInput(schema map[string]any) bool {
	if len(arr(schema["required"])) > 0 {
		return true
	}
	for _, branch := range arr(schema["allOf"]) {
		if schemaNeedsInput(obj(branch)) {
			return true
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		branches := arr(schema[keyword])
		if len(branches) == 0 {
			continue
		}
		allNeedInput := true
		for _, branch := range branches {
			allNeedInput = allNeedInput && schemaNeedsInput(obj(branch))
		}
		if allNeedInput {
			return true
		}
	}
	return false
}

func copySchema(schema map[string]any) map[string]any {
	data, _ := json.Marshal(schema)
	var result map[string]any
	_ = json.Unmarshal(data, &result)
	return result
}

func credentialInputSchema(op Operation, c Credential) map[string]any {
	schema := copySchema(op.InputSchema)
	provided := map[string]map[string]bool{"header": {}, "query": {}, "cookie": {}}
	provideHeader := func(name, value string) {
		name = strings.ToLower(name)
		provided["header"][name] = true
		if name == "cookie" {
			r := &http.Request{Header: http.Header{"Cookie": []string{value}}}
			for _, cookie := range r.Cookies() {
				provided["cookie"][cookie.Name] = true
			}
		}
	}
	for _, entry := range c.Entries {
		if !entry.Enabled || entry.Location == "body" {
			continue
		}
		name := entry.Name
		if entry.Location == "header" {
			provideHeader(name, entry.Value)
			continue
		}
		provided[entry.Location][name] = true
	}
	if c.Kind == "apiKey" {
		provideHeader(c.Header, c.Value)
	}
	if c.Kind == "basic" || c.Kind == "bearer" {
		provideHeader("Authorization", "")
	}
	properties := obj(schema["properties"])
	for location, names := range provided {
		group := obj(properties[location])
		fields := obj(group["properties"])
		for name := range fields {
			key := name
			if location == "header" {
				key = strings.ToLower(key)
			}
			if names[key] {
				delete(fields, name)
				removeRequired(group, name)
			}
		}
		if len(fields) == 0 {
			delete(properties, location)
			removeRequired(schema, location)
		} else if len(arr(group["required"])) == 0 {
			removeRequired(schema, location)
		}
	}
	if body := credentialBody(c); hasCredentialBody(c) && op.RequestBody != nil {
		group := obj(properties["body"])
		bodyCredentialSchema(group, body, true)
		if !schemaNeedsInput(group) {
			removeRequired(schema, "body")
		}
	}
	return schema
}
