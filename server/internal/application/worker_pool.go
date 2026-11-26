package application

import (
	"context"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

// RunWorkerPool 在单一 Worker 进程内提供有界并发，资源互斥由 SQLite claim 保证。
func (r *Runtime) RunWorkerPool(ctx context.Context) {
	concurrency := r.Config.WorkerConcurrency
	if concurrency < 1 {
		concurrency = 4
	}
	interval := time.Duration(r.Config.WorkerIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}
	var group sync.WaitGroup
	for slot := 0; slot < concurrency; slot++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for ctx.Err() == nil {
				result, err := r.RunWorkerOnce(ctx)
				if err != nil {
					platform.Diagnostic("worker", "error", "iteration_failed", "Worker 本轮执行失败", stableErrorCode(err, "WORKER_ITERATION_FAILED"))
				}
				if err == nil && result.Processed > 0 {
					continue
				}
				timer := time.NewTimer(interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	group.Wait()
}
