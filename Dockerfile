# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY CGO_ENABLED=0
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/api2mcp ./cmd/api2mcp && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/mockapi ./cmd/mockapi && \
    mkdir -p /out/data

FROM scratch AS base
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
USER 65532:65532
WORKDIR /

FROM base AS mock
COPY --from=build /out/mockapi /mockapi
EXPOSE 9090
ENTRYPOINT ["/mockapi"]

FROM base AS runtime
COPY --from=build /out/api2mcp /api2mcp
COPY --from=build --chown=65532:65532 /out/data /data
ENV HTTP_ADDR=:8080 DATA_PATH=/data/api2mcp.db
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=4s --start-period=10s CMD ["/api2mcp", "-healthcheck"]
ENTRYPOINT ["/api2mcp"]
