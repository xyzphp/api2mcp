package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	web "api2mcp"
	"api2mcp/internal/envfile"
	"api2mcp/internal/platform"
)

func main() {
	check := flag.Bool("healthcheck", false, "check local HTTP health and exit")
	flag.Parse()
	if *check {
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	envfile.Load(".env")
	cfg, err := platform.ConfigFromEnv()
	if err != nil {
		slog.Error("配置错误", "error", err)
		os.Exit(1)
	}
	app, err := platform.NewApp(cfg, web.Files)
	if err != nil {
		slog.Error("启动失败", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	server := &http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), cfg.UpstreamTimeout+5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	}()
	slog.Info("API2MCP 已启动", "listen", cfg.Addr, "url", cfg.PublicOrigin)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP 服务异常", "error", err)
		os.Exit(1)
	}
	<-shutdownDone
}
