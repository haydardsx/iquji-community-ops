package application

import (
	"net"
	"sort"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func monitoringSwarmOverview(state domain.State, now time.Time) map[string]any {
	latest := latestSwarmSnapshots(state)
	selected := map[string]domain.Object{}
	localNodes := map[[2]string]domain.Object{}
	snapshots := []domain.Object{}
	unknownMembers := 0
	unchecked := false
	for _, server := range state.Servers {
		serverID := stringValue(server["id"])
		snapshot := currentSwarmSnapshot(server, latest[serverID])
		if snapshot == nil {
			if !swarmSnapshotInactive(latest[serverID]) && !swarmMetricsInactive(storedSwarmMetrics(objectMapFromAny(server["health"]))) {
				unchecked = true
			}
			continue
		}
		snapshots = append(snapshots, snapshot)
		if !currentSwarmMember(snapshot, now) {
			continue
		}
		clusterID := stringValue(snapshot["clusterId"])
		if clusterID == "" && (stringValue(snapshot["nodeId"]) != "" || snapshot["localNodeState"] == "active") {
			unknownMembers++
		}
		localNodes[[2]string{clusterID, stringValue(snapshot["nodeId"])}] = snapshot
		key := firstNonEmptyString(clusterID, "unverified/"+serverID)
		previous := selected[key]
		// 同一集群以最近的 manager 全量快照为准，避免旧节点重新出现。
		if previous == nil || preferredSwarmSnapshot(snapshot, previous, now) {
			selected[key] = snapshot
		}
	}

	rows := map[string][]domain.Object{
		"nodes": {}, "services": {}, "tasks": {}, "networks": {},
	}
	clusters := []domain.Object{}
	keys := make([]string, 0, len(selected))
	for key := range selected {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshot := selected[key]
		clusterID := stringValue(snapshot["clusterId"])
		for _, kind := range []string{"nodes", "services", "tasks", "networks"} {
			if swarmSampleStatus(snapshot, now) != "fresh" {
				continue
			}
			seen := map[string]bool{}
			for _, original := range objectSlice(snapshot[kind]) {
				id := firstNonEmptyString(stringValue(original["id"]), stringValue(original["name"]))
				if id == "" || seen[id] || kind == "nodes" && swarmNodeSuperseded(state, original, snapshot, latest) {
					continue
				}
				seen[id] = true
				item := cloneObject(original)
				item["clusterId"] = clusterID
				item["sampledAt"] = snapshot["sampledAt"]
				item["sampleStatus"] = swarmSampleStatus(snapshot, now)
				if kind == "nodes" {
					item["role"] = swarmNodeRole(item)
					own := localNodes[[2]string{clusterID, id}]
					identityAt := firstNonEmptyString(stringValue(own["identityAt"]), stringValue(own["sampledAt"]))
					if own != nil && own["role"] != item["role"] && observedAfter(identityAt, stringValue(snapshot["sampledAt"])) {
						item["role"], item["sampledAt"] = own["role"], identityAt
						delete(item, "managerStatus")
						delete(item, "managerAddress")
						delete(item, "advertiseAddr")
					}
					enrichSwarmNodeConnection(state, item)
				}
				rows[kind] = append(rows[kind], item)
			}
		}
		if clusterID != "" {
			clusters = append(clusters, domain.Object{
				"id": clusterID, "sampledAt": snapshot["sampledAt"],
				"sampleStatus":       swarmSampleStatus(snapshot, now),
				"verificationStatus": snapshot["verificationStatus"],
			})
		}
	}

	// Worker 只上报本机事实；缺少 manager 全量快照时仍展示已经观测到的成员。
	for _, snapshot := range snapshots {
		if !currentSwarmMember(snapshot, now) {
			continue
		}
		nodeID := stringValue(snapshot["nodeId"])
		clusterID := stringValue(snapshot["clusterId"])
		if nodeID == "" || swarmRowsContain(rows["nodes"], clusterID, nodeID) {
			continue
		}
		selectedSnapshot := selected[firstNonEmptyString(clusterID, "unverified/"+stringValue(snapshot["serverId"]))]
		identityAt := firstNonEmptyString(stringValue(snapshot["identityAt"]), stringValue(snapshot["sampledAt"]))
		if isManagerSnapshot(selectedSnapshot) && swarmSampleStatus(selectedSnapshot, now) == "fresh" && observedAfter(stringValue(selectedSnapshot["sampledAt"]), identityAt) {
			continue
		}
		node := domain.Object{
			"id": nodeID, "serverId": snapshot["serverId"], "hostname": snapshot["serverName"],
			"clusterId": clusterID, "role": snapshot["role"], "status": "unknown",
			"sampledAt": identityAt, "sampleStatus": monitoringSampleStatus(identityAt, now),
		}
		enrichSwarmNodeConnection(state, node)
		rows["nodes"] = append(rows["nodes"], node)
	}
	status := monitoringSwarmStatus(snapshots, unknownMembers, now)
	if unchecked && (status == "not_enabled" || status == "ready") {
		status = "unverified"
	}
	return map[string]any{
		"agents": monitoringAgentViews(state, now), "snapshots": snapshots, "clusters": clusters,
		"clusterCount": len(clusters), "unverifiedMemberCount": unknownMembers, "status": status,
		"nodes": rows["nodes"], "services": rows["services"], "tasks": rows["tasks"], "networks": rows["networks"],
		"alerts": []domain.Object{}, "lastSampleAt": latestSwarmSample(snapshots),
		"refreshIntervalSeconds": 15, "generatedAt": now.UTC().Format(time.RFC3339Nano),
	}
}

func latestSwarmSnapshots(state domain.State) map[string]domain.Object {
	latest := map[string]domain.Object{}
	failures := map[string]domain.Object{}
	for _, snapshot := range state.SwarmSnapshots {
		serverID := stringValue(snapshot["serverId"])
		if serverID == "" {
			continue
		}
		target := latest
		if snapshot["status"] == "error" && snapshot["localNodeState"] != "active" && snapshot["localNodeState"] != "inactive" {
			target = failures
		}
		if target[serverID] == nil || observedAfter(stringValue(snapshot["sampledAt"]), stringValue(target[serverID]["sampledAt"])) {
			target[serverID] = snapshot
		}
	}
	for serverID, failure := range failures {
		previous := latest[serverID]
		if previous == nil {
			latest[serverID] = failure
		} else if observedAfter(stringValue(failure["sampledAt"]), stringValue(previous["sampledAt"])) {
			item := cloneObject(previous)
			item["failureAt"], item["errorCode"] = failure["sampledAt"], failure["errorCode"]
			latest[serverID] = item
		}
	}
	return latest
}

func currentSwarmSnapshot(server, snapshot domain.Object) domain.Object {
	metrics := observedServerMetrics(objectMapFromAny(server["health"]), snapshot)
	if swarmMetricsInactive(metrics) || swarmSnapshotInactive(snapshot) && metrics["swarm"] != true && metrics["swarmStatus"] != "unknown" {
		return nil
	}
	sampledAt, checkedAt := stringValue(snapshot["sampledAt"]), stringValue(metrics["swarmCheckedAt"])
	if absentAt := stringValue(metrics["swarmAbsentAt"]); observedAfter(absentAt, sampledAt) {
		snapshot = nil
	}
	result := domain.Object{"serverId": server["id"], "serverName": server["name"]}
	for key, value := range snapshot {
		result[key] = value
	}
	if code, exists := result["errorCode"]; exists {
		result["errorCode"] = swarmObservationErrorCode(stringValue(code))
	}
	nodeID, clusterID := stringValue(metrics["swarmNodeId"]), stringValue(metrics["swarmClusterId"])
	if metrics["swarm"] == false && (metrics["swarmVerificationStatus"] == "stale" || metrics["swarmVerificationStatus"] == "unverified") {
		if checkedAt == "" && snapshot == nil {
			return nil
		}
		if nodeID == "" && !swarmSnapshotInactive(snapshot) {
			nodeID, clusterID = stringValue(snapshot["nodeId"]), stringValue(snapshot["clusterId"])
		}
		status, verification := "unverified", "unverified"
		if nodeID != "" {
			status, verification = "stale", "stale"
		}
		if snapshot["status"] == "error" && observedAfter(sampledAt, checkedAt) {
			status = "error"
		}
		return domain.Object{
			"serverId": server["id"], "serverName": server["name"], "sampledAt": firstNonEmptyString(stringValue(metrics["swarmVerifiedAt"]), sampledAt, checkedAt),
			"failureAt": checkedAt, "nodeId": nodeID, "clusterId": clusterID, "role": "unknown", "controlAvailable": false, "localNodeState": "unknown",
			"status": status, "verificationStatus": verification, "errorCode": swarmObservationErrorCode(firstNonEmptyString(stringValue(result["errorCode"]), stringValue(metrics["swarmVerificationError"]))),
		}
	}
	if metrics["swarm"] == true && nodeID != "" {
		if stringValue(result["clusterId"]) != "" && result["clusterId"] != clusterID || !legacySnapshotMatchesNode(result, nodeID) || swarmSnapshotInactive(result) {
			result = domain.Object{"serverId": server["id"], "serverName": server["name"]}
		}
		if result["role"] != "manager" || metrics["swarmRole"] != "manager" || metrics["swarmControlAvailable"] != true && !snapshotManagerControl(result, false) || result["status"] == "error" {
			clearSwarmTopology(result)
		}
		result["nodeId"], result["clusterId"], result["role"] = nodeID, clusterID, metrics["swarmRole"]
		result["localNodeState"], result["identityAt"] = "active", checkedAt
		result["verificationStatus"] = firstNonEmptyString(stringValue(metrics["swarmVerificationStatus"]), "unverified")
		if metrics["swarmControlAvailable"] != nil {
			result["controlAvailable"] = metrics["swarmControlAvailable"]
		}
		if len(objectSlice(result["nodes"])) == 0 {
			result["sampledAt"] = checkedAt
		}
	} else if result["role"] != "manager" || !snapshotManagerControl(result, false) || result["status"] == "error" {
		clearSwarmTopology(result)
	}
	if stringValue(result["nodeId"]) == "" && len(objectSlice(result["nodes"])) == 0 && result["localNodeState"] != "active" && result["status"] != "error" {
		return nil
	}
	return result
}

// 无当前身份的历史观测留在 snapshots 中，不计入集群、节点和集群资源。
func currentSwarmMember(snapshot domain.Object, now time.Time) bool {
	if snapshot["verificationStatus"] == "stale" || snapshot["role"] == "unknown" || snapshot["status"] == "error" || snapshot["status"] == "unverified" {
		return false
	}
	return monitoringSampleStatus(firstNonEmptyString(stringValue(snapshot["identityAt"]), stringValue(snapshot["sampledAt"])), now) == "fresh"
}

func swarmSnapshotInactive(snapshot domain.Object) bool {
	return snapshot["localNodeState"] == "inactive" || snapshot["status"] == "not_enabled"
}

func swarmMetricsInactive(metrics domain.Object) bool {
	return metrics["swarmVerificationStatus"] == "not_joined" || metrics["swarmLocalNodeState"] == "inactive" || metrics["swarmStatus"] == "inactive"
}

func clearSwarmTopology(snapshot domain.Object) {
	for _, key := range []string{"nodes", "services", "tasks", "networks"} {
		delete(snapshot, key)
	}
}

func swarmSampleStatus(snapshot domain.Object, now time.Time) string {
	if snapshot["verificationStatus"] == "stale" {
		return "stale"
	}
	if snapshot["status"] == "error" {
		return "unknown"
	}
	return monitoringSampleStatus(stringValue(snapshot["sampledAt"]), now)
}

func legacySnapshotMatchesNode(snapshot domain.Object, nodeID string) bool {
	if nodeID == "" {
		return false
	}
	if id := stringValue(snapshot["nodeId"]); id != "" {
		return id == nodeID
	}
	for _, node := range objectSlice(snapshot["nodes"]) {
		if node["id"] == nodeID {
			return true
		}
	}
	return false
}

func isManagerSnapshot(snapshot domain.Object) bool {
	return snapshot["role"] == "manager" && snapshotManagerControl(snapshot, false) && snapshot["status"] != "error" && len(objectSlice(snapshot["nodes"])) > 0
}

func preferredSwarmSnapshot(candidate, previous domain.Object, now time.Time) bool {
	if fresh, previousFresh := swarmSampleStatus(candidate, now) == "fresh", swarmSampleStatus(previous, now) == "fresh"; fresh != previousFresh {
		return fresh
	}
	if isManagerSnapshot(candidate) != isManagerSnapshot(previous) {
		return isManagerSnapshot(candidate)
	}
	return observedAfter(stringValue(candidate["sampledAt"]), stringValue(previous["sampledAt"]))
}

func swarmNodeRole(node domain.Object) string {
	if role := stringValue(node["role"]); role != "" {
		return role
	}
	if strings.TrimSpace(stringValue(node["managerStatus"])) != "" {
		return "manager"
	}
	return "worker"
}

func swarmRowsContain(nodes []domain.Object, clusterID, nodeID string) bool {
	for _, node := range nodes {
		if node["id"] == nodeID && stringValue(node["clusterId"]) == clusterID {
			return true
		}
	}
	return false
}

func swarmNodeSuperseded(state domain.State, node, snapshot domain.Object, latest map[string]domain.Object) bool {
	serverID := stringValue(node["serverId"])
	if serverID == "" {
		for _, own := range state.SwarmSnapshots {
			if own["nodeId"] == node["id"] {
				serverID = stringValue(own["serverId"])
				break
			}
		}
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return false
	}
	current := currentSwarmSnapshot(server, latest[serverID])
	metrics := storedSwarmMetrics(objectMapFromAny(server["health"]))
	checkedAt := firstNonEmptyString(stringValue(metrics["swarmCheckedAt"]), stringValue(objectMapFromAny(server["health"])["checkedAt"]))
	if own := latest[serverID]; swarmSnapshotInactive(own) && observedAfter(stringValue(own["sampledAt"]), stringValue(snapshot["sampledAt"])) && (current == nil || current["nodeId"] != node["id"]) {
		return true
	}
	if absentAt := stringValue(metrics["swarmAbsentAt"]); absentAt != "" && observedAfter(absentAt, stringValue(snapshot["sampledAt"])) {
		return true
	}
	if current == nil && swarmMetricsInactive(metrics) && observedAfter(checkedAt, stringValue(snapshot["sampledAt"])) {
		return true
	}
	return current != nil && observedAfter(firstNonEmptyString(stringValue(current["identityAt"]), stringValue(current["sampledAt"])), stringValue(snapshot["sampledAt"])) && (stringValue(current["clusterId"]) != "" && current["clusterId"] != snapshot["clusterId"] || stringValue(current["nodeId"]) != "" && current["nodeId"] != node["id"])
}

func monitoringSwarmStatus(snapshots []domain.Object, unknownMembers int, now time.Time) string {
	if len(snapshots) == 0 {
		return "not_enabled"
	}
	status := "ready"
	if unknownMembers > 0 {
		status = "unverified"
	}
	for _, snapshot := range snapshots {
		if snapshot["status"] == "error" {
			return "error"
		}
		if snapshot["status"] == "unverified" {
			if status == "ready" {
				status = "unverified"
			}
			continue
		}
		if swarmSampleStatus(snapshot, now) != "fresh" || snapshot["verificationStatus"] == "stale" {
			status = "stale"
		} else if status == "ready" && snapshot["verificationStatus"] == "unverified" {
			status = "unverified"
		}
	}
	return status
}

func enrichSwarmNodeConnection(state domain.State, node domain.Object) {
	nodeID := stringValue(node["id"])
	for _, server := range state.Servers {
		metrics := storedSwarmMetrics(objectMapFromAny(server["health"]))
		if (nodeID != "" && nodeID == stringValue(metrics["swarmNodeId"])) || (stringValue(node["serverId"]) != "" && stringValue(node["serverId"]) == stringValue(server["id"])) {
			if stringValue(node["serverId"]) == "" {
				node["serverId"] = server["id"]
			}
			if stringValue(node["serverName"]) == "" {
				node["serverName"] = server["name"]
			}
			if stringValue(node["publicHost"]) == "" {
				node["publicHost"] = server["publicHost"]
			}
			mode := stringValue(metrics["swarmConnectionMode"])
			managerEndpoint := stringValue(metrics["swarmManagerAddress"])
			if nodeID == stringValue(metrics["swarmNodeId"]) && node["role"] == metrics["swarmRole"] && stringValue(node["advertiseAddr"]) == "" && stringValue(node["managerAddress"]) == "" {
				if mode == "public" || mode == "private" || node["role"] == "manager" && managerEndpoint != "" {
					node["advertiseAddr"] = metrics["swarmAdvertiseAddr"]
					if node["role"] == "manager" {
						node["managerAddress"] = managerEndpoint
					}
				}
			}
			break
		}
	}
	address := stringValue(node["advertiseAddr"])
	if node["role"] == "manager" {
		if endpoint := stringValue(node["managerAddress"]); endpoint != "" {
			normalized, err := normalizeSwarmManagerEndpoint(endpoint)
			if err != nil {
				node["connectionMode"] = "unknown"
				return
			}
			address, _, _ = net.SplitHostPort(normalized)
			node["managerAddress"], node["advertiseAddr"] = normalized, address
		}
	}
	node["connectionMode"] = swarmAddressConnectionMode(address)
}
