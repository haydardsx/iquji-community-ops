package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) UpsertApplicationConfig(ctx context.Context, applicationID string, input domain.Object) (domain.Object, error) {
	if input == nil {
		input = domain.Object{}
	}
	if err := validateManagedDeploymentConfigInput(input); err != nil {
		return nil, err
	}
	checkedManager, err := r.checkedApplicationManager(ctx, applicationID, input)
	if err != nil {
		return nil, err
	}
	var result domain.Object
	err = r.Store.MutateState(ctx, func(state *domain.State) error {
		application := findObject(state.Applications, applicationID)
		if application == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		if applicationOperationActive(*state, applicationID) != nil {
			return errors.New("APPLICATION_OPERATION_ACTIVE")
		}
		existing := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
		if _, provided := input["deploymentManagerServerId"]; provided && checkedManager != stringValue(existing["deploymentManagerServerId"]) {
			return errors.New("APPLICATION_DEPLOYMENT_LOCATION_CHANGED")
		}
		result = cloneObject(existing)
		if result == nil {
			result = domain.Object{}
		}
		for key, value := range domain.CloneObjectWithoutUserReferences(input) {
			if key != "id" && key != "createdAt" && key != "updatedAt" && key != "applicationId" {
				result[key] = value
			}
		}
		if err := normalizeApplicationConfiguration(*state, application, result, false); err != nil {
			return err
		}
		updateApplicationSourceKind(application, result)
		managerChanged := stringValue(existing["deploymentManagerServerId"]) != stringValue(result["deploymentManagerServerId"])
		if managerChanged && checkedManager == "" {
			location := deployedLocation(application)
			if original := stringValue(location["deploymentManagerServerId"]); original != "" && original == stringValue(result["deploymentManagerServerId"]) && stringValue(location["deploymentClusterId"]) != "" {
				managerChanged = false
			}
		}
		if managerChanged && deployedLocation(application) != nil {
			return errors.New("APPLICATION_RUNTIME_UNINSTALL_REQUIRED")
		}
		if managerChanged {
			for _, route := range filterObjectsBy(state.Routes, "applicationId", applicationID) {
				if len(objectMapFromAny(route["lastApplied"])) > 0 || len(objectMapFromAny(route["pendingRemote"])) > 0 {
					return errors.New("APPLICATION_ROUTE_REVOKE_REQUIRED")
				}
			}
		}
		gatewayID := stringValue(result["gatewayServerId"])
		gatewayChanged := gatewayID != stringValue(existing["gatewayServerId"])
		previousNetwork := stringValue(objectMapFromAny(existing["swarmRuntime"])["overlayNetwork"])
		nextNetwork := stringValue(objectMapFromAny(result["swarmRuntime"])["overlayNetwork"])
		if gatewayChanged || previousNetwork != nextNetwork {
			for _, route := range filterObjectsBy(state.Routes, "applicationId", applicationID) {
				if len(possibleRouteSnapshots(route)) == 0 {
					continue
				}
				if gatewayChanged && gatewayID != "" {
					if err := validateRouteDeploymentCluster(*state, application, result); err != nil {
						return err
					}
				}
				route["status"], route["updatedAt"] = "pending", domain.Now()
			}
		}
		now := domain.Now()
		if stringValue(result["id"]) == "" {
			result["id"] = domain.NewID("depconf")
			result["createdAt"] = now
		}
		result["applicationId"], result["updatedAt"] = applicationID, now
		if existing == nil {
			state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, result)
		} else {
			for index, item := range state.ApplicationDeploymentConfigs {
				if stringValue(item["applicationId"]) == applicationID {
					state.ApplicationDeploymentConfigs[index] = result
					break
				}
			}
		}
		state.OperationLogs = append(state.OperationLogs, newOperationLog("update_application_deployment_config", "application", applicationID, "succeeded", "应用配置已保存", ""))
		return nil
	})
	return result, err
}

func updateApplicationSourceKind(application, config domain.Object) {
	if provider := stringValue(objectMapFromAny(config["sourceConfig"])["provider"]); provider != "" {
		application["sourceKind"] = provider
		if provider == "git" {
			application["deploymentEngine"] = "jenkins-swarm"
		} else {
			application["deploymentEngine"] = "swarm"
			delete(config, "buildServerId")
			delete(config, "deliveryConfig")
			delete(config, "buildConfig")
		}
	}
}

func validateManagedDeploymentConfigInput(input domain.Object) error {
	if !objectOnlyContainsKeys(input, "id", "applicationId", "createdAt", "updatedAt", "sourceConfig", "buildServerId", "deploymentManagerServerId", "gatewayServerId", "placement", "environmentVariables", "outputVariables", "deliveryConfig", "swarmRuntime", "buildConfig", "imageRetentionPolicy") {
		return errors.New("APPLICATION_DEPLOYMENT_CONFIG_INVALID")
	}
	if raw, exists := input["sourceConfig"]; exists {
		if _, err := validateApplicationSourceConfig(objectMapFromAny(raw)); err != nil {
			return err
		}
	}
	if raw, exists := input["buildConfig"]; exists {
		build := objectMapFromAny(raw)
		if !objectOnlyContainsKeys(build, "environment", "buildStrategy", "buildType", "toolchainRequirements", "dockerfileConfig", "buildImagePolicy") {
			return errors.New("MANAGED_JENKINS_BUILD_CONFIG_INVALID")
		}
		if len(objectSlice(build["toolchainRequirements"])) > 0 {
			return errors.New("MANAGED_JENKINS_CUSTOM_COMMAND_FORBIDDEN")
		}
		if dockerfile := objectMapFromAny(build["dockerfileConfig"]); len(dockerfile) > 0 && !objectOnlyContainsKeys(dockerfile, "buildContextPath", "dockerfilePath", "buildArgsTemplate") {
			return errors.New("MANAGED_JENKINS_BUILD_CONFIG_INVALID")
		}
		if policy := objectMapFromAny(build["buildImagePolicy"]); len(policy) > 0 && !objectOnlyContainsKeys(policy, "cacheEnabled") {
			return errors.New("MANAGED_JENKINS_BUILD_CONFIG_INVALID")
		}
	}
	raw, exists := input["deliveryConfig"]
	if !exists {
		return nil
	}
	delivery := objectMapFromAny(raw)
	if len(delivery) == 0 {
		return errors.New("MANAGED_JENKINS_PIPELINE_INVALID")
	}
	if _, exists := delivery["buildServerId"]; exists {
		return errors.New("BUILD_SERVER_NESTED_FORBIDDEN")
	}
	if executor := stringValue(delivery["executor"]); executor != "" && executor != "jenkins" {
		return errors.New("MANAGED_JENKINS_PIPELINE_INVALID")
	}
	if !objectOnlyContainsKeys(delivery, "executor", "pipeline", "registrySync") {
		return errors.New("MANAGED_JENKINS_PIPELINE_INVALID")
	}
	pipeline := objectMapFromAny(delivery["pipeline"])
	if _, supplied := delivery["pipeline"]; supplied && (len(pipeline) == 0 || !objectOnlyContainsKeys(pipeline, "folder", "jobName", "jobTemplate", "triggerMode")) {
		return errors.New("MANAGED_JENKINS_PIPELINE_INVALID")
	}
	for key, expected := range map[string]string{
		"folder":      domain.ManagedJenkinsFolder,
		"jobName":     domain.ManagedJenkinsJob,
		"jobTemplate": domain.ManagedJenkinsJobTemplate,
		"triggerMode": "ops_manual",
	} {
		if value := stringValue(pipeline[key]); value != "" && value != expected {
			return errors.New("MANAGED_JENKINS_PIPELINE_INVALID")
		}
	}
	if rawSync, exists := delivery["registrySync"]; exists {
		return validateRegistrySync(objectMapFromAny(rawSync))
	}
	return nil
}

func objectOnlyContainsKeys(input domain.Object, allowed ...string) bool {
	allowedKeys := map[string]bool{}
	for _, key := range allowed {
		allowedKeys[key] = true
	}
	for key := range input {
		if !allowedKeys[key] {
			return false
		}
	}
	return true
}

func normalizeManagedDeliveryConfig(config domain.Object) {
	delivery := objectMapFromAny(config["deliveryConfig"])
	if len(delivery) == 0 {
		return
	}
	delete(delivery, "buildServerId")
	pipeline := objectMapFromAny(delivery["pipeline"])
	if len(pipeline) == 0 {
		pipeline = domain.Object{}
	}
	pipeline["folder"] = domain.ManagedJenkinsFolder
	pipeline["jobName"] = domain.ManagedJenkinsJob
	pipeline["jobTemplate"] = domain.ManagedJenkinsJobTemplate
	pipeline["triggerMode"] = "ops_manual"
	delivery["pipeline"] = pipeline
	config["deliveryConfig"] = delivery
}

func serverAvailableToProject(server domain.Object, projectID string) bool {
	projectIDs := objectStringSlice(server["projectIds"])
	return len(projectIDs) == 0 || containsString(projectIDs, projectID)
}
