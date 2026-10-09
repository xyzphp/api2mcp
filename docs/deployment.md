# Docker 与 Nginx 部署

## 从源码运行

```sh
make init
make up
```

默认后台 `http://localhost:8080`，Mock 端口 9090。`make init` 生成 `.env`，登录使用其中的 `ADMIN_TOKEN`。完整变量见[配置说明](configuration.md)。

仅有 Docker、没有本机 Go 时，可以在官方 Go 容器中生成配置：

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.25-alpine go run ./cmd/setup
./scripts/compose.sh up --build -d
```

## 使用 Release 镜像

首次发布 Release 后，仓库的 GitHub Actions 会提供两个镜像。先克隆仓库、生成 `.env`，然后在 `.env` 中指定同一版本（以下 `v0.2.0` 是示例，请选择已经发布的标签）：

```dotenv
API2MCP_IMAGE=ghcr.io/xyzphp/api2mcp:v0.2.0
API2MCP_MOCK_IMAGE=ghcr.io/xyzphp/api2mcp-mock:v0.2.0
```

```sh
./scripts/compose.sh pull
./scripts/compose.sh up -d --no-build --wait
```

正式部署建议固定版本标签；`latest` 跟随最近正式 Release。应用和 Mock 均支持 amd64 / arm64。关闭 Mock 初始化可设 `SEED_MOCK=false`；Compose 的 Mock 容器仍可保留，应用不依赖它执行真实业务请求。

## 局域网访问

例如宿主机的局域网 IP 为 `192.168.1.10`：

```dotenv
BIND_ADDRESS=192.168.1.10
APP_PORT=18080
PUBLIC_ORIGIN=http://192.168.1.10:18080
COOKIE_SECURE=false
```

也可用 `BIND_ADDRESS=0.0.0.0` 监听全部网卡。保存后重新创建应用容器：

```sh
./scripts/compose.sh up -d --no-build --no-deps --wait api2mcp
```

通过 `http://192.168.1.10:18080/` 登录，复制的 MCP 地址会使用这个 IP 和端口。Mock 的宿主机端口始终只绑定 127.0.0.1；应用容器通过服务名访问 Mock。

## HTTPS 与子路径

例如使用 `https://mcp.example.com/http_mcp/`：

```dotenv
PUBLIC_ORIGIN=https://mcp.example.com
BASE_PATH=/http_mcp
COOKIE_SECURE=true
```

`PUBLIC_ORIGIN` 不含路径，`BASE_PATH` 无结尾斜杠。Nginx 示例：

```nginx
location = /http_mcp {
    return 301 /http_mcp/;
}

location /http_mcp/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $http_host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_read_timeout 300s;
    proxy_send_timeout 300s;
    # MCP Token 位于查询串，访问日志不应记录完整查询参数。
    access_log off;
}
```

检查并重载 Nginx 后访问 `/http_mcp/`。上述配置保留上游路径前缀；应用也兼容通过带尾斜杠的 `proxy_pass` 剥离前缀。根路径部署时留空 `BASE_PATH`，将 location 改为 `/`。

应用依据请求协议和 Host 生成 MCP 地址。TLS 在反向代理终止时，需要正确传递 `X-Forwarded-Proto`；不要保留客户端伪造的值。HTTPS 与 HTTP 局域网入口可以同时使用，登录 Cookie 按当前入口协议设置。

## 构建代理

`.env` 可选：

```dotenv
DOCKER_HTTP_PROXY=http://proxy.example.local:7890
DOCKER_HTTPS_PROXY=http://proxy.example.local:7890
DOCKER_ALL_PROXY=socks5://proxy.example.local:7890
GOPROXY=https://proxy.golang.org,direct
```

这些值只用于构建步骤。拉取基础镜像仍需为 Docker Desktop / Docker daemon 单独设置代理；参见 [Docker 构建代理文档](https://docs.docker.com/engine/cli/proxy/)。不要将本机代理地址提交到仓库。

业务请求直接连接上游，不继承镜像构建代理。容器内 `localhost` 指当前容器；访问宿主机 API 可使用 `host.docker.internal`（Docker Desktop）或宿主机局域网 IP。Linux 可按实际环境添加 `host-gateway` 映射。

## 更新与回滚

源码更新后运行 `make up`。仅更新应用时可以执行：

```sh
./scripts/compose.sh build api2mcp
./scripts/compose.sh up -d --no-build --no-deps --wait api2mcp
```

镜像部署则修改 `.env` 中两个镜像版本，执行 `pull` 和 `up --no-build`。更新会使管理员会话失效，需要重新登录；数据、凭证和调用 Token 保留。回滚前确认数据库格式兼容，并先备份。

## 备份与恢复

为保证 SQLite、WAL 与工作空间一致，先停止应用，再复制整个数据目录。以下命令在后台停止时备份数据卷，不需要直接猜 Compose 卷名：

```sh
mkdir -p .local/backups
./scripts/compose.sh stop api2mcp
./scripts/compose.sh cp api2mcp:/data/. .local/backups/data
cp .env .local/backups/env
chmod 600 .local/backups/env
./scripts/compose.sh start api2mcp
```

将备份转存到受保护的位置。恢复时先停应用，把原 `.env` 的 `ENCRYPTION_KEY` 放回配置，再使用 `compose cp` 将数据复制回 `/data/`，确保属主为 65532，最后启动应用。不要在运行时仅复制主 `.db` 文件；不要更换密钥后尝试直接打开旧数据库。

`make down` 保留卷；`down -v` 会删除数据。当前不支持多实例同时挂载同一个数据库。
