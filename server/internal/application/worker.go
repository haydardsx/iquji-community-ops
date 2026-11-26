package application

import (
	"context"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

type WorkerResult struct {
	Processed int
}

type taskHandler func(context.Context, domain.Task) (string, error)

func (r *Runtime) RunWorkerOnce(ctx context.Context) (WorkerResult, error) {
	ctx = r.WithServerConnections(ctx)
	// 远端清理优先于领取新任务，避免关联资源持续阻塞。
	var cleanupErr error
	if r.cleanupMu.TryLock() {
		cleaned, err := r.reconcileJenkinsCleanup(ctx)
		cleanupErr = err
		r.cleanupMu.Unlock()
		if cleaned.Processed > 0 {
			return cleaned, nil
		}
		if err != nil {
			platform.Diagnostic("worker", "warn", "jenkins_cleanup_pending", "Jenkins 执行资源待清理", stableErrorCode(err, "JENKINS_CLEANUP_REQUIRED"))
		}
	}
	task, err := r.Store.ClaimNextTask(ctx, 15*time.Minute, sqlite.TaskClaimOptions{Owner: r.Config.WorkerID, Concurrency: r.Config.WorkerConcurrency})
	if err != nil {
		return WorkerResult{}, err
	}
	if task == nil {
		return WorkerResult{}, cleanupErr
	}
	ctx = domain.WithTaskClaim(ctx, *task)
	executionCtx, cancelExecution := context.WithCancel(ctx)
	defer cancelExecution()
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(ctx))
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := r.Store.HeartbeatTask(heartbeatCtx, task.ID); err != nil {
					cancelExecution()
					return
				}
			}
		}
	}()
	r.appendRuntimeMessage(*task, "worker", "execute", "info", "开始执行操作，完成后将核验实际结果")
	message, executionErr := r.executeWorkerTask(executionCtx, *task)
	stopHeartbeat()
	<-heartbeatDone
	status := "succeeded"
	if executionErr != nil {
		status = "failed"
		message = stableErrorCode(executionErr, "TASK_EXECUTION_FAILED")
	}
	if executionErr != nil && (executionErr.Error() == "JENKINS_RELEASE_CANCELLED" || isSwarmMaintenanceAction(task.Action) && errors.Is(executionErr, context.Canceled)) {
		status = "cancelled"
	}
	completionCtx, cancelCompletion := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelCompletion()
	if err := r.finalizeSecretAssetTask(completionCtx, *task, status); err != nil {
		return WorkerResult{}, err
	}
	if executionErr == nil {
		if err := r.applyTaskEffect(completionCtx, *task, status); err != nil {
			return WorkerResult{}, err
		}
	}
	if err := r.Store.CompleteTask(completionCtx, task.ID, status, message); err != nil {
		return WorkerResult{}, err
	}
	return WorkerResult{Processed: 1}, nil
}

func (r *Runtime) executeWorkerTask(ctx context.Context, task domain.Task) (string, error) {
	if changed, _ := task.Payload["queueScopeChanged"].(bool); changed {
		r.appendRuntimeMessage(task, "worker", "preflight", "error", "资源范围已变化，请重新提交确认")
		return "资源范围已变化，请重新提交确认", errors.New("TASK_RESOURCE_SCOPE_CHANGED")
	}
	if handler, ok := r.workerTaskHandlers()[domain.TaskAction(task.Action)]; ok {
		return handler(ctx, task)
	}
	return "操作没有可用执行器", errors.New("TASK_ACTION_UNSUPPORTED")
}

func (r *Runtime) workerTaskHandlers() map[domain.TaskAction]taskHandler {
	return map[domain.TaskAction]taskHandler{
		domain.TaskActionCreateSecretAsset: r.executeSecretAssetOperation,
		domain.TaskActionRotateSecretAsset: r.executeSecretAssetOperation,
		domain.TaskActionStartFrpTunnel: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeStartFrpTunnel(ctx, task)
		},
		domain.TaskActionStopFrpTunnel: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeStopFrpTunnel(ctx, task)
		},
		domain.TaskActionDeleteFrpTunnel: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeDeleteFrpTunnel(ctx, task)
		},
		domain.TaskActionInstallDockerEngine: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeInstallDockerEngine(ctx, task)
		},
		domain.TaskActionUninstallDockerEngine: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeUninstallDockerEngine(ctx, task)
		},
		domain.TaskActionInstallDockerSwarm: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeInstallDockerSwarm(ctx, task)
		},
		domain.TaskActionReconfigureDockerSwarm:    r.executeReconfigureDockerSwarm,
		domain.TaskActionRebuildDockerSwarm:        r.executeRebuildDockerSwarm,
		domain.TaskActionRecoverDockerSwarmRebuild: r.executeRebuildDockerSwarm,
		domain.TaskActionUninstallDockerSwarm: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeUninstallDockerSwarm(ctx, task)
		},
		domain.TaskActionInstallTraefikGateway: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeInstallTraefikGateway(ctx, task)
		},
		domain.TaskActionUninstallTraefikGateway: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeUninstallTraefikGateway(ctx, task)
		},
		domain.TaskActionInstallFrps: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeFrpsRuntimeAction(ctx, task, true)
		},
		domain.TaskActionUninstallFrps: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeFrpsRuntimeAction(ctx, task, false)
		},
		domain.TaskActionInstallJenkins: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeInstallJenkins(ctx, task)
		},
		domain.TaskActionUninstallJenkins: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeUninstallJenkins(ctx, task)
		},
		domain.TaskActionInstallMonitoringAgent: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeInstallMonitoringAgent(ctx, task)
		},
		domain.TaskActionUninstallMonitoringAgent: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeUninstallMonitoringAgent(ctx, task)
		},
		domain.TaskActionUninstallApplication: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeApplicationServiceOperation(ctx, task)
		},
		domain.TaskActionVerifyCertificate: func(ctx context.Context, task domain.Task) (string, error) {
			if _, err := r.VerifyCertificate(ctx, task.TargetID); err != nil {
				return "证书签发校验未通过", err
			}
			return "证书已在网关签发并通过域名校验", nil
		},
		"start_application":                    r.executeApplicationServiceOperation,
		"stop_application":                     r.executeApplicationServiceOperation,
		"restart_application":                  r.executeApplicationServiceOperation,
		"apply_route":                          r.executeRouteOperation,
		domain.TaskActionBindJenkinsAccess:     r.executeJenkinsAccess,
		domain.TaskActionUnbindJenkinsAccess:   r.executeJenkinsAccess,
		applyDomainRedirect:                    r.executeDomainRedirect,
		removeDomainRedirect:                   r.executeDomainRedirect,
		"disable_route":                        r.executeRouteOperation,
		"delete_route":                         r.executeRouteOperation,
		domain.TaskActionRequestJenkinsRelease: r.executeRequestJenkinsRelease,
		domain.TaskActionDeployApplication:     r.executeContainerRelease,
		domain.TaskActionRestoreBackup: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeRestoreTask(ctx, task)
		},
		domain.TaskActionRestoreBackupOverwrite: func(ctx context.Context, task domain.Task) (string, error) {
			return r.executeRestoreTask(ctx, task)
		},
	}
}

func (r *Runtime) applyTaskEffect(ctx context.Context, task domain.Task, status string) error {
	if status != "succeeded" {
		return nil
	}
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		if task.Action == "uninstall_docker_engine" {
			setServerHealthMetric(state, task.TargetID, "docker", false)
		}
		return nil
	})
}

func updateStateObject(items *[]domain.Object, id string, patch domain.Object) {
	domain.PatchObjectByID(items, id, patch)
}

func upsertStateObjectByKey(items *[]domain.Object, key string, value string, patch domain.Object) {
	domain.UpsertObjectByKey(items, key, value, patch)
}

func setServerHealthMetric(state *domain.State, serverID string, key string, value bool) {
	for index := range state.Servers {
		if stringValue(state.Servers[index]["id"]) != serverID {
			continue
		}
		health, ok := state.Servers[index]["health"].(map[string]any)
		if !ok {
			health = map[string]any{"status": "idle", "summary": "Server runtime updated", "metrics": map[string]any{}}
		}
		metrics, ok := health["metrics"].(map[string]any)
		if !ok {
			metrics = map[string]any{}
		}
		metrics[key] = value
		if !value {
			clearServerRuntimeMetricDetails(metrics, key)
		}
		metrics["checkedAt"] = domain.Now()
		health["metrics"] = metrics
		state.Servers[index]["health"] = health
		state.Servers[index]["updatedAt"] = domain.Now()
		return
	}
}

func removeApplicationInstances(items []domain.Object, applicationID string) []domain.Object {
	result := make([]domain.Object, 0, len(items))
	for _, item := range items {
		if stringValue(item["applicationId"]) != applicationID {
			result = append(result, item)
		}
	}
	return result
}
