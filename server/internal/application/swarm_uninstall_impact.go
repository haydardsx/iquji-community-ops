package application

import (
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 预览和执行共用 Swarm 依赖范围，独立容器和保存的应用配置不属于退出依赖。
func swarmUninstallReferenceImpact(state domain.State, serverID, exceptTaskID string) (serverReferenceImpact, error) {
	impact := serverReferenceImpact{Reasons: []domain.Object{}, Counts: map[string]int{}}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return impact, errors.New("SERVER_NOT_FOUND")
	}
	if active := activeServerSwarmTask(state, serverID, exceptTaskID); active != nil {
		impact.Reasons = append(impact.Reasons, domain.Object{
			"code": "SERVER_HAS_ACTIVE_TASK", "resourceKind": "task", "resourceId": active.ID,
			"resourceName": firstNonEmptyString(active.TargetName, stringValue(server["name"])), "field": "targetId",
		})
		impact.Counts["task"]++
	}
	metrics := serverRuntimeMetrics(server)
	localState := firstNonEmptyString(stringValue(metrics["swarmLocalNodeState"]), stringValue(metrics["swarmStatus"]))
	if localState == "pending" || localState == "error" {
		if !unfinishedSwarmJoinCanLeave(remoteSwarmStatus{
			Status: localState, Active: metrics["swarm"] == true, ControlAvailable: metrics["swarmControlAvailable"] == true,
			NodeID: stringValue(metrics["swarmNodeId"]), ClusterID: stringValue(metrics["swarmClusterId"]),
		}, server) {
			impact.Reasons = append(impact.Reasons, domain.Object{
				"code": "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED", "resourceKind": "server", "resourceId": serverID,
				"resourceName": stringValue(server["name"]), "field": "health.metrics.swarmNodeId",
			})
			impact.Counts["server"]++
			return impact, errors.New("SWARM_UNFINISHED_JOIN_EXIT_BLOCKED")
		}
		// 未形成成员身份的加入只清理本机状态，远端执行仍须无 force 退出并证明 inactive。
		if impact.Blocked() {
			return impact, errors.New("SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK")
		}
		return impact, nil
	}
	blockers := map[string]string{
		"SERVER_HAS_MANAGED_TRAEFIK_RUNTIME":          "SWARM_UNINSTALL_BLOCKED_TRAEFIK",
		"SERVER_HAS_MANAGED_FRPS_RUNTIME":             "SWARM_UNINSTALL_BLOCKED_FRPS",
		"SERVER_HAS_APPLICATION_INSTANCE":             "SWARM_UNINSTALL_BLOCKED_APPLICATION_RUNTIME",
		"SERVER_HAS_ACTIVE_TUNNEL_RUN":                "SWARM_UNINSTALL_BLOCKED_TUNNEL",
		"SERVER_REFERENCED_BY_ACTIVE_RELEASE_MANAGER": "SWARM_UNINSTALL_BLOCKED_RELEASE_PLAN",
		"SERVER_REFERENCED_BY_ACTIVE_RELEASE_SERVICE": "SWARM_UNINSTALL_BLOCKED_RELEASE_PLAN",
		"SERVER_IS_SWARM_CLUSTER_MEMBER":              "SWARM_UNINSTALL_BLOCKED_CLUSTER_MEMBER",
	}
	if metrics["swarmRole"] == "worker" {
		// Worker 是否存在任务由执行器实时核验，缓存引用和同群其他节点不阻止本机普通离群。
		for _, code := range []string{"SERVER_IS_SWARM_CLUSTER_MEMBER", "SERVER_HAS_APPLICATION_INSTANCE", "SERVER_HAS_MANAGED_TRAEFIK_RUNTIME", "SERVER_HAS_ACTIVE_TUNNEL_RUN"} {
			delete(blockers, code)
		}
	}
	code := ""
	if impact.Blocked() {
		code = "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK"
	}
	for _, reason := range calculateServerReferenceImpact(state, serverID).Reasons {
		blocker := blockers[stringValue(reason["code"])]
		if blocker == "" {
			continue
		}
		reason["resourceName"] = swarmImpactResourceName(state, reason)
		impact.Reasons = append(impact.Reasons, reason)
		impact.Counts[stringValue(reason["resourceKind"])]++
		code = firstNonEmptyString(code, blocker)
	}
	if code != "" {
		return impact, errors.New(code)
	}
	return impact, nil
}

func swarmImpactResourceName(state domain.State, reason domain.Object) string {
	if name := stringValue(reason["resourceName"]); name != "" {
		return name
	}
	id := stringValue(reason["resourceId"])
	switch stringValue(reason["resourceKind"]) {
	case "managedRuntime":
		if id == "frps" {
			return "FRPS"
		}
		return "Traefik"
	case "swarmClusterMember":
		return stringValue(findObject(state.Servers, id)["name"])
	case "frpTunnelRun":
		for _, run := range state.FrpTunnelRuns {
			if run.ID == id {
				if tunnel := domain.FindFrpTunnel(state.FrpTunnels, run.TunnelID); tunnel != nil {
					return tunnel.Name
				}
			}
		}
	case "releasePlan":
		plan := findObject(state.ReleasePlans, id)
		return stringValue(findObject(state.Applications, stringValue(plan["applicationId"]))["name"])
	}
	return ""
}
