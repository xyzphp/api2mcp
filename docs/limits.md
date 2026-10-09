# 能力边界与约束

## 支持

- OpenAPI 3.0 / 3.1、Swagger 2.0 的 JSON / YAML，文件、粘贴和 URL 导入。
- 单文档全局 API 基础地址，可在后台或 MCP `base_url` 中覆盖。
- 路径 / 查询 / Header / Cookie 参数，JSON 和 URL 编码表单 Body。
- query 的 form / deepObject / spaceDelimited / pipeDelimited；path / header 的 simple，简单 Cookie 参数。
- 同文档内部 `$ref`（受解析器校验约束）、工具 JSON Schema 校验。
- 无状态 Streamable HTTP，使用官方 MCP Go SDK；不提供 stdio 命令行服务。

## 当前未提供

- 普通 Markdown、PDF / Word 文档解析、定时抓取与自动同步。
- 外部 `$ref` 自动下载、循环请求 Schema、路径 / 接口级 servers。
- OpenAPI 参数 content 与全部高级序列化规则。
- multipart 文件上传、二进制响应透传、XML、任意脚本签名。
- 凭证自动登录 / 刷新 Token、OAuth 授权服务器、动态客户端注册、旧版独立 SSE 端点。
- 多用户、RBAC、多工作空间、多实例共享 SQLite、高可用集群。

更新文档需手动点击“更新文档”；客户端需重新读取工具列表或重新连接。多基础地址可拆分文档，再在 MCP 服务内组合。文档凭证作用于全部接口，需要不同凭证时也可拆分文档。

## 容量与时限

| 项目 | 当前限制 |
| --- | --- |
| 导入文档原文 | 2 MB |
| 单文档接口 / 单服务工具 | 500 |
| 文档 / 服务数量 | 各 100 |
| 单文档凭证条目 | 100 |
| 单凭证值 / 凭证总量 | 16 KB / 256 KB |
| 客户端 Header | 总量 16 KB，最多 100 项 |
| 上游响应 | 4 MB |
| 上游超时 | 默认 20 秒，可设 1–120 秒 |
| 管理会话 | 默认 8 小时，重启失效 |
| 调用日志 | 最近 1000 条 |

上游允许任意可达 HTTP / HTTPS 地址，不配置白名单；重定向按上游状态原样返回，不自动跟随。非 2xx 返回完整工具错误 / 测试响应。TLS 证书正常校验，不支持在页面关闭证书验证。
