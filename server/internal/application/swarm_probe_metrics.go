package application

import (
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 旧版探测仅在检查记录中保留了卸载成功等证据。读取和写入均使用相同的判定。
func dockerProbeStatus(metrics domain.Object, checks []domain.Object) string {
	if explicit := stringValue(metrics["dockerStatus"]); explicit != "" {
		return explicit
	}
	if metrics["ssh"] == false {
		return "unknown"
	}
	if metrics["docker"] == true {
		return "ready"
	}
	for _, check := range checks {
		if check["name"] == "docker" && (check["summary"] == "REMOTE_DOCKER_NOT_INSTALLED" || check["summary"] == "Docker Engine removed" && check["status"] == serverRuntimeCheckSuccess) {
			return "missing"
		}
	}
	if metrics["ssh"] == true && metrics["docker"] == false {
		return "unavailable"
	}
	return "unknown"
}

func storedSwarmMetrics(health domain.Object) domain.Object {
	metrics := domain.Object{}
	for key, value := range objectMapFromAny(health["metrics"]) {
		metrics[key] = value
	}
	checkedAt := firstNonEmptyString(stringValue(health["checkedAt"]), stringValue(metrics["checkedAt"]))
	metrics["dockerStatus"] = dockerProbeStatus(metrics, objectSlice(health["checks"]))
	swarmAt := stringValue(metrics["swarmCheckedAt"])
	if swarmAt == "" {
		metrics["swarmCheckedAt"] = checkedAt
	}
	if swarmAt != "" && !observedAfter(checkedAt, swarmAt) {
		return metrics
	}
	// 早期版本会在 SSH 或 Docker 失败时保留上次的角色、控制权甚至 inactive。
	if metrics["dockerStatus"] == "missing" || (metrics["swarmVerificationStatus"] == "not_joined" || metrics["swarmStatus"] == "inactive" || metrics["swarmLocalNodeState"] == "inactive") && metrics["docker"] != false && metrics["ssh"] != false {
		reconcileSwarmIdentityMetrics(metrics, domain.Object{"swarmVerificationStatus": "not_joined"})
	} else if metrics["ssh"] == false || metrics["docker"] == false {
		reconcileSwarmIdentityMetrics(metrics, domain.Object{"swarm": false})
	} else if metrics["swarmRole"] == "manager" && metrics["swarmControlAvailable"] == false {
		metrics["swarmRole"], metrics["swarmVerificationStatus"] = "unknown", "unverified"
		metrics["swarmManagerAddress"], metrics["swarmAdvertiseAddr"], metrics["swarmConnectionMode"] = "", "", "unknown"
	} else {
		return metrics
	}
	metrics["swarmCheckedAt"] = checkedAt
	return metrics
}

func mergeSwarmProbeMetrics(existing, incoming domain.Object, checkedAt string) {
	if checkedAt == "" || !observedAfter(checkedAt, stringValue(existing["swarmCheckedAt"])) {
		return
	}
	for key, value := range incoming {
		if strings.HasPrefix(key, "swarm") {
			existing[key] = value
		}
	}
	reconcileSwarmIdentityMetrics(existing, incoming)
	existing["swarmCheckedAt"] = checkedAt
	if existing["swarmVerificationStatus"] == "not_joined" {
		existing["swarmAbsentAt"] = checkedAt
	}
}

// 面向查询的服务器快照吸收更新的 Agent 本机观测，原始探测及历史采样保持各自的时间。
func observedServerMetrics(health, snapshot domain.Object) domain.Object {
	metrics := storedSwarmMetrics(health)
	sampledAt, checkedAt := stringValue(snapshot["sampledAt"]), stringValue(metrics["swarmCheckedAt"])
	if observedAfter(sampledAt, checkedAt) && !observedAfter(checkedAt, sampledAt) {
		switch {
		case swarmSnapshotInactive(snapshot):
			mergeSwarmProbeMetrics(metrics, domain.Object{"swarmVerificationStatus": "not_joined"}, sampledAt)
		case stringValue(snapshot["nodeId"]) != "" && (snapshot["role"] == "manager" || snapshot["role"] == "worker"):
			nodeID, clusterID := stringValue(snapshot["nodeId"]), stringValue(snapshot["clusterId"])
			sameIdentity := metrics["swarmNodeId"] == nodeID && (clusterID == "" || metrics["swarmClusterId"] == clusterID)
			priorControl := sameIdentity && metrics["swarmVerificationStatus"] == "verified" && metrics["swarmRole"] == "manager" && metrics["swarmControlAvailable"] == true
			if !sameIdentity {
				clearServerRuntimeMetricDetails(metrics, "swarm")
			} else if metrics["swarmRole"] != snapshot["role"] {
				delete(metrics, "swarmManagerAddress")
				delete(metrics, "swarmAdvertiseAddr")
				delete(metrics, "swarmNetwork")
			}
			if !sameIdentity || metrics["swarmRole"] != snapshot["role"] {
				metrics["swarmManagerServerId"], metrics["swarmConnectionMode"] = "", "unknown"
			}
			if clusterID != "" {
				metrics["swarmClusterId"] = clusterID
			}
			metrics["swarmNodeId"], metrics["swarmRole"] = nodeID, snapshot["role"]
			metrics["swarm"], metrics["swarmLocalNodeState"], metrics["swarmStatus"] = true, "active", "active"
			metrics["swarmControlAvailable"] = snapshot["role"] == "manager" && snapshotManagerControl(snapshot, priorControl)
			if snapshot["role"] == "manager" && metrics["swarmControlAvailable"] != true {
				metrics["swarmRole"], metrics["swarmVerificationStatus"] = "unknown", "unverified"
			} else if metrics["swarmControlAvailable"] == true && clusterID != "" {
				metrics["swarmVerificationStatus"], metrics["swarmVerificationError"], metrics["swarmVerifiedAt"] = "verified", "", sampledAt
			} else if !sameIdentity || metrics["swarmVerificationStatus"] != "verified" {
				metrics["swarmVerificationStatus"], metrics["swarmVerificationError"] = "unverified", "SWARM_CLUSTER_UNVERIFIED"
			}
			metrics["swarmCheckedAt"] = sampledAt
		case snapshot["status"] == "error":
			mergeSwarmProbeMetrics(metrics, domain.Object{"swarm": false}, sampledAt)
		}
	}
	if failureAt := stringValue(snapshot["failureAt"]); observedAfter(failureAt, stringValue(metrics["swarmCheckedAt"])) {
		mergeSwarmProbeMetrics(metrics, domain.Object{"swarm": false}, failureAt)
	}
	return metrics
}

// 旧 Agent 缺少控制位时仅采信同一身份的已核验控制位，或本节点在 manager 清单中的实际状态。
func snapshotManagerControl(snapshot domain.Object, priorControl bool) bool {
	if control, ok := snapshot["controlAvailable"].(bool); ok {
		return control
	}
	if priorControl {
		return true
	}
	for _, node := range objectSlice(snapshot["nodes"]) {
		if node["id"] == snapshot["nodeId"] && (node["managerStatus"] == "Leader" || node["managerStatus"] == "Reachable") {
			return true
		}
	}
	return false
}
