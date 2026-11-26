package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type TaskClaimOptions struct {
	Owner       string
	Concurrency int
}

// ExpirePreviousWorkerLeases 仅在取得单 Worker 进程锁后调用，重启立即进入恢复流程。
func (s *Store) ExpirePreviousWorkerLeases(ctx context.Context) error {
	return s.MutateState(ctx, func(state *domain.State) error {
		for i := range state.Tasks {
			task := &state.Tasks[i]
			if task.Status == "executing" || task.Status == "preflight_running" || task.Status == "verifying" {
				task.LeaseExpiresAt = time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano)
			}
		}
		return nil
	})
}

func checkTaskClaim(ctx context.Context, state domain.State) error {
	claim, ok := domain.TaskClaimFromContext(ctx)
	if !ok {
		return nil
	}
	for _, task := range state.Tasks {
		if task.ID != claim.ID {
			continue
		}
		if task.ClaimToken != claim.ClaimToken || task.Attempt != claim.Attempt || (task.Status != "executing" && task.Status != "verifying") {
			return errors.New("TASK_LEASE_LOST")
		}
		if expiry, err := time.Parse(time.RFC3339Nano, task.LeaseExpiresAt); err != nil || !expiry.After(time.Now().UTC()) {
			return errors.New("TASK_LEASE_LOST")
		}
		return nil
	}
	return errors.New("TASK_LEASE_LOST")
}

// taskClaimBlocker 保留 FIFO 公平性；后续无关资源可以跳过等待项。
func taskClaimBlocker(state domain.State, candidate domain.Task) string {
	// 恢复保留活动预检合同，执行前发现活动任务时明确失败。
	if candidate.TargetKind == "backup_restore" {
		return ""
	}
	resources := domain.TaskResources(state, candidate)
	for _, other := range state.Tasks {
		if other.ID == candidate.ID || other.Status == "pending_confirmation" {
			continue
		}
		active := other.Status == "executing" || other.Status == "verifying" || other.Status == "preflight_running"
		preceding := other.Status == "queued" && domain.TaskPrecedes(other, candidate)
		if candidate.Action == string(domain.TaskActionRecoverDockerSwarmRebuild) {
			preceding = false
		}

		if (active || preceding) && domain.TaskLocksConflict(resources, domain.TaskResources(state, other)) {
			return other.ID
		}
	}
	if owner, _ := domain.TaskExternalBlocker(state, candidate); owner != "" {
		return owner
	}

	return ""
}

func initializeTaskQueue(state *domain.State) {
	for i := range state.Tasks {
		task := &state.Tasks[i]
		if task.Status == "queued" && task.QueueOrder == 0 {
			task.QueueOrder = domain.NextTaskQueueOrder(state.Tasks)
		}
		if len(task.QueueResources) == 0 {
			task.QueueResources = domain.TaskResources(*state, *task)
		}
	}
}

func taskLeaseExpired(task domain.Task, now time.Time, staleAfter time.Duration) bool {
	if task.LeaseExpiresAt != "" {
		expiry, err := time.Parse(time.RFC3339Nano, task.LeaseExpiresAt)
		return err == nil && !expiry.After(now)
	}
	updated, err := time.Parse(time.RFC3339Nano, task.UpdatedAt)
	return err == nil && now.Sub(updated) >= staleAfter
}
