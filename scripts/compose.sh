#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if docker compose version >/dev/null 2>&1; then
  exec docker compose "$@"
fi
# Some Docker Desktop installations don't register the bundled CLI plugins.
compose_cli=/Applications/Docker.app/Contents/Resources/cli-plugins/docker-compose
if [ -x "$compose_cli" ]; then
  exec "$compose_cli" "$@"
fi
printf '%s\n' 'Docker Compose 不可用，请安装 Docker Compose v2 或 Docker Desktop。' >&2
exit 1
