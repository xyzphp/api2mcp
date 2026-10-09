# 管理 HTTP API

除登录、健康检查外，`/api/*` 需要管理员登录会话；修改请求还需 `X-Requested-With: API2MCP`。所有 JSON 请求使用 `Content-Type: application/json`。

| 方法 | 路由 | 用途 |
| --- | --- | --- |
| POST | `/api/login` | `{ "token": "..." }` 登录 |
| POST | `/api/logout` | 退出 |
| GET | `/api/workspace` | 已脱敏的管理数据（含管理员可复制的服务调用 Token） |
| GET | `/api/servers/{id}/client-headers` | 管理员会话下读取该服务所选 API 文档的一致 Header 凭证，用于生成客户端 JSON |
| POST / PUT | `/api/documents` / `/api/documents/{id}` | `{content,filename}` 或 `{url}` 导入 / 更新 |
| PUT | `/api/documents/{id}/credentials` | `{baseUrl,kind,header,username,value,replaceValue,bodyFormat,entries}` 配置上游；`entries` 每项含 `{id,in,name,value,valueType,enabled,replaceValue}` |
| GET | `/api/documents/{id}/test?operationId=...` | 获取移除固定凭证字段后的测试 Schema、Body 格式与超时 |
| POST | `/api/documents/{id}/test` | `{operationId,arguments,body?,bodyFormat}` 直接测试 API；`arguments` 按 path / query / header / cookie 分组，`body` 为原始 JSON 字符串，`bodyFormat` 为 configured / json / form |
| DELETE | `/api/documents/{id}` | 删除未被服务或草稿引用的文档 |
| POST / PUT | `/api/servers` / `/api/servers/{id}` | `{mode: "draft"或"publish", draft: {...}}` |
| POST | `/api/servers/{id}/state` | `{status: "running"或"stopped"}` |
| POST | `/api/servers/{id}/token` | 重置调用 Token |
| DELETE | `/api/servers/{id}` | 删除服务 |
| POST | `/api/servers/{id}/test` | 真实连接与工具列表检查 |
| POST | `/api/servers/{id}/call` | `{name,arguments}` 经 MCP 协议试调用 |
| GET / DELETE | `/api/logs` | 查询 / 清空调用记录 |
| POST | `/mcp/{slug}` | MCP Streamable HTTP，需 URL 的 token 或兼容 Bearer Token |
| GET | `/healthz` | 无敏感信息的健康状态 |


配置 `BASE_PATH` 时为所有路由加上此前缀，例如 `/http_mcp/api/login`。普通管理 API 使用 JSON 错误体 `{ "error": "说明" }`；MCP 方法遵循协议响应。

## 调用管理 API

使用 HTTP 客户端的 Cookie jar 保存登录响应的 Cookie，再进行管理操作。浏览器请求的 Origin 必须匹配当前访问源或配置的 `PUBLIC_ORIGIN`；修改请求发送 `X-Requested-With: API2MCP`。

文档接口内部 ID 取自 `/api/workspace` 的 `documents[].operations[].id`，服务 ID 取自 `servers[].id`；MCP 路径使用服务 `slug`，不可混用。

创建服务请求示例：

```json
{
  "mode": "publish",
  "draft": {
    "name": "客户服务",
    "slug": "customer-service",
    "description": "查询客户资料",
    "color": "blue",
    "operationIds": ["<文档接口内部 ID>"]
  }
}
```

文档请求示例：

```json
{
  "content": "<OpenAPI JSON 或 YAML 原文>",
  "filename": "openapi.yaml"
}
```

不能用后台会话 Cookie 代替 MCP Token，也不能用后台管理员 Token 直接调用某个服务。上游凭证格式与优先级见[凭证说明](credentials.md)。
