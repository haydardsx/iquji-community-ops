package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) CreateSwarmRebuildOperation(ctx context.Context, serverID, mode, address string) (domain.Task, error) {
	mode, address = strings.ToLower(strings.TrimSpace(mode)), strings.TrimSpace(address)
	if err := ValidateSwarmConnectionInput(mode, address, true); err != nil {
		return domain.Task{}, err
	}
	task := newTask(string(domain.TaskActionRebuildDockerSwarm), "server", serverID, "", domain.Object{"connectionMode": mode, "advertiseAddr": address})
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		server := findObject(state.Servers, serverID)
		if server == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		task.TargetName = stringValue(server["name"])
		for _, previous := range state.Tasks {
			checkpoint := objectMapFromAny(previous.Payload["swarmRebuildCheckpoint"])
			phase := stringValue(checkpoint["phase"])
			if previous.Action == task.Action && !domain.IsTaskActive(previous.Status) && swarmServersRelated(*state, previous.TargetID, serverID) && len(checkpoint) > 0 && phase != "verified" && phase != "recovered" {
				return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
			}
		}
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func (r *Runtime) executeRebuildDockerSwarm(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "Swarm 重建执行器未配置", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "读取 Swarm 重建任务失败", err
	}
	explicitRecovery := task.Action == string(domain.TaskActionRecoverDockerSwarmRebuild)
	ownerID := task.ID
	checkpoint := objectMapFromAny(task.Payload["swarmRebuildCheckpoint"])
	if explicitRecovery {
		owner, recoveryErr := swarmRebuildRecoveryOwner(state, stringValue(task.Payload["sourceOperationRunId"]), task.TargetID)
		if recoveryErr != nil {
			return "读取 Swarm 原始备份任务失败", recoveryErr
		}
		ownerID, checkpoint = owner.ID, objectMapFromAny(owner.Payload["swarmRebuildCheckpoint"])
		err = swarmRebuildRecoveryAvailable(state, task.TargetID, task.ID, ownerID)
	} else {
		err = swarmRebuildOperationAvailable(state, task.TargetID, task.ID)
	}
	if err != nil {
		return "Swarm 重建存在冲突操作", err
	}
	restoreOnly := len(checkpoint) > 0
	executionCtx, stop := context.WithCancel(ctx)
	if !restoreOnly {
		stop()
		executionCtx, stop = r.swarmConnectionExecutionContext(ctx, task.ID)
	}
	defer stop()
	input, err := r.swarmRuntimeExecutionInput(executionCtx, task)
	if err != nil {
		return "准备 Swarm 重建失败", err
	}
	input.SwarmRebuildID, input.SwarmRebuildRestoreOnly, input.SwarmRebuildCheckpoint = ownerID, restoreOnly, checkpoint
	saveCheckpoint := r.swarmRebuildCheckpointSaver(task.ID, ownerID)
	input.SaveSwarmRebuildCheckpoint = func(saveCtx context.Context, checkpoint domain.Object) error {
		return saveCheckpoint(domain.WithTaskClaim(saveCtx, task), checkpoint)
	}
	if _, err = r.ServerRuntimeExecutor.RebuildDockerSwarm(executionCtx, input); err != nil {
		recovered := errors.Is(err, errSwarmRebuildRecovered)
		if recovered || errors.Is(err, context.Canceled) {
			statusCtx, stopStatus := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stopStatus()
			latest, loadErr := r.Store.LoadState(statusCtx)
			if loadErr != nil {
				return "读取 Swarm 恢复任务状态失败", loadErr
			}
			current := findTask(latest.Tasks, task.ID)
			if current == nil {
				return "读取 Swarm 恢复任务状态失败", errors.New("TASK_NOT_FOUND")
			}
			if recovered {
				if current.CancelRequested {
					err = context.Canceled
				} else if explicitRecovery {
					err = nil
				} else {
					err = errors.New("SWARM_REBUILD_INTERRUPTED_RECOVERED")
				}
			} else {
				// 新任务也可能已经保存检查点，必须读取最新状态再判断取消是否安全。
				latestCheckpoint := objectMapFromAny(current.Payload["swarmRebuildCheckpoint"])
				phase := stringValue(latestCheckpoint["phase"])
				if len(latestCheckpoint) > 0 && phase != "verified" && phase != "recovered" {
					err = errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
				}
			}
		}
		if err != nil {
			r.reconcileServerRuntimeAfterFailure(ctx, input, "swarm", err)
			return "Swarm 重建未完成，已执行恢复核验", err
		}
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err == nil {
		err = verifySwarmInstallProbe(probe, "manager")
	}
	if err != nil {
		return "Swarm 重建复探测失败", err
	}
	if explicitRecovery {
		return "Swarm 与受管网关已按原始备份恢复并真实核验", nil
	}
	return "Swarm 已按所选连接方式重建，受管网关已恢复并真实核验", nil
}

func swarmRebuildOperationAvailable(state domain.State, serverID, exceptID string) error {
	if err := swarmConnectionOperationAvailable(state, serverID, exceptID); err != nil {
		return err
	}
	for _, task := range state.Tasks {
		if task.ID == exceptID || task.Action != string(domain.TaskActionRebuildDockerSwarm) || !swarmServersRelated(state, task.TargetID, serverID) {
			continue
		}
		checkpoint := objectMapFromAny(task.Payload["swarmRebuildCheckpoint"])
		phase := stringValue(checkpoint["phase"])
		if len(checkpoint) > 0 && phase != "verified" && phase != "recovered" {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
	}
	return nil
}

func isSwarmMaintenanceAction(action string) bool {
	return action == string(domain.TaskActionReconfigureDockerSwarm) || action == string(domain.TaskActionRebuildDockerSwarm) || action == string(domain.TaskActionRecoverDockerSwarmRebuild)
}

func swarmMaintenanceBlocksTask(task domain.Task) bool {
	if !isSwarmMaintenanceAction(task.Action) {
		return false
	}
	if domain.IsTaskActive(task.Status) {
		return true
	}
	checkpoint := objectMapFromAny(task.Payload["swarmRebuildCheckpoint"])
	phase := stringValue(checkpoint["phase"])
	return task.Action == string(domain.TaskActionRebuildDockerSwarm) && len(checkpoint) > 0 && phase != "verified" && phase != "recovered"
}

func swarmMaintenanceNeedsCleanup(task domain.Task) bool {
	key := "swarmConnectionCheckpoint"
	if task.Action != string(domain.TaskActionReconfigureDockerSwarm) {
		key = "swarmRebuildCheckpoint"
	}
	return isSwarmMaintenanceAction(task.Action) && (task.Status == "executing" || task.Status == "queued" && len(objectMapFromAny(task.Payload[key])) > 0)
}
