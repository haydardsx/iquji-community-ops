package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) DashboardSummary(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	statusCounts := map[string]int{"running": 0, "errored": 0, "idle": 0}
	for _, instance := range state.ApplicationInstances {
		status, _ := instance["status"].(string)
		switch status {
		case "running", "healthy", "deployed":
			statusCounts["running"]++
		case "failed", "error", "unhealthy":
			statusCounts["errored"]++
		default:
			statusCounts["idle"]++
		}
	}
	recent := make([]domain.Object, 0, len(state.ReleasePlans))
	for _, plan := range state.ReleasePlans {
		recent = append(recent, dashboardDeployment(plan, state.Applications))
	}
	if len(recent) > 5 {
		recent = recent[:5]
	}
	return map[string]any{
		"metrics": map[string]any{
			"projectCount":         len(state.Projects),
			"applicationCount":     len(state.Applications),
			"deploymentsLast7Days": deploymentsLast7Days(state.Tasks),
		},
		"statusCounts": map[string]any{
			"running": statusCounts["running"],
			"errored": statusCounts["errored"],
			"idle":    statusCounts["idle"],
		},
		"recentDeployments": recent,
	}, nil
}

func dashboardDeployment(plan domain.Object, applications []domain.Object) domain.Object {
	applicationID := stringValue(plan["applicationId"])
	applicationName := stringValue(plan["applicationName"])
	if applicationName == "" {
		for _, application := range applications {
			if stringValue(application["id"]) == applicationID {
				applicationName = stringValue(application["name"])
				break
			}
		}
	}
	if applicationName == "" {
		applicationName = "unknown"
	}
	status := stringValue(plan["status"])
	if status == "" {
		status = "succeeded"
	}
	createdAt := stringValue(plan["createdAt"])
	if createdAt == "" {
		createdAt = domain.Now()
	}
	versionLabel := stringValue(plan["versionLabel"])
	if versionLabel == "" {
		versionLabel = stringValue(plan["branch"])
	}
	if versionLabel == "" {
		versionLabel = stringValue(plan["version"])
	}
	if versionLabel == "" {
		versionLabel = "snapshot"
	}
	return domain.Object{
		"id":              stringValue(plan["id"]),
		"projectId":       stringValue(plan["projectId"]),
		"applicationId":   applicationID,
		"applicationName": applicationName,
		"versionLabel":    versionLabel,
		"status":          status,
		"createdAt":       createdAt,
	}
}

func (r *Runtime) CreateObject(ctx context.Context, collection string, prefix string, input domain.Object) (domain.Object, error) {
	if collection == "servers" && (input["connectionProxy"] != nil || input["sshHostFingerprint"] != nil) {
		return nil, errors.New("SERVER_CONNECTION_REQUIRES_DEDICATED_ENDPOINT")
	}
	if input == nil {
		input = domain.Object{}
	}
	if collection == "applications" {
		for _, reserved := range []string{"inputChanges", "appliedInputRevisions", "appliedInputSignatures", "publishedBuildOutputs", "appliedRuntimeKeys", "runtimeSnapshot", "deploymentLocation", "inputStatus"} {
			if _, ok := input[reserved]; ok {
				return nil, errors.New("APPLICATION_READ_ONLY_FIELD")
			}
		}
		if _, exists := input["sourceConfig"]; exists {
			return nil, errors.New("APPLICATION_SOURCE_REQUIRES_DEPLOYMENT_CONFIG")
		}
	}
	now := domain.Now()
	item := domain.CloneObjectWithoutUserReferences(input)
	item["id"] = domain.NewID(prefix)
	item["createdAt"] = now
	item["updatedAt"] = now
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if collection == "servers" && input["jenkinsAccess"] != nil {
			return errors.New("JENKINS_ACCESS_READ_ONLY")
		}
		if collection == "servers" {
			if _, err := desiredDockerRegistryMirrorURL(item); err != nil {
				return err
			}
			if err := rejectPendingServerCredentials(*state, objectStringSlice(item["secretAssetIds"])); err != nil {
				return err
			}
		}
		if collection == "applications" && findObject(state.Projects, stringValue(item["projectId"])) == nil {
			return errors.New("PROJECT_NOT_FOUND")
		}
		item = normalizeCreatedObject(collection, item, state)
		if collection == "projects" {
			delete(item, "builtin")
		}
		if collection == "applications" {
			if templateID := stringValue(item["templateId"]); templateID != "" {
				if err := instantiateApplicationTemplate(state, item, templateID); err != nil {
					return err
				}
			}
			delete(item, "templateId")
		}
		var err error
		item, err = normalizeDomainAsset(collection, item, *state, nil)
		if err != nil {
			return err
		}
		target, err := collectionRef(state, collection)
		if err != nil {
			return err
		}
		*target = append(*target, item)
		state.OperationLogs = append(state.OperationLogs, newOperationLog("create_"+collection, collection, stringValue(item["id"]), "succeeded", "已创建资源 "+collection, ""))
		return nil
	})
	return item, err
}

func normalizeCreatedObject(collection string, item domain.Object, state *domain.State) domain.Object {
	switch collection {
	case "projects":
		item["description"] = firstNonEmptyString(stringValue(item["description"]), "")
		if _, ok := item["tags"]; !ok {
			item["tags"] = []string{}
		}
		item["applicationIds"] = objectStringSlice(item["applicationIds"])
		item["serverIds"] = objectStringSlice(item["serverIds"])
	case "applications":
		item["status"] = firstNonEmptyString(stringValue(item["status"]), "idle")
		item["runtimeMode"] = "swarm"
		item["sourceKind"] = firstNonEmptyString(stringValue(item["sourceKind"]), "git")
		item["deploymentEngine"] = firstNonEmptyString(stringValue(item["deploymentEngine"]), "jenkins-swarm")
		item["upgradeStrategy"] = firstNonEmptyString(stringValue(item["upgradeStrategy"]), "downtime_replace")
	case "servers":
		domain.NormalizeServerConnections([]domain.Object{item})
		item["status"] = firstNonEmptyString(stringValue(item["status"]), "idle")
		item["port"] = numberOrDefault(item["port"], 22)
		item["sshUser"] = firstNonEmptyString(stringValue(item["sshUser"]), "root")
		item["environment"] = firstNonEmptyString(stringValue(item["environment"]), "staging")
		item["labels"] = objectStringSlice(item["labels"])
		item["projectIds"] = objectStringSlice(item["projectIds"])
		item["secretAssetIds"] = objectStringSlice(item["secretAssetIds"])
		if _, ok := item["platformProfile"]; !ok {
			item["platformProfile"] = defaultServerPlatformProfile()
		}
	case "routes":
		item = normalizeRouteObject(item, state)
	case "certificates":
		item["status"] = firstNonEmptyString(stringValue(item["status"]), "idle")
		item["renewalStatus"] = firstNonEmptyString(stringValue(item["renewalStatus"]), "pending")
	}
	return item
}

func (r *Runtime) UpdateObject(ctx context.Context, collection string, id string, input domain.Object) (domain.Object, error) {
	if collection == "servers" {
		for _, field := range []string{"connectionProxy", "sshHostFingerprint", "jenkinsAccess"} {
			if _, exists := input[field]; exists {
				return nil, errors.New("SERVER_CONNECTION_REQUIRES_DEDICATED_ENDPOINT")
			}
		}
	}
	if collection == "projects" && (input["builtin"] != nil || (id == domain.BuiltinProjectID && input["name"] != nil && input["name"] != "基础服务")) {
		return nil, errors.New("BUILTIN_PROJECT_PROTECTED")
	}
	if input == nil {
		input = domain.Object{}
	}
	if collection == "applications" {
		for _, reserved := range []string{"inputChanges", "appliedInputRevisions", "appliedInputSignatures", "publishedBuildOutputs", "appliedRuntimeKeys", "runtimeSnapshot", "deploymentLocation", "inputStatus"} {
			if _, ok := input[reserved]; ok {
				return nil, errors.New("APPLICATION_READ_ONLY_FIELD")
			}
		}
		if _, exists := input["sourceConfig"]; exists {
			return nil, errors.New("APPLICATION_SOURCE_REQUIRES_DEPLOYMENT_CONFIG")
		}
	}
	var result domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		target, err := collectionRef(state, collection)
		if err != nil {
			return err
		}
		for index := range *target {
			if stringValue((*target)[index]["id"]) != id {
				continue
			}
			next := cloneObject((*target)[index])
			for key, value := range domain.CloneObjectWithoutUserReferences(input) {
				if key == "id" || key == "createdAt" {
					continue
				}
				if collection == "servers" && key == "platformProfile" {
					profile := cloneObject(objectMapFromAny(next[key]))
					for profileKey, profileValue := range objectMapFromAny(value) {
						profile[profileKey] = profileValue
					}
					next[key] = profile
					continue
				}
				next[key] = value
			}
			next["updatedAt"] = domain.Now()
			if collection == "servers" {
				if _, supplied := objectMapFromAny(input["platformProfile"])["dockerRegistryMirrorUrl"]; supplied {
					if _, err := desiredDockerRegistryMirrorURL(next); err != nil {
						return err
					}
				}
				if err := rejectPendingServerCredentials(*state, objectStringSlice(next["secretAssetIds"])); err != nil {
					return err
				}
			}
			next, err = normalizeDomainAsset(collection, next, *state, (*target)[index])
			if err != nil {
				return err
			}
			(*target)[index] = next
			result = next
			if collection == "servers" {
				result = serverWithPublicMirror(next)
			}
			state.OperationLogs = append(state.OperationLogs, newOperationLog("update_"+collection, collection, id, "succeeded", "已更新资源 "+collection, ""))
			return nil
		}
		return errors.New("RESOURCE_NOT_FOUND")
	})
	return result, err
}

func (r *Runtime) DeleteObject(ctx context.Context, collection string, id string) error {
	if collection == "projects" && id == domain.BuiltinProjectID {
		return errors.New("BUILTIN_PROJECT_PROTECTED")
	}
	if collection == "applications" {
		if err := r.RefreshApplicationBeforeDelete(ctx, id); err != nil {
			return err
		}
	}
	var deleteRefs []string
	if collection == "servers" {
		deleteRefs = []string{proxyMaterialRef(id)}
	}
	return r.Store.MutateStateWithSecretChanges(ctx, nil, deleteRefs, func(state *domain.State) error {
		if collection == "servers" {
			cleanupOrphanApplicationConfigurations(state)
		}
		if err := validateConfigurationDeletion(*state, collection, id); err != nil {
			return err
		}
		if err := validateDomainAssetDeletion(*state, collection, id); err != nil {
			return err
		}
		if collection == "servers" {
			impact := calculateServerReferenceImpact(*state, id)
			if impact.Blocked() {
				return &ServerDeleteBlockedError{Impact: impact.Object()}
			}
		}
		target, err := collectionRef(state, collection)
		if err != nil {
			return err
		}
		orphanIngressID := ""
		deletedItem := findObject(*target, id)
		if collection == "resolvers" {
			if resolver := findObject(state.Resolvers, id); resolver != nil {
				orphanIngressID = stringValue(resolver["ingressId"])
			}
		}
		next := make([]domain.Object, 0, len(*target))
		found := false
		for _, item := range *target {
			if stringValue(item["id"]) == id {
				found = true
				continue
			}
			next = append(next, item)
		}
		if !found {
			return errors.New("RESOURCE_NOT_FOUND")
		}
		*target = next
		if collection == "ingresses" {
			if err := cascadeIngressDeletion(state, id); err != nil {
				return err
			}
		}
		if collection == "certificates" {
			cascadeCertificateDeletion(state, deletedItem)
		}
		if collection == "resolvers" {
			cascadeOrphanIngress(state, orphanIngressID)
		}
		cleanupDeletedConfiguration(state, collection, id)
		state.OperationLogs = append(state.OperationLogs, newOperationLog("delete_"+collection, collection, id, "succeeded", "已删除资源 "+collection, ""))
		return nil
	})
}

func (r *Runtime) ApplicationDetail(ctx context.Context, id string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	application := findObject(state.Applications, id)
	if application == nil {
		return nil, errors.New("APPLICATION_NOT_FOUND")
	}
	return applicationDetailFromState(state, id), nil
}
