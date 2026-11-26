package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	httpapi "github.com/haydardsx/iquji-community-ops/server/internal/interface/http"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func main() {
	ctx := context.Background()
	config := platform.Resolve(platform.RoleAPI)
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		platform.Diagnostic("api", "error", "state_open_failed", "无法打开控制面数据库", "STATE_OPEN_FAILED")
		os.Exit(1)
	}
	defer func() {
		_ = store.Close()
	}()
	runtime := application.NewRuntime(config, store)
	if err := runtime.StartFrpProbeServer(ctx); err != nil {
		platform.Diagnostic("api", "warn", "probe_start_failed", "穿透探针启动失败", "FRP_PROBE_START_FAILED")
	}
	probeStatus := runtime.FrpProbe.Status()
	if enabled, _ := probeStatus["enabled"].(bool); enabled {
		platform.Diagnostic("api", "info", "probe_listening", "穿透探针已开始监听", "")
	} else if summary, _ := probeStatus["errorSummary"].(string); summary != "" {
		platform.Diagnostic("api", "warn", "probe_unavailable", "穿透探针不可用", "FRP_PROBE_UNAVAILABLE")
	}
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:           httpapi.New(runtime).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		platform.Diagnostic("api", "error", "listen_failed", "API 监听失败", "API_LISTEN_FAILED")
		os.Exit(1)
	}
	platform.Diagnostic("api", "info", "listening", "API 开始监听", "")
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			platform.Diagnostic("api", "error", "serve_failed", "API 服务异常退出", "API_SERVE_FAILED")
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = runtime.ShutdownFrpProbeServer(shutdownCtx)
}
