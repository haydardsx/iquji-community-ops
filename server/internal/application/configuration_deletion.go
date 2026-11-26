package application

import (
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func validateConfigurationDeletion(state domain.State, collection, id string) error {
	if collection == "release_plans" {
		plan := findObject(state.ReleasePlans, id)
		if plan != nil && (stringValue(plan["operationRunId"]) != "" || (plan["status"] != "draft" && plan["status"] != "queued")) {
			return errors.New("RELEASE_PLAN_STATE_INVALID")
		}
	}
	if collection == "projects" {
		if len(filterObjectsBy(state.Applications, "projectId", id)) > 0 {
			return errors.New("PROJECT_HAS_APPLICATIONS")
		}
		for _, task := range state.Tasks {
			if task.TargetKind == "project" && task.TargetID == id && domain.IsTaskActive(task.Status) {
				return errors.New("PROJECT_OPERATION_ACTIVE")
			}
		}
	}
	if collection != "applications" {
		return nil
	}
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", id)
	outputIDs := []string{}
	for _, o := range objectSlice(config["outputVariables"]) {
		outputIDs = append(outputIDs, stringValue(o["id"]))
	}
	if err := outputUsageError(state, "APPLICATION_OUTPUT_IN_USE", id, outputIDs, false, false); err != nil {
		return err
	}
	if applicationOperationActive(state, id) != nil {
		return errors.New("APPLICATION_OPERATION_ACTIVE")
	}
	application := findObject(state.Applications, id)
	if deployedLocation(application) != nil ||
		len(filterObjectsBy(state.ApplicationInstances, "applicationId", id)) > 0 {
		return errors.New("APPLICATION_RUNTIME_UNINSTALL_REQUIRED")
	}
	for _, route := range filterObjectsBy(state.Routes, "applicationId", id) {
		if len(objectMapFromAny(route["lastApplied"])) > 0 || len(objectMapFromAny(route["pendingRemote"])) > 0 {
			return errors.New("APPLICATION_ROUTE_REVOKE_REQUIRED")
		}
	}
	return nil
}

func cleanupDeletedConfiguration(state *domain.State, collection, id string) {
	if collection == "projects" {
		for _, server := range state.Servers {
			server["projectIds"] = withoutString(objectStringSlice(server["projectIds"]), id)
		}
		return
	}
	if collection != "applications" {
		return
	}
	state.ApplicationDeploymentConfigs = withoutApplicationObjects(state.ApplicationDeploymentConfigs, id)
	state.Routes = withoutApplicationObjects(state.Routes, id)
	state.RuntimeLogStreams = withoutApplicationObjects(state.RuntimeLogStreams, id)
	for _, project := range state.Projects {
		project["applicationIds"] = withoutString(objectStringSlice(project["applicationIds"]), id)
	}
	plans := []domain.Object{}
	for _, plan := range state.ReleasePlans {
		if stringValue(plan["applicationId"]) == id && (stringValue(plan["status"]) == "draft" || stringValue(plan["status"]) == "queued") {
			continue
		}
		plans = append(plans, plan)
	}
	state.ReleasePlans = plans
}

// cleanupOrphanApplicationConfigurations 清理应用记录已经删除后遗留的部署配置。
// 这类配置没有可操作的应用入口，却仍会阻止服务器安全删除；只删除明确
// 找不到对应应用的配置，不触碰仍存在应用的部署材料。
func cleanupOrphanApplicationConfigurations(state *domain.State) {
	valid := make(map[string]struct{}, len(state.Applications))
	for _, application := range state.Applications {
		valid[stringValue(application["id"])] = struct{}{}
	}
	kept := make([]domain.Object, 0, len(state.ApplicationDeploymentConfigs))
	for _, config := range state.ApplicationDeploymentConfigs {
		if _, ok := valid[stringValue(config["applicationId"])]; ok {
			kept = append(kept, config)
		}
	}
	state.ApplicationDeploymentConfigs = kept
}

func withoutApplicationObjects(items []domain.Object, applicationID string) []domain.Object {
	result := make([]domain.Object, 0, len(items))
	for _, item := range items {
		if stringValue(item["applicationId"]) != applicationID {
			result = append(result, item)
		}
	}
	return result
}

func withoutString(items []string, target string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item != target {
			result = append(result, item)
		}
	}
	return result
}
