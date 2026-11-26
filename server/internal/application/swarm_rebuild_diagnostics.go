package application

import (
	"context"
	"errors"
)

// 范围检查只对外返回稳定错误码；诊断类别由内部固定值提供。
type swarmRebuildScopeError string

func (swarmRebuildScopeError) Error() string { return "SWARM_REBUILD_SCOPE_UNSUPPORTED" }

func (e swarmRebuildScopeError) RebuildReason() string { return string(e) }

func swarmRebuildReason(err error) string {
	var diagnostic interface{ RebuildReason() string }
	if errors.As(err, &diagnostic) {
		return diagnostic.RebuildReason()
	}
	if errors.Is(err, context.Canceled) {
		return "OPERATION_CANCELED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "OPERATION_TIMED_OUT"
	}
	if err != nil {
		switch err.Error() {
		case "SWARM_REBUILD_SCOPE_UNSUPPORTED":
			return "SCOPE_UNSUPPORTED"
		case "SWARM_REBUILD_BACKUP_FAILED":
			return "BACKUP_FAILED"
		case "SWARM_REBUILD_STATE_UNAVAILABLE":
			return "STATE_UNAVAILABLE"
		case "SWARM_REBUILD_CHECKPOINT_FAILED":
			return "CHECKPOINT_FAILED"
		case "SWARM_REBUILD_CHECKPOINT_INVALID":
			return "CHECKPOINT_INVALID"
		case "SWARM_REBUILD_VERIFY_FAILED":
			return "VERIFY_FAILED"
		case "SWARM_REBUILD_RECOVERY_REQUIRED":
			return "RECOVERY_REQUIRED"
		case "SWARM_REBUILD_ROLLBACK_FAILED":
			return "ROLLBACK_FAILED"
		case "SWARM_REBUILD_INTERRUPTED_RECOVERED":
			return "INTERRUPTED_RECOVERED"
		}
	}
	return "ENGINE_REQUEST_FAILED"
}
