# MCP 代理

已有 HTTP MCP 服务时，可直接填写它的 URL，生成 API2MCP 的独立访问地址。无需导入 OpenAPI 文档。代理与 API 转换服务共用列表、详情、草稿、发布、启停、Token、测试和 JSON 配置功能。

## 在后台使用

1. 进入 **MCP Server → 添加 MCP 代理**。
2. 填写名称、唯一服务标识与完整的 **Streamable HTTP** 上游地址，例如 `https://mcp.example.com/mcp`。路径、末尾斜线和查询参数都会保留。
3. 如需认证，在“上游认证 Header”填写 JSON 对象：

   ```json
   {
     "Authorization": "Bearer <UPSTREAM_TOKEN>",
     "X-Tenant-ID": "demo"
   }
   ```

4. 点击 **发布并生成地址**，在详情中 **测试连接**，可读取工具列表并试调用工具。
5. 在 **JSON 配置** 中复制客户端配置。地址包含本平台的调用 Token；上游地址与认证 Header 自动加入 `headers`，可自行修改。

后台的连接测试使用保存的已发布配置。自定义客户端 Header 仅作用于该客户端请求，不会修改后台配置。保存草稿不会改变正在使用的地址或凭证；发布更新后新配置生效。已发布服务的类型和标识固定，可复制成新服务再调整。

## 客户端配置

```json
{
  "mcpServers": {
    "remote-proxy": {
      "type": "http",
      "url": "http://localhost:8080/mcp/remote-proxy?token=<GATEWAY_TOKEN>",
      "headers": {
        "base_url": "https://mcp.example.com/mcp?tenant=demo",
        "Authorization": "Bearer <UPSTREAM_TOKEN>",
        "X-Tenant-ID": "demo"
      }
    }
  }
}
```

只填写 `url` 也能使用后台保存的目标与认证。`base_url` 或 `X-API2MCP-Base-URL` 可覆盖完整的上游 MCP 地址，允许携带上游查询参数；两者只能提供一个。普通客户端 Header 按名称忽略大小写覆盖后台同名 Header。

调用优先级：**客户端 Header → 已发布的后台代理配置**。平台的 URL `token`、兼容 Bearer 调用 Token 和路由 Header 在本平台消费；它们不会被误当作上游凭证。上游 URL 自己的 `token` 查询参数仍会保留。

代理允许任意网络可达的 HTTP / HTTPS 地址，包括局域网和回环地址，不配置白名单。Docker 中的 `localhost` 指容器自身；宿主机上的服务可使用 `host.docker.internal` 或宿主机可达 IP。

## 透传范围

- GET、POST、DELETE；初始化结果、工具、资源、提示词等 JSON-RPC 消息原样转发。
- JSON 和 SSE 响应、上游状态码、`Mcp-Session-Id`、`Mcp-Protocol-Version`、`Last-Event-ID` 与通知流。
- 每次请求都验证本平台 Token 和服务启用状态；客户端断开后取消该上游 HTTP 请求。
- 上游连接 / 响应头受 `UPSTREAM_TIMEOUT_SECONDS` 限制；已经建立的 SSE 流不受普通 API 请求总超时和应用普通响应写入超时限制。
- 代理日志记录 HTTP 方法、上游状态和响应头耗时；不解析工具执行结果来判断业务成功，不保存请求 Body 或凭证。

协议依据：[MCP Streamable HTTP 传输规范](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)。旧版 2024-11-05 独立 SSE / messages 双端点不在当前范围，需使用上游的 Streamable HTTP 端点。代理保留上游能力，不做工具筛选或协议格式转换。

## 认证与会话说明

当前认证方式是手工提供上游 URL 查询参数或 Header，不代办 OAuth 授权与 Token 刷新。浏览器 Origin 在本平台校验后不会转发；平台后台登录 Cookie、网络转发元数据与逐跳 Header 不透传。上游 `Set-Cookie` 不回传，避免影响后台登录；MCP 会话使用协议规定的 `Mcp-Session-Id`。

普通工作空间响应隐藏代理 Header 值，只返回 Header 名称；编辑和 JSON 导出由已登录管理员通过专门接口读取。完整上游 URL 可能含查询凭证，管理员应按凭证对待。代理配置与调用 Token 一同加密保存，重启无需重新填写。

循环代理在最多 8 层转发后返回 508。上游连不上时返回 502，并提供经过脱敏的网络错误说明；上游返回 401 / 403 等状态则原样返回。

## Nginx

已有子路径配置继续有效。流式调用建议在对应 `location` 添加：

```nginx
proxy_buffering off;
proxy_read_timeout 3600s;
proxy_send_timeout 3600s;
```

应用也会发送 `X-Accel-Buffering: no`。外部 Nginx、网关和客户端自身的超时仍需按流式调用需求设置，完整部署示例见[部署文档](deployment.md)。
