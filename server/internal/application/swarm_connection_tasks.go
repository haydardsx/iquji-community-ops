package application

import (
	"context"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeReconfigureDockerSwarm(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "Swarm 连接设置执行器未配置", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "读取 Swarm 连接设置失败", err
	}
	if err = swarmConnectionOperationAvailable(state, task.TargetID, task.ID); err != nil {
		return "Swarm 连接设置存在冲突操作", err
	}
	// 旧版本的检查点意味着远端可能已改动，Worker 重启后必须先恢复，不能当作新请求阻断。
	restoreOnly := len(objectMapFromAny(task.Payload["swarmConnectionCheckpoint"])) > 0
	executionCtx, stop := context.WithCancel(ctx)
	if !restoreOnly {
		stop()
		executionCtx, stop = r.swarmConnectionExecutionContext(ctx, task.ID)
	}
	defer stop()
	input, err := r.swarmRuntimeExecutionInput(executionCtx, task)
	if err != nil {
		return "准备 Swarm 连接设置失败", err
	}
	input.SwarmConnectionRestoreOnly = restoreOnly
	input.SwarmConnectionCheckpoint = objectMapFromAny(task.Payload["swarmConnectionCheckpoint"])
	input.SaveSwarmConnectionCheckpoint = func(saveCtx context.Context, checkpoint domain.Object) error {
		return r.Store.MutateState(domain.WithTaskClaim(saveCtx, task), func(state *domain.State) error {
			current := findTask(state.Tasks, task.ID)
			if current == nil {
				return errors.New("TASK_NOT_FOUND")
			}
			if current.CancelRequested {
				return context.Canceled
			}
			current.Payload["swarmConnectionCheckpoint"] = checkpoint
			return nil
		})
	}
	if _, err = r.ServerRuntimeExecutor.ReconfigureDockerSwarm(executionCtx, input); err != nil {
		if restoreOnly && errors.Is(err, context.Canceled) && !task.CancelRequested {
			latest, loadErr := r.Store.LoadState(ctx)
			if loadErr != nil {
				return "读取 Swarm 恢复任务状态失败", loadErr
			}
			current := findTask(latest.Tasks, task.ID)
			if current == nil || !current.CancelRequested {
				err = errors.New("SWARM_CONNECTION_MIGRATION_REQUIRED")
			}
		}
		r.reconcileServerRuntimeAfterFailure(ctx, input, "swarm", err)
		return "Swarm 连接维护未完成", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err == nil {
		err = verifySwarmInstallProbe(probe, "manager")
	}
	if err != nil {
		return "Swarm 连接设置复探测失败", err
	}
	return "Swarm 连接状态已真实核验", nil
}

func swarmConnectionOperationAvailable(state domain.State, serverID, exceptID string) error {
	for _, task := range state.Tasks {
		if task.ID == exceptID || !taskCurrentlyExecuting(task) {
			continue
		}
		if swarmTaskTouchesServer(state, task, serverID) {
			return errors.New("SWARM_CONNECTION_OPERATION_ACTIVE")
		}
	}
	for _, plan := range state.ReleasePlans {
		if !releasePlanTerminal(plan) && stringValue(plan["status"]) != "queued" && stringValue(plan["operationRunId"]) != exceptID && swarmServersRelated(state, stringValue(plan["deploymentManagerServerId"]), serverID) {
			return errors.New("SWARM_CONNECTION_OPERATION_ACTIVE")
		}
	}
	return nil
}

func swarmTaskTouchesServer(state domain.State, task domain.Task, serverID string) bool {
	if task.Action == string(domain.TaskActionVerifyCertificate) && certificateUsesSwarmServer(state, task.TargetID, serverID) {
		return true
	}
	switch domain.TaskAction(task.Action) {
	case domain.TaskActionStartFrpTunnel, domain.TaskActionStopFrpTunnel, domain.TaskActionDeleteFrpTunnel:
		return swarmServersRelated(state, stringValue(task.Payload["serverId"]), serverID)
	}
	if applicationID := taskApplicationID(state, task); applicationID != "" && applicationUsesSwarmServer(state, applicationID, serverID) {
		return true
	}
	if task.TargetKind != "server" {
		return false
	}
	switch domain.TaskAction(task.Action) {
	case domain.TaskActionInstallFrps, domain.TaskActionUninstallFrps:
		return swarmServersRelated(state, task.TargetID, serverID)
	case domain.TaskActionInstallDockerEngine, domain.TaskActionUninstallDockerEngine, domain.TaskActionInstallDockerSwarm, domain.TaskActionUninstallDockerSwarm, domain.TaskActionReconfigureDockerSwarm, domain.TaskActionRebuildDockerSwarm, domain.TaskActionRecoverDockerSwarmRebuild, domain.TaskActionInstallTraefikGateway, domain.TaskActionUninstallTraefikGateway:
		return swarmServersRelated(state, task.TargetID, serverID) || swarmServersRelated(state, stringValue(task.Payload["managerServerId"]), serverID)
	}
	return false
}

func applicationUsesSwarmServer(state domain.State, applicationID, serverID string) bool {
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	if swarmServersRelated(state, stringValue(config["deploymentManagerServerId"]), serverID) || swarmServersRelated(state, stringValue(config["gatewayServerId"]), serverID) {
		return true
	}
	for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
		if swarmServersRelated(state, stringValue(service["serverId"]), serverID) {
			return true
		}
	}
	return false
}

func swarmServersRelated(state domain.State, left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	cluster := stringValue(serverRuntimeMetrics(findObject(state.Servers, left))["swarmClusterId"])
	return cluster != "" && cluster == stringValue(serverRuntimeMetrics(findObject(state.Servers, right))["swarmClusterId"])
}

func activeSwarmMaintenanceForTask(state domain.State, task domain.Task) *domain.Task {
	for _, existing := range state.Tasks {
		if existing.ID != task.ID && swarmMaintenanceBlocksTask(existing) && swarmTaskTouchesServer(state, task, existing.TargetID) {
			copy := existing
			return &copy
		}
	}
	return nil
}

func (r *Runtime) swarmConnectionExecutionContext(ctx context.Context, taskID string) (context.Context, func()) {
	executionCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			state, err := r.Store.LoadState(executionCtx)
			task := findTask(state.Tasks, taskID)
			if err != nil || task == nil || task.CancelRequested {
				cancel()
				return
			}
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return executionCtx, func() { cancel(); <-done }
}
