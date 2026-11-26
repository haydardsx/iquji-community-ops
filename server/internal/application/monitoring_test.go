package application

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func monitoringTestServer(id, nodeID, clusterID, role, checkedAt string) domain.Object {
	return domain.Object{"id": id, "name": id, "health": domain.Object{
		"checkedAt": checkedAt,
		"metrics": domain.Object{"ssh": true, "swarm": true, "swarmNodeId": nodeID, "swarmClusterId": clusterID, "swarmRole": role, "swarmControlAvailable": role == "manager",
			"swarmCheckedAt": checkedAt, "swarmLocalNodeState": "active", "swarmVerificationStatus": "verified"},
	}}
}

func TestMonitoringAgentFreshnessAndAbsentAgent(t *testing.T) {
	now := time.Now()
	for _, scenario := range []struct {
		name   string
		agent  domain.Object
		status string
	}{
		{"未安装", nil, "not_installed"},
		{"已收到心跳", domain.Object{"status": "online", "lastHeartbeatAt": now.Format(time.RFC3339Nano)}, "online"},
		{"历史心跳", domain.Object{"status": "online", "lastHeartbeatAt": now.Add(-time.Hour).Format(time.RFC3339Nano)}, "stale"},
		{"缺心跳", domain.Object{"status": "online"}, "unknown"},
		{"未来异常时间", domain.Object{"status": "online", "lastHeartbeatAt": now.Add(time.Hour).Format(time.RFC3339Nano)}, "stale"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if actual := monitoringAgentView(scenario.agent, now)["status"]; actual != scenario.status {
				t.Fatalf("状态 = %v，预期 %s", actual, scenario.status)
			}
		})
	}
	server := monitoringTestServer("a", "node-a", "cluster", "manager", now.Format(time.RFC3339Nano))
	if monitoringServerConnectionStatus(server, domain.Object{"status": "offline"}, now) != "online" {
		t.Fatal("已确认 SSH 可达的主机应保持在线")
	}
}

func TestMonitoringSwarmUsesLatestManagerPerCluster(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Minute).Format(time.RFC3339Nano)
	current := now.Format(time.RFC3339Nano)
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("a", "na", "c1", "manager", older), monitoringTestServer("b", "nb", "c1", "manager", older), monitoringTestServer("c", "nc", "c2", "manager", older)}
	state.SwarmSnapshots = []domain.Object{
		{"serverId": "a", "clusterId": "c1", "nodeId": "na", "role": "manager", "sampledAt": older, "nodes": []domain.Object{{"id": "na", "managerStatus": "Leader"}, {"id": "deleted"}}},
		{"serverId": "b", "clusterId": "c1", "nodeId": "nb", "role": "manager", "sampledAt": current, "nodes": []domain.Object{{"id": "na", "managerStatus": "Leader"}, {"id": "nb", "managerStatus": "Reachable"}}, "services": []domain.Object{{"id": "s1"}}},
		{"serverId": "c", "clusterId": "c2", "nodeId": "nc", "role": "manager", "sampledAt": current, "nodes": []domain.Object{{"id": "nc", "managerStatus": "Leader"}}},
	}
	result := monitoringSwarmOverview(state, now)
	if result["clusterCount"] != 2 || len(objectSlice(result["nodes"])) != 3 || len(objectSlice(result["services"])) != 1 {
		t.Fatal("集群聚合错误")
	}
	for _, node := range objectSlice(result["nodes"]) {
		if node["id"] == "deleted" {
			t.Fatal("旧 manager 快照中的已移除节点重新出现")
		}
	}
}

func TestMonitoringSwarmFallbackAndWorkerIdentity(t *testing.T) {
	now := time.Now()
	checked := now.Add(-time.Second).Format(time.RFC3339Nano)
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("a", "na", "c1", "manager", checked), monitoringTestServer("b", "nb", "c1", "worker", checked), {"id": "empty"}}
	state.SwarmSnapshots = []domain.Object{{"serverId": "b", "nodeId": "nb", "role": "worker", "localNodeState": "active", "sampledAt": now.Format(time.RFC3339Nano), "nodes": []domain.Object{{"id": "nb", "role": "worker"}}}}
	result := monitoringSwarmOverview(state, now)
	if result["clusterCount"] != 1 || len(objectSlice(result["nodes"])) != 2 {
		t.Fatal("缺快照和 worker 归属回填错误")
	}
}

func TestMonitoringSwarmDoesNotRestoreDepartedNode(t *testing.T) {
	now := time.Now()
	older := now.Add(-time.Minute).Format(time.RFC3339Nano)
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("a", "na", "c1", "manager", older), {"id": "b", "health": domain.Object{"checkedAt": now.Format(time.RFC3339Nano), "metrics": domain.Object{"swarmVerificationStatus": "not_joined", "swarmCheckedAt": now.Format(time.RFC3339Nano)}}}}
	state.SwarmSnapshots = []domain.Object{
		{"serverId": "a", "clusterId": "c1", "nodeId": "na", "role": "manager", "sampledAt": older, "nodes": []domain.Object{{"id": "na", "managerStatus": "Leader"}, {"id": "nb"}}},
		{"serverId": "b", "nodeId": "nb", "role": "worker", "sampledAt": older, "localNodeState": "active"},
		{"serverId": "b", "sampledAt": now.Format(time.RFC3339Nano), "localNodeState": "inactive"},
	}
	result := monitoringSwarmOverview(state, now)
	if len(objectSlice(result["nodes"])) != 1 {
		t.Fatal("退出节点应从历史集群快照中剔除")
	}
}

func TestMonitoringAcceptsTypedSnapshotAndPreservesSampleOrder(t *testing.T) {
	state := domain.EmptyState()
	now := time.Now()
	current := now.Format(time.RFC3339Nano)
	older := now.Add(-time.Minute).Format(time.RFC3339Nano)
	applyMonitoringReportToState(&state, domain.Object{"serverId": "a", "sampledAt": current, "swarmSnapshot": domain.Object{"nodeId": "na", "localNodeState": "active"}})
	applyMonitoringReportToState(&state, domain.Object{"serverId": "a", "sampledAt": older})
	if len(state.SwarmSnapshots) != 1 || state.MonitoringAgents[0]["lastSampleAt"] != current {
		t.Fatal("报告快照或时间顺序不正确")
	}
}

func TestMonitoringPollLeaseAndLifecycleRace(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "a"}}
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "online"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	token, err := runtime.claimMonitoringPoll(ctx, "a")
	if err != nil || token == "" {
		t.Fatalf("领取失败：%v", err)
	}
	second, err := runtime.claimMonitoringPoll(ctx, "a")
	if err != nil || second != "" {
		t.Fatalf("并发采集应仅领取一次：%s %v", second, err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.MonitoringAgents[0] = domain.Object{"id": "replacement", "serverId": "a", "status": "not_installed"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.finishMonitoringPoll(ctx, "a", token, monitoringCollection{Report: domain.Object{"serverId": "a", "sampledAt": domain.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	if state.MonitoringAgents[0]["status"] != "not_installed" {
		t.Fatal("旧采集不得复活已卸载实例")
	}
}

func TestMonitoringLateFailureKeepsNewerHeartbeat(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	now := time.Now()
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "a"}}
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "online", "pollToken": "poll", "lastPollAt": now.Add(-time.Second).Format(time.RFC3339Nano), "lastHeartbeatAt": now.Format(time.RFC3339Nano)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.finishMonitoringPoll(ctx, "a", "poll", monitoringCollection{}, errors.New("MONITORING_AGENT_REPORT_UNAVAILABLE")); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	if state.MonitoringAgents[0]["status"] != "online" {
		t.Fatal("较晚返回的失败覆盖了新心跳")
	}
}

type fakeMonitoringCollector struct {
	fakeServerRuntimeExecutor
	calls atomic.Int32
}

func (collector *fakeMonitoringCollector) CollectMonitoringReport(ctx context.Context, input ServerRuntimeExecutionInput) (monitoringCollection, error) {
	collector.calls.Add(1)
	return monitoringCollection{Report: domain.Object{"serverId": input.Server["id"], "sampledAt": domain.Now(), "samples": []domain.Object{{"targetKind": "server", "targetId": input.Server["id"], "values": domain.Object{"cpuPercent": 7}}}}}, nil
}

func TestMonitoringCollectorSamplesInstalledAgentsAndRespectsInterval(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	collector := &fakeMonitoringCollector{}
	runtime.ServerRuntimeExecutor = collector
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "srv_gateway", "status": "offline"}, {"id": "unused", "serverId": "missing", "status": "not_installed"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CollectMonitoringOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CollectMonitoringOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	if collector.calls.Load() != 1 || state.MonitoringAgents[0]["status"] != "online" || len(state.MetricSamples) != 1 {
		t.Fatalf("采集结果或周期错误，调用次数为 %d", collector.calls.Load())
	}
}

func TestMonitoringSwarmDistinguishesInactiveUnknownAndStale(t *testing.T) {
	now := time.Now()
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "a"}}
	if status := monitoringSwarmOverview(state, now)["status"]; status != "unverified" {
		t.Fatalf("尚未探测的主机应待核验：%v", status)
	}
	state.SwarmSnapshots = []domain.Object{{"serverId": "a", "nodeId": "node", "localNodeState": "active", "role": "worker", "sampledAt": now.Format(time.RFC3339Nano)}}
	if status := monitoringSwarmOverview(state, now)["status"]; status != "unverified" {
		t.Fatalf("未知归属应待核验：%v", status)
	}
	state.SwarmSnapshots[0]["sampledAt"] = now.Add(-time.Hour).Format(time.RFC3339Nano)
	if status := monitoringSwarmOverview(state, now)["status"]; status != "stale" {
		t.Fatalf("旧快照应过期：%v", status)
	}
}

func TestMonitoringProbeFailurePreservesInstalledAgentForRetry(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "a"}}
	state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "online", "version": "v1", "lastHeartbeatAt": "2026-01-01T00:00:00Z"}}
	applyServerRuntimeMetrics(&state, "a", domain.Object{"agent": false})
	if state.MonitoringAgents[0]["status"] != "offline" || state.MonitoringAgents[0]["version"] != "v1" {
		t.Fatal("暂时故障不应被记作卸载")
	}
}

func TestMonitoringSwarmUnverifiedIdentityAndErrorPriority(t *testing.T) {
	now := time.Now()
	snapshots := []domain.Object{{"sampledAt": now.Format(time.RFC3339Nano), "verificationStatus": "unverified"}}
	if monitoringSwarmStatus(snapshots, 0, now) != "unverified" {
		t.Fatal("旧身份应待核验")
	}
	snapshots = append(snapshots, domain.Object{"sampledAt": now.Add(-time.Hour).Format(time.RFC3339Nano)}, domain.Object{"status": "error"})
	if monitoringSwarmStatus(snapshots, 0, now) != "error" {
		t.Fatal("采集失败应优先显示")
	}
}

func TestMonitoringHostSampleTimeIsIndependentFromHeartbeat(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "a"}}
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "online", "lastHeartbeatAt": domain.Now(), "lastSampleAt": domain.Now()}}
		state.MetricSamples = []domain.Object{{"serverId": "a", "targetKind": "server", "targetId": "a", "sampledAt": old, "values": domain.Object{"cpuPercent": 50}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.MonitoringServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row := objectSlice(result["items"])[0]
	if row["lastSampleAt"] != old || row["sampleStatus"] != "stale" || row["agentStatus"] != "online" {
		t.Fatal("新心跳不应刷新旧资源指标")
	}
}

func TestMonitoringSwarmKeepsFirstProbeFailure(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "a"}}
	state.SwarmSnapshots = []domain.Object{{"serverId": "a", "sampledAt": domain.Now(), "status": "error", "errorCode": "SWARM_PROBE_FAILED"}}
	if status := monitoringSwarmOverview(state, time.Now())["status"]; status != "error" {
		t.Fatalf("首次探测失败应展示失败：%v", status)
	}
}

func TestMonitoringNewHeartbeatDoesNotDiscardSuccessfulSample(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	now := time.Now()
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "a"}}
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "online", "pollToken": "poll", "lastPollAt": now.Add(-time.Second).Format(time.RFC3339Nano), "lastHeartbeatAt": now.Format(time.RFC3339Nano)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	collected := monitoringCollection{Report: domain.Object{"serverId": "a", "sampledAt": now.Format(time.RFC3339Nano), "samples": []domain.Object{{"targetKind": "server", "values": domain.Object{"cpuPercent": 5}}}}}
	if err := runtime.finishMonitoringPoll(ctx, "a", "poll", collected, nil); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	if len(state.MetricSamples) != 1 {
		t.Fatal("更新心跳不应丢弃成功样本")
	}
}

func TestMonitoringSlowSwarmProbeKeepsNewerMembership(t *testing.T) {
	now := time.Now()
	state := domain.EmptyState()
	state.Servers = []domain.Object{monitoringTestServer("a", "new-node", "new-cluster", "worker", now.Format(time.RFC3339Nano))}
	applyMonitoringSwarmMetrics(&state, "a", domain.Object{"swarmNodeId": "old-node", "swarmClusterId": "old-cluster", "swarmCheckedAt": now.Add(time.Second).Format(time.RFC3339Nano)}, now.Add(-time.Second).Format(time.RFC3339Nano))
	if serverRuntimeMetrics(state.Servers[0])["swarmNodeId"] != "new-node" {
		t.Fatal("慢探测覆盖了新成员身份")
	}
}

func TestMonitoringOldManualProbeCannotRestoreUninstalledAgent(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	startedAt := time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "a", "health": domain.Object{"metrics": domain.Object{"agent": false}}}}
		state.MonitoringAgents = []domain.Object{{"id": "agent", "serverId": "a", "status": "not_installed", "installedAt": domain.Now()}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result := domain.Object{"checkedAt": startedAt, "metrics": domain.Object{"agent": true, "ssh": true}, "monitoringReport": domain.Object{"serverId": "a", "sampledAt": startedAt}}
	if err := runtime.applyServerRuntimeProbe(ctx, "a", result); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	if state.MonitoringAgents[0]["status"] != "not_installed" || serverRuntimeMetrics(state.Servers[0])["agent"] != false {
		t.Fatal("旧完整探测复活了已卸载Agent")
	}
}
