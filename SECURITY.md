# 安全说明

## 部署模型

API2MCP 是供可信使用者自托管的单管理员 API 代理。后台使用管理员 Token 登录，MCP 服务使用独立调用 Token；加密持久化不替代对主机、备份和登录凭证的保护。

当前上游访问策略按产品设计允许任意网络可达的 HTTP / HTTPS 地址，包括局域网、回环地址和云内网；没有目标白名单。MCP 调用者还能通过 `base_url` 覆盖上游地址。这意味着持有调用 Token 的客户端可以使用服务进程可达的网络能力，应按代理访问权限分发 Token。业务隔离需求可在 Docker 网络、主机防火墙和反向代理层实现。

## 凭证

- `.env`、数据库、备份和本机记录不应提交到仓库。
- `ENCRYPTION_KEY` 与数据库必须配套备份，变更密钥不会自动迁移旧数据。
- MCP JSON 会包含真实调用 Token，以及管理员选择导出的上游 Header 凭证。
- MCP URL 的查询串包含 Token。反向代理和网关日志应去掉查询参数或关闭该路径的访问日志。
- 公网入口使用 HTTPS；正确设置 Host 与 `X-Forwarded-Proto`，不接受客户端伪造的代理头。
- HTTP 局域网入口支持登录与复制地址，适用于可信网络。

后台会话使用 HttpOnly / SameSite Cookie，默认 8 小时，进程重启失效。服务可单独停用、删除或重置 Token。日志仅保留操作元信息，API 调试响应保留在当前弹窗。

## 报告问题

不要在公开 Issue 中发布真实凭证、可利用的业务地址或含敏感数据的完整请求。可通过仓库的 [Security → Advisories](https://github.com/xyzphp/api2mcp/security/advisories) 使用私密漏洞报告（仓库启用该功能后可用），或先创建不含利用细节的 Issue 联系维护者。
