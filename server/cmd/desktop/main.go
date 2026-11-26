package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/automation"
	httpapi "github.com/haydardsx/iquji-community-ops/server/internal/interface/http"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func main() {
	ctx, cancelRuntime := context.WithCancel(context.Background())
	defer cancelRuntime()
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
	api := httpapi.New(runtime).Handler()
	outer := http.NewServeMux()
	outer.Handle("/", desktopHandler(windowAPI(api, os.Getenv("IQUOPS_AUTOMATION_WINDOW")), os.Getenv("IQUOPS_FRONTEND_DIR")))
	if owner := os.Getenv("IQUOPS_AUTOMATION_WINDOW"); owner != "" && config.Host == "127.0.0.1" {
		service, err := automation.NewService(runtime, config.StateDir, owner, os.Getenv("IQUOPS_MCP_BINARY"))
		if err != nil {
			platform.Diagnostic("api", "error", "automation_init_failed", "无法读取本地自动化授权", "AUTOMATION_INIT_FAILED")
			os.Exit(1)
		}
		outer.Handle("/api/automation/", http.StripPrefix("/api/automation", service.Handler()))
	}
	server := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return ctx },
		Addr:              fmt.Sprintf("%s:%d", config.Host, config.Port),
		Handler:           outer,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		platform.Diagnostic("api", "error", "listen_failed", "API 监听失败", "API_LISTEN_FAILED")
		os.Exit(1)
	}
	if os.Getenv("IQUOPS_AUTOMATION_WINDOW") != "" && config.Host == "127.0.0.1" {
		cleanup, err := automation.PublishEndpoint(config.StateDir, fmt.Sprint(listener.Addr().(*net.TCPAddr).Port))
		if err != nil {
			panic("无法登记本地自动化入口")
		}
		defer cleanup()
	}
	platform.Diagnostic("api", "info", "listening", "桌面 API 开始监听", "")
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			platform.Diagnostic("api", "error", "serve_failed", "桌面 API 异常退出", "API_SERVE_FAILED")
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancelRuntime()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = runtime.ShutdownFrpProbeServer(shutdownCtx)
}

// desktopHandler mounts the existing API under /api while keeping the external
// agent and health endpoints at the root, then falls back to the SPA.
func desktopHandler(api http.Handler, frontendDir string) http.Handler {
	static := spaHandler(frontendDir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			clone := r.Clone(r.Context())
			clone.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			if clone.URL.Path == "" {
				clone.URL.Path = "/"
			}
			clone.URL.RawPath = ""
			api.ServeHTTP(w, clone)
			return
		}
		if r.URL.Path == "/health" ||
			strings.HasPrefix(r.URL.Path, "/platform/") ||
			strings.HasPrefix(r.URL.Path, "/monitoring/agents/") {
			api.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}

func spaHandler(frontendDir string) http.Handler {
	root, err := filepath.Abs(frontendDir)
	if err != nil || frontendDir == "" {
		return http.NotFoundHandler()
	}
	index := filepath.Join(root, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		cleaned := filepath.Clean("/" + r.URL.Path)
		candidate := filepath.Join(root, cleaned)
		if strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				http.ServeFile(w, r, candidate)
				return
			}
		}
		http.ServeFile(w, r, index)
	})
}

// 桌面窗口凭据保护本地业务 API。外部回调与终端数据流继续验证各自的短期凭据。
func windowAPI(api http.Handler, key string) http.Handler {
	if key == "" {
		return api
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		external := p == "/health" || strings.HasPrefix(p, "/platform/jenkins/") || p == "/servers/terminal-stream"
		if !external && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errorCode":"DESKTOP_WINDOW_REQUIRED"}`))
			return
		}
		api.ServeHTTP(w, r)
	})
}
