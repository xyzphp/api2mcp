# API 凭证、调试与 MCP 客户端

## 从文档到调用

在文档列表进入独立详情页，点击“配置凭证”设置基础地址与认证参数。API 接口支持按请求方式分组和搜索；点击“测试”可直接请求业务 API，无需先发布 MCP。

测试弹窗支持 Path / Query、Headers、Cookies、JSON / 表单 Body，以及响应状态、耗时、大小、Header、原文 / 格式化和脱敏后的实际请求。Ctrl / Command + Enter 发送，取消只中断等待，不撤销已完成的上游操作。

测试与 MCP 调用复用保存的文档凭证；已配置的固定字段由服务端注入，不必重复填写。输入的测试参数和响应仅保留在弹窗内，日志不存参数和响应。

## API 请求与凭证配置

在“API 文档 → 配置凭证”中，预设认证与自定义请求参数可以同时使用。例如：

| 位置 | 名称 / 路径 | 值类型 | 用途 |
| --- | --- | --- | --- |
| Header | `Authorization` | 文本 | 自定义 `Token xxx`、`ApiKey xxx` 等完整认证格式 |
| Header | `X-Tenant-ID` | 文本 | 固定租户信息 |
| Query | `api_key` | 文本 | URL 查询参数中的 API Key |
| Body | `/auth/token` 或 `auth.token` | 文本 | 注入嵌套 JSON 字段 |
| Body | `app_id` | JSON | 数字、布尔值、数组、对象或 `null` |
| Body | `$` | JSON 对象 | 与每次调用的请求体逐层合并 |
| Cookie | `session` | 文本 | 独立 Cookie；整段 Cookie 也可放入 Header |

Header 支持 `Name: Value` 原文或 JSON 对象批量粘贴，Query 支持 URL 编码查询串或 JSON 对象，Cookie 支持原文或 JSON 对象。Body 批量输入使用 JSON 对象，原始 JSON 会保留，避免大整数精度丢失。

Body 默认按 API 文档选择格式，也可手动改为 JSON 或 `application/x-www-form-urlencoded`。JSON 对象逐层合并，数组和普通值整体覆盖；表单中的数组发送为重复字段，对象发送为 JSON 字符串。JSON 嵌套路径使用 `/auth/token` 或点分隔形式；带点号的字面字段名可写为 `/api.key`，路径中的 `/` 与 `~` 分别使用 `~1` 与 `~0`。本版嵌套路径用于对象字段，数组请作为 JSON 值整体设置。

启用的配置覆盖同名调用参数，Body 条目从上到下合并，预设认证优先于同名自定义 Header。固定字段不再要求 MCP 调用者填写，其他业务字段继续校验。改变配置后，客户端需要重新读取工具列表。配置会应用到这份文档的全部接口，Body 配置也会在调用时发送；不同接口使用不同凭证时可拆分文档。

已保存的值留空保留；勾选“空值”才会将其替换为空字符串，JSON 空值直接填写 `null`。取消启用保留密钥，移除条目并保存才会删除。选择“不使用预设认证”仅清除预设认证，自定义参数继续使用。旧版仅提交 `{baseUrl,kind,header,value}` 的管理 API 仍可用，未提交 `entries` 时保留已有自定义参数，提交 `entries: []` 可清空它们。

每份文档最多 100 条请求参数，单个值最多 16 KB，总配置最多 256 KB。Host 与 HTTP 连接 / 传输控制字段由地址和客户端管理，不能通过凭证覆盖。动态登录、自动刷新 Token、multipart 文件上传、XML 或任意脚本签名仍不在本次范围内。

工具名为清理后的 `operationId` 加稳定接口哈希，避免跨文档同名冲突。以后台实际展示 / MCP `tools/list` 返回值为准。

参数按位置分组，避免同名路径、查询和 Header 参数相互覆盖：

```json
{
  "path": {"id": "usr_001"},
  "query": {"page": 1},
  "header": {"X-Trace-ID": "test"},
  "body": {"name": "张三"}
}
```

只填写该接口 Schema 存在的字段。管理后台支持直接试调用；对 POST / PUT / PATCH / DELETE 的执行会实际修改上游业务数据。

在服务列表点击“JSON 配置”，或在服务详情的连接卡片切换到“JSON 配置”，可预览并复制通用客户端配置。配置会自动加入所选 API 文档中一致的基础地址和 Header 凭证；Query、Body 等凭证仍由服务端按文档配置使用。例如：

```json
{
  "mcpServers": {
    "mock-users": {
      "type": "http",
      "url": "http://localhost:8080/mcp/mock-users?token=<此服务的调用 Token>",
      "headers": {
        "base_url": "https://api.example.com/v1",
        "Authorization": "Bearer <API 文档中保存的 Token>"
      }
    }
  }
}
```

客户端需要支持 HTTP MCP。MCP 服务调用 Token 放在 URL 的 `token` 查询参数中；后台管理员 Token 不能代替 MCP 服务调用 Token。第一版未实现 OAuth 授权服务器、动态客户端注册或旧版独立 SSE 端点。

### MCP 客户端 Header 与代理

服务详情的“地址与 Token”分别展示不含 Token 的连接地址和调用 Token；连接卡片可切换到 JSON 视图。JSON 会自动显示所选 API 文档中一致的基础地址和 Header 凭证。详情页的 Header 编辑器可填写额外值或覆盖默认凭证，编辑内容仅保留在当前页面内存；复制出的 MCP JSON 会将这些字段放进标准 `headers` 对象，不会使用 `X-API2MCP-Credentials` 包装。多份文档的同名 Header 值不一致时，不会导出该项，页面会显示冲突名称；后台仍按各自文档的设置调用。

例如，为 API 配置 Bearer、API Key 和上游基础地址：

```json
{
  "mcpServers": {
    "my-service": {
      "type": "http",
      "url": "http://localhost:8080/mcp/my-service?token=<MCP 服务调用 Token>",
      "headers": {
        "base_url": "https://api.example.com/v1",
        "Authorization": "Bearer <上游 API Token>",
        "X-API-Key": "<上游 API Key>"
      }
    }
  }
}
```

`base_url`（也可使用 `X-API2MCP-Base-URL`）是代理路由设置：服务端用它覆盖所选 API 文档的基础地址，并且不会将该字段传给上游。未设置时使用 API 文档导入的基础地址。其他自定义 Header（包括上游 `Authorization`、`Cookie` 和 `X-API-Key`）逐请求透传到上游。Token 查询参数只验证 MCP 服务访问权限，不会作为上游查询参数转发。

上游凭证 Header 优先级高于后台保存的凭证和工具参数；每个 MCP 请求分别应用，不修改服务端保存的数据，也不会写入调用日志。为了兼容此前生成的客户端配置，服务端仍识别旧版 `X-API2MCP-Credentials`，但新配置不会再生成该 Header。使用旧版 Authorization Bearer MCP Token 的客户端也可继续连接。

客户端 Header 总量限制为 16 KB、最多 100 项，并校验非法控制字符和 HTTP 传输控制字段。上游地址不使用白名单限制。
