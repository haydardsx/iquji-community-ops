package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type taskRequestKey struct{}
type taskRequest struct{ key, digest string }

func taskCurrentlyExecuting(task domain.Task) bool {
	return task.Status == "executing" || task.Status == "preflight_running" || task.Status == "verifying"
}

// WithTaskRequest 只保存请求摘要；密钥、请求正文与原始幂等键不进入任务。
func WithTaskRequest(ctx context.Context, key, digest string) context.Context {
	hash := sha256.Sum256([]byte(key))
	return context.WithValue(ctx, taskRequestKey{}, taskRequest{key: hex.EncodeToString(hash[:]), digest: digest})
}

func reuseTaskRequest(ctx context.Context, state domain.State, task *domain.Task) (bool, error) {
	request, ok := ctx.Value(taskRequestKey{}).(taskRequest)
	if !ok {
		return false, nil
	}
	for _, existing := range state.Tasks {
		if existing.RequestKey != request.key {
			continue
		}
		if existing.RequestDigest != request.digest {
			return true, errors.New("IDEMPOTENCY_KEY_CONFLICT")
		}
		*task = existing
		return true, nil
	}
	return false, nil
}

func enqueueTaskRequest(ctx context.Context, state *domain.State, task *domain.Task) error {
	if reused, err := reuseTaskRequest(ctx, *state, task); reused || err != nil {
		return err
	}
	if request, ok := ctx.Value(taskRequestKey{}).(taskRequest); ok {
		task.RequestKey, task.RequestDigest = request.key, request.digest
	}
	task.QueueResources = domain.TaskResources(*state, *task)
	if task.Status == "queued" {
		task.QueueOrder = domain.NextTaskQueueOrder(state.Tasks)
	}
	appendTaskState(state, *task)
	return nil
}

func operationQueueView(state domain.State, id string) domain.Object {
	task := findTask(state.Tasks, id)
	if task == nil || !domain.IsTaskActive(task.Status) {
		return nil
	}
	status := task.Status
	if status != "pending_confirmation" && status != "queued" {
		status = "running"
	}
	view := domain.Object{"state": status, "position": 0, "canCancel": status != "running" || task.Action == string(domain.TaskActionRequestJenkinsRelease) || isSwarmMaintenanceAction(task.Action)}
	if status == "pending_confirmation" {
		view["reason"] = "等待确认"
		return view
	}
	if status != "queued" {
		return view
	}
	resources := domain.TaskResources(state, *task)
	predecessors := []domain.Task{}
	for _, other := range state.Tasks {
		if other.ID == id || other.Status == "pending_confirmation" || !domain.IsTaskActive(other.Status) {
			continue
		}
		if other.Status == "queued" && !domain.TaskPrecedes(other, *task) {
			continue
		}
		if domain.TaskLocksConflict(resources, domain.TaskResources(state, other)) {
			predecessors = append(predecessors, other)
		}
	}
	sort.SliceStable(predecessors, func(i, j int) bool { return domain.TaskPrecedes(predecessors[i], predecessors[j]) })
	view["position"] = len(predecessors) + 1
	if len(predecessors) > 0 {
		view["blockedByOperationRunId"] = predecessors[0].ID
		view["reason"] = "等待前序操作完成"
	} else {
		view["reason"] = "等待可用执行槽位"
	}
	if owner, reason := domain.TaskExternalBlocker(state, *task); owner != "" {
		view["blockedByOperationRunId"], view["reason"] = owner, reason
	}
	return view
}
