# 配置说明

先执行 `make init` 生成 `.env`。该命令创建随机管理员 Token 与 32 字节 Base64 加密密钥，权限为 0600，不覆盖已有配置。`.env.example` 仅包含占位符，不能直接作为有效密钥使用。

Go 命令会加载本机 `.env`，已有进程环境变量优先。Compose 读取 `.env` 并注入明确声明的运行参数；构建代理不注入业务请求环境。

| 变量 | 用途 |
| --- | --- |
| `ADMIN_TOKEN` | 后台登录 Token，至少 24 字符。修改后重启服务，旧会话失效 |
| `ENCRYPTION_KEY` | 32 字节随机值的 Base64 编码，首次生成后固定保存 |
| `PUBLIC_ORIGIN` | 对外访问源，例如 `http://localhost:8080` 或 HTTPS 域名，不能带路径 |
| `BASE_PATH` | 可选的反代路径前缀，例如 `/http_mcp`；留空表示部署在域名根路径 |
| `APP_PORT` / `MOCK_PORT` | Docker 对外端口，容器内固定为 8080 / 9090 |
| `BIND_ADDRESS` | 后台绑定的宿主机 IP；`127.0.0.1` 仅本机可访问，设为局域网网卡 IP 后可由局域网访问 |
| `HTTP_ADDR` / `MOCK_ADDR` | 直接运行 Go 时监听的地址 |
| `DATA_PATH` | 直接运行的 SQLite 路径；容器内固定 `/data/api2mcp.db` |
| `COOKIE_SECURE` | HTTPS 部署时设为 `true`；HTTPS 的 PUBLIC_ORIGIN 会自动开启 |
| `SEED_MOCK` | 仅对空数据库初始化 Mock 示例，后续不会覆盖用户配置 |
| `MOCK_API_URL` | 直接运行时的 Mock 基础地址；Compose 使用容器内服务名覆盖 |
| `UPSTREAM_TIMEOUT_SECONDS` | 上游请求超时，1–120 秒，默认 20 秒 |
| `DOCKER_HTTP_PROXY` / `DOCKER_HTTPS_PROXY` / `DOCKER_ALL_PROXY` | 可选构建代理，不会写入应用运行环境 |
| `GOPROXY` | Go 模块源，默认模板为 `https://goproxy.cn,direct` |

| `API2MCP_IMAGE` / `API2MCP_MOCK_IMAGE` | 可选预构建镜像名，默认 `api2mcp:local` / `api2mcp-mock:local` |
| `SMOKE_ORIGIN` | 可选，仅供 `make smoke` 选择访问源，优先于 PUBLIC_ORIGIN |

## Token 与密钥

`ADMIN_TOKEN` 仅用于后台登录，不可代替每个 MCP 服务的 Token。MCP Token 在发布服务时生成，可在服务详情重置。修改管理员 Token 或重启应用后，管理员需重新登录。

`ENCRYPTION_KEY` 首次生成后应固定保留。改动此值不会轮换旧数据库密钥，而会导致旧数据库无法解密。备份需要原密钥和数据库，二者都应保密。

## 地址和端口

`PUBLIC_ORIGIN` 仅包含协议、主机和端口，不含路径。子路径部署使用 `BASE_PATH=/http_mcp`，不得以 `/` 结尾；根路径留空。

MCP 地址根据当前请求的 Host、协议和 `BASE_PATH` 生成。局域网 IP 登录生成局域网地址，HTTPS 域名登录生成域名地址。反向代理应传递正确 Host / X-Forwarded-Proto。

Compose 容器内固定监听 8080 / 9090，宿主机端口由 `APP_PORT` / `MOCK_PORT` 控制。修改 `APP_PORT` 时同步修改 `PUBLIC_ORIGIN`；直接运行 Go 时还需更新 `HTTP_ADDR`。修改 Mock 端口时，直接运行的 `MOCK_ADDR` / `MOCK_API_URL` 也需保持一致。

`BIND_ADDRESS=127.0.0.1` 只允许本机访问；局域网使用实际网卡 IP 或 `0.0.0.0`。部署实例见[部署文档](deployment.md)。

## Mock 与数据

`SEED_MOCK=true` 只在空数据库初始化示例，不会覆盖已有文档或服务。Mock API 数据保存在内存中，重启后恢复；管理配置保存在 SQLite 卷中，重启后保留。

Compose 为应用强制设置 `MOCK_API_URL=http://mock-api:9090/v1`；直接运行 Go 时使用 `.env` 的宿主机地址。两种运行方式使用不同数据库。
