package application

import (
	"context"
	"fmt"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmDeparturesKeepPendingClustersAndOriginalOperation(t *testing.T) {
	records := []domain.Object{
		{"nodeId": "node_a", "clusterId": "cluster_a", "cleanupStatus": "pending", "operationRunId": "leave_original", "departedAt": "2026-01-01T00:00:00Z"},
		{"nodeId": "node_a", "clusterId": "cluster_b", "cleanupStatus": "pending", "operationRunId": "leave_b"},
		{"nodeId": "legacy_node", "clusterId": "", "cleanupStatus": "pending", "operationRunId": "leave_legacy"},
	}
	for i := 0; i < 25; i++ {
		records = append(records, domain.Object{"nodeId": fmt.Sprintf("completed_%d", i), "clusterId": "cluster_completed", "cleanupStatus": "removed", "updatedAt": fmt.Sprintf("2026-01-%02dT00:00:00Z", i+1)})
	}
	merged, err := mergeSwarmDepartures(records, domain.Object{"nodeId": "node_a", "clusterId": "cluster_a", "cleanupStatus": "removed", "operationRunId": "join_new", "updatedAt": "2026-02-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	pending := 0
	for _, record := range merged {
		if record["cleanupStatus"] == "pending" {
			pending++
		}
		if record["clusterId"] == "cluster_a" && (record["operationRunId"] != "leave_original" || record["departedAt"] != "2026-01-01T00:00:00Z") {
			t.Fatal("后续清理覆盖了最初离群操作或时间")
		}
	}
	if pending != 2 || len(merged) != swarmDepartureCompletedLimit+2 {
		t.Fatal("跨群待清理记录丢失或完成历史未收敛")
	}
	merged, err = mergeSwarmDepartures(merged, domain.Object{"nodeId": "legacy_node", "clusterId": "cluster_verified", "cleanupStatus": "pending", "operationRunId": "join_verified"})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range merged {
		if record["nodeId"] == "legacy_node" && (record["clusterId"] != "cluster_verified" || record["operationRunId"] != "leave_legacy") {
			t.Fatal("同节点旧空集群身份未补全或丢失原日志入口")
		}
	}
	if _, err := mergeSwarmDepartures(nil, domain.Object{"nodeId": "node_a", "cleanupStatus": "pending", "token": "不得保存"}); err == nil {
		t.Fatal("离群记录接受了非白名单字段")
	}
	if _, err := mergeSwarmDepartures(nil, domain.Object{"nodeId": "node_a", "cleanupStatus": "pending", "reasonCode": "EXAMPLE_UNREGISTERED_CODE"}); err == nil {
		t.Fatal("离群记录接受了未登记的诊断码")
	}
	if _, err := mergeSwarmDepartures(nil, domain.Object{"nodeId": "node_a", "cleanupStatus": "pending", "reasonCode": "SWARM_WORKER_MANAGER_NOT_ASSOCIATED"}); err != nil {
		t.Fatal("离群记录拒绝了有效诊断码")
	}
}

func TestSwarmDepartureCallbackOwnsOperationIdentityAndPersistsHistory(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	input, err := runtime.serverRuntimeExecutionInput(ctx, "srv_gateway", domain.Task{ID: "leave_original"})
	if err != nil {
		t.Fatal(err)
	}
	record := domain.Object{"nodeId": "node_a", "clusterId": "cluster_a", "cleanupStatus": "pending", "operationRunId": "forged_operation"}
	if err := input.SaveSwarmDeparture(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	input, err = runtime.serverRuntimeExecutionInput(ctx, "srv_gateway", domain.Task{ID: "join_new"})
	if err != nil {
		t.Fatal(err)
	}
	record["cleanupStatus"] = "removed"
	if err := input.SaveSwarmDeparture(ctx, record); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	history := objectSlice(state.Servers[0]["swarmDepartures"])
	if len(history) != 1 || history[0]["operationRunId"] != "leave_original" || history[0]["cleanupStatus"] != "removed" {
		t.Fatal("回调接受了伪造日志入口或后续任务覆盖原离群操作")
	}
}
