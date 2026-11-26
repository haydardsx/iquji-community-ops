package application

import (
	"context"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func testManagerTopology(serverID, at string) domain.Object {
	return domain.Object{
		"serverId": serverID, "sampledAt": at, "role": "manager", "controlAvailable": true, "localNodeState": "active", "nodeId": "manager_node", "clusterId": "cluster", "status": "success",
		"nodes":    []domain.Object{{"id": "manager_node", "role": "manager"}, {"id": "other_node", "role": "worker"}},
		"services": []domain.Object{{"id": "service"}}, "tasks": []domain.Object{{"id": "task"}}, "networks": []domain.Object{{"id": "network"}},
	}
}

func assertCurrentSwarmRows(t *testing.T, result domain.Object, clusters, nodes, other int) {
	t.Helper()
	if result["clusterCount"] != clusters || len(objectSlice(result["nodes"])) != nodes {
		t.Fatal("当前集群或节点数量不符")
	}
	for _, key := range []string{"services", "tasks", "networks"} {
		if len(objectSlice(result[key])) != other {
			t.Fatalf("当前 %s 数量不符", key)
		}
	}
}

func TestSwarmReadAfterReopenSeparatesHistoricalIdentityFromCurrentMembership(t *testing.T) {
	for _, scenario := range []struct {
		name, dockerCheck, dockerCheckStatus, verification, role, status string
		ssh                                                              bool
	}{
		{"卸载成功", "Docker Engine removed", serverRuntimeCheckSuccess, "not_joined", "none", "not_enabled", true},
		{"守护进程不可用", "REMOTE_DOCKER_UNAVAILABLE", serverRuntimeCheckDanger, "stale", "unknown", "stale", true},
		{"SSH 不可用", "", serverRuntimeCheckDanger, "stale", "unknown", "stale", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r, store, ctx := newFrpRuntime(t)
			now := time.Now()
			older := now.Add(-time.Minute).Format(time.RFC3339Nano)
			current := now.Format(time.RFC3339Nano)
			if err := store.MutateState(ctx, func(state *domain.State) error {
				server := monitoringTestServer("host", "manager_node", "cluster", "manager", older)
				health := objectMapFromAny(server["health"])
				metrics := objectMapFromAny(health["metrics"])
				metrics["ssh"], metrics["docker"], metrics["swarmControlAvailable"] = scenario.ssh, false, true
				metrics["swarmManagerAddress"], metrics["swarmVerifiedAt"] = "192.0.2.11:2377", older
				health["checkedAt"], health["checks"] = current, []domain.Object{serverRuntimeCheck("docker", scenario.dockerCheckStatus, scenario.dockerCheck)}
				state.Servers = []domain.Object{server}
				applyMonitoringReportToState(state, domain.Object{"serverId": "host", "sampledAt": older, "swarmSnapshot": testManagerTopology("host", older)})
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.Open(ctx, r.Config.DatabasePath, r.Config.RepositoryRoot)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			r = NewRuntime(r.Config, reopened)
			result, err := r.MonitoringSwarm(ctx)
			if err != nil {
				t.Fatal(err)
			}
			assertCurrentSwarmRows(t, result, 0, 0, 0)
			if result["status"] != scenario.status {
				t.Fatal("监控状态错误")
			}
			servers, err := r.ListObjects(ctx, "servers")
			if err != nil {
				t.Fatal(err)
			}
			health, err := r.ServerHealth(ctx, "host")
			if err != nil {
				t.Fatal(err)
			}
			monitor, err := r.MonitoringServers(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range []domain.Object{serverRuntimeMetrics(objectSlice(servers["items"])[0]), objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"]), objectSlice(monitor["items"])[0]} {
				if m["swarmVerificationStatus"] != scenario.verification || m["swarmRole"] != scenario.role || m["swarmControlAvailable"] != false || stringValue(m["swarmManagerAddress"]) != "" {
					t.Fatal("重启首读仍展示过时管理身份或地址")
				}
				if (stringValue(m["swarmNodeId"]) == "manager_node") != (scenario.verification == "stale") {
					t.Fatal("历史身份清理范围错误")
				}
			}
		})
	}
}

func TestSwarmLateObservationsCannotRestoreRemovedTopology(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	now := time.Now()
	older := now.Add(-time.Minute).Format(time.RFC3339Nano)
	current := now.Format(time.RFC3339Nano)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{monitoringTestServer("host", "manager_node", "cluster", "manager", older)}
		applyMonitoringReportToState(state, domain.Object{"serverId": "host", "sampledAt": older, "swarmSnapshot": testManagerTopology("host", older)})
		applyMonitoringSwarmMetrics(state, "host", domain.Object{"swarm": false, "swarmStatus": "inactive", "swarmVerificationStatus": "not_joined"}, current)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.applyServerRuntimeProbe(ctx, "host", domain.Object{
		"checkedAt": older, "metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmCheckedAt": now.Add(time.Hour).Format(time.RFC3339Nano), "swarmRole": "manager", "swarmNodeId": "manager_node", "swarmClusterId": "cluster", "swarmVerificationStatus": "verified"},
		"monitoringReport": domain.Object{"serverId": "host", "sampledAt": older, "swarmSnapshot": testManagerTopology("host", older)},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := r.MonitoringSwarm(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentSwarmRows(t, result, 0, 0, 0)
	if result["status"] != "not_enabled" {
		t.Fatal("迟到探测覆盖离群")
	}
	failureAt := now.Add(time.Second).Format(time.RFC3339Nano)
	if err := r.applyServerRuntimeProbe(ctx, "host", domain.Object{"checkedAt": failureAt, "metrics": domain.Object{"ssh": false, "docker": false, "swarm": false}}); err != nil {
		t.Fatal(err)
	}
	result, err = r.MonitoringSwarm(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertCurrentSwarmRows(t, result, 0, 0, 0)
	if result["status"] != "unverified" {
		t.Fatal("失联应待核验")
	}
}

func TestSwarmDemotionAndExpiredTopology(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Second).Format(time.RFC3339Nano)
	recent := now.Format(time.RFC3339Nano)
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("host", "manager_node", "cluster", "worker", recent)}
	state.SwarmSnapshots = []domain.Object{testManagerTopology("host", older)}
	result := monitoringSwarmOverview(state, now)
	assertCurrentSwarmRows(t, result, 1, 1, 0)
	if objectSlice(result["nodes"])[0]["role"] != "worker" {
		t.Fatal("旧管理拓扑覆盖降级后的角色")
	}

	state.Servers = []domain.Object{{"id": "host"}}
	result = monitoringSwarmOverview(state, now)
	assertCurrentSwarmRows(t, result, 1, 2, 1)
	result = monitoringSwarmOverview(state, now.Add(3*time.Minute))
	assertCurrentSwarmRows(t, result, 0, 0, 0)
	if result["status"] != "stale" || len(objectSlice(result["snapshots"])) != 1 {
		t.Fatal("过期资料未保留为历史")
	}
	delete(state.SwarmSnapshots[0], "controlAvailable")
	assertCurrentSwarmRows(t, monitoringSwarmOverview(state, now), 0, 0, 0)
	state.SwarmSnapshots[0]["controlAvailable"] = false
	assertCurrentSwarmRows(t, monitoringSwarmOverview(state, now), 0, 0, 0)
}

func TestRemoteDockerFailureStatusRequiresConfirmedAbsence(t *testing.T) {
	for _, test := range []struct {
		output string
		exit   uint32
		status string
	}{{"missing", 0, "missing"}, {"present", 0, "unavailable"}, {"", 1, "unavailable"}} {
		t.Run(test.output+test.status, func(t *testing.T) {
			client, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
				if command != dockerPresenceCommand {
					t.Error("意外命令")
				}
				return test.output, test.exit
			})
			status, _ := remoteDockerFailureStatus(context.Background(), client)
			if status != test.status {
				t.Fatalf("Docker 状态 = %s，预期 %s", status, test.status)
			}
		})
	}
}

func TestNewerWorkerIdentityOverridesAnotherManagersOlderRole(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Second).Format(time.RFC3339Nano)
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("manager", "manager_node", "cluster", "manager", older), monitoringTestServer("second", "other_node", "cluster", "worker", now.Format(time.RFC3339Nano))}
	snapshot := testManagerTopology("manager", older)
	node := objectSlice(snapshot["nodes"])[1]
	node["serverId"], node["role"], node["managerStatus"], node["managerAddress"] = "second", "manager", "Reachable", "192.0.2.12:2377"
	state.SwarmSnapshots = []domain.Object{snapshot}
	result := monitoringSwarmOverview(state, now)
	nodes := objectSlice(result["nodes"])
	if len(nodes) != 2 || nodes[1]["role"] != "worker" || stringValue(nodes[1]["managerAddress"]) != "" {
		t.Fatal("其他管理节点的旧角色覆盖了更新的本机事实")
	}
}
