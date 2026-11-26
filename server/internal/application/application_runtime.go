package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// RefreshApplicationBeforeDelete 在删除或迁移管理入口前确认远端服务确实已不存在。
func (r *Runtime) RefreshApplicationBeforeDelete(ctx context.Context, applicationID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	if findObject(state.Applications, applicationID) == nil {
		return errors.New("APPLICATION_NOT_FOUND")
	}
	if active := applicationOperationActive(state, applicationID); active != nil {
		return &TaskAlreadyActiveError{ActiveTaskID: active.ID}
	}
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	if stringValue(config["deploymentManagerServerId"]) == "" && deployedLocation(findObject(state.Applications, applicationID)) == nil {
		return nil
	}
	snapshot, err := r.ApplicationRuntime(ctx, applicationID)
	if err != nil {
		return err
	}
	if snapshot.Availability == "unavailable" {
		return errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
	}
	if snapshot.Availability != "not_deployed" || len(snapshot.Services) > 0 {
		return errors.New("APPLICATION_RUNTIME_UNINSTALL_REQUIRED")
	}
	return nil
}

func (r *Runtime) checkedApplicationManager(ctx context.Context, applicationID string, next domain.Object) (string, error) {
	nextManager, provided := next["deploymentManagerServerId"]
	if !provided {
		return "", nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "", err
	}
	existing := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	manager := stringValue(existing["deploymentManagerServerId"])
	location := deployedLocation(findObject(state.Applications, applicationID))
	if manager == stringValue(nextManager) || manager == "" && location == nil {
		return manager, nil
	}
	if manager == "" && stringValue(nextManager) == stringValue(location["deploymentManagerServerId"]) {
		snapshot, err := r.ApplicationRuntime(ctx, applicationID)
		if err != nil {
			return manager, err
		}
		if snapshot.Availability == "unavailable" {
			return manager, errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
		}
		return manager, nil
	}
	return manager, r.RefreshApplicationBeforeDelete(ctx, applicationID)
}

func (r *Runtime) ApplicationRuntime(ctx context.Context, applicationID string) (ApplicationRuntimeSnapshot, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	input, err := r.applicationServiceInput(queryCtx, applicationID, nil)
	if err != nil {
		if err.Error() == "APPLICATION_NOT_FOUND" {
			return ApplicationRuntimeSnapshot{}, err
		}
		return r.recordUnavailableApplicationRuntime(ctx, applicationID, err)
	}
	if r.ApplicationRuntimeExecutor == nil {
		return r.recordUnavailableApplicationRuntime(ctx, applicationID, errors.New("APPLICATION_EXECUTOR_UNAVAILABLE"))
	}
	snapshot, err := r.ApplicationRuntimeExecutor.Inspect(queryCtx, input)
	if err != nil {
		return r.recordUnavailableApplicationRuntime(ctx, applicationID, err)
	}
	if err := r.observeAndRecordRuntime(ctx, queryCtx, input, snapshot); err != nil {
		return ApplicationRuntimeSnapshot{}, err
	}
	return snapshot, nil
}

// 可选的端点探测超时后仍保存已核实的副本状态，写入受调用方取消和独立时限约束。
func (r *Runtime) observeAndRecordRuntime(ctx, queryCtx context.Context, input ApplicationServiceInput, snapshot ApplicationRuntimeSnapshot) error {
	r.observeApplicationEndpoints(queryCtx, input, snapshot)
	saveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return r.recordApplicationRuntime(saveCtx, input.ApplicationID, snapshot)
}

// 查询失败时保留最后的服务归属，端点证据则立即失效。
func (r *Runtime) recordUnavailableApplicationRuntime(ctx context.Context, applicationID string, cause error) (ApplicationRuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ApplicationRuntimeSnapshot{}, err
	}
	result := unavailableApplicationRuntime(cause)
	saveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := r.Store.MutateState(saveCtx, func(state *domain.State) error {
		app := findObject(state.Applications, applicationID)
		prior := objectMapFromAny(app["runtimeSnapshot"])
		if len(prior) > 0 && stringValue(prior["checkedAt"]) <= result.CheckedAt {
			prior["availability"], prior["unavailableReason"], prior["checkedAt"] = result.Availability, result.UnavailableReason, result.CheckedAt
			app["runtimeSnapshot"] = prior
		}
		return nil
	})
	return result, err
}

func unavailableApplicationRuntime(err error) ApplicationRuntimeSnapshot {
	availability, status := "unavailable", "unknown"
	if err != nil && (err.Error() == "APPLICATION_DEPLOYMENT_MANAGER_REQUIRED" || err.Error() == "APPLICATION_NOT_DEPLOYED") {
		availability, status = "not_deployed", "not_deployed"
	}
	return ApplicationRuntimeSnapshot{Availability: availability, UnavailableReason: applicationUnavailableReason(err), Status: status, CheckedAt: domain.Now(), Services: []ApplicationServiceState{}}
}

func (r *Runtime) ApplicationServiceLogs(ctx context.Context, applicationID, serviceSlotKey string) (ApplicationServiceLogs, error) {
	ctx, cancel := context.WithTimeout(ctx, applicationLogTimeout)
	defer cancel()
	input, err := r.applicationServiceInput(ctx, applicationID, nil)
	if err != nil && err.Error() == "APPLICATION_NOT_FOUND" {
		return ApplicationServiceLogs{}, err
	}
	result := ApplicationServiceLogs{Availability: "unavailable", CheckedAt: domain.Now(), Items: []ApplicationServiceLog{}}
	if err == nil {
		if r.ApplicationRuntimeExecutor == nil {
			err = errors.New("APPLICATION_EXECUTOR_UNAVAILABLE")
		} else {
			input.LogSecrets, err = r.applicationLogSecrets(ctx, applicationID, serviceSlotKey)
			if err == nil {
				result, err = r.ApplicationRuntimeExecutor.Logs(ctx, input, serviceSlotKey)
			}
		}
	}
	if err != nil {
		result.Availability = "unavailable"
		if err.Error() == "APPLICATION_DEPLOYMENT_MANAGER_REQUIRED" || err.Error() == "APPLICATION_NOT_DEPLOYED" {
			result.Availability = "not_deployed"
		}
		result.UnavailableReason = applicationUnavailableReason(err)
	}
	return result, nil
}

func (r *Runtime) executeApplicationServiceOperation(ctx context.Context, task domain.Task) (string, error) {
	if r.ApplicationRuntimeExecutor == nil {
		return "应用远端执行器不可用", errors.New("APPLICATION_EXECUTOR_UNAVAILABLE")
	}
	input, err := r.applicationServiceInput(ctx, task.TargetID, objectMapFromAny(task.Payload["runtimeConfigSnapshot"]))
	if err != nil {
		return "读取应用运行配置失败", err
	}
	operation := strings.TrimSuffix(task.Action, "_application")
	removeVolumes, _ := task.Payload["removeVolumes"].(bool)
	inputs := objectSlice(task.Payload["applicationInputs"])
	if operation == "start" || operation == "restart" {
		input.PreviousInputKeys = objectStringSlice(task.Payload["previousInputKeys"])
		input.Environment = map[string]map[string]string{}
		for _, srv := range input.Services {
			values, err := r.resolvedStageValues(ctx, inputs, "runtime", srv.ServiceSlotKey)
			if err != nil {
				return "无法读取本次应用凭据", err
			}
			if len(values) > 0 || len(input.PreviousInputKeys) > 0 {
				input.Environment[srv.ServiceSlotKey] = values
			}
		}
	}
	r.appendRuntimeMessage(task, "swarm", operation, "info", "正在对受管 Swarm 服务执行 "+operation)
	snapshot, executionErr := r.ApplicationRuntimeExecutor.Apply(ctx, input, operation, removeVolumes)
	// 远端可能已经部分生效；使用独立有界上下文读取结果，不回写预测状态。
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	queryCtx, stopQuery := context.WithTimeout(refreshCtx, 10*time.Second)
	defer stopQuery()
	if executionErr != nil {
		if refreshed, queryErr := r.ApplicationRuntimeExecutor.Inspect(queryCtx, input); queryErr == nil {
			snapshot = refreshed
		}
	}
	if snapshot.CheckedAt != "" && (snapshot.Availability == "available" || snapshot.Availability == "not_deployed") {
		if err := r.observeAndRecordRuntime(refreshCtx, queryCtx, input, snapshot); err != nil {
			return "保存远端运行事实失败", err
		}
	}
	if executionErr != nil {
		_ = r.Store.MutateState(refreshCtx, func(state *domain.State) error {
			app := findObject(state.Applications, task.TargetID)
			if app != nil {
				app["lastRuntimeError"] = applicationUnavailableReason(executionErr)
				app["lastRuntimeErrorAt"] = domain.Now()
			}
			return nil
		})
		return "远端操作未全部完成，请查看实际服务状态", executionErr
	}
	if _, has := task.Payload["applicationInputs"]; has && (operation == "restart" || operation == "start") {
		for _, service := range snapshot.Services {
			if service.DesiredReplicas == 0 {
				return "已更新运行中的服务，已停止服务将在启动时应用", nil
			}
		}
		if err := r.verifyRestartedContainerInputs(ctx, objectMapFromAny(task.Payload["runtimeConfigSnapshot"]), snapshot, task.TargetID); err != nil {
			return "应用初始化或健康核验失败，变量仍待生效", err
		}
		if err := r.Store.MutateState(refreshCtx, func(state *domain.State) error {
			recordAppliedApplicationInputs(state, task.TargetID, inputs, "runtime")
			return nil
		}); err != nil {
			return "保存应用变量修订失败", err
		}
	}
	return "受管 Swarm 服务操作已完成并验证", nil
}

func (r *Runtime) recordApplicationRuntime(ctx context.Context, applicationID string, snapshot ApplicationRuntimeSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	value := domain.Object{}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		app := findObject(state.Applications, applicationID)
		if app == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		previous := objectMapFromAny(app["runtimeSnapshot"])
		if stringValue(previous["checkedAt"]) > snapshot.CheckedAt {
			return nil
		}
		if snapshot.DeploymentManagerServerID != "" {
			config := runtimeLocationConfig(app, findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID))
			if config["deploymentManagerServerId"] != snapshot.DeploymentManagerServerID {
				return errors.New("APPLICATION_DEPLOYMENT_LOCATION_CHANGED")
			}
			if err := validateDeploymentLocation(app, config, snapshot.DeploymentClusterID, true); err != nil {
				return err
			}
			if len(snapshot.Services) > 0 {
				app["deploymentLocation"] = domain.Object{"deploymentManagerServerId": snapshot.DeploymentManagerServerID, "deploymentClusterId": snapshot.DeploymentClusterID}
			} else if snapshot.Availability == "not_deployed" {
				active := applicationOperationActive(*state, applicationID)
				if active == nil || active.Action != string(domain.TaskActionDeployApplication) && active.Action != string(domain.TaskActionRequestJenkinsRelease) {
					delete(app, "deploymentLocation")
				}
			}
		}
		app["runtimeSnapshot"], app["status"] = value, snapshot.Status
		delete(app, "lastRuntimeError")
		delete(app, "lastRuntimeErrorAt")
		state.ApplicationInstances = removeApplicationInstances(state.ApplicationInstances, applicationID)
		for _, service := range snapshot.Services {
			for _, task := range service.Tasks {
				switch task.Status {
				case "complete", "shutdown", "failed", "rejected", "remove", "orphaned":
					continue
				}
				state.ApplicationInstances = append(state.ApplicationInstances, domain.Object{
					"id": task.ID, "applicationId": applicationID, "projectId": app["projectId"],
					"serviceId": service.ServiceID, "serviceName": service.ServiceName, "serviceSlotKey": service.ServiceSlotKey,
					"nodeId": task.NodeID, "serverId": task.ServerID, "status": task.Status,
					"checkedAt": snapshot.CheckedAt, "discoveredAt": snapshot.CheckedAt,
				})
			}
		}
		return nil
	})
}
