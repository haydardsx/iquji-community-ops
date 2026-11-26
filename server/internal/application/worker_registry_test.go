package application

import (
	"context"
	"errors"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestWorkerTaskRegistryCoversRuntimeActions(t *testing.T) {
	runtime := &Runtime{}
	handlers := runtime.workerTaskHandlers()
	for _, action := range []domain.TaskAction{
		domain.TaskActionCreateSecretAsset,
		domain.TaskActionRotateSecretAsset,
		domain.TaskActionStartFrpTunnel,
		domain.TaskActionStopFrpTunnel,
		domain.TaskActionInstallDockerEngine,
		domain.TaskActionUninstallDockerEngine,
		domain.TaskActionInstallDockerSwarm,
		domain.TaskActionUninstallDockerSwarm,
		domain.TaskActionReconfigureDockerSwarm,
		domain.TaskActionRebuildDockerSwarm,
		domain.TaskActionRecoverDockerSwarmRebuild,
		domain.TaskActionInstallTraefikGateway,
		domain.TaskActionUninstallTraefikGateway,
		domain.TaskActionInstallFrps,
		domain.TaskActionUninstallFrps,
		domain.TaskActionInstallJenkins,
		domain.TaskActionUninstallJenkins,
		domain.TaskActionInstallMonitoringAgent,
		domain.TaskActionUninstallMonitoringAgent,
		domain.TaskActionUninstallApplication,
		domain.TaskActionRequestJenkinsRelease,
		domain.TaskActionDeployApplication,
		domain.TaskActionRestoreBackup,
		domain.TaskActionRestoreBackupOverwrite,
	} {
		if handlers[action] == nil {
			t.Fatalf("worker registry missing %s", action)
		}
	}
}

func TestWorkerTaskRegistryRejectsUnknownActionAndUnboundDeployment(t *testing.T) {
	runtime := &Runtime{}
	_, err := runtime.executeWorkerTask(context.Background(), domain.Task{Action: "unknown_action"})
	if err == nil || err.Error() != "TASK_ACTION_UNSUPPORTED" {
		t.Fatal("未拒绝未知任务动作")
	}

	_, err = runtime.executeWorkerTask(context.Background(), domain.Task{Action: string(domain.TaskActionDeployApplication)})
	if err == nil || err.Error() != "APPLICATION_CONTAINER_RELEASE_REQUIRED" {
		t.Fatal("未拒绝缺少目标或快照的发布任务")
	}
}

func TestWorkerRejectsJenkinsReleaseWithoutTarget(t *testing.T) {
	runtime := &Runtime{}
	_, err := runtime.executeWorkerTask(context.Background(), domain.Task{
		Action: string(domain.TaskActionRequestJenkinsRelease),
	})
	if err == nil || err.Error() != "JENKINS_RELEASE_PLAN_REQUIRED" {
		t.Fatal("Jenkins release without a target was not rejected")
	}
}

func TestWorkerPersistsFailureWhenExecutionContextIsCancelled(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runCtx, cancel := context.WithCancel(ctx)
	runtime.ServerRuntimeExecutor.(*fakeServerRuntimeExecutor).installJenkins = func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
		cancel()
		return nil, context.Canceled
	}
	task, err := runtime.CreateTask(ctx, string(domain.TaskActionInstallJenkins), "server", "srv_gateway", "Gateway", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.RunWorkerOnce(runCtx)
	if err != nil || result.Processed != 1 {
		t.Fatal("cancelled worker did not process exactly one task")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	completed := findTask(state.Tasks, task.ID)
	var operation domain.OperationRun
	for _, run := range state.OperationRuns {
		if run.ID == task.ID {
			operation = run
		}
	}
	if completed == nil || completed.Status != "failed" || operation.Status != "failed" || operation.ErrorCode != "OPERATION_CANCELLED" {
		t.Fatal("cancelled task was not finalized")
	}
}

func TestFailedSwarmInstallReconcilesTheActualRemoteState(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		installSwarm: func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
			return nil, errors.New("SWARM_NODE_LABEL_FAILED")
		},
		probeResult: domain.Object{
			"metrics": domain.Object{
				"ssh":                   true,
				"docker":                true,
				"swarm":                 true,
				"swarmStatus":           "active",
				"swarmRole":             "manager",
				"swarmControlAvailable": true,
				"swarmNodeId":           "node_manager",
				"swarmClusterId":        "cluster_manager",
			},
			"checks": []domain.Object{},
		},
	}
	task, err := runtime.CreateTask(ctx, "install_docker_swarm", "server", "srv_gateway", "Gateway", map[string]any{"mode": "manager"})
	if err != nil {
		t.Fatalf("create Swarm task: %v", err)
	}
	confirmAndRunTaskForTest(t, runtime, ctx, task.ID)

	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	updated := findTask(state.Tasks, task.ID)
	var operation domain.OperationRun
	for _, run := range state.OperationRuns {
		if run.ID == task.ID {
			operation = run
		}
	}
	if updated == nil || updated.Status != "failed" || operation.Status != "failed" || operation.ErrorCode != "SWARM_NODE_LABEL_FAILED" {
		t.Fatal("failed Swarm installation did not preserve its error code")
	}
	server := findObject(state.Servers, "srv_gateway")
	metrics := objectMapFromAny(objectMapFromAny(server["health"])["metrics"])
	if metrics["swarm"] != true || metrics["swarmRole"] != "manager" || metrics["swarmNodeId"] != "node_manager" {
		t.Fatal("Swarm role or node identifier was not reconciled")
	}
}
