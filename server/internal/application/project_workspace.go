package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) Workspace(ctx context.Context, projectID string, applicationID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	project := findObject(state.Projects, projectID)
	if project == nil {
		return nil, errors.New("PROJECT_NOT_FOUND")
	}
	applications := filterObjectsBy(state.Applications, "projectId", projectID)
	applicationIDs := map[string]bool{}
	for index, application := range applications {
		applicationIDs[stringValue(application["id"])] = true
		applications[index] = applicationWithInputStatus(state, application)
	}
	if applicationID != "" && !applicationIDs[applicationID] {
		return nil, errors.New("APPLICATION_NOT_FOUND")
	}
	configs := projectApplicationObjects(state.ApplicationDeploymentConfigs, applicationIDs)
	instances := projectApplicationObjects(state.ApplicationInstances, applicationIDs)
	routes := projectApplicationObjects(state.Routes, applicationIDs)
	releasePlans := filterObjectsBy(state.ReleasePlans, "projectId", projectID)
	candidateServers, relatedServers := projectServers(state, project, configs, instances)
	selectedApplication := domain.Object(nil)
	if applicationID != "" {
		selectedApplication = applicationDetailFromState(state, applicationID)
	}
	return map[string]any{
		"project": projectWithApplications(project, applications), "applications": applications,
		"availableOutputs": availableApplicationOutputs(state, projectID), "selectedApplication": selectedApplication, "applicationInstances": instances,
		"applicationDeploymentConfigs": configs, "routes": routes,
		"relatedOperations": projectOperations(state, projectID, applicationIDs, routes, releasePlans),
		"servers":           candidateServers, "relatedServers": relatedServers,
		"domains": state.Domains, "certificates": state.Certificates,
		"resolvers": state.Resolvers, "ingresses": state.Ingresses,
	}, nil
}

func projectApplicationObjects(items []domain.Object, applicationIDs map[string]bool) []domain.Object {
	result := []domain.Object{}
	for _, item := range items {
		if applicationIDs[stringValue(item["applicationId"])] {
			result = append(result, item)
		}
	}
	return result
}

func projectServers(state domain.State, project domain.Object, configs, instances []domain.Object) ([]domain.Object, []domain.Object) {
	projectID := stringValue(project["id"])
	relatedIDs := stringSet(objectStringSlice(project["serverIds"]))
	for _, instance := range instances {
		relatedIDs[stringValue(instance["serverId"])] = true
	}
	latest := latestSwarmSnapshots(state)
	candidates, related := []domain.Object{}, []domain.Object{}
	for _, server := range state.Servers {
		id := stringValue(server["id"])
		if containsString(objectStringSlice(server["projectIds"]), projectID) {
			relatedIDs[id] = true
		}
		for _, config := range configs {
			if applicationConfigReferencesServer(config, id) {
				relatedIDs[id] = true
			}
		}
		if serverAvailableToProject(server, projectID) {
			candidates = append(candidates, enrichServerWithSnapshot(server, state, latest[id]))
		}
		if relatedIDs[id] {
			related = append(related, enrichServerWithSnapshot(server, state, latest[id]))
		}
	}
	return candidates, related
}

func projectOperations(state domain.State, projectID string, applicationIDs map[string]bool, routes, plans []domain.Object) []domain.OperationRun {
	routeIDs, planIDs := map[string]bool{}, map[string]bool{}
	taskIDs := map[string]bool{}
	for _, task := range state.Tasks {
		if stringValue(task.Payload["projectId"]) == projectID || applicationIDs[taskApplicationID(state, task)] {
			taskIDs[task.ID] = true
		}
	}
	for _, route := range routes {
		routeIDs[stringValue(route["id"])] = true
	}
	for _, plan := range plans {
		planIDs[stringValue(plan["id"])] = true
	}
	result := []domain.OperationRun{}
	for _, operation := range state.OperationRuns {
		if taskIDs[operation.ID] || operation.TargetKind == "project" && operation.TargetID == projectID ||
			operation.TargetKind == "application" && applicationIDs[operation.TargetID] ||
			operation.TargetKind == "route" && routeIDs[operation.TargetID] ||
			operation.TargetKind == "release_plan" && planIDs[operation.TargetID] {
			result = append(result, operation)
		}
	}
	return result
}

func projectWithApplications(project domain.Object, applications []domain.Object) domain.Object {
	result := cloneObject(project)
	ids := make([]string, 0, len(applications))
	for _, application := range applications {
		ids = append(ids, stringValue(application["id"]))
	}
	result["applicationIds"] = ids
	return result
}
