package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func persistServerProbeSnapshot(state *domain.State, serverID string, result domain.Object) {
	server := findObject(state.Servers, serverID)
	if server == nil {
		return
	}
	checkedAt := firstNonEmptyString(stringValue(result["checkedAt"]), domain.Now())
	health := objectMapFromAny(server["health"])
	health["checkedAt"] = checkedAt
	health["checks"] = objectSlice(result["checks"])
	metrics := objectMapFromAny(health["metrics"])
	metrics["checkedAt"] = checkedAt
	health["metrics"] = metrics
	report := serverSnapshotReport(server, health)
	health["status"] = report.Status
	if report.Decision.Allowed {
		health["summary"] = "最近一次运行时探测已完成"
	} else {
		health["summary"] = "最近一次运行时探测发现未就绪能力"
	}
	server["health"] = health
	if metrics["ssh"] == true {
		server["connectionState"] = "reachable"
		server["status"] = "online"
	} else {
		server["connectionState"] = "unreachable"
		server["status"] = "offline"
	}
}

func serverSnapshotReport(server, health domain.Object) domain.CheckReport {
	subject := resourceRef("server", stringValue(server["id"]), stringValue(server["name"]))
	report := legacyRuntimeProcess(domain.CheckKindRuntimeProbe, subject, "probe", domain.Object{
		"metrics": health["metrics"], "checks": normalizedOptionalRuntimeChecks(health),
	})
	checkedAt := stringValue(health["checkedAt"])
	if checkedAt != "" {
		report.GeneratedAt = checkedAt
		for index := range report.Checks {
			report.Checks[index].ObservedAt = checkedAt
		}
	}
	return report
}

// 旧快照中明确未安装的可选组件按当前合同展示，保留已安装故障。
func normalizedOptionalRuntimeChecks(health domain.Object) []domain.Object {
	checks := domain.CloneObjectSliceWithoutUserReferences(objectSlice(health["checks"]))
	metrics := objectMapFromAny(health["metrics"])
	absentCodes := map[string]string{
		"SWARM_NOT_INITIALIZED": "swarm",
		"TRAEFIK_NOT_INSTALLED": "traefik", "TRAEFIK_GATEWAY_REQUIRED": "traefik",
		"TRAEFIK_SWARM_MANAGER_REQUIRED": "traefik", "TRAEFIK_NOT_REQUIRED": "traefik",
		"FRPS_NOT_INSTALLED":    "frps",
		"JENKINS_NOT_INSTALLED": "jenkins", "JENKINS_NOT_REQUIRED": "jenkins",
	}
	for _, check := range checks {
		code := firstNonEmptyString(stringValue(check["summary"]), stringValue(check["code"]))
		component := absentCodes[strings.TrimSpace(code)]
		if component == "" || metrics[component] == true {
			continue
		}
		if component == "swarm" && (stringValue(metrics["swarmClusterId"]) != "" || stringValue(metrics["swarmNodeId"]) != "") {
			continue
		}
		if component == "traefik" && (stringValue(metrics["traefikImage"]) != "" || stringValue(metrics["traefikService"]) != "") {
			continue
		}
		if component == "jenkins" && (stringValue(metrics["jenkinsImage"]) != "" || stringValue(metrics["jenkinsContainer"]) != "") {
			continue
		}
		check["status"] = serverRuntimeCheckSkipped
	}
	return checks
}

func (r *Runtime) ServerHealth(ctx context.Context, serverID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	health := cloneObject(objectMapFromAny(enrichServer(server, state)["health"]))
	if stringValue(health["checkedAt"]) == "" {
		subject := resourceRef("server", serverID, stringValue(server["name"]))
		builder := domain.NewCheckReportBuilder(domain.CheckKindResourceStatus, subject)
		builder.Check("SERVER_SSH_UNVERIFIED", "server", "health", subject, domain.CheckOutcomeWarning, domain.CheckSeverityWarning, false, "尚未执行远端运行时探测", nil)
		return processResponse(builder.Report(), "serverHealth", health), nil
	}
	report := serverSnapshotReport(server, health)
	health["checks"] = report.Checks
	return processResponse(report, "serverHealth", health), nil
}

func (r *Runtime) ServerResources(ctx context.Context, serverID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	appIDs := map[string]bool{}
	projectIDs := stringSet(objectStringSlice(server["projectIds"]))
	for _, config := range state.ApplicationDeploymentConfigs {
		if applicationConfigReferencesServer(config, serverID) {
			appIDs[stringValue(config["applicationId"])] = true
		}
	}
	for _, instance := range state.ApplicationInstances {
		if stringValue(instance["serverId"]) == serverID {
			appIDs[stringValue(instance["applicationId"])] = true
		}
	}
	applications := []domain.Object{}
	for _, app := range state.Applications {
		if appIDs[stringValue(app["id"])] {
			projectIDs[stringValue(app["projectId"])] = true
			applications = append(applications, domain.Object{
				"id": app["id"], "name": firstNonEmptyString(stringValue(app["name"]), stringValue(app["id"])),
				"projectId": app["projectId"], "status": app["status"],
			})
		}
	}
	projects := []domain.Object{}
	for _, project := range state.Projects {
		if projectIDs[stringValue(project["id"])] || containsString(objectStringSlice(project["serverIds"]), serverID) {
			projects = append(projects, objectSummary(project, "name"))
		}
	}
	return map[string]any{"projects": projects, "applications": applications}, nil
}

func applicationConfigReferencesServer(config domain.Object, serverID string) bool {
	if deploymentBuildServerID(config) == serverID || stringValue(config["deploymentManagerServerId"]) == serverID || stringValue(config["gatewayServerId"]) == serverID || containsString(placementServerIDs(config), serverID) {
		return true
	}
	for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
		if stringValue(service["serverId"]) == serverID {
			return true
		}
	}
	return false
}
