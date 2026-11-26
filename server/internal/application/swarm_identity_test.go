package application

import (
	"context"
	"slices"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmInstallFailsWhenFinalMembershipVerificationFails(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		installSwarm: func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) { return domain.Object{}, nil },
		probeResult: domain.Object{"metrics": domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmClusterId": "cluster_one",
			"swarmVerificationStatus": "stale", "swarmVerificationError": "SWARM_MANAGER_SSH_UNAVAILABLE",
		}},
	}
	task, err := runtime.CreateTask(ctx, "install_docker_swarm", "server", "srv_gateway", "网关", domain.Object{"mode": "manager"})
	if err != nil {
		t.Fatal(err)
	}
	confirmAndRunTaskForTest(t, runtime, ctx, task.ID)
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	operation := findTask(state.Tasks, task.ID)
	if operation == nil || operation.Status != "failed" {
		t.Fatal("最终归属未核验却返回成功")
	}
	for _, run := range state.OperationRuns {
		if run.ID == task.ID && run.ErrorCode != "SWARM_MANAGER_SSH_UNAVAILABLE" {
			t.Fatal("操作未保留最终核验失败原因")
		}
	}
}

func TestSwarmProbeKeepsTrustedIdentityStaleUntilVerified(t *testing.T) {
	server := readinessServer("worker", domain.Object{
		"swarm": true, "swarmRole": "worker", "swarmClusterId": "cluster_one", "swarmNodeId": "node_one",
		"swarmManagerServerId": "manager", "swarmVerificationStatus": "verified", "swarmVerifiedAt": "earlier",
	})
	state := domain.EmptyState()
	state.Servers = []domain.Object{server}
	input := ServerRuntimeExecutionInput{Server: server, SwarmManagerServerID: "manager"}
	result := domain.Object{}
	status := remoteSwarmStatus{Active: true, Status: "active", Role: "worker", NodeID: "node_one"}
	applySwarmIdentityProbe(input, result, status, "SWARM_MANAGER_SSH_UNAVAILABLE")
	applyServerRuntimeMetrics(&state, "worker", objectMapFromAny(result["metrics"]))
	metrics := serverRuntimeMetrics(server)
	if metrics["swarmClusterId"] != "cluster_one" || metrics["swarmNodeId"] != "node_one" || metrics["swarmVerifiedAt"] != "earlier" || metrics["swarmVerificationStatus"] != "stale" {
		t.Fatal("暂时失联丢失可信身份或错误通过核验")
	}
	if code := swarmIdentityReadinessCode(metrics); code != "SWARM_MANAGER_SSH_UNAVAILABLE" {
		t.Fatalf("暂时失联应阻止发布并给出原因：%s", code)
	}
	status.ClusterID = "cluster_one"
	result = domain.Object{}
	applySwarmIdentityProbe(input, result, status, "")
	applyServerRuntimeMetrics(&state, "worker", objectMapFromAny(result["metrics"]))
	if code := swarmIdentityReadinessCode(serverRuntimeMetrics(server)); code != "" {
		t.Fatalf("真实复验后未恢复：%s", code)
	}
	result = domain.Object{}
	applySwarmIdentityProbe(input, result, remoteSwarmStatus{Status: "inactive"}, "")
	applyServerRuntimeMetrics(&state, "worker", objectMapFromAny(result["metrics"]))
	metrics = serverRuntimeMetrics(server)
	if metrics["swarmVerificationStatus"] != "not_joined" || stringValue(metrics["swarmClusterId"]) != "" || stringValue(metrics["swarmManagerServerId"]) != "" {
		t.Fatal("确认退群后仍保留当前归属")
	}
}

func TestSwarmProbeDoesNotBindRejectedManagerOrReuseAnotherNodeIdentity(t *testing.T) {
	server := readinessServer("worker", domain.Object{
		"swarmClusterId": "cluster_one", "swarmNodeId": "old_node", "swarmManagerServerId": "old_manager",
	})
	result := domain.Object{}
	input := ServerRuntimeExecutionInput{Server: server, SwarmManagerServerID: "wrong_manager"}
	applySwarmIdentityProbe(input, result, remoteSwarmStatus{Active: true, Status: "active", Role: "worker", NodeID: "new_node"}, "SWARM_CLUSTER_MISMATCH")
	state := domain.EmptyState()
	state.Servers = []domain.Object{server}
	applyServerRuntimeMetrics(&state, "worker", objectMapFromAny(result["metrics"]))
	metrics := serverRuntimeMetrics(server)
	if metrics["swarmManagerServerId"] != "old_manager" || stringValue(metrics["swarmClusterId"]) != "" || metrics["swarmVerificationStatus"] != "unverified" {
		t.Fatal("失败关联覆盖原绑定或复用了另一节点身份")
	}
}

func TestLegacySwarmIdentityRequiresVerificationAndOnlyKnownDifferentClustersMismatch(t *testing.T) {
	state := domain.EmptyState()
	gateway := readinessServer("gateway", domain.Object{
		"ssh": true, "docker": true, "swarm": true, "traefik": true,
		"swarmRole": "manager", "swarmClusterId": "cluster_one", "swarmVerificationStatus": "verified",
	})
	worker := readinessServer("worker", domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmClusterId": "cluster_one"})
	state.Servers = []domain.Object{gateway, worker}
	for _, test := range []struct {
		name    string
		metrics domain.Object
		code    string
	}{
		{"旧快照", domain.Object{"swarm": true, "swarmClusterId": "cluster_one"}, "SWARM_CLUSTER_UNVERIFIED"},
		{"身份缺失", domain.Object{"swarm": true, "swarmVerificationStatus": "verified"}, "SWARM_CLUSTER_UNVERIFIED"},
		{"未入群", domain.Object{"swarm": false, "swarmStatus": "inactive"}, "SWARM_NOT_INITIALIZED"},
		{"管理节点断联", domain.Object{"swarm": true, "swarmClusterId": "cluster_one", "swarmVerificationStatus": "stale", "swarmVerificationError": "SWARM_MANAGER_SSH_UNAVAILABLE"}, "SWARM_MANAGER_SSH_UNAVAILABLE"},
		{"真实异群", domain.Object{"swarm": true, "swarmClusterId": "cluster_other", "swarmVerificationStatus": "verified"}, "SWARM_CLUSTER_MISMATCH"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker["health"] = domain.Object{"metrics": test.metrics}
			failed := failedReadinessCodes(deploymentServerReadiness(state, "worker", "gateway", ""))
			if !slices.Contains(failed, test.code) || test.code != "SWARM_CLUSTER_MISMATCH" && slices.Contains(failed, "SWARM_CLUSTER_MISMATCH") {
				t.Fatal("集群状态原因错误")
			}
		})
	}
}

func TestOptionalLegacyProbeChecksAreNeutralInListAndDetail(t *testing.T) {
	health := domain.Object{
		"checkedAt": "2026-11-16T00:00:00Z", "status": "blocked",
		"metrics": domain.Object{"ssh": true, "docker": true, "swarm": false, "traefik": false, "jenkins": false},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("swarm", serverRuntimeCheckDanger, "SWARM_NOT_INITIALIZED"),
			serverRuntimeCheck("traefik", serverRuntimeCheckDanger, "TRAEFIK_SWARM_MANAGER_REQUIRED"),
			serverRuntimeCheck("jenkins", serverRuntimeCheckDanger, "JENKINS_NOT_INSTALLED"),
		},
	}
	server := domain.Object{"id": "server", "health": health}
	report := serverSnapshotReport(server, health)
	if !report.Decision.Allowed || len(report.Decision.Warnings) != 0 {
		t.Fatal("未启用可选组件仍产生全局告警")
	}
	for _, check := range report.Checks[1:] {
		if check.Outcome != domain.CheckOutcomeSkipped || check.Severity != domain.CheckSeverityInfo {
			t.Fatal("可选组件没有中性跳过")
		}
	}
	enriched := enrichServer(server, domain.EmptyState())
	if objectMapFromAny(enriched["health"])["status"] != report.Status {
		t.Fatal("列表和详情使用了不同的健康语义")
	}
	health["checks"] = []domain.Object{serverRuntimeCheck("jenkins", serverRuntimeCheckDanger, "JENKINS_HEALTHCHECK_FAILED")}
	if serverSnapshotReport(server, health).Decision.Allowed {
		t.Fatal("已安装 Jenkins 故障被忽略")
	}
}
