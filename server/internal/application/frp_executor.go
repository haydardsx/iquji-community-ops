package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

type SystemFrpTunnelExecutor struct {
	mu        sync.Mutex
	processes map[string]*exec.Cmd
}

func NewSystemFrpTunnelExecutor() *SystemFrpTunnelExecutor {
	return &SystemFrpTunnelExecutor{processes: map[string]*exec.Cmd{}}
}

type frpStartNotActivatedError struct{ error }

func (err frpStartNotActivatedError) Unwrap() error { return err.error }

func (e *SystemFrpTunnelExecutor) StartFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error {
	input = withFrpLogStep(input)
	if input.Run.ID == "" {
		return frpStartNotActivatedError{errors.New("FRP_TUNNEL_RUN_ID_REQUIRED")}
	}
	if _, err := executableFrpcPath(input); err != nil {
		return frpStartNotActivatedError{err}
	}
	if err := e.checkLocalPorts(ctx, input); err != nil {
		return frpStartNotActivatedError{err}
	}
	configPath, err := e.writeLocalFrpcConfig(input)
	if err != nil {
		return frpStartNotActivatedError{err}
	}
	if err := e.verifyFrpc(ctx, input, configPath); err != nil {
		return frpStartNotActivatedError{err}
	}
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return frpStartNotActivatedError{err}
	}
	defer func() { _ = client.Close() }()
	if err := activateManagedFrps(ctx, client, input); err != nil {
		return err
	}
	if err := e.startFrpc(input, configPath); err != nil {
		return err
	}
	if err := e.verifyPublicEndpoints(ctx, input); err != nil {
		return err
	}
	if !e.frpcRunning(input.Run.ID) {
		return errors.New("FRPC_PROCESS_EXITED")
	}
	return nil
}

func (e *SystemFrpTunnelExecutor) StopFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error {
	input = withFrpLogStep(input)
	e.stopLocalFrpc(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return parkRemoteFrps(ctx, client, input)
}

func (e *SystemFrpTunnelExecutor) CleanupFrpTunnel(ctx context.Context, input FrpTunnelExecutionInput) error {
	return e.StopFrpTunnel(ctx, input)
}

func (e *SystemFrpTunnelExecutor) checkLocalPorts(ctx context.Context, input FrpTunnelExecutionInput) error {
	for _, mapping := range input.Run.Mappings {
		endpoint := net.JoinHostPort(mapping.LocalHost, strconv.Itoa(mapping.LocalPort))
		input.LogStep("frpc", "local-port-check", "info", "正在检查本地 TCP 端口 "+endpoint)
		dialer := net.Dialer{Timeout: 2 * time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", endpoint)
		if err != nil {
			return fmt.Errorf("LOCAL_TCP_PORT_UNREACHABLE:%s: %w", mapping.Name, err)
		}
		_ = conn.Close()
	}
	return nil
}

func frpSwarmManagerNodeID(ctx context.Context, client sshSessionClient) (string, error) {
	output, err := remoteOutput(ctx, client, "docker info --format '{{.Swarm.LocalNodeState}}|{{.Swarm.ControlAvailable}}|{{.Swarm.NodeID}}'")
	parts := strings.SplitN(strings.TrimSpace(output), "|", 3)
	if err != nil || len(parts) != 3 || parts[0] != "active" || parts[1] != "true" || strings.TrimSpace(parts[2]) == "" {
		return "", errors.New("FRP_SWARM_MANAGER_REQUIRED")
	}
	return strings.TrimSpace(parts[2]), nil
}

func frpsSwarmServiceRemoveCommand(service string) string {
	quoted := shellQuote(service)
	return "if docker service inspect " + quoted + " >/dev/null 2>&1; then docker service rm " + quoted + " >/dev/null; fi; for attempt in $(seq 1 30); do if ! docker service inspect " + quoted + " >/dev/null 2>&1; then break; fi; sleep 1; done; if docker service inspect " + quoted + " >/dev/null 2>&1; then exit 1; fi"
}

func frpsSwarmServiceReadyCommand(service string) string {
	quoted := shellQuote(service)
	return "for attempt in $(seq 1 30); do replicas=$(docker service ls --filter name=" + quoted + " --format '{{.Name}}|{{.Replicas}}' | awk -F'|' -v expected=" + quoted + " '$1 == expected { print $2 }'); test \"$replicas\" = '1/1' && exit 0; sleep 1; done; exit 1"
}

func (e *SystemFrpTunnelExecutor) writeLocalFrpcConfig(input FrpTunnelExecutionInput) (string, error) {
	localDir := localFrpTunnelDir(input)
	if localDir == "" {
		return "", errors.New("FRP_LOCAL_STATE_DIR_REQUIRED")
	}
	if err := os.MkdirAll(localDir, 0o700); err != nil {
		return "", err
	}
	configPath := filepath.Join(localDir, "frpc.toml")
	if err := os.WriteFile(configPath, []byte(frpcConfig(input)), 0o600); err != nil {
		return "", err
	}
	return configPath, nil
}

func (e *SystemFrpTunnelExecutor) verifyFrpc(ctx context.Context, input FrpTunnelExecutionInput, configPath string) error {
	frpcPath, err := executableFrpcPath(input)
	if err != nil {
		return err
	}
	input.LogStep("frpc", "verify", "info", "正在校验本地 frpc 配置")
	cmd := exec.CommandContext(ctx, frpcPath, "verify", "-c", configPath)
	cmd.Env = frpcEnvironment(os.Environ(), input)
	if err := cmd.Run(); err != nil {
		return errors.New("FRPC_VERIFY_FAILED")
	}
	return nil
}

func (e *SystemFrpTunnelExecutor) startFrpc(input FrpTunnelExecutionInput, configPath string) error {
	frpcPath, err := executableFrpcPath(input)
	if err != nil {
		return err
	}
	runID := input.Run.ID
	const maxAttempts = 6
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			input.LogStep("frpc", "start-retry", "warn", "frpc 提前退出，正在重试")
			time.Sleep(2 * time.Second)
		}
		if err := e.startFrpcAttempt(input, frpcPath, configPath, runID); err != nil {
			lastErr = err
			if strings.HasPrefix(err.Error(), "FRPC_PROCESS_EXITED") {
				continue
			}
			return err
		}
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("FRPC_PROCESS_EXITED")
}

func (e *SystemFrpTunnelExecutor) startFrpcAttempt(input FrpTunnelExecutionInput, frpcPath string, configPath string, runID string) error {
	input.LogStep("frpc", "start", "info", "正在启动本地 frpc")
	cmd := exec.Command(frpcPath, "-c", configPath)
	cmd.Env = frpcEnvironment(os.Environ(), input)
	if err := cmd.Start(); err != nil {
		return err
	}
	e.mu.Lock()
	if previous := e.processes[runID]; previous != nil && previous.Process != nil {
		_ = previous.Process.Kill()
	}
	e.processes[runID] = cmd
	e.mu.Unlock()
	waitDone := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		e.mu.Lock()
		wasCurrent := e.processes[runID] == cmd
		if wasCurrent {
			delete(e.processes, runID)
		}
		e.mu.Unlock()
		if err != nil {
			input.LogStep("frpc", "exit", "error", "FRPC_PROCESS_EXITED；本地 frpc 进程异常退出")
		} else {
			input.LogStep("frpc", "exit", "info", "本地 frpc 已退出")
		}
		waitDone <- err
		if wasCurrent && input.OnFrpcExit != nil {
			input.OnFrpcExit()
		}
	}()
	select {
	case <-waitDone:
		return errors.New("FRPC_PROCESS_EXITED")
	case <-time.After(1500 * time.Millisecond):
	}
	return nil
}

type cappedLogBuffer struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (b *cappedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if b.limit > 0 && len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *cappedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func (e *SystemFrpTunnelExecutor) verifyPublicEndpoints(ctx context.Context, input FrpTunnelExecutionInput) error {
	for _, mapping := range input.Run.Mappings {
		publicURL := frpTunnelPublicURL(input.Server, mapping, input.TraefikHTTPPort, input.TraefikHTTPSPort, false)
		input.LogStep("frpc", "public-endpoint-check", "info", "正在检查公共入口 "+publicURL)
		var err error
		switch {
		case mapping.TargetType == domain.FrpTunnelMappingTargetHTTPProbe:
			err = verifyPublicFrpProbe(ctx, publicURL, input, mapping.Name)
		case mapping.PublicDomain == "":
			err = retryFrpEndpoint(ctx, 10, 700*time.Millisecond, func() error {
				return verifyTCPPort(ctx, serverPublicHost(input.Server), mapping.FrpsProxyPort, 2*time.Second)
			})
		default:
			err = httpGetWithRetry(ctx, publicURL, 10, 700*time.Millisecond)
		}
		if err != nil {
			return fmt.Errorf("FRP_PUBLIC_ENDPOINT_UNREACHABLE:%s: %w", mapping.Name, err)
		}
	}
	return nil
}

func (e *SystemFrpTunnelExecutor) frpcRunning(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.processes[runID] != nil
}

func (e *SystemFrpTunnelExecutor) stopLocalFrpc(input FrpTunnelExecutionInput) {
	runID := input.Run.ID
	e.mu.Lock()
	cmd := e.processes[runID]
	delete(e.processes, runID)
	e.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	input.LogStep("frpc", "stop", "info", "正在停止本地 frpc")
	_ = cmd.Process.Kill()
}

func sshClient(ctx context.Context, server map[string]any, privateKey string) (*ssh.Client, error) {
	if strings.TrimSpace(privateKey) == "" {
		return nil, errors.New("SSH_SECRET_MATERIAL_REQUIRED")
	}
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return nil, fmt.Errorf("SSH_PRIVATE_KEY_INVALID: %w", err)
	}
	return openServerSSH(ctx, server, signer, false, nil)
}

func remoteRun(ctx context.Context, client *ssh.Client, command string) error {
	_, err := remoteOutput(ctx, client, command)
	return err
}

func remoteOutput(ctx context.Context, client sshSessionClient, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()
	type commandResult struct {
		output []byte
		err    error
	}
	done := make(chan commandResult, 1)
	go func() {
		output, err := session.CombinedOutput(command)
		done <- commandResult{output: output, err: err}
	}()
	select {
	case result := <-done:
		output := strings.TrimSpace(string(result.output))
		if result.err != nil {
			return output, fmt.Errorf("%s: %w", output, result.err)
		}
		return output, nil
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		select {
		case <-done:
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		_ = session.Close()
		return "", ctx.Err()
	}
}

func writeRemoteFile(ctx context.Context, client *ssh.Client, path string, content string) error {
	return writeRemoteFileBytes(ctx, client, path, []byte(content))
}

func writeRemoteFileBytes(ctx context.Context, client *ssh.Client, path string, content []byte) error {
	return writeRemoteStream(ctx, client, "mkdir -p "+shellQuote(filepath.Dir(path))+" && umask 077 && cat > "+shellQuote(path), bytes.NewReader(content))
}

func writeRemoteStream(ctx context.Context, client *ssh.Client, command string, content io.Reader) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	if err := session.Start(command); err != nil {
		return err
	}
	go func() {
		_, writeErr := io.Copy(stdin, content)
		closeErr := stdin.Close()
		if writeErr != nil {
			done <- writeErr
			return
		}
		if closeErr != nil {
			done <- closeErr
			return
		}
		done <- session.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return ctx.Err()
	}
}
