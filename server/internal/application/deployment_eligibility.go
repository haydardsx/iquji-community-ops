package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ApplicationDeploymentEligibility(ctx context.Context, applicationID, managerID string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	application := findObject(state.Applications, applicationID)
	if application == nil {
		return nil, errors.New("APPLICATION_NOT_FOUND")
	}
	projectID := stringValue(application["projectId"])
	managerChecks := managerRuntimeReadiness(state, managerID, projectID)
	manager := findObject(state.Servers, managerID)
	if err := validateDeploymentLocation(application, domain.Object{"deploymentManagerServerId": managerID}, stringValue(serverRuntimeMetrics(manager)["swarmClusterId"]), false); err != nil {
		managerChecks = append(managerChecks, runtimeReadinessCheck{Code: err.Error(), Summary: "请先核对应用原部署位置并刷新运行状态"})
	}
	servers := []domain.Object{}
	for _, server := range state.Servers {
		if !serverAvailableToProject(server, projectID) {
			continue
		}
		serverID := stringValue(server["id"])
		checks := append([]runtimeReadinessCheck{}, managerChecks...)
		checks = append(checks, deploymentServerReadiness(state, serverID, managerID, projectID)...)
		reasons := []domain.Object{}
		seen := map[string]bool{}
		for _, check := range checks {
			if check.Passed || seen[check.Code] {
				continue
			}
			seen[check.Code] = true
			reasons = append(reasons, domain.Object{"code": check.Code, "message": check.Summary})
		}
		metrics := serverRuntimeMetrics(server)
		servers = append(servers, domain.Object{
			"serverId": serverID, "eligible": len(reasons) == 0, "reasons": reasons,
			"clusterId": stringValue(metrics["swarmClusterId"]), "role": stringValue(metrics["swarmRole"]),
		})
	}
	return domain.Object{
		"deploymentManagerServerId": managerID,
		"deploymentClusterId":       stringValue(serverRuntimeMetrics(findObject(state.Servers, managerID))["swarmClusterId"]),
		"servers":                   servers,
	}, nil
}

func managerRuntimeReadiness(state domain.State, managerID, projectID string) []runtimeReadinessCheck {
	checks := serverCapabilityChecks(state, managerID, "DEPLOYMENT_MANAGER", []runtimeCapabilityRequirement{
		{Metric: "ssh", Code: "SSH_UNAVAILABLE", Summary: "部署管理节点 SSH 不可用"},
		{Metric: "docker", Code: "DOCKER_UNAVAILABLE", Summary: "部署管理节点 Docker 不可用"},
		{Metric: "swarm", Code: "SWARM_MANAGER_REQUIRED", Summary: "部署管理节点 Swarm 未就绪"},
	})
	checks = append(checks, serverProjectScopeChecks(state, managerID, projectID, "DEPLOYMENT_MANAGER_NOT_AVAILABLE_TO_PROJECT")...)
	manager := findObject(state.Servers, managerID)
	if manager == nil {
		return checks
	}
	metrics := serverRuntimeMetrics(manager)
	checks = append(checks, runtimeReadinessCheck{
		Code: "DEPLOYMENT_MANAGER_ROLE_REQUIRED", Target: serverResourceRef(manager),
		Passed:  metrics["swarm"] == true && metrics["swarmRole"] == "manager",
		Summary: "部署管理节点需要处于 active 状态的 Swarm Manager", Details: domain.Object{"swarmRole": metrics["swarmRole"]},
	})
	return append(checks, swarmIdentityReadiness(manager))
}

func deploymentServerReadiness(state domain.State, serverID, managerID, projectID string) []runtimeReadinessCheck {
	checks := serverCapabilityChecks(state, serverID, "DEPLOYMENT_SERVER", []runtimeCapabilityRequirement{
		{Metric: "ssh", Code: "SSH_UNAVAILABLE", Summary: "部署服务器 SSH 不可用"},
		{Metric: "docker", Code: "DOCKER_UNAVAILABLE", Summary: "部署服务器 Docker 不可用"},
	})
	checks = append(checks, serverProjectScopeChecks(state, serverID, projectID, "DEPLOYMENT_SERVER_NOT_AVAILABLE_TO_PROJECT")...)
	server := findObject(state.Servers, serverID)
	if server == nil {
		return checks
	}
	checks = append(checks, swarmIdentityReadiness(server))
	manager := findObject(state.Servers, managerID)
	metrics, managerMetrics := serverRuntimeMetrics(server), serverRuntimeMetrics(manager)
	// 两端身份均已核验后才比较，避免把未安装或暂时失联误报为异群。
	if swarmIdentityReadinessCode(metrics) != "" || swarmIdentityReadinessCode(managerMetrics) != "" {
		return checks
	}
	clusterID, managerCluster := stringValue(metrics["swarmClusterId"]), stringValue(managerMetrics["swarmClusterId"])
	return append(checks, runtimeReadinessCheck{
		Code: "SWARM_CLUSTER_MISMATCH", Target: serverResourceRef(server), Passed: clusterID == managerCluster,
		Summary: "部署服务器与部署管理节点需要属于同一个 Swarm 集群",
		Details: domain.Object{"deploymentManagerServerId": managerID, "deploymentManagerServerName": firstNonEmptyString(stringValue(manager["name"]), managerID), "deploymentClusterId": managerCluster, "serverClusterId": clusterID},
	})
}

func swarmIdentityReadiness(server domain.Object) runtimeReadinessCheck {
	metrics := serverRuntimeMetrics(server)
	code := swarmIdentityReadinessCode(metrics)
	passed := code == ""
	if passed {
		code = "SWARM_CLUSTER_VERIFIED"
	}
	summaries := map[string]string{
		"SWARM_CLUSTER_VERIFIED":               "Swarm 集群归属已核验",
		"SWARM_NOT_INITIALIZED":                "服务器尚未加入 Swarm 集群",
		"SWARM_CLUSTER_UNVERIFIED":             "Swarm 集群归属待核验，请关联 Manager 并刷新探测",
		"SWARM_MANAGER_SSH_UNAVAILABLE":        "Swarm Manager 暂时无法连接，请恢复 SSH 后重新探测",
		"SWARM_MANAGER_NOT_READY":              "关联的 Swarm Manager 未就绪",
		"SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE": "Swarm Manager 的集群身份待核验",
		"SWARM_WORKER_IDENTITY_REQUIRED":       "Swarm Worker 的节点身份待核验",
		"SWARM_CLUSTER_MISMATCH":               "服务器与关联的 Swarm Manager 属于不同集群",
		"SWARM_PROBE_FAILED":                   "Swarm 探测失败，请恢复连接后重新探测",
		"SWARM_MANAGER_ENDPOINT_UNAVAILABLE":   "节点无法访问 Manager 控制端点，请检查节点间路由、防火墙和安全组",
		"SWARM_JOIN_PENDING":                   "节点仍在后台加入 Swarm，请等待或检查加入诊断",
		"SWARM_NODE_ERROR":                     "Swarm 节点处于错误状态，请检查加入诊断",
		"SWARM_NODE_LOCKED":                    "Swarm 节点已锁定，请先解锁后重新探测",
		"SWARM_JOIN_TOKEN_REJECTED":            "Swarm 加入令牌被拒绝，请检查目标集群后重试",
		"SWARM_JOIN_CERTIFICATE_FAILED":        "Swarm 加入证书验证失败，请检查节点时间与集群证书",
		"SWARM_ADVERTISE_ADDRESS_INVALID":      "节点通告 IP 无效，请检查本节点网络配置",
		"DEPLOYMENT_SERVER_SWARM_UNAVAILABLE":  "服务器 Swarm 未就绪",
	}
	return runtimeReadinessCheck{
		Code: code, Passed: passed, Target: serverResourceRef(server), Summary: firstNonEmptyString(summaries[code], code),
		Details: domain.Object{"verificationStatus": metrics["swarmVerificationStatus"], "serverClusterId": metrics["swarmClusterId"]},
	}
}
