# 更新记录

## Unreleased

- 首次公开发布项目源码、MIT 许可证和自托管文档。
- API 文档改为全宽列表与独立详情页，新增凭证状态筛选、HTTP 方法分组和引用服务入口。
- 文档详情可直接创建 MCP 服务，编辑器默认筛选当前文档。
- 优化移动端列表、详情表格、空状态、菜单与删除后的返回行为。
- 增加项目封面、架构图、流水线图和隔离 Mock 环境截图。
- 增加提交 / PR 验证与 Release 多架构 GHCR 镜像发布。
- Dockerfile 支持原生 Go 交叉编译，Compose 支持使用预构建镜像。
- Smoke 验证支持 `BASE_PATH` 和选择访问入口。

## 已有功能

- OpenAPI 3.x / Swagger 2.0 导入与更新，多文档 / 多服务管理。
- 官方 MCP Go SDK Streamable HTTP、工具 Schema 与真实 API 转发。
- Token 登录、独立 MCP Token、AES-256-GCM 加密 SQLite。
- Header / Query / Body / Cookie 凭证、直接 API 测试与调用日志。
- 客户端 JSON 导出、Header 透传与 `base_url` 覆盖。
- Docker 本地开发、局域网与 Nginx 子路径部署。
