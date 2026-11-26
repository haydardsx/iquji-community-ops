package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

const swarmLocalTaskContainersCommand = "docker ps --filter 'label=com.docker.swarm.task.id' --format '{{.ID}}'"

// 本机脱离与远端节点清理是两个事实，外部或失联的 manager 不阻止普通 worker 脱离。
func uninstallSwarmWorker(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, status remoteSwarmStatus) error {
	input = withServerRuntimeLogStep(input)
	if !status.Active {
		if len(input.SwarmManagerServer) == 0 || input.SwarmManagerSSHKey == "" {
			return nil
		}
		manager, err := sshClient(ctx, input.SwarmManagerServer, input.SwarmManagerSSHKey)
		if err != nil {
			return nil
		}
		defer func() { _ = manager.Close() }()
		managerStatus, err := readRemoteSwarmStatus(ctx, manager, "")
		if err != nil || !managerStatus.Active || managerStatus.Role != "manager" {
			return nil
		}
		return cleanupSwarmDeparturesBeforeJoin(ctx, manager, input, status, managerStatus)
	}
	if status.Role != "worker" || status.ControlAvailable || status.NodeID == "" {
		return errors.New("SWARM_WORKER_IDENTITY_REQUIRED")
	}
	output, err := remoteOutput(ctx, client, swarmLocalTaskContainersCommand)
	if err != nil {
		return swarmOperationError(ctx, err, "SWARM_WORKER_TASK_PROBE_FAILED")
	}
	if strings.TrimSpace(output) != "" {
		return errors.New("SWARM_LEAVE_BLOCKED_SERVICES")
	}
	metrics := serverRuntimeMetrics(input.Server)
	record := domain.Object{"nodeId": status.NodeID, "clusterId": firstNonEmptyString(status.ClusterID, stringValue(metrics["swarmClusterId"])), "managerServerId": input.SwarmManagerServerID, "managerAddress": status.ManagerAddress, "cleanupStatus": "leaving", "reasonCode": "", "departedAt": "", "updatedAt": domain.Now()}
	manager, reason, err := prepareSwarmDepartureManager(ctx, input, status, record)
	if manager != nil {
		defer func() { _ = manager.Close() }()
	}
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := saveSwarmWorkerDeparture(ctx, input, record); err != nil {
		return err
	}
	input.LogStep("swarm", "leave", "info", "本机无运行中的 Swarm 任务容器，开始普通脱离集群；保留 Docker、Jenkins 和独立容器")
	if _, err := remoteOutput(ctx, client, "docker swarm leave"); err != nil {
		record["cleanupStatus"], record["reasonCode"] = "leave_failed", "SWARM_WORKER_LEAVE_FAILED"
		_ = saveSwarmWorkerDepartureAfterLeave(ctx, input, record)
		return swarmOperationError(ctx, err, "SWARM_WORKER_LEAVE_FAILED")
	}
	post, err := readRemoteSwarmStatus(ctx, client, "")
	if err != nil || post.Status != "inactive" || post.Active || post.ControlAvailable || post.NodeID != "" || post.ClusterID != "" {
		record["cleanupStatus"], record["reasonCode"] = "leave_failed", "SWARM_POST_UNINSTALL_PROBE_FAILED"
		_ = saveSwarmWorkerDepartureAfterLeave(ctx, input, record)
		return swarmOperationError(ctx, err, "SWARM_POST_UNINSTALL_PROBE_FAILED")
	}
	record["departedAt"], record["cleanupStatus"] = domain.Now(), "pending"
	if manager != nil && reason == "" {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
		err := removeOwnedDepartedSwarmWorker(cleanupCtx, func(command string) (string, error) { return remoteOutput(cleanupCtx, manager, command) }, status.NodeID, stringValue(input.Server["id"]))
		cancel()
		if err == nil {
			record["cleanupStatus"] = "removed"
		} else {
			reason = stableErrorCode(err, "SWARM_WORKER_NODE_REMOVE_FAILED")
		}
	}
	record["reasonCode"] = reason
	if err := saveSwarmWorkerDepartureAfterLeave(ctx, input, record); err != nil {
		return err
	}
	if record["cleanupStatus"] == "removed" {
		input.LogStep("swarm", "leave", "info", "本机已确认脱离集群，manager 上归属匹配的旧离线节点记录已清理")
	} else {
		input.LogStep("swarm", "leave", "warn", "本机已确认脱离集群；远端旧节点记录待集群管理员清理；诊断类别："+reason)
	}
	return nil
}

func prepareSwarmDepartureManager(ctx context.Context, input ServerRuntimeExecutionInput, status remoteSwarmStatus, record domain.Object) (*ssh.Client, string, error) {
	if len(input.SwarmManagerServer) == 0 || input.SwarmManagerSSHKey == "" {
		return nil, "SWARM_WORKER_MANAGER_NOT_ASSOCIATED", nil
	}
	manager, err := sshClient(ctx, input.SwarmManagerServer, input.SwarmManagerSSHKey)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "SWARM_MANAGER_SSH_UNAVAILABLE", nil
	}
	reject := func(reason string) (*ssh.Client, string, error) { _ = manager.Close(); return nil, reason, nil }
	managerStatus, err := readRemoteSwarmStatus(ctx, manager, "")
	if err != nil || !managerStatus.Active || !managerStatus.ControlAvailable || managerStatus.Role != "manager" {
		return reject("SWARM_MANAGER_NOT_READY")
	}
	if err := validateSwarmClusterIDs(stringValue(record["clusterId"]), managerStatus.ClusterID); err != nil {
		return reject(stableErrorCode(err, "SWARM_CLUSTER_MISMATCH"))
	}
	run := func(command string) (string, error) { return remoteOutput(ctx, manager, command) }
	exists, _, err := inspectOwnedSwarmWorker(run, status.NodeID, stringValue(input.Server["id"]))
	if err != nil {
		return reject(stableErrorCode(err, "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH"))
	}
	if !exists {
		return reject("SWARM_CLUSTER_MISMATCH")
	}
	record["clusterId"], record["managerAddress"] = managerStatus.ClusterID, managerStatus.ManagerAddress
	output, err := run("docker node ps --filter desired-state=running --format '{{.ID}}|{{.DesiredState}}|{{.CurrentState}}' " + shellQuote(status.NodeID))
	if err != nil {
		return reject("SWARM_WORKER_TASK_PROBE_FAILED")
	}
	for _, line := range nonEmptyLines(output) {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) == 3 {
			states := strings.Fields(parts[2])
			if len(states) > 0 {
				switch strings.ToLower(states[0]) {
				case "shutdown", "complete", "failed", "rejected", "remove":
					continue
				}
			}
		}
		_ = manager.Close()
		return nil, "", errors.New("SWARM_LEAVE_BLOCKED_SERVICES")
	}
	return manager, "", nil
}

func removeOwnedDepartedSwarmWorker(ctx context.Context, run func(string) (string, error), nodeID, serverID string) error {
	deadline := time.NewTimer(35 * time.Second)
	defer deadline.Stop()
	for {
		exists, removable, err := inspectOwnedSwarmWorker(run, nodeID, serverID)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		if removable {
			if _, err := run("docker node rm " + shellQuote(nodeID)); err != nil {
				return errors.New("SWARM_WORKER_NODE_REMOVE_FAILED")
			}
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return errors.New("SWARM_WORKER_NODE_STILL_ACTIVE")
		case <-timer.C:
		}
	}
}

func saveSwarmWorkerDeparture(ctx context.Context, input ServerRuntimeExecutionInput, record domain.Object) error {
	record["updatedAt"] = domain.Now()
	if input.SaveSwarmDeparture == nil {
		return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
	}
	if err := input.SaveSwarmDeparture(ctx, cloneObject(record)); err != nil {
		return errors.New("SWARM_DEPARTURE_RECORD_SAVE_FAILED")
	}
	return nil
}

func saveSwarmWorkerDepartureAfterLeave(ctx context.Context, input ServerRuntimeExecutionInput, record domain.Object) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return saveSwarmWorkerDeparture(ctx, input, record)
}

func cleanupSwarmDeparturesBeforeJoin(ctx context.Context, manager sshSessionClient, input ServerRuntimeExecutionInput, current, managerStatus remoteSwarmStatus) error {
	for _, record := range objectSlice(input.Server["swarmDepartures"]) {
		nodeID := stringValue(record["nodeId"])
		if record["cleanupStatus"] == "removed" || nodeID == "" || nodeID == current.NodeID || stringValue(record["clusterId"]) == "" || record["clusterId"] != managerStatus.ClusterID {
			continue
		}
		run := func(command string) (string, error) { return remoteOutput(ctx, manager, command) }
		exists, _, err := inspectOwnedSwarmWorker(run, nodeID, stringValue(input.Server["id"]))
		if err != nil && err.Error() == "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH" {
			record["cleanupStatus"], record["reasonCode"] = "pending", err.Error()
			if err := saveSwarmWorkerDeparture(ctx, input, record); err != nil {
				return err
			}
			input.LogStep("swarm", "join-cleanup", "warn", "同群旧节点不具备可验证的 Ops 归属，保留给集群管理员清理，不删除当前节点")
			continue
		}
		if err != nil {
			return err
		}
		if exists {
			if err := removeOwnedDepartedSwarmWorker(ctx, run, nodeID, stringValue(input.Server["id"])); err != nil {
				return err
			}
		}
		record["cleanupStatus"], record["reasonCode"] = "removed", ""
		if err := saveSwarmWorkerDeparture(ctx, input, record); err != nil {
			return err
		}
		input.LogStep("swarm", "join-cleanup", "info", "已清理同一目标集群中归属匹配的旧离线 worker 记录，保留当前新成员身份")
	}
	return nil
}
