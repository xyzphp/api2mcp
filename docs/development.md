# 本地开发与验证

## 依赖

- Go 1.25+：后端、初始化、Mock、开发监听和 smoke 客户端。
- Docker / Compose v2：构建与部署。
- Node.js 22+：前端契约测试；生产镜像无需 Node。
- macOS / Linux：SQLite 进程锁使用 Unix 文件锁。

## 开发方式

```sh
make init
make dev
```

Go 监听器监测 Go、HTML、CSS、JavaScript、示例、依赖与 `.env`，修改后自动重建 Docker 服务。Ctrl+C 退出监听但保留容器；`make down` 停止。重建后管理员需重新登录。

不使用 Docker 时，分两个终端运行 `make mock` 和 `make run`。它们读取 `.env`；本地数据库默认 `data/api2mcp.db`，与 Docker 卷分开。改变端口时同步设置 `HTTP_ADDR`、`PUBLIC_ORIGIN`、`MOCK_ADDR`、`MOCK_API_URL`。

前端通过 `go:embed` 打包，修改文件后需要重启 Go 进程。直接双击 `index.html` 无法调用管理 API。

## 代码结构

```text
cmd/api2mcp/         HTTP 服务入口
cmd/mockapi/         独立 Mock 业务 API
cmd/setup/           随机生成本机配置
cmd/dev/             源码监听与 Docker 重建
cmd/smoke/           官方 SDK 端到端调用
internal/platform/  认证、管理、解析、MCP、代理、加密存储
internal/envfile/   开发配置加载
assets/             原生前端与样式
index.html          管理页面
login.html          登录页面
examples/           OpenAPI 示例
scripts/            Compose 兼容入口、CI 容器验证
.github/workflows/  提交验证与 Release 镜像发布
docs/               项目文档与说明图
```

## 必要检查

```sh
make test
go vet ./...
node --check assets/app.js
node --check assets/core.js
node --check assets/login.js
make build
```

Go 集成测试覆盖认证 / CSRF、加密存储、MCP 协议、Token 隔离、草稿、更新 / 删除保护、参数序列化、凭证覆盖、上游错误与 API 调试。前端测试覆盖 MCP JSON、参数类型、原始 JSON、大整数与原型安全。

```sh
make smoke
```

Smoke 需要已运行的应用、有效管理员 Token、至少一个没有必填参数的只读工具（默认 Mock 满足）。支持 `BASE_PATH`；可用 `SMOKE_ORIGIN=http://localhost:8080 make smoke` 临时选择访问入口，不能在该值中加路径。会执行真实只读 API 调用并留下调用记录，不打印 Token。不要将它当作任意业务接口的压力测试。

## UI 审查

使用隔离数据库与 Mock 示例，不在公开截图中展示业务凭证或真实 Token。

| 场景 | 检查内容 |
| --- | --- |
| 文档列表 | 搜索、凭证筛选、无匹配、无数据、更多菜单 |
| 文档详情 | 直接 URL、刷新、返回、方法分组、组合搜索、接口调试、引用服务 |
| 服务编辑 | 按方法 / 文档筛选，分组勾选，隐藏项选择保留 |
| 服务详情 | 地址与 Token 分开、JSON 导出、凭证冲突 / 覆盖 |
| 窄屏 | 390 px、760 px、960 px；无横向页面溢出，表格和筛选可操作 |
| 键盘 | 焦点可见、弹窗 Tab 限制、Escape 关闭、Ctrl / Command + Enter 发送 |
| 部署 | 根路径、`BASE_PATH`、HTTP 局域网与 HTTPS 反代 |

文档路由为 `#/documents/{id}`，服务为 `#/servers/{id}`。浏览器返回与刷新均可恢复详情；搜索与方法筛选保留在当前页面内存。

## 调试

后台弹窗显示错误原因和上游响应；日志页只存元信息，不持久化请求参数、Body 或响应。需要抓取敏感请求时只在本机操作，完成后删除临时记录。

无法访问上游时检查容器网络、基础地址与 TLS 证书；本项目不使用目标白名单。无法登录时检查 Token、Host / Origin、反代协议头；子路径 404 时检查 `BASE_PATH` 与 HTML 的 base 地址。更多见[部署说明](deployment.md)。
