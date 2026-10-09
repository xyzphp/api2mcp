package platform

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

func (a *App) importDocument(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Content  string `json:"content"`
		Filename string `json:"filename"`
		URL      string `json:"url"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	source := "本地上传 / 粘贴"
	content := []byte(input.Content)
	filename := path.Base(input.Filename)
	if input.URL != "" {
		u, err := validURL(strings.TrimSpace(input.URL), false)
		if err != nil {
			return bad(err.Error())
		}
		req, err := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
		if err != nil {
			return bad(err.Error())
		}
		response, err := a.upstream.Do(req)
		if err != nil {
			return bad("文档下载失败：" + upstreamRequestError(err).Error())
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return bad(fmt.Sprintf("文档服务返回 HTTP %d", response.StatusCode))
		}
		content, err = io.ReadAll(io.LimitReader(response.Body, maxDocumentBytes+1))
		if err != nil {
			return bad("文档下载中断")
		}
		filename, source = path.Base(u.Path), u.String()
	}
	if filename == "." || filename == "/" || filename == "" {
		filename = "openapi.yaml"
	}
	doc, err := ParseDocument(content, filename, source)
	if err != nil {
		return bad(err.Error())
	}
	if err := a.store.Update(func(s *Workspace) error {
		if id := r.PathValue("id"); id != "" {
			for i, previous := range s.Documents {
				if previous.ID != id {
					continue
				}
				doc.ID, doc.BaseURL, doc.Credential = previous.ID, previous.BaseURL, previous.Credential
				available := map[string]bool{}
				for j := range doc.Operations {
					op := &doc.Operations[j]
					op.ID = doc.ID + ":" + strings.ToLower(op.Method) + ":" + op.Path
					op.DocumentID = doc.ID
					op.ToolName = toolName(op.OperationID, op.ID)
					available[op.ID] = true
				}
				for j := range s.Servers {
					server := &s.Servers[j]
					ids := append([]string{}, server.OperationIDs...)
					if server.Pending != nil {
						ids = append(ids, server.Pending.OperationIDs...)
					}
					for _, old := range previous.Operations {
						if slices.Contains(ids, old.ID) && !available[old.ID] {
							return httpErr(409, "新文档移除了服务或草稿仍在使用的 API，请先取消勾选并发布对应服务")
						}
					}
					if server.Status != "draft" {
						for _, old := range previous.Operations {
							if slices.Contains(server.OperationIDs, old.ID) {
								server.Version++
								server.UpdatedAt = now()
								break
							}
						}
					}
				}
				s.Documents[i] = doc
				return nil
			}
			return httpErr(404, "文档不存在")
		}
		if len(s.Documents) >= 100 {
			return bad("第一版最多支持 100 份文档")
		}
		s.Documents = append(s.Documents, doc)
		return nil
	}); err != nil {
		return err
	}
	doc.Credential = publicCredential(doc.Credential)
	status := 201
	if r.PathValue("id") != "" {
		status = 200
	}
	return sendJSON(w, status, doc)
}

func (a *App) saveCredential(w http.ResponseWriter, r *http.Request) error {
	var input credentialInput
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	u, err := validURL(strings.TrimSpace(input.BaseURL), true)
	if err != nil {
		return bad(err.Error())
	}
	var result Document
	err = a.store.Update(func(s *Workspace) error {
		for i := range s.Documents {
			doc := &s.Documents[i]
			if doc.ID != r.PathValue("id") {
				continue
			}
			credential, err := mergeCredential(doc.Credential, input)
			if err != nil {
				return err
			}
			doc.BaseURL = strings.TrimRight(u.String(), "/")
			doc.Credential = credential
			result = *doc
			result.Credential = publicCredential(result.Credential)
			return nil
		}
		return httpErr(404, "文档不存在")
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, result)
}

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$`)

func validateDraft(d *Draft, existing *Server, s *Workspace, publish bool) error {
	d.Name, d.Slug, d.Description = strings.TrimSpace(d.Name), strings.ToLower(strings.TrimSpace(d.Slug)), strings.TrimSpace(d.Description)
	if utf8.RuneCountInString(d.Name) < 1 || utf8.RuneCountInString(d.Name) > 40 {
		return bad("服务名称应为 1–40 个字符")
	}
	if !slugPattern.MatchString(d.Slug) {
		return bad("服务标识应为 1–48 位小写字母、数字或中划线")
	}
	if utf8.RuneCountInString(d.Description) > 200 {
		return bad("服务描述最多 200 个字符")
	}
	if !slices.Contains([]string{"blue", "green", "purple"}, d.Color) {
		return bad("服务颜色无效")
	}
	if existing != nil && existing.Status != "draft" && d.Slug != existing.Slug {
		return bad("已发布服务的标识不可修改")
	}
	for _, server := range s.Servers {
		if server.Slug == d.Slug && (existing == nil || server.ID != existing.ID) {
			return httpErr(409, "服务标识已被占用")
		}
	}
	available := map[string]Document{}
	for _, doc := range s.Documents {
		for _, op := range doc.Operations {
			available[op.ID] = doc
		}
	}
	ids := []string{}
	for _, id := range d.OperationIDs {
		doc, ok := available[id]
		if !ok {
			return bad("勾选的 API 已不存在，请重新选择")
		}
		if publish && doc.BaseURL == "" {
			return bad("请先配置文档「" + doc.Name + "」的 API 基础地址")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	d.OperationIDs = ids
	if publish && len(ids) == 0 {
		return bad("至少选择一个 API 才能发布")
	}
	if len(ids) > 500 {
		return bad("每个服务最多开放 500 个 API")
	}
	return nil
}

func (a *App) saveServer(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Draft Draft  `json:"draft"`
		Mode  string `json:"mode"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	if input.Mode != "draft" && input.Mode != "publish" {
		return bad("保存方式无效")
	}
	var result Server
	err := a.store.Update(func(s *Workspace) error {
		index := -1
		var existing *Server
		if id := r.PathValue("id"); id != "" {
			for i := range s.Servers {
				if s.Servers[i].ID == id {
					index, existing = i, &s.Servers[i]
					break
				}
			}
			if existing == nil {
				return httpErr(404, "服务不存在")
			}
		}
		if existing == nil && len(s.Servers) >= 100 {
			return bad("第一版最多支持 100 个服务")
		}
		if err := validateDraft(&input.Draft, existing, s, input.Mode == "publish"); err != nil {
			return err
		}
		if existing != nil {
			result = *existing
		} else {
			result = Server{ID: newID(), Status: "draft"}
		}
		if input.Mode == "draft" && result.Status != "draft" {
			result.Pending = &input.Draft
		} else {
			result.Draft, result.Pending = input.Draft, nil
			if input.Mode == "publish" {
				result.Status = "running"
				result.Version++
				if result.Token == "" {
					result.Token = newToken()
				}
			}
		}
		result.UpdatedAt = now()
		if index >= 0 {
			s.Servers[index] = result
		} else {
			s.Servers = append(s.Servers, result)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, result)
}

func (a *App) serverState(w http.ResponseWriter, r *http.Request) error {
	var input struct {
		Status string `json:"status"`
	}
	if err := readJSON(w, r, &input); err != nil {
		return err
	}
	if input.Status != "running" && input.Status != "stopped" {
		return bad("服务状态无效")
	}
	err := a.store.Update(func(s *Workspace) error {
		for i := range s.Servers {
			server := &s.Servers[i]
			if server.ID == r.PathValue("id") {
				if server.Status == "draft" {
					return bad("请先发布服务")
				}
				server.Status, server.UpdatedAt = input.Status, now()
				return nil
			}
		}
		return httpErr(404, "服务不存在")
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) rotateToken(w http.ResponseWriter, r *http.Request) error {
	err := a.store.Update(func(s *Workspace) error {
		for i := range s.Servers {
			if s.Servers[i].ID == r.PathValue("id") {
				if s.Servers[i].Status == "draft" {
					return bad("草稿尚无调用 Token")
				}
				s.Servers[i].Token, s.Servers[i].UpdatedAt = newToken(), now()
				return nil
			}
		}
		return httpErr(404, "服务不存在")
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) deleteServer(w http.ResponseWriter, r *http.Request) error {
	err := a.store.Update(func(s *Workspace) error {
		for i, server := range s.Servers {
			if server.ID == r.PathValue("id") {
				s.Servers = append(s.Servers[:i], s.Servers[i+1:]...)
				return nil
			}
		}
		return httpErr(404, "服务不存在")
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) deleteDocument(w http.ResponseWriter, r *http.Request) error {
	err := a.store.Update(func(s *Workspace) error {
		for i, doc := range s.Documents {
			if doc.ID == r.PathValue("id") {
				for _, server := range s.Servers {
					ids := append([]string{}, server.OperationIDs...)
					if server.Pending != nil {
						ids = append(ids, server.Pending.OperationIDs...)
					}
					for _, op := range doc.Operations {
						if slices.Contains(ids, op.ID) {
							return httpErr(409, "文档仍被服务或草稿引用，请先移除对应 API")
						}
					}
				}
				s.Documents = append(s.Documents[:i], s.Documents[i+1:]...)
				return nil
			}
		}
		return httpErr(404, "文档不存在")
	})
	if err != nil {
		return err
	}
	return sendJSON(w, 200, map[string]bool{"ok": true})
}
