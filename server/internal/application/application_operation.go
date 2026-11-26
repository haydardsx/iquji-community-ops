package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// applicationOperationActive 将应用及其路由、发布计划的互斥范围合并到同一个应用。
func applicationOperationActive(state domain.State, applicationID string) *domain.Task {
	for _, task := range state.Tasks {
		if domain.IsTaskActive(task.Status) && taskApplicationID(state, task) == applicationID || swarmMaintenanceBlocksTask(task) && applicationUsesSwarmServer(state, applicationID, task.TargetID) {
			copy := task
			return &copy
		}
	}
	return nil
}

func taskApplicationID(state domain.State, task domain.Task) string {
	switch task.TargetKind {
	case "application":
		return task.TargetID
	case "route":
		if route := findObject(state.Routes, task.TargetID); route != nil {
			return stringValue(route["applicationId"])
		}
	case "release_plan":
		if plan := findObject(state.ReleasePlans, task.TargetID); plan != nil {
			return stringValue(plan["applicationId"])
		}
	}
	return stringValue(task.Payload["applicationId"])
}

func (r *Runtime) CreateApplicationOperation(ctx context.Context, applicationID, operation string, removeVolumes bool) (domain.Task, error) {
	if removeVolumes {
		return domain.Task{}, errors.New("APPLICATION_VOLUME_REMOVAL_UNSUPPORTED")
	}
	if operation != "start" && operation != "stop" && operation != "restart" && operation != "uninstall" {
		return domain.Task{}, errors.New("APPLICATION_ACTION_UNSUPPORTED")
	}
	var task domain.Task
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		app := findObject(state.Applications, applicationID)
		if app == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		if operation == "uninstall" {
			for _, route := range state.Routes {
				if stringValue(route["applicationId"]) == applicationID && (len(objectMapFromAny(route["lastApplied"])) > 0 || len(objectMapFromAny(route["pendingRemote"])) > 0) {
					return errors.New("APPLICATION_ROUTE_DISABLE_REQUIRED")
				}
			}
		}
		config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
		config = runtimeLocationConfig(app, config)
		payload := map[string]any{
			"applicationId": applicationID, "removeVolumes": removeVolumes,
			"projectId":             app["projectId"],
			"runtimeConfigSnapshot": domain.CloneObjectWithoutUserReferences(config),
		}
		if operation == "start" || operation == "restart" {
			snapshot, err := applicationStageInputSnapshot(*state, applicationID, config, "runtime")
			if err != nil {
				return err
			}
			payload["applicationInputs"] = snapshot
			payload["previousInputKeys"] = app["appliedRuntimeKeys"]
		}
		task = newTask(operation+"_application", "application", applicationID, stringValue(app["name"]), payload)
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func (r *Runtime) applicationServiceInput(ctx context.Context, applicationID string, configSnapshot domain.Object) (ApplicationServiceInput, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return ApplicationServiceInput{}, err
	}
	app := findObject(state.Applications, applicationID)
	if app == nil {
		return ApplicationServiceInput{}, errors.New("APPLICATION_NOT_FOUND")
	}
	config := configSnapshot
	if config == nil {
		config = findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	}
	config = runtimeLocationConfig(app, config)
	serverID := stringValue(config["deploymentManagerServerId"])
	location := deployedLocation(app)
	if serverID == "" && location != nil {
		return ApplicationServiceInput{}, errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
	}
	if serverID == "" {
		return ApplicationServiceInput{}, errors.New("APPLICATION_DEPLOYMENT_MANAGER_REQUIRED")
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return ApplicationServiceInput{}, errors.New("SERVER_NOT_FOUND")
	}
	if !serverAvailableToProject(server, stringValue(app["projectId"])) {
		return ApplicationServiceInput{}, errors.New("APPLICATION_DEPLOYMENT_MANAGER_PROJECT_MISMATCH")
	}
	cluster := firstNonEmptyString(stringValue(location["deploymentClusterId"]), stringValue(config["deploymentClusterId"]), stringValue(serverRuntimeMetrics(server)["swarmClusterId"]))
	if err := validateDeploymentLocation(app, config, cluster, true); err != nil {
		return ApplicationServiceInput{}, err
	}
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return ApplicationServiceInput{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	input := ApplicationServiceInput{
		ApplicationID: applicationID, Server: cloneObject(server), SSHKey: key,
		Network: firstNonEmptyString(stringValue(objectMapFromAny(config["swarmRuntime"])["overlayNetwork"]), "iquops-public"), Outputs: objectSlice(config["outputVariables"]),
		ClusterID:            cluster,
		VerifyLegacyLocation: location != nil && stringValue(location["deploymentClusterId"]) == "",
		Services:             []ApplicationServiceTarget{}, Servers: state.Servers,
	}
	if input.VerifyLegacyLocation {
		for _, service := range objectSlice(objectMapFromAny(app["runtimeSnapshot"])["services"]) {
			if id := stringValue(service["serviceId"]); id != "" {
				input.LegacyServiceIDs = append(input.LegacyServiceIDs, id)
			}
		}
	}
	for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
		input.Services = append(input.Services, ApplicationServiceTarget{
			ServiceSlotKey: stringValue(service["slotKey"]), ServiceName: stringValue(service["serviceName"]),
			ServerID: stringValue(service["serverId"]), Replicas: int(numberValue(service["replicas"])),
		})
	}
	return input, nil
}

func applicationUnavailableReason(err error) string {
	if err == nil {
		return ""
	}
	code, _, _ := strings.Cut(err.Error(), ":")
	if domain.IsPublicDiagnosticCode(code) && (strings.HasPrefix(code, "APPLICATION_") || strings.HasPrefix(code, "SWARM_") || strings.HasPrefix(code, "SERVER_") || strings.HasPrefix(code, "SSH_")) {
		return code
	}
	return "APPLICATION_REMOTE_QUERY_FAILED"
}
