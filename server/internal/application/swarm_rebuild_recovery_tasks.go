package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) CreateSwarmRebuildRecoveryOperation(ctx context.Context, serverID, operationID string) (domain.Task, error) {
	var task domain.Task
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		owner, err := swarmRebuildRecoveryOwner(*state, strings.TrimSpace(operationID), serverID)
		if err != nil {
			return err
		}
		if findObject(state.Servers, serverID) == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		task = newTask(string(domain.TaskActionRecoverDockerSwarmRebuild), "server", serverID, owner.TargetName, domain.Object{
			"sourceOperationRunId": owner.ID, "connectionMode": owner.Payload["connectionMode"], "advertiseAddr": owner.Payload["advertiseAddr"],
			"swarmRebuildCheckpoint": cloneObject(objectMapFromAny(owner.Payload["swarmRebuildCheckpoint"])),
		})
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func swarmRebuildRecoveryOwner(state domain.State, operationID, serverID string) (*domain.Task, error) {
	source := findTask(state.Tasks, operationID)
	if source == nil {
		return nil, errors.New("OPERATION_RUN_NOT_FOUND")
	}
	if source.Action == string(domain.TaskActionRecoverDockerSwarmRebuild) {
		source = findTask(state.Tasks, stringValue(source.Payload["sourceOperationRunId"]))
	}
	if source == nil || source.Action != string(domain.TaskActionRebuildDockerSwarm) || source.TargetKind != "server" || source.TargetID != serverID {
		return nil, errors.New("SWARM_REBUILD_RECOVERY_INVALID")
	}
	if domain.IsTaskActive(source.Status) {
		return nil, &TaskAlreadyActiveError{ActiveTaskID: source.ID}
	}
	checkpoint := objectMapFromAny(source.Payload["swarmRebuildCheckpoint"])
	phase := stringValue(checkpoint["phase"])
	if len(checkpoint) == 0 || phase == "verified" || phase == "recovered" {
		return nil, errors.New("SWARM_REBUILD_RECOVERY_NOT_REQUIRED")
	}
	if validateSwarmRebuildCheckpointObject(checkpoint) != nil || stringValue(checkpoint["backupId"]) != source.ID {
		return nil, errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	return source, nil
}

func swarmRebuildRecoveryAvailable(state domain.State, serverID, exceptID, ownerID string) error {
	for _, task := range state.Tasks {
		if task.ID != exceptID && taskCurrentlyExecuting(task) && swarmTaskTouchesServer(state, task, serverID) {
			return &TaskAlreadyActiveError{ActiveTaskID: task.ID}
		}
		if task.ID != ownerID && task.ID != exceptID && task.Action == string(domain.TaskActionRebuildDockerSwarm) && swarmMaintenanceBlocksTask(task) && swarmServersRelated(state, task.TargetID, serverID) {
			return errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
	}
	return swarmConnectionOperationAvailable(state, serverID, exceptID)
}

func (r *Runtime) swarmRebuildCheckpointSaver(taskID, ownerID string) func(context.Context, domain.Object) error {
	return func(ctx context.Context, next domain.Object) error {
		if err := validateSwarmRebuildCheckpointObject(next); err != nil {
			return err
		}
		if stringValue(next["backupId"]) != ownerID {
			return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
		}
		return r.Store.MutateState(ctx, func(state *domain.State) error {
			current, owner := findTask(state.Tasks, taskID), findTask(state.Tasks, ownerID)
			if current == nil || owner == nil {
				return errors.New("TASK_NOT_FOUND")
			}
			for _, task := range []*domain.Task{current, owner} {
				previous := objectMapFromAny(task.Payload["swarmRebuildCheckpoint"])
				if len(previous) > 0 && (previous["backupId"] != next["backupId"] || previous["manifestHash"] != next["manifestHash"] || stringValue(previous["dataHash"]) != "" && previous["dataHash"] != next["dataHash"]) {
					return errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
				}
			}
			// 取消后仍记录恢复阶段；原任务只更新检查点，不改写历史终态或错误。
			current.Payload["swarmRebuildCheckpoint"], owner.Payload["swarmRebuildCheckpoint"] = cloneObject(next), cloneObject(next)
			return nil
		})
	}
}

func operationRunResponseWithRecovery(state domain.State, run domain.OperationRun) map[string]any {
	response := domain.OperationRunResponse(run)
	if queue := operationQueueView(state, run.ID); queue != nil {
		response["queue"] = queue
	}
	if run.Action != string(domain.TaskActionRebuildDockerSwarm) && run.Action != string(domain.TaskActionRecoverDockerSwarmRebuild) {
		return response
	}
	task := findTask(state.Tasks, run.ID)
	if task == nil {
		return response
	}
	ownerID := task.ID
	if task.Action == string(domain.TaskActionRecoverDockerSwarmRebuild) {
		ownerID = stringValue(task.Payload["sourceOperationRunId"])
	}
	owner := findTask(state.Tasks, ownerID)
	needsRecovery := false
	if owner != nil && !domain.IsTaskActive(owner.Status) {
		checkpoint := objectMapFromAny(owner.Payload["swarmRebuildCheckpoint"])
		phase := stringValue(checkpoint["phase"])
		needsRecovery = len(checkpoint) > 0 && phase != "verified" && phase != "recovered"
		for _, candidate := range state.Tasks {
			if candidate.Action == string(domain.TaskActionRecoverDockerSwarmRebuild) && domain.IsTaskActive(candidate.Status) && stringValue(candidate.Payload["sourceOperationRunId"]) == ownerID {
				needsRecovery = false
			}
		}
	}
	response["details"] = domain.Object{"swarmRebuildRecovery": domain.Object{"needsRecovery": needsRecovery, "operationRunId": ownerID}}
	return response
}
