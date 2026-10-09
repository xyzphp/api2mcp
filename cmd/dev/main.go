// Dev provides a portable Docker development loop without depending on the
// platform-specific Compose Watch implementation (Compose v5 on macOS can panic
// while closing its native file watcher).
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var watched = []string{"assets", "cmd", "internal", "examples", "index.html", "login.html", "web.go", "go.mod", "go.sum", "Dockerfile", "compose.yaml", ".env"}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "开发环境启动失败:", err)
		os.Exit(1)
	}
}

func snapshot() ([32]byte, error) {
	files := []string{}
	for _, root := range watched {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".swp") || entry.Name() == ".DS_Store" {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return [32]byte{}, err
		}
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, path := range files {
		content, err := os.ReadFile(path)
		if err != nil {
			return [32]byte{}, err
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func rebuild(ctx context.Context) error {
	command := exec.CommandContext(ctx, "./scripts/compose.sh", "up", "--build", "-d", "--wait", "--wait-timeout", "60")
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	command.Env = append(os.Environ(), "COMPOSE_ANSI=never", "BUILDKIT_PROGRESS=plain", "COMPOSE_MENU=false")
	return command.Run()
}

func run(ctx context.Context) error {
	current, err := snapshot()
	if err != nil {
		return err
	}
	if err := rebuild(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	fmt.Println("开发环境已就绪。监听 Go、HTML、CSS、JS 和配置文件；Ctrl+C 退出监听，容器继续运行。")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("开发监听已退出。使用 make down 停止容器。")
			return nil
		case <-ticker.C:
			next, err := snapshot()
			if err != nil {
				fmt.Println("文件正在写入，稍后重试。")
				continue
			}
			if next == current {
				continue
			}
			current = next
			fmt.Println("检测到源码变更，正在重建 Docker 服务…")
			if err := rebuild(ctx); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				fmt.Println("重建失败。修改源码后会自动重试；现有容器和数据卷保留。")
			} else {
				fmt.Println("Docker 开发服务已更新。")
			}
		}
	}
}
