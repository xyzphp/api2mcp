package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (a *App) documentOperation(documentID, operationID string) (Document, Operation, error) {
	for _, doc := range a.store.Snapshot().Documents {
		if doc.ID != documentID {
			continue
		}
		for _, op := range doc.Operations {
			if op.ID == operationID {
				return doc, op, nil
			}
		}
		return Document{}, Operation{}, httpErr(404, "接口不属于当前文档或已被移除")
	}
	return Document{}, Operation{}, httpErr(404, "文档不存在")
}

func requestBodyFormat(doc Document, op Operation) string {
	if doc.Credential.BodyFormat == "form" || doc.Credential.BodyFormat != "json" && op.ContentType == "application/x-www-form-urlencoded" {
		return "form"
	}
	return "json"
}

func (a *App) describeDocumentTest(w http.ResponseWriter, r *http.Request) error {
	doc, op, err := a.documentOperation(r.PathValue("id"), r.URL.Query().Get("operationId"))
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]any{
		"inputSchema": credentialInputSchema(op, doc.Credential),
		"bodyFormat":  requestBodyFormat(doc, op),
		"timeout":     a.cfg.UpstreamTimeout.Seconds(),
	})
}

// Tests share the request builder and transport with MCP, without requiring a
// published service. Extra request parameters exist only for this invocation.
func testerOperation(op Operation, credential Credential, args map[string]any) (Operation, map[string]any, error) {
	op.Parameters = append([]Parameter(nil), op.Parameters...)
	schema := credentialInputSchema(op, credential)
	properties := obj(schema["properties"])
	count := 0
	for _, location := range []string{"query", "header", "cookie"} {
		values, exists := args[location]
		if !exists {
			continue
		}
		fields, ok := values.(map[string]any)
		if !ok {
			return op, nil, bad("Query、Headers 和 Cookie 参数需要使用对象")
		}
		count += len(fields)
		if count > 100 {
			return op, nil, bad("本次请求最多添加 100 个 Query、Header 或 Cookie 参数")
		}
		group, ok := properties[location].(map[string]any)
		if !ok {
			group = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
			properties[location] = group
		}
		groupFields := obj(group["properties"])
		seen := map[string]bool{}
		for _, name := range sortedKeys(fields) {
			value := fields[name]
			key := name
			if location == "header" {
				key = strings.ToLower(name)
			}
			if seen[key] {
				return op, nil, bad("Header 名称不能重复，名称不区分大小写")
			}
			seen[key] = true
			if name == "" || len(name) > 256 || !safeHeaderValue(name) || strings.ContainsRune(name, '\t') || len(textValue(value)) > 16384 {
				return op, nil, bad("请求参数名称或值无效：名称最多 256 字节，值最多 16 KB")
			}
			if location == "header" && (!credentialHeader(name) || reservedHeader(name)) {
				return op, nil, bad("Authorization、Cookie、Content-Type 请使用凭证或 Body 格式配置，不能覆盖 HTTP 传输控制字段")
			}
			matched := false
			for _, parameter := range op.Parameters {
				if parameter.Location != location || parameter.Name != name && !(location == "header" && strings.EqualFold(parameter.Name, name)) {
					continue
				}
				matched = true
				if parameter.Name != name {
					delete(fields, name)
					fields[parameter.Name] = value
					name = parameter.Name
				}
				break
			}
			if !matched {
				style := "form"
				if location == "header" {
					style = "simple"
				}
				op.Parameters = append(op.Parameters, Parameter{Name: name, Location: location, Style: style, Explode: true})
			}
			if _, exists := groupFields[name]; !exists {
				groupFields[name] = map[string]any{}
			}
		}
	}
	if _, hasBody := args["body"]; hasBody && op.RequestBody == nil {
		properties["body"] = map[string]any{}
	}
	return op, schema, nil
}

type requestPreview struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

func maskBodyFields(body, fixed map[string]any) {
	for key, value := range fixed {
		if nested, ok := value.(map[string]any); ok {
			maskBodyFields(obj(body[key]), nested)
		} else {
			body[key] = "••••••"
		}
	}
}

func omitInjectedBodyFields(body, fixed map[string]any) {
	for name, value := range fixed {
		if nested, ok := value.(map[string]any); ok {
			if child, ok := body[name].(map[string]any); ok {
				omitInjectedBodyFields(child, nested)
				continue
			}
		}
		delete(body, name)
	}
}

func previewRequest(req *http.Request, credential Credential) requestPreview {
	u := *req.URL
	query := u.Query()
	headers := req.Header.Clone()
	for _, name := range []string{"Authorization", "Cookie"} {
		if headers.Get(name) != "" {
			headers.Set(name, "••••••")
		}
	}
	if credential.Kind == "apiKey" {
		headers.Set(credential.Header, "••••••")
	}
	for _, entry := range credential.Entries {
		if !entry.Enabled {
			continue
		}
		switch entry.Location {
		case "header":
			headers.Set(entry.Name, "••••••")
		case "query":
			query.Set(entry.Name, "••••••")
		}
	}
	u.RawQuery = query.Encode()
	preview := requestPreview{Method: req.Method, URL: u.String(), Headers: headers}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer body.Close()
			data, _ := io.ReadAll(body)
			preview.Body = string(data)
			if hasCredentialBody(credential) {
				fixed := credentialBody(credential)
				var object map[string]any
				decoder := json.NewDecoder(strings.NewReader(string(data)))
				decoder.UseNumber()
				if decoder.Decode(&object) == nil && object != nil {
					maskBodyFields(object, fixed)
					masked, _ := json.MarshalIndent(object, "", "  ")
					preview.Body = string(masked)
				} else if form, parseErr := url.ParseQuery(string(data)); parseErr == nil {
					for name := range fixed {
						form.Set(name, "••••••")
					}
					preview.Body = form.Encode()
				} else {
					preview.Body = "••••••"
				}
			}
		}
	}
	return preview
}

func (a *App) testDocument(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		OperationID string          `json:"operationId"`
		Arguments   json.RawMessage `json:"arguments"`
		Body        *string         `json:"body,omitempty"`
		BodyFormat  string          `json:"bodyFormat"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	doc, op, err := a.documentOperation(r.PathValue("id"), input.OperationID)
	if err != nil {
		return err
	}
	if input.BodyFormat != "" && input.BodyFormat != "configured" {
		if input.BodyFormat != "json" && input.BodyFormat != "form" {
			return bad("Body 格式应为已保存配置、JSON 或 URL 编码表单")
		}
		doc.Credential.BodyFormat = input.BodyFormat
	}
	args := map[string]any{}
	if len(input.Arguments) != 0 {
		decoder := json.NewDecoder(strings.NewReader(string(input.Arguments)))
		decoder.UseNumber()
		if decoder.Decode(&args) != nil || args == nil {
			return bad("请求参数必须为 JSON 对象")
		}
	}
	if input.Body != nil {
		if !json.Valid([]byte(*input.Body)) {
			return bad("请求 Body 不是有效 JSON，请检查格式")
		}
		var body any
		decoder := json.NewDecoder(strings.NewReader(*input.Body))
		decoder.UseNumber()
		_ = decoder.Decode(&body)
		args["body"] = body
	}
	// Full JSON payloads copied from API documentation may still include fixed
	// authentication fields. Ignore those supplied values before validating the
	// caller's partial body; the shared builder injects the saved values later.
	if body, ok := args["body"].(map[string]any); ok && hasCredentialBody(doc.Credential) {
		omitInjectedBodyFields(body, credentialBody(doc.Credential))
	}
	op, schema, err := testerOperation(op, doc.Credential, args)
	if err != nil {
		return err
	}
	compiled, err := compileSchema(schema)
	if err != nil {
		return bad("接口参数 Schema 无效")
	}
	encoded, _ := json.Marshal(args)
	var normalized any
	_ = json.Unmarshal(encoded, &normalized)
	if err := compiled.Validate(normalized); err != nil {
		return bad("请求参数不符合文档 Schema，请检查必填项和参数类型：" + err.Error())
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.UpstreamTimeout)
	defer cancel()
	req, err := buildUpstreamRequest(ctx, doc, op, args)
	if err != nil {
		return bad(err.Error())
	}
	preview := previewRequest(req, doc.Credential)
	start := time.Now()
	response, err := a.executeUpstream(req)
	duration := time.Since(start)
	result := map[string]any{"success": err == nil && response.Status >= 200 && response.Status < 300, "duration": duration.Milliseconds(), "request": preview}
	message := ""
	if err != nil {
		message = err.Error()
		if r.Context().Err() != nil {
			message = "请求已取消"
		}
		result["error"] = message
	} else {
		result["response"] = response
	}
	a.record(Server{Draft: Draft{Name: "API 测试 · " + doc.Name}}, fmt.Sprintf("%s %s", op.Method, op.Path), result["success"].(bool), duration, response.Status, message)
	return sendJSON(w, 200, result)
}
