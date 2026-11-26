package application

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (e *SystemServerRuntimeExecutor) ReconfigureDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	if err := ValidateSwarmConnectionInput(input.SwarmConnectionMode, input.SwarmAdvertiseAddr, true); err != nil {
		return nil, err
	}
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	current, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil {
		return nil, swarmOperationError(ctx, err, "SWARM_PROBE_FAILED")
	}
	readLocal := func(ctx context.Context) (swarmLocalConnection, error) { return readSwarmLocalConnection(ctx, client) }
	if err := verifyOrRestoreSwarmConnection(ctx, input, current, swarmEngineOverSSH(client), readLocal); err != nil {
		return nil, err
	}
	post, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil {
		return nil, swarmOperationError(ctx, err, "SWARM_CONNECTION_VERIFY_FAILED")
	}
	return swarmRuntimeResult(input, post, true), nil
}

func verifyOrRestoreSwarmConnection(ctx context.Context, input ServerRuntimeExecutionInput, current remoteSwarmStatus, call swarmEngineRequest, readLocal func(context.Context) (swarmLocalConnection, error)) error {
	if !current.Active || !current.ControlAvailable || current.Role != "manager" || current.NodeID == "" {
		return errors.New("SWARM_CONNECTION_MANAGER_REQUIRED")
	}
	if !input.SwarmConnectionRestoreOnly {
		managerIP, _, err := net.SplitHostPort(current.ManagerAddress)
		if err != nil || net.ParseIP(managerIP) == nil {
			return errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
		}
		if !net.ParseIP(managerIP).Equal(net.ParseIP(input.SwarmAdvertiseAddr)) {
			return swarmConnectionMigrationRequired(input)
		}
	}
	local, err := readLocal(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if !input.SwarmConnectionRestoreOnly {
		endpoint, endpointErr := normalizeSwarmManagerEndpoint(local.AdvertiseAddr)
		if endpointErr != nil || endpoint != current.ManagerAddress || local.DataPathAddr != "" && !net.ParseIP(local.DataPathAddr).Equal(net.ParseIP(input.SwarmAdvertiseAddr)) {
			return swarmConnectionMigrationRequired(input)
		}
	}
	baseline, err := readSwarmEngineBaseline(ctx, call, current.NodeID, local)
	if err != nil {
		return err
	}
	baseline.ReadLocal = readLocal
	if baseline.ClusterID != current.ClusterID {
		return errors.New("SWARM_CLUSTER_MISMATCH")
	}
	if input.SwarmConnectionRestoreOnly || len(input.SwarmConnectionCheckpoint) > 0 {
		baseline, err = prepareSwarmConnectionCheckpoint(ctx, input, baseline)
		if err != nil {
			return err
		}
	}
	if input.SwarmConnectionRestoreOnly {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := restoreSwarmConnection(recoveryCtx, call, baseline); err != nil {
			input.LogStep("swarm", "connection", "error", "SWARM_CONNECTION_ROLLBACK_FAILED；原通告恢复未通过真实验证；诊断类别："+swarmConnectionFailureReason(err))
			return errors.New("SWARM_CONNECTION_ROLLBACK_FAILED")
		}
		return context.Canceled
	}
	input.LogStep("swarm", "connection", "info", "Swarm 通告地址已符合所选连接方式，正在核验真实资源")
	err = verifySwarmConnection(ctx, call, baseline, local)
	if err != nil {
		input.LogStep("swarm", "connection", "error", "Swarm 连接核验失败；诊断类别："+swarmConnectionFailureReason(err))
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return err
}

func swarmConnectionMigrationRequired(input ServerRuntimeExecutionInput) error {
	input.LogStep("swarm", "connection", "warn", "SWARM_CONNECTION_MIGRATION_REQUIRED；现有 manager 地址变更请使用“备份后重建 Swarm”；仅支持单 manager、单节点、无业务 Swarm 服务及 secret/config 的受限范围，并恢复受管网关")
	return errors.New("SWARM_CONNECTION_MIGRATION_REQUIRED")
}

func restoreSwarmConnection(ctx context.Context, call swarmEngineRequest, baseline swarmEngineBaseline) error {
	if verifySwarmConnection(ctx, call, baseline, baseline.Local) == nil {
		return nil
	}
	// Engine 的控制锁串行化初始化，即使新控制面不可读，也直接有界提交旧配置。
	request, err := swarmConnectionInitBody(baseline.Init)
	if err != nil {
		return err
	}
	if _, err := call(ctx, http.MethodPost, "/swarm/init", request); err != nil {
		return err
	}
	return waitSwarmConnection(ctx, call, baseline, baseline.Local, 45*time.Second)
}

func waitSwarmConnection(ctx context.Context, call swarmEngineRequest, baseline swarmEngineBaseline, expected swarmLocalConnection, timeout time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		err := verifySwarmConnection(waitCtx, call, baseline, expected)
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

func waitSwarmConnectionTick(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func verifySwarmConnection(ctx context.Context, call swarmEngineRequest, baseline swarmEngineBaseline, expected swarmLocalConnection) error {
	raw, err := call(ctx, http.MethodGet, "/swarm", nil)
	var cluster struct {
		ID              string
		Spec            json.RawMessage
		DataPathPort    uint32
		DefaultAddrPool []string
		SubnetSize      uint32
	}
	if err != nil {
		return swarmConnectionVerificationError("CLUSTER_UNAVAILABLE")
	}
	if json.Unmarshal(raw, &cluster) != nil {
		return swarmConnectionVerificationError("CLUSTER_RESPONSE_INVALID")
	}
	if cluster.ID != baseline.ClusterID {
		return swarmConnectionVerificationError("CLUSTER_ID_CHANGED")
	}
	if !swarmJSONEqual(cluster.Spec, baseline.Init["Spec"]) {
		return swarmConnectionVerificationError("CLUSTER_SPEC_CHANGED")
	}
	if cluster.DataPathPort != baseline.Init["DataPathPort"] {
		return swarmConnectionVerificationError("DATA_PATH_PORT_CHANGED")
	}
	if !reflect.DeepEqual(effectiveSwarmDefaultAddrPool(cluster.DefaultAddrPool), effectiveSwarmDefaultAddrPool(baseline.Init["DefaultAddrPool"].([]string))) {
		return swarmConnectionVerificationError("DEFAULT_ADDR_POOL_CHANGED")
	}
	if cluster.SubnetSize != baseline.Init["SubnetSize"] {
		return swarmConnectionVerificationError("SUBNET_SIZE_CHANGED")
	}
	raw, err = call(ctx, http.MethodGet, "/nodes", nil)
	var nodes []struct {
		ID   string
		Spec struct {
			Role         string
			Availability string
		}
		ManagerStatus *struct {
			Addr         string
			Reachability string
			Leader       bool
		}
	}
	if err != nil || json.Unmarshal(raw, &nodes) != nil {
		return swarmConnectionVerificationError("NODES_UNAVAILABLE")
	}
	actualNodes := map[string]bool{}
	managerReason, managers := "MANAGER_MISSING", 0
	for _, node := range nodes {
		actualNodes[node.ID] = true
		if node.Spec.Role == "manager" {
			managers++
		}
		if node.ID == baseline.NodeID && node.Spec.Role == "manager" && node.ManagerStatus != nil {
			switch {
			case node.ManagerStatus.Addr != expected.AdvertiseAddr:
				managerReason = "MANAGER_ENDPOINT_CHANGED"
			case node.Spec.Availability != baseline.Init["Availability"]:
				managerReason = "MANAGER_AVAILABILITY_CHANGED"
			case node.ManagerStatus.Reachability != "reachable" || !node.ManagerStatus.Leader:
				managerReason = "MANAGER_NOT_READY"
			default:
				managerReason = ""
			}
		}
	}
	if !reflect.DeepEqual(actualNodes, baseline.Nodes) {
		return swarmConnectionVerificationError("NODE_IDENTITIES_CHANGED")
	}
	if managers != 1 {
		return swarmConnectionVerificationError("MANAGER_COUNT_CHANGED")
	}
	if managerReason != "" {
		return swarmConnectionVerificationError(managerReason)
	}
	if baseline.ReadLocal == nil {
		return errors.New("SWARM_CONNECTION_STATE_UNAVAILABLE")
	}
	local, err := baseline.ReadLocal(ctx)
	if err != nil {
		return swarmConnectionVerificationError("LOCAL_STATE_UNAVAILABLE")
	}
	if local.ListenAddr != expected.ListenAddr {
		return swarmConnectionVerificationError("LOCAL_LISTEN_CHANGED")
	}
	if local.AdvertiseAddr != expected.AdvertiseAddr {
		return swarmConnectionVerificationError("LOCAL_ADVERTISE_CHANGED")
	}
	if local.DataPathAddr != expected.DataPathAddr {
		return swarmConnectionVerificationError("LOCAL_DATA_PATH_CHANGED")
	}
	services, running, networks, err := readSwarmEngineResources(ctx, call)
	if err != nil {
		return swarmConnectionVerificationError("RESOURCES_UNAVAILABLE")
	}
	if !reflect.DeepEqual(services, baseline.Services) {
		return swarmConnectionVerificationError("SERVICE_SPEC_CHANGED")
	}
	if !reflect.DeepEqual(networks, baseline.Networks) {
		return swarmConnectionVerificationError("NETWORK_IDENTITIES_CHANGED")
	}
	for service, count := range baseline.Running {
		if running[service] < count {
			return swarmConnectionVerificationError("RUNNING_TASKS_BELOW_BASELINE")
		}
	}
	return nil
}

// 公开错误码保持稳定；诊断日志只使用固定类别，不拼接 Engine 原始输出或配置值。
type swarmConnectionVerificationError string

func (swarmConnectionVerificationError) Error() string { return "SWARM_CONNECTION_VERIFY_FAILED" }

func swarmConnectionFailureReason(err error) string {
	var verification swarmConnectionVerificationError
	if errors.As(err, &verification) {
		return string(verification)
	}
	if errors.Is(err, context.Canceled) {
		return "OPERATION_CANCELED"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "OPERATION_TIMED_OUT"
	}
	return "ENGINE_REQUEST_FAILED"
}

func swarmJSONEqual(raw json.RawMessage, expected any) bool {
	hash := swarmValueHash(raw)
	return hash != "" && hash == swarmValueHash(expected)
}
