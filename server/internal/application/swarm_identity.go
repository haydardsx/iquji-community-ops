package application

import (
	"context"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func applyRemoteSwarmProbe(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, result domain.Object) {
	status, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil {
		if objectMapFromAny(result["metrics"])["docker"] != true {
			dockerStatus, _ := remoteDockerFailureStatus(ctx, client)
			if dockerStatus == "missing" {
				applySwarmIdentityProbe(input, result, remoteSwarmStatus{Status: "inactive"}, "")
				return
			}
		}
		applySwarmIdentityProbe(input, result, remoteSwarmStatus{Status: "unknown"}, "SWARM_PROBE_FAILED")
		return
	}
	verificationError := ""
	if status.Status == "error" || status.Status == "locked" {
		if stateErr := swarmWorkerJoinStateError(status); stateErr != nil {
			verificationError = stateErr.Error()
		}
	}
	if status.Status == "pending" {
		verificationError = "SWARM_JOIN_PENDING"
	}
	if status.Active && status.Role == "worker" {
		verificationError = verifyRemoteSwarmMembership(ctx, input, &status)
	}
	if status.Active && (status.ClusterID == "" || status.NodeID == "") && verificationError == "" {
		verificationError = "SWARM_CLUSTER_UNVERIFIED"
	}
	applySwarmIdentityProbe(input, result, status, verificationError)
}

func verifyRemoteSwarmMembership(ctx context.Context, input ServerRuntimeExecutionInput, status *remoteSwarmStatus) string {
	if input.SwarmManagerServerID == "" {
		return "SWARM_CLUSTER_UNVERIFIED"
	}
	if len(input.SwarmManagerServer) == 0 || strings.TrimSpace(input.SwarmManagerSSHKey) == "" {
		return "SWARM_MANAGER_SSH_UNAVAILABLE"
	}
	manager, err := sshClient(ctx, input.SwarmManagerServer, input.SwarmManagerSSHKey)
	if err != nil {
		return "SWARM_MANAGER_SSH_UNAVAILABLE"
	}
	defer func() { _ = manager.Close() }()
	managerStatus, err := readRemoteSwarmStatus(ctx, manager, input.TraefikNetwork)
	if err != nil {
		return "SWARM_CLUSTER_UNVERIFIED"
	}
	clusterID, err := verifiedSwarmWorkerCluster(ctx, manager, *status, managerStatus)
	if err != nil {
		return stableSwarmProbeError(err)
	}
	status.ClusterID = clusterID
	address, addressErr := readSwarmWorkerAddress(ctx, manager, status.NodeID)
	status.AdvertiseAddr = address
	if addressErr != nil {
		return "SWARM_NODE_ADDRESS_UNVERIFIED"
	}
	status.ManagerAddress = managerStatus.ManagerAddress
	return ""
}

func applySwarmIdentityProbe(input ServerRuntimeExecutionInput, result domain.Object, status remoteSwarmStatus, verificationError string) {
	setMetric(result, "swarm", status.Active)
	setMetric(result, "swarmStatus", status.Status)
	setMetric(result, "swarmLocalNodeState", status.Status)
	checkedAt := firstNonEmptyString(stringValue(result["checkedAt"]), domain.Now())
	setMetric(result, "swarmCheckedAt", checkedAt)
	setMetric(result, "swarmVerificationError", verificationError)
	setMetric(result, "swarmManagerAddress", "")
	setMetric(result, "swarmConnectionMode", "unknown")
	if status.Status == "inactive" {
		setMetric(result, "swarmVerificationStatus", "not_joined")
		setMetric(result, "swarmManagerServerId", "")
		setMetric(result, "swarmRole", "none")
		appendServerRuntimeCheck(result, "swarm", serverRuntimeCheckSkipped, "SWARM_NOT_INITIALIZED")
		return
	}
	if status.Role != "" {
		setMetric(result, "swarmRole", status.Role)
		setMetric(result, "swarmControlAvailable", status.ControlAvailable)
	}
	previous := serverRuntimeMetrics(input.Server)
	if status.NodeID != "" {
		setMetric(result, "swarmNodeId", status.NodeID)
		if previousID := stringValue(previous["swarmNodeId"]); previousID != "" && previousID != status.NodeID {
			setMetric(result, "swarmClusterId", "")
		}
	}
	setMetric(result, "swarmAdvertiseAddr", status.AdvertiseAddr)
	if verificationError != "" || !status.Active {
		verificationStatus := "unverified"
		if stringValue(previous["swarmClusterId"]) != "" && (status.NodeID == "" || stringValue(previous["swarmNodeId"]) == status.NodeID) {
			verificationStatus = "stale"
		}
		setMetric(result, "swarmVerificationStatus", verificationStatus)
		if verificationError == "" {
			verificationError = "SWARM_CLUSTER_UNVERIFIED"
			setMetric(result, "swarmVerificationError", verificationError)
		}
		appendServerRuntimeCheck(result, "swarm", serverRuntimeCheckDanger, verificationError)
		return
	}
	setMetric(result, "swarmClusterId", status.ClusterID)
	address := status.AdvertiseAddr
	if status.Role == "manager" {
		address = status.ManagerAddress
	}
	setMetric(result, "swarmConnectionMode", swarmAddressConnectionMode(address))
	setMetric(result, "swarmManagerAddress", status.ManagerAddress)
	setMetric(result, "swarmVerificationStatus", "verified")
	setMetric(result, "swarmVerifiedAt", checkedAt)
	if status.Role == "worker" {
		setMetric(result, "swarmManagerServerId", input.SwarmManagerServerID)
	}
	if status.Role == "manager" {
		setMetric(result, "swarmManagerServerId", "")
		setMetric(result, "swarmManagerAddress", status.ManagerAddress)
	}
	if status.NetworkReady {
		setMetric(result, "swarmNetwork", input.TraefikNetwork)
	}
	appendServerRuntimeCheck(result, "swarm", serverRuntimeCheckSuccess, "Swarm 集群归属已核验")
}

// 已核验的身份可用于失联诊断；当前角色、控制权与通信端点只来自本次成功观测。
func reconcileSwarmIdentityMetrics(existing map[string]any, incoming domain.Object) {
	if incoming["dockerStatus"] == "missing" || incoming["swarmVerificationStatus"] == "not_joined" || incoming["swarmStatus"] == "inactive" && incoming["docker"] == true {
		clearServerRuntimeMetricDetails(existing, "swarm")
		existing["swarm"], existing["swarmControlAvailable"] = false, false
		existing["swarmVerificationStatus"], existing["swarmVerificationError"] = "not_joined", ""
		existing["swarmStatus"], existing["swarmLocalNodeState"], existing["swarmRole"] = "inactive", "inactive", "none"
		existing["swarmManagerServerId"], existing["swarmConnectionMode"] = "", "unknown"
		return
	}
	if incoming["swarm"] != false && incoming["ssh"] != false && incoming["docker"] != false {
		return
	}
	if incoming["swarmStatus"] == "unknown" || incoming["ssh"] == false || incoming["docker"] == false || incoming["swarmStatus"] == nil {
		existing["swarmRole"], existing["swarmControlAvailable"] = "unknown", false
		existing["swarm"], existing["swarmLocalNodeState"], existing["swarmStatus"] = false, "unknown", "unknown"
		delete(existing, "swarmNetwork")
		existing["swarmManagerAddress"], existing["swarmAdvertiseAddr"], existing["swarmConnectionMode"] = "", "", "unknown"
	}
	if incoming["swarmVerificationStatus"] != nil {
		return
	}
	status := "unverified"
	if stringValue(existing["swarmClusterId"]) != "" {
		status = "stale"
	}
	existing["swarmVerificationStatus"], existing["swarmVerificationError"] = status, "SWARM_PROBE_FAILED"
}

func swarmIdentityReadinessCode(metrics domain.Object) string {
	if metrics["swarmVerificationStatus"] == "not_joined" || metrics["swarmStatus"] == "inactive" {
		return "SWARM_NOT_INITIALIZED"
	}
	if code := stringValue(metrics["swarmVerificationError"]); code != "" {
		return swarmObservationErrorCode(code)
	}
	if metrics["swarm"] != true {
		return "DEPLOYMENT_SERVER_SWARM_UNAVAILABLE"
	}
	if metrics["swarmVerificationStatus"] != "verified" || stringValue(metrics["swarmClusterId"]) == "" {
		return "SWARM_CLUSTER_UNVERIFIED"
	}
	return ""
}

func swarmObservationErrorCode(code string) string {
	if code == "" || domain.IsPublicDiagnosticCode(code) {
		return code
	}
	return "SWARM_PROBE_FAILED"
}
