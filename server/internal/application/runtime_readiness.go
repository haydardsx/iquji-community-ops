package application

import (
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type runtimeReadinessCheck struct {
	Code    string
	Target  domain.ResourceRef
	Passed  bool
	Summary string
	Details domain.Object
}

func deploymentRuntimeReadiness(state domain.State, config domain.Object) []runtimeReadinessCheck {
	checks := []runtimeReadinessCheck{}
	buildServerID := stringValue(config["buildServerId"])
	managerID := stringValue(config["deploymentManagerServerId"])
	application := findObject(state.Applications, stringValue(config["applicationId"]))
	projectID := stringValue(application["projectId"])

	if !isContainerSource(objectMapFromAny(config["sourceConfig"])) {
		checks = append(checks, serverCapabilityChecks(
			state,
			buildServerID,
			"BUILD_SERVER",
			[]runtimeCapabilityRequirement{
				{Metric: "ssh", Code: "SSH_UNAVAILABLE", Summary: "build server SSH is unavailable"},
				{Metric: "docker", Code: "DOCKER_UNAVAILABLE", Summary: "build server Docker is unavailable"},
				{Metric: "jenkins", Code: "JENKINS_UNAVAILABLE", Summary: "build server Jenkins is unavailable"},
			},
		)...)
		checks = append(checks, serverProjectScopeChecks(state, buildServerID, projectID, "BUILD_SERVER_NOT_AVAILABLE_TO_PROJECT")...)
		if stringValue(objectMapFromAny(config["sourceConfig"])["provider"]) == "git" {
			buildServer := findObject(state.Servers, buildServerID)
			buildMirror, _ := desiredDockerRegistryMirrorURL(buildServer)
			gatewayMirror, _ := desiredDockerRegistryMirrorURL(findObject(state.Servers, managerID))
			if stringValue(objectMapFromAny(config["sourceConfig"])["registryMirrorUrl"]) != "" || buildMirror != "" || gatewayMirror != "" {
				checks = append(checks, runtimeReadinessCheck{Code: "JENKINS_MIRROR_BUNDLE_REQUIRED", Target: serverResourceRef(buildServer), Passed: jenkinsRuntimeBundleRevision(buildServer) >= 13, Summary: "覆盖拉取镜像源需要 v13 或更高版本的 Jenkins 运行包"})
			}
			if registrySyncConfig(config)["enabled"] == true {
				checks = append(checks, runtimeReadinessCheck{Code: "JENKINS_REGISTRY_SYNC_BUNDLE_REQUIRED", Target: serverResourceRef(buildServer), Passed: jenkinsRuntimeBundleRevision(buildServer) >= 13, Summary: "同步镜像需要 v13 或更高版本的 Jenkins 运行包"})
			}
			checks = append(checks, runtimeReadinessCheck{
				Code:    "JENKINS_APPLICATION_BUNDLE_UPDATE_REQUIRED",
				Target:  domain.Resource("server", buildServerID, "构建服务器"),
				Passed:  jenkinsRuntimeBundleRevision(buildServer) >= 12,
				Summary: "请将构建服务器的 Jenkins 受管运行包更新至 v12 或更高版本后发布",
				Details: domain.Object{"requiredBundleRevision": 12},
			})
		}
	}

	checks = append(checks, managerRuntimeReadiness(state, managerID, projectID)...)
	manager := findObject(state.Servers, managerID)
	if err := validateDeploymentLocation(application, config, stringValue(serverRuntimeMetrics(manager)["swarmClusterId"]), false); err != nil {
		checks = append(checks, runtimeReadinessCheck{Code: err.Error(), Target: serverResourceRef(manager), Summary: "请核对原部署管理节点和集群，并刷新应用运行状态"})
	}
	if _, pinned := config["deploymentClusterId"]; pinned {
		current := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", stringValue(config["applicationId"]))
		if current != nil && current["deploymentManagerServerId"] != config["deploymentManagerServerId"] {
			checks = append(checks, runtimeReadinessCheck{Code: "APPLICATION_DEPLOYMENT_LOCATION_CHANGED", Target: serverResourceRef(manager), Summary: "应用已更换部署管理节点，请重新创建发布计划"})
		}
	}

	services := objectSlice(objectMapFromAny(config["swarmRuntime"])["services"])
	if len(services) == 0 {
		checks = append(checks, runtimeReadinessCheck{
			Code:    "SWARM_SERVICE_REQUIRED",
			Target:  domain.Resource("application", stringValue(config["applicationId"]), "application"),
			Passed:  false,
			Summary: "at least one Swarm service is required",
		})
	}
	seenServers := map[string]bool{}
	for _, service := range services {
		serverID := stringValue(service["serverId"])
		if serverID == "" {
			checks = append(checks, runtimeReadinessCheck{Code: "APPLICATION_DEPLOYMENT_NODE_REQUIRED", Target: domain.Resource("application", stringValue(config["applicationId"]), "应用"), Summary: "请为服务槽位选择部署节点", Details: domain.Object{"slotKey": service["slotKey"]}})
			continue
		}
		if seenServers[serverID] {
			continue
		}
		seenServers[serverID] = true
		checks = append(checks, deploymentServerReadiness(state, serverID, managerID, projectID)...)
	}
	return checks
}

func serverProjectScopeChecks(state domain.State, serverID string, projectID string, code string) []runtimeReadinessCheck {
	server := findObject(state.Servers, serverID)
	if server == nil || serverID == "" || projectID == "" {
		return nil
	}
	return []runtimeReadinessCheck{{
		Code:    code,
		Target:  serverResourceRef(server),
		Passed:  serverAvailableToProject(server, projectID),
		Summary: "服务器需要属于当前应用的项目可用范围",
		Details: domain.Object{"projectId": projectID},
	}}
}

type runtimeCapabilityRequirement struct {
	Metric  string
	Code    string
	Summary string
}

func serverCapabilityChecks(state domain.State, serverID string, prefix string, requirements []runtimeCapabilityRequirement) []runtimeReadinessCheck {
	roleName := firstNonEmptyString(map[string]string{"BUILD_SERVER": "构建服务器", "DEPLOYMENT_MANAGER": "部署管理节点", "GATEWAY_SERVER": "网关服务器", "DEPLOYMENT_SERVER": "部署服务器"}[prefix], "服务器")
	if serverID == "" {
		return []runtimeReadinessCheck{{
			Code:    prefix + "_REQUIRED",
			Target:  domain.Resource("server", prefix, strings.ToLower(strings.ReplaceAll(prefix, "_", " "))),
			Passed:  false,
			Summary: "请选择" + roleName,
		}}
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return []runtimeReadinessCheck{{
			Code:    prefix + "_NOT_FOUND",
			Target:  domain.Resource("server", serverID, serverID),
			Passed:  false,
			Summary: roleName + "不存在，请重新选择",
		}}
	}
	metrics := serverRuntimeMetrics(server)
	checks := make([]runtimeReadinessCheck, 0, len(requirements)+1)
	checks = append(checks, runtimeReadinessCheck{
		Code:    prefix + "_SELECTED",
		Target:  serverResourceRef(server),
		Passed:  true,
		Summary: strings.ToLower(strings.ReplaceAll(prefix, "_", " ")) + " is selected",
		Details: domain.Object{"serverId": serverID},
	})
	for _, requirement := range requirements {
		checks = append(checks, runtimeReadinessCheck{
			Code:    prefix + "_" + requirement.Code,
			Target:  serverResourceRef(server),
			Passed:  metrics[requirement.Metric] == true,
			Summary: requirement.Summary,
			Details: domain.Object{"capability": requirement.Metric},
		})
	}
	return checks
}

func addRuntimeReadinessChecks(builder *domain.CheckReportBuilder, checks []runtimeReadinessCheck, phase string) {
	for _, check := range checks {
		outcome := domain.CheckOutcomeFailed
		severity := domain.CheckSeverityDanger
		if check.Passed {
			outcome = domain.CheckOutcomePassed
			severity = domain.CheckSeverityInfo
		}
		builder.Check(check.Code, "runtime", phase, check.Target, outcome, severity, !check.Passed, check.Summary, check.Details)
	}
}

func runtimeReadinessAllPassed(checks []runtimeReadinessCheck) bool {
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func serverRuntimeMetrics(server domain.Object) domain.Object {
	if server == nil {
		return domain.Object{}
	}
	health := objectMapFromAny(server["health"])
	return objectMapFromAny(health["metrics"])
}

func serverResourceRef(server domain.Object) domain.ResourceRef {
	return domain.Resource(
		"server",
		stringValue(server["id"]),
		firstNonEmptyString(stringValue(server["name"]), stringValue(server["id"])),
	)
}
