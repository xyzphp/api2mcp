package platform

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

type Workspace struct {
	SchemaVersion int        `json:"schemaVersion"`
	Documents     []Document `json:"documents"`
	Servers       []Server   `json:"servers"`
	Logs          []CallLog  `json:"logs"`
	Settings      Settings   `json:"settings"`
}

type Settings struct {
	EndpointOrigin string `json:"endpointOrigin"`
}

type Document struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Filename    string      `json:"filename"`
	SpecVersion string      `json:"specVersion"`
	BaseURL     string      `json:"baseUrl"`
	ImportedAt  string      `json:"importedAt"`
	Source      string      `json:"source"`
	Operations  []Operation `json:"operations"`
	Credential  Credential  `json:"credential"`
}

type Credential struct {
	Kind               string            `json:"kind"`
	Header             string            `json:"header,omitempty"`
	Username           string            `json:"username,omitempty"`
	Value              string            `json:"value,omitempty"`
	BodyFormat         string            `json:"bodyFormat,omitempty"`
	Configured         bool              `json:"configured"`
	ValueConfigured    bool              `json:"valueConfigured"`
	EmptyValue         bool              `json:"emptyValue,omitempty"`
	UsernameConfigured bool              `json:"usernameConfigured"`
	Entries            []CredentialEntry `json:"entries,omitempty"`
}

type CredentialEntry struct {
	ID         string `json:"id"`
	Location   string `json:"in"`
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	ValueType  string `json:"valueType,omitempty"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	EmptyValue bool   `json:"emptyValue,omitempty"`
}

type Operation struct {
	ID              string         `json:"id"`
	DocumentID      string         `json:"documentId"`
	OperationID     string         `json:"operationId"`
	ToolName        string         `json:"toolName"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Method          string         `json:"method"`
	Path            string         `json:"path"`
	Tag             string         `json:"tag"`
	Parameters      []Parameter    `json:"parameters"`
	RequestBody     map[string]any `json:"requestBody,omitempty"`
	BodyRequired    bool           `json:"bodyRequired,omitempty"`
	ContentType     string         `json:"contentType,omitempty"`
	ResponseExample any            `json:"responseExample,omitempty"`
	InputSchema     map[string]any `json:"inputSchema"`
}

type Parameter struct {
	Name             string         `json:"name"`
	Location         string         `json:"location"`
	Type             string         `json:"type"`
	Required         bool           `json:"required"`
	Description      string         `json:"description"`
	Schema           map[string]any `json:"schema"`
	Style            string         `json:"style,omitempty"`
	Explode          bool           `json:"explode"`
	CollectionFormat string         `json:"collectionFormat,omitempty"`
}

type Draft struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Description  string   `json:"description"`
	OperationIDs []string `json:"operationIds"`
	Color        string   `json:"color"`
}

type Server struct {
	Draft
	ID        string `json:"id"`
	Status    string `json:"status"`
	Token     string `json:"token"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updatedAt"`
	Pending   *Draft `json:"draft,omitempty"`
}

type CallLog struct {
	ID         string `json:"id"`
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	Action     string `json:"action"`
	Success    bool   `json:"success"`
	Duration   int64  `json:"duration"`
	CreatedAt  string `json:"createdAt"`
	Status     int    `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func newToken() string { return "mcp_" + newID() + newID() }

func emptyWorkspace() Workspace {
	return Workspace{SchemaVersion: 1, Documents: []Document{}, Servers: []Server{}, Logs: []CallLog{}}
}
