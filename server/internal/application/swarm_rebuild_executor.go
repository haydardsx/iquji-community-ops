package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var errSwarmRebuildRecovered = errors.New("SWARM_REBUILD_RECOVERY_COMPLETE")

func (e *SystemServerRuntimeExecutor) RebuildDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	if len(input.SwarmRebuildCheckpoint) > 0 {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		ctx = recoveryCtx
	}
	if err := ValidateSwarmConnectionInput(input.SwarmConnectionMode, input.SwarmAdvertiseAddr, true); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`).MatchString(input.SwarmRebuildID) {
		return nil, errors.New("SWARM_REBUILD_CHECKPOINT_INVALID")
	}
	remote, closeRemote, err := connectSwarmRebuildRemote(ctx, input)
	if err != nil {
		return nil, err
	}
	defer closeRemote()
	status, err := executeSwarmRebuild(ctx, input, remote)
	if err != nil {
		return nil, err
	}
	return swarmRuntimeResult(input, status, true), nil
}

func connectSwarmRebuildRemote(ctx context.Context, input ServerRuntimeExecutionInput) (swarmRebuildRemote, func(), error) {
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return swarmRebuildRemote{}, func() {}, err
	}
	remote := swarmRebuildRemote{
		Call:   swarmEngineOverSSH(client),
		Status: func(ctx context.Context) (remoteSwarmStatus, error) { return readRemoteSwarmStatus(ctx, client, "") },
		Local:  func(ctx context.Context) (swarmLocalConnection, error) { return readSwarmLocalConnection(ctx, client) },
		Output: func(ctx context.Context, command string) (string, error) { return remoteOutput(ctx, client, command) },
		Write: func(ctx context.Context, path string, raw []byte) error {
			return writeRemoteFileBytes(ctx, client, path, raw)
		},
		Reconnect: func(ctx context.Context) (swarmRebuildRemote, func(), error) {
			return connectSwarmRebuildRemote(ctx, input)
		},
	}
	return remote, func() { _ = client.Close() }, nil
}

func executeSwarmRebuild(ctx context.Context, input ServerRuntimeExecutionInput, remote swarmRebuildRemote) (remoteSwarmStatus, error) {
	if len(input.SwarmRebuildCheckpoint) > 0 {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		ctx = recoveryCtx
		if err := validateSwarmRebuildCheckpointObject(input.SwarmRebuildCheckpoint); err != nil {
			return remoteSwarmStatus{}, err
		}
		raw, _ := json.Marshal(input.SwarmRebuildCheckpoint)
		var cp swarmRebuildCheckpoint
		_ = json.Unmarshal(raw, &cp)
		if cp.BackupID != input.SwarmRebuildID {
			return remoteSwarmStatus{}, errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
		}
		m, err := readSwarmRebuildBackup(ctx, remote, cp)
		if err != nil {
			return remoteSwarmStatus{}, err
		}
		if cp.Phase == "verified" || cp.Phase == "recovered" {
			target, err := swarmRebuildTarget(m, input.SwarmAdvertiseAddr)
			if cp.Phase == "recovered" {
				target = m.Local
				err = nil
			}
			if err != nil {
				return remoteSwarmStatus{}, err
			}
			if err := waitVerifySwarmRebuild(ctx, remote, m, cp, target); err != nil {
				return remoteSwarmStatus{}, err
			}
			cleanupSwarmRebuildOwner(ctx, input, remote)
			if cp.Phase == "recovered" {
				return remoteSwarmStatus{}, errSwarmRebuildRecovered
			}
			return remote.Status(ctx)
		}
		input.LogStep("swarm", "rebuild-recovery", "info", "发现已有恢复检查点，使用原始备份恢复连接与受管网关，不创建新备份")
		if err := recoverSwarmRebuild(ctx, input, remote, m, &cp); err != nil {
			input.LogStep("swarm", "rebuild-recovery", "error", "Swarm 恢复未完成；阶段："+cp.Phase+"；诊断类别："+swarmRebuildReason(err))
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return remoteSwarmStatus{}, errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
			}
			return remoteSwarmStatus{}, safeSwarmRebuildError(err, "SWARM_REBUILD_ROLLBACK_FAILED")
		}
		return remoteSwarmStatus{}, errSwarmRebuildRecovered
	}
	if input.SwarmRebuildRestoreOnly {
		return remoteSwarmStatus{}, errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}
	status, err := remote.Status(ctx)
	if err != nil || !status.Active || !status.ControlAvailable || status.Role != "manager" {
		return status, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	local, err := remote.Local(ctx)
	if err != nil {
		return status, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	m, err := readSwarmRebuildManifest(ctx, remote.Call, input, local)
	if err != nil {
		input.LogStep("swarm", "rebuild-preflight", "warn", "Swarm 重建前置检查未通过；诊断类别："+swarmRebuildReason(err))
		return status, safeSwarmRebuildError(err, "SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	if stringValue(rebuildObject(m.Cluster)["ID"]) != status.ClusterID || stringValue(rebuildObject(m.Node)["ID"]) != status.NodeID || local.AdvertiseAddr != status.ManagerAddress {
		return status, errors.New("SWARM_REBUILD_STATE_UNAVAILABLE")
	}
	input.LogStep("swarm", "rebuild-backup", "info", "受限范围已核验，开始保存远端逻辑备份；不会重启 Docker 或停止独立容器")
	cp, err := prepareSwarmRebuildBackup(ctx, remote, m)
	if err != nil {
		return status, safeSwarmRebuildError(err, "SWARM_REBUILD_BACKUP_FAILED")
	}
	if err := saveSwarmRebuildCheckpoint(ctx, input, cp); err != nil {
		return status, err
	}
	result, executionErr := performSwarmRebuild(ctx, input, remote, m, &cp)
	if executionErr == nil {
		return result, nil
	}
	input.LogStep("swarm", "rebuild-recovery", "warn", "Swarm 重建未完成，开始根据原始备份恢复连接和受管网关；阶段："+cp.Phase+"；诊断类别："+swarmRebuildReason(executionErr))
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	if remote.Reconnect != nil {
		fresh, closeFresh, connectErr := remote.Reconnect(recoveryCtx)
		if connectErr != nil {
			return result, errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
		}
		defer closeFresh()
		remote = fresh
	}
	if err := recoverSwarmRebuild(recoveryCtx, input, remote, m, &cp); err != nil {
		input.LogStep("swarm", "rebuild-recovery", "error", "SWARM_REBUILD_ROLLBACK_FAILED；自动恢复未通过核验，保留原始备份，需继续恢复；阶段："+cp.Phase+"；诊断类别："+swarmRebuildReason(err))
		return result, errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
	}
	input.LogStep("swarm", "rebuild-recovery", "info", "原连接配置和受管网关已完成逻辑恢复并核验；集群及节点身份可能已变化")
	if ctx.Err() != nil {
		return result, errSwarmRebuildRecovered
	}
	return result, errors.New("SWARM_REBUILD_INTERRUPTED_RECOVERED")
}

func performSwarmRebuild(ctx context.Context, input ServerRuntimeExecutionInput, remote swarmRebuildRemote, m swarmRebuildManifest, cp *swarmRebuildCheckpoint) (remoteSwarmStatus, error) {
	if err := stopSwarmRebuildGateway(ctx, remote, m); err != nil {
		return remoteSwarmStatus{}, err
	}
	dataHash, err := backupSwarmRebuildData(ctx, remote, *cp, len(m.Gateway) > 0)
	if err != nil {
		return remoteSwarmStatus{}, err
	}
	cp.DataHash, cp.Phase = dataHash, "backed_up"
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		return remoteSwarmStatus{}, err
	}
	if err := verifySwarmRebuildData(ctx, remote, *cp); err != nil {
		return remoteSwarmStatus{}, err
	}
	// 最终备份完成后再次检查资源范围，防止备份期间出现新成员或业务资源。
	current, err := readSwarmRebuildManifest(ctx, remote.Call, input, m.Local)
	if err != nil || len(current.Gateway) > 0 || !swarmRebuildBaselineEqual(m, current) {
		return remoteSwarmStatus{}, errors.New("SWARM_REBUILD_SCOPE_UNSUPPORTED")
	}
	cp.Phase = "leaving"
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		return remoteSwarmStatus{}, err
	}
	input.LogStep("swarm", "rebuild", "info", "逻辑配置和停止写入后的网关数据备份已校验，开始退出旧 Swarm 并创建新集群")
	if _, err := remote.Call(ctx, http.MethodPost, "/swarm/leave?force=1", nil); err != nil {
		return remoteSwarmStatus{}, err
	}
	if err := waitSwarmRebuildOverlaysRemoved(ctx, remote); err != nil {
		return remoteSwarmStatus{}, err
	}
	target, err := swarmRebuildTarget(m, input.SwarmAdvertiseAddr)
	if err != nil {
		return remoteSwarmStatus{}, err
	}
	status, err := initSwarmRebuild(ctx, remote, m, target)
	if err != nil {
		return status, err
	}
	if status.ClusterID == stringValue(rebuildObject(m.Cluster)["ID"]) || status.NodeID == stringValue(rebuildObject(m.Node)["ID"]) {
		return status, errors.New("SWARM_REBUILD_VERIFY_FAILED")
	}
	cp.NewClusterID, cp.NewNodeID, cp.Phase = status.ClusterID, status.NodeID, "initialized"
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		return status, err
	}
	if err := restoreSwarmRebuildData(ctx, remote, *cp); err != nil {
		return status, err
	}
	if _, err := restoreSwarmRebuildResources(ctx, remote, m, status.NodeID); err != nil {
		return status, err
	}
	cp.Phase = "restored"
	if err := saveSwarmRebuildCheckpoint(ctx, input, *cp); err != nil {
		return status, err
	}
	if err := waitVerifySwarmRebuild(ctx, remote, m, *cp, target); err != nil {
		return status, err
	}
	if err := commitSwarmRebuild(ctx, input, remote, cp, "verified"); err != nil {
		return status, err
	}
	input.LogStep("swarm", "rebuild", "info", "新集群身份、真实通告端点、原网络配置、节点标签及受管网关已恢复并核验，远端原始备份已保留")
	return status, nil
}

func waitVerifySwarmRebuild(ctx context.Context, remote swarmRebuildRemote, m swarmRebuildManifest, cp swarmRebuildCheckpoint, target swarmLocalConnection) error {
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	var lastErr error
	for {
		err := verifySwarmRebuild(waitCtx, remote, m, cp, target)
		if err == nil {
			return nil
		}
		if waitCtx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		if err := waitSwarmConnectionTick(waitCtx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return lastErr
		}
	}
}

func safeSwarmRebuildError(err error, fallback string) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	for _, code := range []string{"SWARM_REBUILD_SCOPE_UNSUPPORTED", "SWARM_REBUILD_BACKUP_FAILED", "SWARM_REBUILD_STATE_UNAVAILABLE", "SWARM_REBUILD_CHECKPOINT_FAILED", "SWARM_REBUILD_CHECKPOINT_INVALID", "SWARM_REBUILD_VERIFY_FAILED", "SWARM_REBUILD_RECOVERY_REQUIRED", "SWARM_REBUILD_ROLLBACK_FAILED", "SWARM_REBUILD_INTERRUPTED_RECOVERED"} {
		if err != nil && err.Error() == code {
			return errors.New(code)
		}
	}
	return errors.New(fallback)
}
