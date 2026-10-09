package platform

import (
	"encoding/base64"
	"net/http"
	"slices"
	"sort"
	"strings"
)

type clientHeaderValue struct {
	Name  string
	Value string
}

type clientHeaderConfig struct {
	Headers   map[string]string `json:"headers"`
	Conflicts []string          `json:"conflicts"`
}

// serverClientHeaders exposes only the upstream Header values needed by this
// MCP service's selected API documents. The general workspace response remains
// redacted; this endpoint is protected by the admin session like other APIs.
func (a *App) serverClientHeaders(w http.ResponseWriter, r *http.Request) error {
	workspace := a.store.Snapshot()
	var server *Server
	for i := range workspace.Servers {
		if workspace.Servers[i].ID == r.PathValue("id") {
			server = &workspace.Servers[i]
			break
		}
	}
	if server == nil {
		return httpErr(404, "服务不存在")
	}
	if server.Type == "proxy" {
		result := clientHeaderConfig{Headers: map[string]string{}, Conflicts: []string{}}
		if server.Proxy != nil {
			result.Headers[proxyBaseURLHeader] = server.Proxy.URL
			for name, value := range server.Proxy.Headers {
				result.Headers[name] = value
			}
		}
		return sendJSON(w, 200, result)
	}

	selected := map[string]bool{}
	for _, id := range server.OperationIDs {
		selected[id] = true
	}
	documents := []Document{}
	for _, doc := range workspace.Documents {
		used := false
		for _, operation := range doc.Operations {
			if selected[operation.ID] {
				used = true
				break
			}
		}
		if used {
			documents = append(documents, doc)
		}
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
	if len(documents) == 0 {
		return sendJSON(w, 200, clientHeaderConfig{Headers: map[string]string{}, Conflicts: []string{}})
	}

	perDocument := make([]map[string]clientHeaderValue, 0, len(documents))
	for _, doc := range documents {
		perDocument = append(perDocument, documentClientHeaders(doc))
	}
	allNames := map[string]clientHeaderValue{}
	for _, headers := range perDocument {
		for key, header := range headers {
			allNames[key] = header
		}
	}

	result := clientHeaderConfig{Headers: map[string]string{}, Conflicts: []string{}}
	keys := make([]string, 0, len(allNames))
	for key := range allNames {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		first, existsEverywhere := perDocument[0][key]
		if existsEverywhere {
			for _, headers := range perDocument[1:] {
				header, exists := headers[key]
				if !exists || header.Value != first.Value {
					existsEverywhere = false
					break
				}
			}
		}
		if existsEverywhere {
			result.Headers[first.Name] = first.Value
		} else {
			result.Conflicts = append(result.Conflicts, allNames[key].Name)
		}
	}
	return sendJSON(w, 200, result)
}

func documentClientHeaders(doc Document) map[string]clientHeaderValue {
	headers := map[string]clientHeaderValue{}
	set := func(name, value string) {
		name = strings.TrimSpace(name)
		if name == "" || !clientConfigHeaderAllowed(name) {
			return
		}
		headers[strings.ToLower(name)] = clientHeaderValue{Name: name, Value: value}
	}
	setCookie := func(value string) {
		if value == "" {
			return
		}
		headers["cookie"] = clientHeaderValue{Name: "Cookie", Value: value}
	}
	if doc.BaseURL != "" {
		set("base_url", doc.BaseURL)
	}

	cookieRequest := &http.Request{Header: make(http.Header)}
	var rawCookie string
	for _, entry := range doc.Credential.Entries {
		if !entry.Enabled || entry.Location != "header" || !strings.EqualFold(entry.Name, "Cookie") {
			continue
		}
		setCredentialCookie(cookieRequest, entry.Value)
		if len(cookieRequest.Cookies()) == 0 {
			rawCookie = entry.Value
		}
		break
	}

	// Header credentials are applied before presets, and configured Cookie
	// entries are applied last in buildUpstreamRequest.
	for _, entry := range doc.Credential.Entries {
		if !entry.Enabled {
			continue
		}
		switch entry.Location {
		case "header":
			if strings.EqualFold(entry.Name, "Cookie") {
				continue
			}
			if strings.EqualFold(entry.Name, proxyBaseURLHeader) || strings.EqualFold(entry.Name, proxyBaseURLHeaderAlias) {
				continue
			}
			set(entry.Name, entry.Value)
		}
	}

	switch doc.Credential.Kind {
	case "basic":
		set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(doc.Credential.Username+":"+doc.Credential.Value)))
	case "bearer":
		set("Authorization", "Bearer "+doc.Credential.Value)
	case "apiKey":
		if strings.EqualFold(doc.Credential.Header, "Cookie") {
			setCredentialCookie(cookieRequest, doc.Credential.Value)
			if len(cookieRequest.Cookies()) == 0 {
				rawCookie = doc.Credential.Value
			}
		} else {
			set(doc.Credential.Header, doc.Credential.Value)
		}
	}

	cookieEntries := []*http.Cookie{}
	for _, entry := range doc.Credential.Entries {
		if entry.Enabled && entry.Location == "cookie" {
			cookieEntries = append(cookieEntries, &http.Cookie{Name: entry.Name, Value: entry.Value})
		}
	}
	if len(cookieEntries) > 0 {
		mergeRequestCookies(cookieRequest, cookieEntries)
		rawCookie = ""
	}
	if cookies := cookieRequest.Cookies(); len(cookies) > 0 {
		parts := make([]string, 0, len(cookies))
		for _, cookie := range cookies {
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
		setCookie(strings.Join(parts, "; "))
	} else if rawCookie != "" {
		setCookie(rawCookie)
	}
	return headers
}

func setCredentialCookie(req *http.Request, value string) {
	source := &http.Request{Header: http.Header{"Cookie": []string{value}}}
	if cookies := source.Cookies(); len(cookies) > 0 {
		mergeRequestCookies(req, cookies)
		return
	}
	req.Header.Set("Cookie", value)
}

func clientConfigHeaderAllowed(name string) bool {
	if !headerName.MatchString(name) {
		return false
	}
	return !slices.Contains([]string{
		"host", "connection", "content-length", "content-type", "transfer-encoding", "trailer", "te", "upgrade",
		"proxy-authorization", "proxy-authenticate", "x-api2mcp-credentials",
	}, strings.ToLower(name))
}
