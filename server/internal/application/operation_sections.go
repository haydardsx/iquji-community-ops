package application

import "github.com/haydardsx/iquji-community-ops/server/internal/domain"

// IsOperationSection 限定可查询的业务区域，避免拼写错误退回全域日志。
func IsOperationSection(section string) bool {
	switch section {
	case "", "server-runtime", "application-routes":
		return true
	default:
		return false
	}
}

func operationSection(targetKind, action string) string {
	switch targetKind {
	case "route":
		return "application-routes"
	case "server":
		switch domain.TaskAction(action) {
		case domain.TaskActionInstallDockerEngine, domain.TaskActionUninstallDockerEngine, domain.TaskActionInstallDockerSwarm, domain.TaskActionUninstallDockerSwarm,
			domain.TaskActionReconfigureDockerSwarm, domain.TaskActionRebuildDockerSwarm, domain.TaskActionRecoverDockerSwarmRebuild,
			domain.TaskActionInstallTraefikGateway, domain.TaskActionUninstallTraefikGateway, domain.TaskActionInstallFrps, domain.TaskActionUninstallFrps, domain.TaskActionInstallJenkins, domain.TaskActionUninstallJenkins, domain.TaskActionBindJenkinsAccess, domain.TaskActionUnbindJenkinsAccess,
			domain.TaskActionInstallMonitoringAgent, domain.TaskActionUninstallMonitoringAgent:
			return "server-runtime"
		}
	}
	return ""
}

// 可达性只决定导航提示；历史与跟踪仍保留资源删除前的真实运行记录。
func operationScopeReachable(state domain.State, scope OperationScope, action string) bool {
	if scope.Category == "secrets" || scope.Category == "domain-certificates" {
		return true
	}
	switch scope.Section {
	case "server-runtime":
		for _, id := range scope.ServerIDs {
			if findObject(state.Servers, id) != nil {
				return true
			}
		}
		return false
	case "application-routes":
		return scope.ApplicationID != "" && findObject(state.Applications, scope.ApplicationID) != nil
	}
	if scope.ApplicationID != "" {
		// 应用详情仅保留构建记录与发布日志入口，启停等生命周期动作不再点亮父级提示圆点。
		if action != string(domain.TaskActionRequestJenkinsRelease) && action != string(domain.TaskActionDeployApplication) {
			return false
		}
		return findObject(state.Applications, scope.ApplicationID) != nil
	}
	if scope.TunnelID != "" {
		return domain.FindFrpTunnel(state.FrpTunnels, scope.TunnelID) != nil
	}
	return false
}
