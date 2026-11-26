package application

import (
	"context"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ServerRuntimeUninstallImpact(ctx context.Context, serverID string, component string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	if component == "swarm" {
		impact, err := swarmUninstallReferenceImpact(state, serverID, "")
		return impact.Object(), err
	}
	impact := calculateServerReferenceImpact(state, serverID).Object()
	switch component {
	case "docker":
		err = r.validateDockerUninstall(ctx, serverID)
	case "jenkins":
		err = r.validateJenkinsUninstall(ctx, serverID)
	case "frps":
		return frpsUninstallImpact(state, serverID)
	default:
		return impact, nil
	}
	// 其他组件保留服务器引用列表，阻断结论使用对应组件的卸载检查。
	impact["blocked"] = err != nil
	return impact, err
}

type ServerDeleteBlockedError struct {
	Impact domain.Object
}

func (e *ServerDeleteBlockedError) Error() string {
	return "SERVER_DELETE_BLOCKED"
}

type serverReferenceImpact struct {
	Reasons []domain.Object
	Counts  map[string]int
}

func (impact serverReferenceImpact) Blocked() bool {
	return len(impact.Reasons) > 0
}

func (impact serverReferenceImpact) Object() domain.Object {
	return domain.Object{"blocked": impact.Blocked(), "counts": impact.Counts, "reasons": impact.Reasons}
}

func (impact serverReferenceImpact) HasReason(codes ...string) bool {
	expected := map[string]bool{}
	for _, code := range codes {
		expected[code] = true
	}
	for _, reason := range impact.Reasons {
		if expected[stringValue(reason["code"])] {
			return true
		}
	}
	return false
}

func calculateServerReferenceImpact(state domain.State, serverID string) serverReferenceImpact {
	impact := serverReferenceImpact{Reasons: []domain.Object{}, Counts: map[string]int{}}
	add := func(code string, kind string, id string, field string) {
		impact.Counts[kind]++
		impact.Reasons = append(impact.Reasons, domain.Object{"code": code, "resourceKind": kind, "resourceId": id, "field": field})
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		configID := firstNonEmptyString(stringValue(config["id"]), stringValue(config["applicationId"]))
		if deploymentBuildServerID(config) == serverID {
			add("SERVER_REFERENCED_AS_BUILD_SERVER", "applicationDeploymentConfig", configID, "buildServerId")
		}
		if stringValue(config["gatewayServerId"]) == serverID {
			add("SERVER_REFERENCED_AS_GATEWAY", "applicationDeploymentConfig", configID, "gatewayServerId")
		}
		if stringValue(config["deploymentManagerServerId"]) == serverID {
			add("SERVER_REFERENCED_AS_DEPLOYMENT_MANAGER", "applicationDeploymentConfig", configID, "deploymentManagerServerId")
		}
		if serverImpactContainsString(objectStringSlice(objectMapFromAny(config["placement"])["eligibleServerIds"]), serverID) {
			add("SERVER_REFERENCED_BY_PLACEMENT", "applicationDeploymentConfig", configID, "placement.eligibleServerIds")
		}
		for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
			if stringValue(service["serverId"]) == serverID {
				add("SERVER_REFERENCED_BY_SWARM_SERVICE", "applicationDeploymentConfig", configID, "swarmRuntime.services.serverId")
			}
		}
	}
	for _, srv := range state.Servers {
		for _, binding := range domain.JenkinsAccessSnapshots(srv) {
			if binding["gatewayServerId"] == serverID || srv["id"] == serverID {
				add("SERVER_REFERENCED_BY_ROUTE_GATEWAY", "jenkinsAccess", stringValue(srv["id"]), "jenkinsAccess")
				break
			}
		}
	}
	for _, route := range state.Routes {
		for _, field := range []string{"lastApplied", "pendingRemote"} {
			if stringValue(objectMapFromAny(route[field])["gatewayServerId"]) == serverID {
				add("SERVER_REFERENCED_BY_ROUTE_GATEWAY", "route", stringValue(route["id"]), field+".gatewayServerId")
				break
			}
		}
	}
	for _, asset := range state.Domains {
		for _, binding := range redirectBindings(asset) {
			if binding["gatewayServerId"] == serverID {
				add("SERVER_REFERENCED_BY_ROUTE_GATEWAY", "domain", stringValue(asset["id"]), "redirect.gatewayServerId")
				break
			}
		}
	}
	for _, instance := range state.ApplicationInstances {
		if stringValue(instance["serverId"]) == serverID {
			reason := domain.Object{
				"code":         "SERVER_HAS_APPLICATION_INSTANCE",
				"resourceKind": "applicationInstance",
				"resourceId":   stringValue(instance["id"]),
				"field":        "serverId",
			}
			application := findObject(state.Applications, stringValue(instance["applicationId"]))
			if application != nil {
				reason["applicationId"] = application["id"]
				reason["applicationName"] = firstNonEmptyString(stringValue(application["name"]), stringValue(application["id"]))
				reason["resourceName"] = reason["applicationName"]
			}
			impact.Counts["applicationInstance"]++
			impact.Reasons = append(impact.Reasons, reason)
		}
	}
	for _, tunnel := range state.FrpTunnels {
		if tunnel.ServerID == serverID {
			add("SERVER_REFERENCED_BY_TUNNEL", "frpTunnel", tunnel.ID, "serverId")
		}
	}
	for _, run := range state.FrpTunnelRuns {
		if run.ServerID == serverID && run.Status.IsActive() {
			add("SERVER_HAS_ACTIVE_TUNNEL_RUN", "frpTunnelRun", run.ID, "serverId")
		}
	}
	for _, plan := range state.ReleasePlans {
		if releasePlanTerminal(plan) {
			continue
		}
		planID := stringValue(plan["id"])
		if releasePlanBuildServerID(plan) == serverID {
			add("SERVER_REFERENCED_BY_ACTIVE_RELEASE_BUILD", "releasePlan", planID, "buildServerId")
		}
		if stringValue(plan["deploymentManagerServerId"]) == serverID {
			add("SERVER_REFERENCED_BY_ACTIVE_RELEASE_MANAGER", "releasePlan", planID, "deploymentManagerServerId")
		}
		for _, service := range objectSlice(objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["swarmRuntime"])["services"]) {
			if stringValue(service["serverId"]) == serverID {
				add("SERVER_REFERENCED_BY_ACTIVE_RELEASE_SERVICE", "releasePlan", planID, "buildSnapshot.swarmRuntime.services.serverId")
			}
		}
	}
	for _, run := range state.JenkinsReleaseRuns {
		if jenkinsRunBuildServerID(state, run) != serverID || jenkinsRunTerminal(run) {
			continue
		}
		add("SERVER_HAS_ACTIVE_JENKINS_RUN", "jenkinsReleaseRun", stringValue(run["id"]), "buildServerId")
	}
	server := findObject(state.Servers, serverID)
	metrics := serverRuntimeMetrics(server)
	for _, runtime := range []string{"jenkins", "traefik", "swarm", "docker"} {
		if metrics[runtime] == true {
			add("SERVER_HAS_MANAGED_"+strings.ToUpper(runtime)+"_RUNTIME", "managedRuntime", runtime, runtime)
		}
	}
	if metrics["frpsManagedPresent"] == true {
		add("SERVER_HAS_MANAGED_FRPS_RUNTIME", "managedRuntime", "frps", "frps")
	}
	clusterID := stringValue(metrics["swarmClusterId"])
	if clusterID != "" {
		for _, peer := range state.Servers {
			if stringValue(peer["id"]) == serverID || stringValue(serverRuntimeMetrics(peer)["swarmClusterId"]) != clusterID {
				continue
			}
			add("SERVER_IS_SWARM_CLUSTER_MEMBER", "swarmClusterMember", stringValue(peer["id"]), "health.metrics.swarmClusterId")
		}
	}
	return impact
}

func serverImpactContainsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
