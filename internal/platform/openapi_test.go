package platform

import (
	"fmt"
	"strings"
	"testing"
)

func TestOpenAPISchemaVariants(t *testing.T) {
	for _, test := range []struct {
		name, schema string
		good, bad    any
	}{
		{"ref", `{"$ref":"#/components/schemas/Input"}`, map[string]any{"name": "ok"}, map[string]any{}},
		{"oneOf", `{"oneOf":[{"type":"integer"},{"type":"string"}]}`, "ok", true},
		{"nullable", `{"type":"string","nullable":true}`, nil, 2},
		{"exclusiveMinimum", `{"type":"number","minimum":1,"exclusiveMinimum":true}`, 2, 1},
		{"booleanSchema", `false`, nil, "anything"},
		{"additionalProperties", `{"type":"object","additionalProperties":false}`, map[string]any{}, map[string]any{"x": 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := fmt.Sprintf(`{"openapi":"3.1.0","info":{"title":"Test","version":"1"},"paths":{"/x":{"post":{"requestBody":{"required":true,"content":{"application/json":{"schema":%s}}},"responses":{"200":{"description":"ok"}}}}},"components":{"schemas":{"Input":{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}}}}`, test.schema)
			d, err := ParseDocument([]byte(spec), "test.json", "")
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := compileSchema(d.Operations[0].InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			if test.name != "booleanSchema" {
				if err := resolved.Validate(map[string]any{"body": test.good}); err != nil {
					t.Fatal("valid value rejected", err)
				}
			}
			if err := resolved.Validate(map[string]any{"body": test.bad}); err == nil {
				t.Fatal("invalid value accepted")
			}
		})
	}
}

func TestSwaggerSharedParametersAndBodies(t *testing.T) {
	spec := `swagger: '2.0'
info: {title: Legacy, version: '1'}
schemes: [https]
host: example.com
basePath: /v1
paths:
  /items/{id}:
    parameters:
      - {in: path, name: id, type: string, required: true}
      - {in: query, name: q, type: string}
    post:
      operationId: update
      parameters:
        - {in: query, name: q, type: integer}
        - in: body
          name: input
          required: true
          schema:
            type: object
            required: [name]
            properties: {name: {type: string}}
      responses: {'200': {description: ok}}
`
	d, err := ParseDocument([]byte(spec), "legacy.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	op := d.Operations[0]
	if d.BaseURL != "https://example.com/v1" || len(op.Parameters) != 2 || op.Parameters[1].Type != "integer" || !op.BodyRequired {
		t.Fatal("Swagger mapping failed")
	}
	resolved, _ := compileSchema(op.InputSchema)
	if err := resolved.Validate(map[string]any{"path": map[string]any{"id": "one"}, "query": map[string]any{"q": 3}, "body": map[string]any{"name": "ok"}}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidDocumentsFailClearly(t *testing.T) {
	for _, content := range []string{
		`{"openapi":"3.0.3","paths":{"/x":{"get":{"parameters":[{"$ref":"https://example.com/param"}]}}}}`,
		`{"openapi":"3.0.3","paths":{"/x":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}},"components":{"schemas":{"A":{"$ref":"#/components/schemas/A"}}}}`,
		`{"openapi":"3.0.3","paths":{"/x":{"post":{"requestBody":{"content":{"multipart/form-data":{"schema":{"type":"object"}}}}}}}}`,
		`{"openapi":"3.0.3","paths":{}}`,
		`{"hello":"world"}`,
		"openapi: 3.0.3\n---\nopenapi: 3.0.3",
		strings.Repeat("x", maxDocumentBytes+1),
	} {
		if _, err := ParseDocument([]byte(content), "bad.yaml", ""); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}

func TestSchemaExpansionBudget(t *testing.T) {
	schemas := map[string]any{"S0": map[string]any{"type": "string"}}
	for i := 1; i < 20; i++ {
		schemas[fmt.Sprint("S", i)] = map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"$ref": fmt.Sprint("#/components/schemas/S", i-1)}, "b": map[string]any{"$ref": fmt.Sprint("#/components/schemas/S", i-1)}}}
	}
	root := map[string]any{"components": map[string]any{"schemas": schemas}}
	if _, err := normalizeSchema(map[string]any{"$ref": "#/components/schemas/S19"}, root, nil, 0); err == nil {
		t.Fatal("unbounded reference expansion")
	}
}

func TestRelativeServerURLs(t *testing.T) {
	spec := strings.Replace(basicSpec("https://example.com/v1"), "https://example.com/v1", "/v2", 1)
	upload, err := ParseDocument([]byte(spec), "file.json", "")
	if err != nil || upload.BaseURL != "" {
		t.Fatal("uploaded relative URL must be configurable", err)
	}
	remote, err := ParseDocument([]byte(spec), "file.json", "https://example.com/docs/openapi.json")
	if err != nil || remote.BaseURL != "https://example.com/v2" {
		t.Fatal("remote relative URL resolution failed", err)
	}
}
