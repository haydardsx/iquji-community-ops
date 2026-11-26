package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmOverviewDoesNotPromoteLegacyWorkerLocalIPToConnectionFact(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "worker", "health": domain.Object{"metrics": domain.Object{
		"swarmNodeId": "worker_node", "swarmRole": "worker", "swarmAdvertiseAddr": fixtureWorkerIP.String(),
	}}}}
	node := domain.Object{"id": "worker_node", "role": "worker"}
	enrichSwarmNodeConnection(state, node)
	if node["connectionMode"] != "unknown" || stringValue(node["advertiseAddr"]) != "" {
		t.Fatal("旧版 worker 本机 IP 被标成了实际通信网络")
	}
	metrics := serverRuntimeMetrics(state.Servers[0])
	metrics["swarmAdvertiseAddr"], metrics["swarmConnectionMode"] = "192.0.2.24", "public"
	enrichSwarmNodeConnection(state, node)
	if node["connectionMode"] != "public" || node["advertiseAddr"] != "192.0.2.24" {
		t.Fatal("新探测的实际公网地址未补齐")
	}
}

func TestSwarmOverviewPrefersSnapshotAddressOverOlderServerMetrics(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "worker", "health": domain.Object{"metrics": domain.Object{
		"swarmNodeId": "worker_node", "swarmRole": "worker", "swarmAdvertiseAddr": fixtureWorkerIP.String(), "swarmConnectionMode": "private",
	}}}}
	node := domain.Object{"id": "worker_node", "role": "worker", "advertiseAddr": "192.0.2.24"}
	enrichSwarmNodeConnection(state, node)
	if node["connectionMode"] != "public" || node["advertiseAddr"] != "192.0.2.24" {
		t.Fatal("manager 快照的真实地址被旧探测覆盖")
	}
}

func TestSwarmOverviewManagerAddressMatchesItsActualControlEndpoint(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "manager", "health": domain.Object{"metrics": domain.Object{
		"swarmNodeId": "manager_node", "swarmRole": "manager", "swarmAdvertiseAddr": fixtureManagerIP.String(), "swarmManagerAddress": "192.0.2.10:3377",
	}}}}
	node := domain.Object{"id": "manager_node", "role": "manager"}
	enrichSwarmNodeConnection(state, node)
	if node["connectionMode"] != "public" || node["advertiseAddr"] != "192.0.2.10" || node["managerAddress"] != "192.0.2.10:3377" {
		t.Fatal("manager 地址与真实控制端点不一致")
	}
}
