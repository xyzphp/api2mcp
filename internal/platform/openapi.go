package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

const maxDocumentBytes = 2_000_000

var methods = []string{"get", "post", "put", "patch", "delete", "head", "options"}
var toolChars = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
var pathParameters = regexp.MustCompile(`\{([^{}]+)\}`)

func obj(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}
func str(v any) string { s, _ := v.(string); return s }
func arr(v any) []any  { a, _ := v.([]any); return a }
func flag(v any) bool  { b, _ := v.(bool); return b }
func fallback(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func ParseDocument(content []byte, filename, source string) (Document, error) {
	if len(content) == 0 || len(content) > maxDocumentBytes {
		return Document{}, fmt.Errorf("文档内容不能为空且不能超过 2 MB")
	}
	var root map[string]any
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&root); err != nil {
		return Document{}, fmt.Errorf("JSON / YAML 语法错误: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Document{}, fmt.Errorf("一次只能导入一份 OpenAPI 文档")
	}
	// Convert YAML numbers and nested maps into the same JSON data model used by
	// the schema validator and MCP clients. Non-string mapping keys are rejected.
	b, err := json.Marshal(root)
	if err != nil {
		return Document{}, fmt.Errorf("文档必须使用 JSON 兼容的数据类型")
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return Document{}, err
	}
	version := fallback(str(root["openapi"]), str(root["swagger"]))
	if version != "2.0" && !strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.") {
		return Document{}, fmt.Errorf("支持 OpenAPI 3.0 / 3.1 或 Swagger 2.0")
	}
	info := obj(root["info"])
	d := Document{ID: newID(), Name: fallback(str(info["title"]), filename), Version: fallback(str(info["version"]), "1.0.0"),
		Filename: filename, SpecVersion: version, Source: source, ImportedAt: now(), Operations: []Operation{}, Credential: Credential{Kind: "none"}}
	if len(d.Name) > 300 {
		return d, fmt.Errorf("文档名称过长")
	}
	servers := arr(root["servers"])
	if len(servers) > 0 {
		s := obj(servers[0])
		d.BaseURL = str(s["url"])
		for name, variable := range obj(s["variables"]) {
			d.BaseURL = strings.ReplaceAll(d.BaseURL, "{"+name+"}", str(obj(variable)["default"]))
		}
	} else if host := str(root["host"]); host != "" {
		scheme := "https"
		if schemes := arr(root["schemes"]); len(schemes) > 0 {
			scheme = str(schemes[0])
		}
		d.BaseURL = scheme + "://" + host + str(root["basePath"])
	}
	if u, err := url.Parse(d.BaseURL); err == nil && !u.IsAbs() {
		if from, err := url.Parse(source); err == nil && strings.HasPrefix(source, "http") {
			d.BaseURL = from.ResolveReference(u).String()
		} else {
			d.BaseURL = ""
		}
	}
	d.BaseURL = strings.TrimRight(d.BaseURL, "/")
	if d.BaseURL != "" {
		if _, err := validURL(d.BaseURL, true); err != nil {
			return d, fmt.Errorf("文档的 servers / host 地址无效: %w", err)
		}
	}
	paths := obj(root["paths"])
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	expandedSize := 0
	for _, path := range keys {
		if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") {
			return d, fmt.Errorf("接口路径无效: %s", path)
		}
		item, err := resolveObject(paths[path], root)
		if err != nil {
			return d, err
		}
		if len(arr(item["servers"])) > 0 {
			return d, fmt.Errorf("第一版每份文档使用一个全局 API 基础地址，请将路径级 servers 拆成独立文档")
		}
		for _, method := range methods {
			if item[method] == nil {
				continue
			}
			raw, err := resolveObject(item[method], root)
			if err != nil {
				return d, err
			}
			if len(arr(raw["servers"])) > 0 {
				return d, fmt.Errorf("第一版暂不支持接口级 servers，请将不同基础地址拆成独立文档")
			}
			op := Operation{ID: d.ID + ":" + method + ":" + path, DocumentID: d.ID, Method: strings.ToUpper(method), Path: path,
				OperationID: fallback(str(raw["operationId"]), method+"_"+toolChars.ReplaceAllString(path, "_")), Description: str(raw["description"]),
				Tag: "未分组", Parameters: []Parameter{}}
			op.Name = fallback(str(raw["summary"]), op.OperationID)
			if tags := arr(raw["tags"]); len(tags) > 0 {
				op.Tag = str(tags[0])
			}
			op.ToolName = toolName(op.OperationID, op.ID)
			params := map[string]map[string]any{}
			for _, p := range append(append([]any{}, arr(item["parameters"])...), arr(raw["parameters"])...) {
				parameter, err := resolveObject(p, root)
				if err != nil {
					return d, err
				}
				params[str(parameter["in"])+":"+str(parameter["name"])] = parameter
			}
			pkeys := make([]string, 0, len(params))
			for key := range params {
				pkeys = append(pkeys, key)
			}
			sort.Strings(pkeys)
			formProps := map[string]any{}
			formRequired := []string{}
			for _, key := range pkeys {
				p := params[key]
				if p["content"] != nil {
					return d, fmt.Errorf("暂不支持使用 content 定义的参数，请使用 schema 定义")
				}
				location, name := str(p["in"]), str(p["name"])
				if location == "body" {
					schema, err := normalizeSchema(p["schema"], root, nil, 0)
					if err != nil {
						return d, err
					}
					op.RequestBody, op.BodyRequired, op.ContentType = schema, flag(p["required"]), "application/json"
					continue
				}
				if location != "path" && location != "query" && location != "header" && location != "cookie" && location != "formData" {
					return d, fmt.Errorf("%s: 不支持参数位置 %s", op.Name, location)
				}
				if name == "" {
					return d, fmt.Errorf("接口参数缺少名称")
				}
				if (location == "header" || location == "cookie") && !headerName.MatchString(name) {
					return d, fmt.Errorf("Header / Cookie 参数名称无效")
				}
				if location == "header" && reservedHeader(name) {
					return d, fmt.Errorf("%s: %s 请通过 API 凭证配置，不支持作为工具 Header 参数", op.Name, name)
				}
				schemaValue := p["schema"]
				if schemaValue == nil {
					schemaValue = p
				}
				schema, err := normalizeSchema(schemaValue, root, nil, 0)
				if err != nil {
					return d, err
				}
				if str(schema["type"]) == "file" {
					return d, fmt.Errorf("第一版暂不支持文件上传接口: %s", op.Name)
				}
				if location == "formData" {
					formProps[name] = schema
					if flag(p["required"]) {
						formRequired = append(formRequired, name)
					}
					continue
				}
				style := fallback(str(p["style"]), "simple")
				if location == "query" || location == "cookie" {
					style = fallback(str(p["style"]), "form")
				}
				explode := style == "form"
				if _, exists := p["explode"]; exists {
					explode = flag(p["explode"])
				}
				if location == "path" && style != "simple" || location == "header" && style != "simple" || location == "cookie" && style != "form" || location == "query" && style != "form" && style != "deepObject" && style != "spaceDelimited" && style != "pipeDelimited" {
					return d, fmt.Errorf("%s: 暂不支持参数序列化样式 %s / %s", op.Name, location, style)
				}
				op.Parameters = append(op.Parameters, Parameter{Name: name, Location: location, Type: fallback(str(schema["type"]), "object"), Required: location == "path" || flag(p["required"]),
					Description: str(p["description"]), Schema: schema, Style: style, Explode: explode, CollectionFormat: str(p["collectionFormat"])})
			}
			if len(formProps) > 0 {
				op.RequestBody = map[string]any{"type": "object", "properties": formProps, "required": formRequired, "additionalProperties": false}
				op.ContentType = "application/x-www-form-urlencoded"
				op.BodyRequired = len(formRequired) > 0
			}
			if version == "2.0" && op.RequestBody != nil {
				consumes := arr(raw["consumes"])
				if len(consumes) == 0 {
					consumes = arr(root["consumes"])
				}
				if len(consumes) > 0 {
					supported := false
					for _, media := range consumes {
						if str(media) == op.ContentType {
							supported = true
						}
					}
					if !supported {
						return d, fmt.Errorf("%s 的 consumes 暂不支持，请使用 JSON 或 URL 编码表单", op.Name)
					}
				}
			}
			if raw["requestBody"] != nil {
				body, err := resolveObject(raw["requestBody"], root)
				if err != nil {
					return d, err
				}
				content := obj(body["content"])
				ct := "application/json"
				if content[ct] == nil {
					ct = "application/x-www-form-urlencoded"
				}
				if content[ct] == nil {
					return d, fmt.Errorf("%s: 请求体只支持 JSON 或 URL 编码表单，暂不支持 multipart / binary", op.Name)
				}
				op.RequestBody, err = normalizeSchema(obj(content[ct])["schema"], root, nil, 0)
				if err != nil {
					return d, err
				}
				op.BodyRequired, op.ContentType = flag(body["required"]), ct
			}
			response := obj(obj(raw["responses"])["200"])
			if len(response) == 0 {
				response = obj(obj(raw["responses"])["201"])
			}
			media := obj(obj(response["content"])["application/json"])
			op.ResponseExample = media["example"]
			if op.ResponseExample == nil {
				op.ResponseExample = obj(media["schema"])["example"]
			}
			op.InputSchema = inputSchema(op)
			for _, match := range pathParameters.FindAllStringSubmatch(op.Path, -1) {
				found := false
				for _, parameter := range op.Parameters {
					if parameter.Location == "path" && parameter.Name == match[1] {
						found = true
					}
				}
				if !found {
					return d, fmt.Errorf("%s 缺少路径参数定义 %s", op.Name, match[1])
				}
			}
			if _, err := compileSchema(op.InputSchema); err != nil {
				return d, fmt.Errorf("%s 的参数 Schema 无效: %w", op.Name, err)
			}
			encoded, _ := json.Marshal(op)
			expandedSize += len(encoded)
			if len(encoded) > 256_000 || expandedSize > 8_000_000 {
				return d, fmt.Errorf("请求 Schema 展开后过大（每个接口最多 256 KB，每份文档最多 8 MB）")
			}
			d.Operations = append(d.Operations, op)
			if len(d.Operations) > 500 {
				return d, fmt.Errorf("每份文档最多支持 500 个接口")
			}
		}
	}
	if len(d.Operations) == 0 {
		return d, fmt.Errorf("文档中没有可用的接口")
	}
	return d, nil
}

func toolName(operationID, id string) string {
	hash := sha256.Sum256([]byte(id))
	prefix := toolChars.ReplaceAllString(operationID, "_")
	if len(prefix) > 50 {
		prefix = prefix[:50]
	}
	return fallback(prefix, "api") + "_" + hex.EncodeToString(hash[:4])
}

func resolveObject(v any, root map[string]any) (map[string]any, error) {
	seen := map[string]bool{}
	for {
		m := obj(v)
		ref := str(m["$ref"])
		if ref == "" {
			return m, nil
		}
		if seen[ref] {
			return nil, fmt.Errorf("文档包含循环引用 %s", ref)
		}
		seen[ref] = true
		var err error
		v, err = lookupRef(root, ref)
		if err != nil {
			return nil, err
		}
	}
}

func lookupRef(root map[string]any, ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("暂不支持外部引用 %s，请先合并为单份文档", ref)
	}
	var value any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		value = obj(value)[part]
		if value == nil {
			return nil, fmt.Errorf("引用不存在 %s", ref)
		}
	}
	return value, nil
}

// Resolve only request schemas. Recursive request models need a bundled schema
// implementation and are rejected explicitly rather than silently truncated.
func normalizeSchema(v any, root map[string]any, seen map[string]bool, depth int) (map[string]any, error) {
	budget := 20000
	return normalizeSchemaBudget(v, root, seen, depth, &budget)
}

func normalizeSchemaBudget(v any, root map[string]any, seen map[string]bool, depth int, budget *int) (map[string]any, error) {
	*budget--
	if *budget < 0 {
		return nil, fmt.Errorf("请求 Schema 展开后过大，请简化引用")
	}
	if boolean, ok := v.(bool); ok {
		if boolean {
			return map[string]any{}, nil
		}
		return map[string]any{"not": map[string]any{}}, nil
	}
	if depth > 40 {
		return nil, fmt.Errorf("参数 Schema 层级过深")
	}
	m := obj(v)
	if ref := str(m["$ref"]); ref != "" {
		if seen[ref] {
			return nil, fmt.Errorf("第一版暂不支持循环请求 Schema: %s", ref)
		}
		next := map[string]bool{}
		for k, v := range seen {
			next[k] = v
		}
		next[ref] = true
		value, err := lookupRef(root, ref)
		if err != nil {
			return nil, err
		}
		merged := map[string]any{}
		for k, v := range obj(value) {
			merged[k] = v
		}
		for k, v := range m {
			if k != "$ref" {
				merged[k] = v
			}
		}
		return normalizeSchemaBudget(merged, root, next, depth+1, budget)
	}
	out := map[string]any{}
	for _, k := range []string{"type", "title", "description", "enum", "const", "default", "format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "pattern", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "minContains", "maxContains", "dependentRequired"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	if required, ok := m["required"].([]any); ok {
		out["required"] = required
	}
	for _, pair := range [][2]string{{"exclusiveMinimum", "minimum"}, {"exclusiveMaximum", "maximum"}} {
		if boolean, ok := out[pair[0]].(bool); ok {
			delete(out, pair[0])
			if boolean {
				if value, ok := out[pair[1]]; ok {
					out[pair[0]] = value
					delete(out, pair[1])
				}
			}
		}
	}
	if flag(m["nullable"]) {
		if t := str(out["type"]); t != "" {
			out["type"] = []string{t, "null"}
		}
	}
	for _, key := range []string{"properties", "$defs", "patternProperties", "dependentSchemas"} {
		if values, exists := m[key]; exists {
			props := map[string]any{}
			for name, value := range obj(values) {
				schema, err := normalizeSchemaBudget(value, root, seen, depth+1, budget)
				if err != nil {
					return nil, err
				}
				props[name] = schema
			}
			out[key] = props
		}
	}
	for _, key := range []string{"items", "not", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "contains", "propertyNames", "if", "then", "else"} {
		if value, exists := m[key]; exists {
			if boolean, ok := value.(bool); ok {
				out[key] = boolean
			} else {
				schema, err := normalizeSchemaBudget(value, root, seen, depth+1, budget)
				if err != nil {
					return nil, err
				}
				out[key] = schema
			}
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if values, exists := m[key]; exists {
			schemas := []any{}
			for _, value := range arr(values) {
				schema, err := normalizeSchemaBudget(value, root, seen, depth+1, budget)
				if err != nil {
					return nil, err
				}
				schemas = append(schemas, schema)
			}
			out[key] = schemas
		}
	}
	return out, nil
}

func inputSchema(op Operation) map[string]any {
	properties := map[string]any{}
	requiredGroups := []string{}
	for _, location := range []string{"path", "query", "header", "cookie"} {
		props := map[string]any{}
		required := []string{}
		for _, p := range op.Parameters {
			if p.Location == location {
				props[p.Name] = p.Schema
				if p.Required {
					required = append(required, p.Name)
				}
			}
		}
		if len(props) > 0 {
			properties[location] = map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
			if len(required) > 0 {
				requiredGroups = append(requiredGroups, location)
			}
		}
	}
	if op.RequestBody != nil {
		properties["body"] = op.RequestBody
		if op.BodyRequired {
			requiredGroups = append(requiredGroups, "body")
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": requiredGroups, "additionalProperties": false}
}

func compileSchema(input map[string]any) (*jsonschema.Resolved, error) {
	b, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(b, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}
