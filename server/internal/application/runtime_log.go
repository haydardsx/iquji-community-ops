package application

import (
	"context"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

// 阶段日志在执行租约内写入，API 与 Worker 只从已提交的记录读取。
func (r *Runtime) appendRuntimeMessage(task domain.Task, source, phase, level, message string) {
	ctx, cancel := context.WithTimeout(domain.WithTaskClaim(context.Background(), task), 2*time.Second)
	defer cancel()
	entry := domain.NewRuntimeLog(task, source, phase, level, message, domain.Now())
	if err := r.Store.MutateState(ctx, func(state *domain.State) error {
		state.RuntimeLogs = append(state.RuntimeLogs, entry)
		return nil
	}); err != nil {
		platform.Diagnostic("worker", "error", "log_write_failed", "阶段日志写入失败", stableErrorCode(err, "RUNTIME_LOG_WRITE_FAILED"))
	}
}
