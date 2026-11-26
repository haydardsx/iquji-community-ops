package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/application"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	config := platform.Resolve(platform.RoleWorker)
	store, releaseWorker, err := openWorkerState(ctx, config)
	if err != nil {
		platform.Diagnostic("worker", "error", "state_open_failed", "无法打开 Worker 状态", "WORKER_STATE_OPEN_FAILED")
		os.Exit(1)
	}
	defer releaseWorker()
	defer func() {
		_ = store.Close()
	}()
	runtime := application.NewRuntime(config, store)
	recoveryCtx, recoveryCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := runtime.RecoverFrpTunnels(recoveryCtx); err != nil {
		platform.Diagnostic("worker", "warn", "tunnel_recovery_failed", "隧道恢复未完成", "FRP_TUNNEL_RECOVERY_FAILED")
	}
	recoveryCancel()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := runtime.ShutdownFrpTunnels(shutdownCtx); err != nil {
			platform.Diagnostic("worker", "warn", "tunnel_cleanup_failed", "隧道退出前清理未完成", "FRP_TUNNEL_CLEANUP_FAILED")
		}
	}()
	if !config.WorkerRunOnce {
		collectorCtx, stopCollector := context.WithCancel(ctx)
		collectorDone := make(chan struct{})
		go func() {
			defer close(collectorDone)
			runtime.RunMonitoringCollector(collectorCtx)
		}()
		defer func() {
			stopCollector()
			<-collectorDone
		}()
	}
	platform.Diagnostic("worker", "info", "started", "Worker 已启动", "")
	if config.WorkerRunOnce {
		_, err := runtime.RunWorkerOnce(ctx)
		if err != nil {
			platform.Diagnostic("worker", "error", "iteration_failed", "Worker 本轮执行失败", "WORKER_ITERATION_FAILED")
		} else {
			platform.Diagnostic("worker", "info", "iteration_completed", "Worker 本轮执行完成", "")
		}
		return
	}
	runtime.RunWorkerPool(ctx)
}
