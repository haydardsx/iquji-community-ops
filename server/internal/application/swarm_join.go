package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const swarmJoinWaitTimeout = 30 * time.Second

func installSwarmWorker(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, current remoteSwarmStatus) error {
	input = withServerRuntimeLogStep(input)
	if err := swarmWorkerJoinStateError(current); err != nil {
		input.LogStep("swarm", "join", "error", err.Error()+"；请先恢复 Manager 通告端点连通，再检查并显式退出失败加入状态后重试")
		return swarmOperationError(ctx, err, err.Error())
	}
	if len(input.SwarmManagerServer) == 0 || strings.TrimSpace(input.SwarmManagerSSHKey) == "" {
		return errors.New("SWARM_MANAGER_REQUIRED")
	}
	manager, err := sshClient(ctx, input.SwarmManagerServer, input.SwarmManagerSSHKey)
	if err != nil {
		return swarmOperationError(ctx, err, "SWARM_MANAGER_SSH_UNAVAILABLE")
	}
	defer func() { _ = manager.Close() }()
	managerStatus, err := readRemoteSwarmStatus(ctx, manager, input.TraefikNetwork)
	if err != nil || !managerStatus.Active || managerStatus.Role != "manager" {
		return swarmOperationError(ctx, err, "SWARM_MANAGER_NOT_READY")
	}
	if managerStatus.ClusterID == "" {
		return errors.New("SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE")
	}
	if err := cleanupSwarmDeparturesBeforeJoin(ctx, manager, input, current, managerStatus); err != nil {
		return err
	}
	endpoint := ""
	if input.SwarmConnectionMode != "" {
		endpoint, err = readSwarmManagerEndpoint(ctx, manager, managerStatus.NodeID)
		if err != nil {
			return err
		}
		if swarmAddressConnectionMode(endpoint) != input.SwarmConnectionMode {
			return errors.New("SWARM_MANAGER_CONNECTION_MODE_MISMATCH")
		}
	}
	if current.Active {
		return verifyAndLabelSwarmWorker(ctx, manager, input, current, managerStatus)
	}
	if current.Status == "pending" {
		input.LogStep("swarm", "join", "info", "节点正在后台加入集群，等待真实成员身份，不重复提交加入命令")
		joined, err := waitForSwarmWorker(ctx, client, input, swarmJoinWaitTimeout)
		if err != nil {
			return err
		}
		return verifyAndLabelSwarmWorker(ctx, manager, input, joined, managerStatus)
	}
	if endpoint == "" {
		endpoint, err = readSwarmManagerEndpoint(ctx, manager, managerStatus.NodeID)
		if err != nil {
			return err
		}
	}
	advertiseAddr, err := swarmAdvertiseAddress(input)
	if err != nil {
		return err
	}
	advertiseIP := net.ParseIP(strings.TrimSpace(advertiseAddr))
	if advertiseIP == nil || advertiseIP.IsUnspecified() || advertiseIP.IsMulticast() {
		return errors.New("SWARM_ADVERTISE_ADDRESS_INVALID")
	}
	advertiseAddr = advertiseIP.String()
	input.LogStep("swarm", "join", "info", "Manager 控制端点 "+endpoint+"；本节点通告 IP "+advertiseAddr)
	if err := probeSwarmManagerEndpoint(ctx, client, endpoint, input); err != nil {
		return err
	}
	token, err := remoteOutput(ctx, manager, "docker swarm join-token -q worker")
	if err != nil || strings.TrimSpace(token) == "" {
		return swarmOperationError(ctx, err, "SWARM_JOIN_TOKEN_UNAVAILABLE")
	}
	join := map[string]any{
		"ListenAddr": "0.0.0.0:2377", "AdvertiseAddr": advertiseAddr,
		"RemoteAddrs": []string{endpoint}, "JoinToken": strings.TrimSpace(token),
	}
	if input.SwarmConnectionMode != "" {
		join["DataPathAddr"] = advertiseAddr
	}
	_, joinErr := swarmEngineOverSSH(client)(ctx, http.MethodPost, "/swarm/join", join)
	if ctx.Err() != nil || errors.Is(joinErr, context.Canceled) || errors.Is(joinErr, context.DeadlineExceeded) {
		return swarmOperationError(ctx, joinErr, "SWARM_WORKER_JOIN_FAILED")
	}
	if joinErr != nil {
		input.LogStep("swarm", "join", "warn", "加入请求返回 "+stableErrorCode(joinErr, "SWARM_WORKER_JOIN_FAILED")+"；正在核验实际成员状态")
	}
	joined, err := waitForSwarmWorker(ctx, client, input, swarmJoinWaitTimeout)
	if err != nil {
		if joinErr != nil && err.Error() == "SWARM_JOIN_STATE_INVALID" {
			return errors.New(stableErrorCode(joinErr, "SWARM_WORKER_JOIN_FAILED"))
		}
		return err
	}
	return verifyAndLabelSwarmWorker(ctx, manager, input, joined, managerStatus)
}

func verifyAndLabelSwarmWorker(ctx context.Context, manager sshSessionClient, input ServerRuntimeExecutionInput, worker, managerStatus remoteSwarmStatus) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	currentManager, err := readRemoteSwarmStatus(ctx, manager, "")
	if err != nil {
		return swarmOperationError(ctx, err, "SWARM_CLUSTER_UNVERIFIED")
	}
	if err := validateSwarmClusterIDs(managerStatus.ClusterID, currentManager.ClusterID); err != nil {
		return err
	}
	if _, err := verifiedSwarmWorkerCluster(ctx, manager, worker, currentManager); err != nil {
		return swarmOperationError(ctx, err, stableErrorCode(err, "SWARM_CLUSTER_UNVERIFIED"))
	}
	if input.SwarmConnectionMode != "" || input.SwarmAdvertiseAddr != "" {
		address, err := readSwarmWorkerAddress(ctx, manager, worker.NodeID)
		if err != nil {
			return err
		}
		worker.AdvertiseAddr = address
		if err := verifyRequestedSwarmConnection(input, worker); err != nil {
			return err
		}
	}
	if err := labelSwarmWorker(ctx, manager, input, worker.NodeID); err != nil {
		return swarmOperationError(ctx, err, "SWARM_NODE_LABEL_FAILED")
	}
	return ctx.Err()
}

func swarmWorkerJoinStateError(status remoteSwarmStatus) error {
	if status.Active {
		if status.Role != "worker" {
			return errors.New("SWARM_ROLE_CONFLICT")
		}
		return nil
	}
	switch status.Status {
	case "inactive":
		if status.ControlAvailable || status.NodeID != "" || status.ClusterID != "" {
			return errors.New("SWARM_JOIN_STATE_INVALID")
		}
		return nil
	case "pending":
		return nil
	case "locked":
		return errors.New("SWARM_NODE_LOCKED")
	case "error":
		if status.FailureCode != "" && status.FailureCode != "SWARM_WORKER_JOIN_FAILED" {
			return errors.New(status.FailureCode)
		}
		return errors.New("SWARM_NODE_ERROR")
	default:
		return errors.New("SWARM_JOIN_STATE_INVALID")
	}
}

func waitForSwarmWorker(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, timeout time.Duration) (remoteSwarmStatus, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		status, err := readRemoteSwarmStatus(waitCtx, client, input.TraefikNetwork)
		if err != nil {
			if ctx.Err() != nil {
				return remoteSwarmStatus{}, ctx.Err()
			}
			if waitCtx.Err() != nil {
				return remoteSwarmStatus{}, errors.New("SWARM_JOIN_PENDING")
			}
			return remoteSwarmStatus{}, swarmOperationError(ctx, err, "SWARM_WORKER_POST_JOIN_PROBE_FAILED")
		}
		if err := swarmWorkerJoinStateError(status); err != nil {
			return status, err
		}
		if status.Active {
			return status, nil
		}
		if status.Status != "pending" {
			return status, errors.New("SWARM_JOIN_STATE_INVALID")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return status, ctx.Err()
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return status, ctx.Err()
			}
			return status, errors.New("SWARM_JOIN_PENDING")
		case <-timer.C:
		}
	}
}

func readSwarmManagerEndpoint(ctx context.Context, client sshSessionClient, nodeID string) (string, error) {
	if nodeID == "" {
		return "", errors.New("SWARM_MANAGER_ENDPOINT_UNAVAILABLE")
	}
	output, err := remoteOutput(ctx, client, "docker node inspect "+shellQuote(nodeID)+" --format '{{.ManagerStatus.Addr}}'")
	if err != nil {
		return "", swarmOperationError(ctx, err, "SWARM_MANAGER_ENDPOINT_UNAVAILABLE")
	}
	return normalizeSwarmManagerEndpoint(output)
}

func normalizeSwarmManagerEndpoint(value string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("SWARM_MANAGER_ENDPOINT_INVALID")
	}
	ip := net.ParseIP(host)
	portNumber, err := strconv.Atoi(port)
	if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("SWARM_MANAGER_ENDPOINT_INVALID")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(portNumber)), nil
}

type swarmEndpointDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func probeSwarmManagerEndpoint(ctx context.Context, client sshSessionClient, endpoint string, input ServerRuntimeExecutionInput) error {
	dialer, ok := client.(swarmEndpointDialer)
	if !ok {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connection, err := dialer.DialContext(probeCtx, "tcp", endpoint)
	if err == nil {
		_ = connection.Close()
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var rejected *ssh.OpenChannelError
	if errors.As(err, &rejected) && (rejected.Reason == ssh.Prohibited || rejected.Reason == ssh.UnknownChannelType || rejected.Reason == ssh.ResourceShortage) {
		input.LogStep("swarm", "join", "info", "SSH 转发探测不可用，继续由 Docker 加入操作验证控制端点")
		return nil
	}
	if !errors.Is(err, context.DeadlineExceeded) && (rejected == nil || rejected.Reason != ssh.ConnectionFailed) {
		input.LogStep("swarm", "join", "info", "SSH 转发探测未取得连通性结论，继续由 Docker 加入操作验证控制端点")
		return nil
	}
	input.LogStep("swarm", "join", "error", "SWARM_MANAGER_ENDPOINT_UNAVAILABLE；本节点无法连接 Manager 控制端点 "+endpoint)
	return errors.New("SWARM_MANAGER_ENDPOINT_UNAVAILABLE")
}

// 原始命令输出仅在内存中匹配类别，不进入日志和错误响应。
func swarmJoinFailureCode(output string) string {
	message := strings.ToLower(output)
	for _, category := range []struct {
		code     string
		patterns []string
	}{
		{"SWARM_ADVERTISE_ADDRESS_INVALID", []string{"invalid advertise address", "invalid advertise-addr", "could not choose an ip address to advertise", "could not find an ip address to advertise", "could not find the system's ip address", "must specify a listening address"}},
		{"SWARM_JOIN_TOKEN_REJECTED", []string{"invalid join token", "join token is necessary", "token is invalid", "token was rejected"}},
		{"SWARM_JOIN_CERTIFICATE_FAILED", []string{"x509", "certificate", "tls handshake", "remote ca does not match"}},
		{"SWARM_MANAGER_ENDPOINT_UNAVAILABLE", []string{"deadline exceeded", "deadlineexceeded", "timeout", "timed out", "connection refused", "no route to host", "network is unreachable", "dial tcp", "name resolution"}},
		{"SWARM_JOIN_STATE_INVALID", []string{"already part of a swarm"}},
	} {
		for _, pattern := range category.patterns {
			if strings.Contains(message, pattern) {
				return category.code
			}
		}
	}
	return "SWARM_WORKER_JOIN_FAILED"
}

func swarmOperationError(ctx context.Context, err error, code string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New(code)
}

func readSwarmWorkerAddress(ctx context.Context, manager sshSessionClient, nodeID string) (string, error) {
	address, err := remoteOutput(ctx, manager, "docker node inspect "+shellQuote(nodeID)+" --format '{{.Status.Addr}}'")
	if err != nil || swarmAddressConnectionMode(address) == "unknown" {
		return "", swarmOperationError(ctx, err, "SWARM_NODE_ADDRESS_UNVERIFIED")
	}
	return net.ParseIP(address).String(), nil
}
