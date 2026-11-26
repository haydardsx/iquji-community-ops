package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeInstallDockerSwarm(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.swarmRuntimeExecutionInput(ctx, task)
	if err != nil {
		return "failed to prepare Docker Swarm install", err
	}
	if _, err := r.ServerRuntimeExecutor.InstallDockerSwarm(ctx, input); err != nil {
		r.reconcileServerRuntimeAfterFailure(ctx, input, "swarm", err)
		return "failed to install Docker Swarm", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err == nil {
		err = verifySwarmInstallProbe(probe, input.SwarmMode)
	}
	if err != nil {
		return "Docker Swarm post-install probe failed", err
	}
	return "Docker Swarm installed and verified", nil
}

func verifySwarmInstallProbe(probe domain.Object, role string) error {
	metrics := objectMapFromAny(probe["metrics"])
	if code := swarmIdentityReadinessCode(metrics); code != "" {
		return errors.New(code)
	}
	if metrics["swarmRole"] != role {
		return errors.New("SWARM_POST_INSTALL_PROBE_FAILED")
	}
	return nil
}

func (r *Runtime) reconcileServerRuntimeAfterFailure(ctx context.Context, input ServerRuntimeExecutionInput, component string, executionErr error) {
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if _, err := r.refreshServerRuntimeAfterAction(probeCtx, input); err == nil {
		return
	}
	_ = r.markServerRuntimeError(context.WithoutCancel(ctx), stringValue(input.Server["id"]), component, executionErr)
}

func (r *Runtime) executeUninstallDockerSwarm(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	if err := r.validateSwarmUninstall(ctx, task.TargetID, task.ID); err != nil {
		return "Docker Swarm uninstall blocked", err
	}
	input, err := r.swarmRuntimeExecutionInput(ctx, task)
	if err != nil {
		return "failed to prepare Docker Swarm uninstall", err
	}
	if _, err := r.ServerRuntimeExecutor.UninstallDockerSwarm(ctx, input); err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "swarm", err)
		return "failed to uninstall Docker Swarm", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err == nil {
		err = verifySwarmUninstallProbe(probe)
	}
	if err != nil {
		return "Docker Swarm post-uninstall probe failed", err
	}
	return "Docker Swarm removed and verified", nil
}

func verifySwarmUninstallProbe(probe domain.Object) error {
	metrics := objectMapFromAny(probe["metrics"])
	swarmReady, present := metrics["swarm"].(bool)
	if !present || swarmReady || metrics["swarmVerificationStatus"] != "not_joined" ||
		firstNonEmptyString(stringValue(metrics["swarmLocalNodeState"]), stringValue(metrics["swarmStatus"])) != "inactive" ||
		metrics["swarmControlAvailable"] == true || stringValue(metrics["swarmNodeId"]) != "" ||
		stringValue(metrics["swarmClusterId"]) != "" || stringValue(metrics["swarmManagerServerId"]) != "" ||
		stringValue(metrics["swarmManagerAddress"]) != "" || metrics["swarmRole"] == "manager" || metrics["swarmRole"] == "worker" {
		return errors.New("SWARM_POST_UNINSTALL_PROBE_FAILED")
	}
	return nil
}

func (r *Runtime) executeInstallJenkins(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Jenkins install", err
	}
	if _, err := r.ServerRuntimeExecutor.InstallJenkins(ctx, input); err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "jenkins", err)
		return "failed to install Jenkins", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err != nil || objectMapFromAny(probe["metrics"])["jenkins"] != true {
		if err == nil {
			err = errors.New("JENKINS_POST_INSTALL_PROBE_FAILED")
		}
		return "Jenkins post-install probe failed", err
	}
	return "Jenkins installed and verified", nil
}

func (r *Runtime) executeUninstallJenkins(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	if err := r.validateJenkinsUninstall(ctx, task.TargetID); err != nil {
		return "Jenkins uninstall blocked", err
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare Jenkins uninstall", err
	}
	if snaps := domain.JenkinsAccessSnapshots(input.Server); len(snaps) > 0 {
		if err := r.changeJenkinsAccess(r.WithServerConnections(ctx), task); err != nil {
			return "未能撤销 Jenkins 访问入口", err
		}
	}
	if _, err := r.ServerRuntimeExecutor.UninstallJenkins(ctx, input); err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "jenkins", err)
		return "failed to uninstall Jenkins", err
	}
	probe, err := r.refreshServerRuntimeAfterAction(ctx, input)
	if err != nil || objectMapFromAny(probe["metrics"])["jenkins"] == true {
		if err == nil {
			err = errors.New("JENKINS_POST_UNINSTALL_PROBE_FAILED")
		}
		return "Jenkins post-uninstall probe failed", err
	}
	return "Jenkins removed and verified", nil
}

func (r *Runtime) swarmRuntimeExecutionInput(ctx context.Context, task domain.Task) (ServerRuntimeExecutionInput, error) {
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return ServerRuntimeExecutionInput{}, err
	}
	input.SwarmAdvertiseAddr = firstNonEmptyString(stringValue(task.Payload["advertiseAddr"]), input.SwarmAdvertiseAddr)
	input.SwarmConnectionMode = strings.ToLower(strings.TrimSpace(stringValue(task.Payload["connectionMode"])))
	if err := ValidateSwarmConnectionInput(input.SwarmConnectionMode, input.SwarmAdvertiseAddr, false); err != nil {
		return ServerRuntimeExecutionInput{}, err
	}
	mode := strings.ToLower(strings.TrimSpace(stringValue(task.Payload["mode"])))
	if mode == "" {
		mode = "manager"
	}
	if mode != "manager" && mode != "worker" {
		return ServerRuntimeExecutionInput{}, errors.New("SWARM_MODE_INVALID")
	}
	input.SwarmMode = mode
	if mode == "manager" {
		return input, nil
	}
	managerServerID := strings.TrimSpace(stringValue(task.Payload["managerServerId"]))
	if managerServerID == "" || managerServerID == task.TargetID {
		return ServerRuntimeExecutionInput{}, errors.New("SWARM_MANAGER_REQUIRED")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return ServerRuntimeExecutionInput{}, err
	}
	manager := findObject(state.Servers, managerServerID)
	if manager == nil {
		return ServerRuntimeExecutionInput{}, errors.New("SWARM_MANAGER_NOT_FOUND")
	}
	managerSSHKey, ok := r.serverSSHSecretMaterial(ctx, state, manager)
	if !ok {
		return ServerRuntimeExecutionInput{}, errors.New("SWARM_MANAGER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	input.SwarmManagerServer = cloneObject(manager)
	input.SwarmManagerSSHKey = managerSSHKey
	input.SwarmManagerServerID = managerServerID
	return input, nil
}

func (r *Runtime) refreshServerRuntimeAfterAction(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	probe, err := r.ServerRuntimeExecutor.ProbeServerRuntime(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := r.applyServerRuntimeProbe(ctx, stringValue(input.Server["id"]), probe); err != nil {
		return nil, err
	}
	return probe, nil
}

func (r *Runtime) validateSwarmUninstall(ctx context.Context, serverID string, taskID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	_, err = swarmUninstallReferenceImpact(state, serverID, taskID)
	return err
}

func swarmWorkerHasManagerBinding(state domain.State, serverID string) bool {
	metrics := serverRuntimeMetrics(findObject(state.Servers, serverID))
	managerID := stringValue(metrics["swarmManagerServerId"])
	return metrics["swarmRole"] == "worker" && managerID != "" && managerID != serverID && findObject(state.Servers, managerID) != nil
}

func activeServerSwarmTask(state domain.State, serverID string, exceptTaskID string) *domain.Task {
	for index := range state.Tasks {
		task := &state.Tasks[index]
		if task.ID == exceptTaskID || task.TargetKind != "server" || task.TargetID != serverID || !domain.IsTaskActive(task.Status) || exceptTaskID != "" && !taskCurrentlyExecuting(*task) {
			continue
		}
		switch domain.TaskAction(task.Action) {
		case domain.TaskActionInstallDockerEngine, domain.TaskActionUninstallDockerEngine,
			domain.TaskActionInstallDockerSwarm, domain.TaskActionUninstallDockerSwarm, domain.TaskActionReconfigureDockerSwarm, domain.TaskActionRebuildDockerSwarm, domain.TaskActionRecoverDockerSwarmRebuild,
			domain.TaskActionInstallTraefikGateway, domain.TaskActionUninstallTraefikGateway,
			domain.TaskActionInstallFrps, domain.TaskActionUninstallFrps:
			return task
		}
	}
	return nil
}

func (r *Runtime) validateJenkinsUninstall(ctx context.Context, serverID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	impact := calculateServerReferenceImpact(state, serverID)
	if impact.HasReason("SERVER_REFERENCED_AS_BUILD_SERVER") {
		return errors.New("JENKINS_UNINSTALL_BLOCKED_APPLICATION_CONFIG")
	}
	if impact.HasReason("SERVER_REFERENCED_BY_ACTIVE_RELEASE_BUILD") {
		return errors.New("JENKINS_UNINSTALL_BLOCKED_RELEASE_PLAN")
	}
	if impact.HasReason("SERVER_HAS_ACTIVE_JENKINS_RUN") {
		return errors.New("JENKINS_UNINSTALL_BLOCKED_ACTIVE_RUN")
	}
	return nil
}

func releasePlanTerminal(plan domain.Object) bool {
	switch stringValue(plan["status"]) {
	case "succeeded", "failed", "expired", "rolled_back", "cancelled":
		return true
	default:
		return false
	}
}

func jenkinsRunBuildServerID(state domain.State, run domain.Object) string {
	releasePlan := findObject(state.ReleasePlans, stringValue(run["releasePlanId"]))
	if releasePlan == nil {
		return ""
	}
	return releasePlanBuildServerID(releasePlan)
}

func deploymentBuildServerID(config domain.Object) string {
	if value := stringValue(config["buildServerId"]); value != "" {
		return value
	}
	return stringValue(objectMapFromAny(config["deliveryConfig"])["buildServerId"])
}

func releasePlanBuildServerID(plan domain.Object) string {
	if value := stringValue(plan["buildServerId"]); value != "" {
		return value
	}
	buildSnapshot := objectMapFromAny(plan["buildSnapshot"])
	if value := stringValue(buildSnapshot["buildServerId"]); value != "" {
		return value
	}
	return stringValue(objectMapFromAny(buildSnapshot["deliveryConfig"])["buildServerId"])
}
