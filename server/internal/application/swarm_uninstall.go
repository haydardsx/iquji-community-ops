package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (e *SystemServerRuntimeExecutor) UninstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	status, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil {
		return nil, swarmOperationError(ctx, err, "SWARM_PROBE_FAILED")
	}
	if status.Status == "pending" || status.Status == "error" {
		if !unfinishedSwarmJoinCanLeave(status, input.Server) {
			return nil, errors.New("SWARM_UNFINISHED_JOIN_EXIT_BLOCKED")
		}
		input.LogStep("swarm", "uninstall", "info", "用户确认退出尚未形成成员身份的 Swarm 加入状态")
		if _, err := remoteOutput(ctx, client, "docker swarm leave"); err != nil {
			return nil, swarmOperationError(ctx, err, "SWARM_WORKER_LEAVE_FAILED")
		}
	} else if !status.Active && status.Status != "inactive" {
		return nil, errors.New("SWARM_UNFINISHED_JOIN_EXIT_BLOCKED")
	} else if status.Role == "worker" || (!status.Active && input.SwarmManagerServerID != "") {
		// Worker 退出后还要移除 Manager 上的归属记录；失败重试继续使用上次探测的节点标识。
		if err := uninstallSwarmWorker(ctx, client, input, status); err != nil {
			return nil, err
		}
	} else if status.Active {
		input.LogStep("swarm", "uninstall", "info", "退出 Docker Swarm 管理节点")
		if output, err := remoteOutput(ctx, client, "docker service ls -q"); err != nil || strings.TrimSpace(output) != "" {
			return nil, errors.New("SWARM_LEAVE_BLOCKED_SERVICES")
		}
		output, err := remoteOutput(ctx, client, "docker node ls -q")
		nodes := nonEmptyLines(output)
		if err != nil || len(nodes) != 1 || status.NodeID == "" || nodes[0] != status.NodeID {
			return nil, errors.New("SWARM_LEAVE_BLOCKED_NODES")
		}
		if _, err := remoteOutput(ctx, client, "docker swarm leave --force"); err != nil {
			return nil, errors.New("SWARM_MANAGER_LEAVE_FAILED")
		}
	}
	post, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil || post.Status != "inactive" || post.Active || post.ControlAvailable || post.NodeID != "" || post.ClusterID != "" {
		return nil, swarmOperationError(ctx, err, "SWARM_POST_UNINSTALL_PROBE_FAILED")
	}
	return swarmRuntimeResult(input, post, false), nil
}

func unfinishedSwarmJoinCanLeave(status remoteSwarmStatus, server domain.Object) bool {
	if status.Active || status.ControlAvailable || status.NodeID != "" || status.ClusterID != "" {
		return false
	}
	metrics := serverRuntimeMetrics(server)
	return (status.Status == "pending" || status.Status == "error") &&
		stringValue(metrics["swarmNodeId"]) == "" && stringValue(metrics["swarmClusterId"]) == "" &&
		stringValue(metrics["swarmManagerServerId"]) == "" && metrics["swarmVerificationStatus"] != "verified" &&
		metrics["swarmRole"] != "manager" && metrics["swarmRole"] != "worker"
}

// 仅删除本机曾加入的 Worker，标签、节点 ID、角色均须吻合；不强制删除在线节点。
func inspectOwnedSwarmWorker(run func(string) (string, error), nodeID, serverID string) (exists, removable bool, err error) {
	if nodeID == "" || serverID == "" {
		return false, false, errors.New("SWARM_WORKER_IDENTITY_REQUIRED")
	}
	nodes, err := run("docker node ls -q")
	if err != nil {
		return false, false, errors.New("SWARM_MANAGER_NOT_READY")
	}
	if !containsString(nonEmptyLines(nodes), nodeID) {
		return false, false, nil
	}
	format := `{{.ID}}|{{index .Spec.Labels "iquops.server-id"}}|{{.Spec.Role}}|{{.Status.State}}`
	output, err := run("docker node inspect --format " + shellQuote(format) + " " + shellQuote(nodeID))
	if err != nil {
		return false, false, errors.New("SWARM_WORKER_NODE_PROBE_FAILED")
	}
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 4 || parts[0] != nodeID || parts[1] != serverID || parts[2] != "worker" {
		return false, false, errors.New("SWARM_WORKER_NODE_OWNERSHIP_MISMATCH")
	}
	return true, strings.EqualFold(parts[3], "down"), nil
}
