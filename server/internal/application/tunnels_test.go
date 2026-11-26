package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

// 每次测试进程生成独立材料，仓库不保存可复用私钥。
var validTestSSHPrivateKey = func() string {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic("生成测试 SSH 私钥失败：" + err.Error())
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic("编码测试 SSH 私钥失败：" + err.Error())
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}()

type fakeFrpExecutor struct {
	startErr   error
	stopErr    error
	cleanupErr error
	starts     int
	stops      int
	cleanups   int
}

func (f *fakeFrpExecutor) StartFrpTunnel(_ context.Context, input FrpTunnelExecutionInput) error {
	f.starts++
	if input.Token == "" {
		return errors.New("TOKEN_REQUIRED")
	}
	return f.startErr
}

func (f *fakeFrpExecutor) StopFrpTunnel(context.Context, FrpTunnelExecutionInput) error {
	f.stops++
	return f.stopErr
}

func (f *fakeFrpExecutor) CleanupFrpTunnel(context.Context, FrpTunnelExecutionInput) error {
	f.cleanups++
	return f.cleanupErr
}

type fakeServerRuntimeExecutor struct {
	probeResult           domain.Object
	probeErr              error
	installDockerResult   domain.Object
	installDockerErr      error
	uninstallDockerResult domain.Object
	uninstallDockerErr    error
	uninstallDockerCalls  int
	installSwarm          func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error)
	uninstallSwarm        func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error)
	reconfigureSwarm      func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error)
	rebuildSwarm          func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error)
	installJenkins        func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error)
}

func (f *fakeServerRuntimeExecutor) ProbeServerRuntime(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.probeResult != nil || f.probeErr != nil {
		return f.probeResult, f.probeErr
	}
	return domain.Object{
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker test"),
			serverRuntimeCheck("swarm", serverRuntimeCheckSuccess, "Docker Swarm manager is active"),
			serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik gateway available"),
			serverRuntimeCheck("jenkins", serverRuntimeCheckDanger, "JENKINS_NOT_INSTALLED"),
			serverRuntimeCheck("agent", serverRuntimeCheckDanger, "IQUOPS_MONITORING_AGENT_NOT_ACTIVE"),
		},
		"metrics": domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frps": true, "frpsStatus": "installed",
			"traefik": true, "traefikStatus": "running",
			"jenkins": false, "jenkinsStatus": "missing",
			"agent": false,
		},
		"remote": domain.Object{},
	}, nil
}

func (f *fakeServerRuntimeExecutor) InstallDockerEngine(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	return f.installDockerResult, f.installDockerErr
}

func (f *fakeServerRuntimeExecutor) UninstallDockerEngine(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	f.uninstallDockerCalls++
	return f.uninstallDockerResult, f.uninstallDockerErr
}

func (f *fakeServerRuntimeExecutor) InstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.installSwarm != nil {
		return f.installSwarm(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) UninstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.uninstallSwarm != nil {
		return f.uninstallSwarm(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) ReconfigureDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.reconfigureSwarm != nil {
		return f.reconfigureSwarm(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) RebuildDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.rebuildSwarm != nil {
		return f.rebuildSwarm(ctx, input)
	}
	return nil, errors.New("未配置测试执行器")
}

func (f *fakeServerRuntimeExecutor) InstallJenkins(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	if f.installJenkins != nil {
		return f.installJenkins(ctx, input)
	}
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) UninstallJenkins(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) InstallMonitoringAgent(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeServerRuntimeExecutor) UninstallMonitoringAgent(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
	return nil, errors.New("not implemented")
}

func newFrpRuntime(t *testing.T) (*Runtime, *sqlite.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	tempDir := t.TempDir()
	config := platform.Resolve(platform.RoleWorker)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = tempDir
	config.DatabasePath = filepath.Join(tempDir, "state.sqlite")
	frpcPath, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	config.FrpcPath = frpcPath
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	runtime := NewRuntime(config, store)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{}
	return runtime, store, ctx
}

func seedFrpServer(t *testing.T, ctx context.Context, store *sqlite.Store, withMaterial bool) {
	t.Helper()
	err := store.MutateState(ctx, func(state *domain.State) error {
		state.SecretAssets = append(state.SecretAssets, domain.Object{
			"id":            "asset_deploy",
			"assetType":     "ssh_key",
			"purpose":       "deployment_server",
			"name":          "Deploy SSH",
			"provider":      "manual",
			"secretRef":     "secret_deploy",
			"maskedSummary": "ssh-key",
			"fingerprint":   "fp",
			"createdAt":     domain.Now(),
			"updatedAt":     domain.Now(),
		})
		state.Servers = append(state.Servers, domain.Object{
			"id":             "srv_gateway",
			"name":           "Gateway",
			"publicHost":     "203.0.113.10",
			"port":           22,
			"sshUser":        "root",
			"environment":    "staging",
			"labels":         []string{},
			"projectIds":     []string{},
			"secretAssetIds": []string{"asset_deploy"},
			"createdAt":      domain.Now(),
			"updatedAt":      domain.Now(),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed state: %v", err)
	}
	if withMaterial {
		if err := store.UpsertSecretMaterial(ctx, "secret_deploy", "private-key"); err != nil {
			t.Fatalf("seed secret material: %v", err)
		}
	}
}

func openLocalTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen local tcp: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func closedLocalTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local tcp port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release local tcp port: %v", err)
	}
	return port
}

func tunnelRequest(localPort ...int) domain.FrpTunnelInput {
	port := 3001
	if len(localPort) > 0 {
		port = localPort[0]
	}
	return domain.FrpTunnelInput{
		Name: "Local API", ServerID: "srv_gateway", FrpsBindPort: 7000,
		Mappings: []domain.FrpTunnelMappingInput{
			{
				TargetType: domain.FrpTunnelMappingTargetLocalService, Name: "local-api",
				LocalHost: "127.0.0.1", LocalPort: port, FrpsProxyPort: 18081,
				PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
			},
		},
	}
}

type createdFrpTunnel struct {
	TunnelID string
	RunID    string
	TaskID   string
}

func createFrpTunnelForTest(t *testing.T, runtime *Runtime, ctx context.Context, request domain.FrpTunnelInput) createdFrpTunnel {
	t.Helper()
	result, err := runtime.CreateFrpTunnel(ctx, request)
	if err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	tunnel := objectMapFromAny(result["tunnel"])
	run := objectMapFromAny(result["run"])
	task := objectMapFromAny(result["task"])
	created := createdFrpTunnel{
		TunnelID: stringValue(tunnel["id"]),
		RunID:    stringValue(run["id"]),
		TaskID:   stringValue(task["id"]),
	}
	if created.TunnelID == "" || created.RunID == "" || created.TaskID == "" {
		t.Fatal("invalid create response")
	}
	target := objectMapFromAny(task["target"])
	if task["status"] != "queued" || target["kind"] != "frp_tunnel_run" || target["id"] != created.RunID {
		t.Fatal("invalid start task response")
	}
	return created
}

func confirmAndRunTaskForTest(t *testing.T, runtime *Runtime, ctx context.Context, taskID string) {
	t.Helper()
	if _, err := runtime.ConfirmTask(ctx, taskID); err != nil {
		t.Fatalf("confirm task: %v", err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("run worker: %v", err)
	}
}

func TestFrpTunnelPreviewBlocksLegacySSHAssetWithoutSecretMaterial(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, false)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest())
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if processDecisionAllowed(preview["process"]) {
		t.Fatal("preview should block legacy key without material")
	}
	reasons := processBlockers(preview["process"])
	if !slices.Contains(reasons, "SERVER_SSH_SECRET_MATERIAL_REQUIRED") {
		t.Fatal("missing secret material block reason")
	}
}

func TestFrpTunnelPreviewBlocksUnreachableLocalPort(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	fakeRuntime := &fakeServerRuntimeExecutor{}
	runtime.ServerRuntimeExecutor = fakeRuntime

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(closedLocalTCPPort(t)))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if processDecisionAllowed(preview["process"]) {
		t.Fatal("preview should block closed local port")
	}
	reasons := processBlockers(preview["process"])
	if !slices.Contains(reasons, "LOCAL_TCP_PORT_UNREACHABLE:local-api") {
		t.Fatal("missing local port block reason")
	}
}

func TestFrpTunnelPreviewBlocksRemoteDockerUnavailable(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckDanger, "REMOTE_DOCKER_UNAVAILABLE"),
			},
			"metrics": domain.Object{"ssh": true, "docker": false, "agent": false},
			"remote":  domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if processDecisionAllowed(preview["process"]) {
		t.Fatal("preview should block missing docker")
	}
	reasons := processBlockers(preview["process"])
	if !slices.Contains(reasons, "REMOTE_DOCKER_UNAVAILABLE") {
		t.Fatal("missing docker block reason")
	}
}

func TestFrpTunnelPreviewAllowsMissingJenkins(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
				serverRuntimeCheck("swarm", serverRuntimeCheckSuccess, "Docker Swarm manager is active"),
				serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik gateway available"),
				serverRuntimeCheck("jenkins", serverRuntimeCheckDanger, "JENKINS_NOT_INSTALLED"),
			},
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frps": true, "frpsStatus": "installed",
				"traefik": true, "traefikStatus": "running",
				"jenkins": false, "jenkinsStatus": "missing",
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !processDecisionAllowed(preview["process"]) {
		t.Fatal("preview must allow FRP when only Jenkins is missing")
	}
	for _, reason := range processBlockers(preview["process"]) {
		if strings.Contains(strings.ToUpper(reason), "JENKINS") {
			t.Fatal("Jenkins must not block FRP preview")
		}
	}
}

func TestFrpTunnelPreviewAllowsMissingAgent(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
				serverRuntimeCheck("swarm", serverRuntimeCheckSuccess, "Docker Swarm manager is active"),
				serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik gateway available"),
				serverRuntimeCheck("agent", serverRuntimeCheckDanger, "IQUOPS_MONITORING_AGENT_NOT_ACTIVE"),
			},
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frps": true, "frpsStatus": "installed",
				"traefik": true, "traefikStatus": "running", "agent": false,
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !processDecisionAllowed(preview["process"]) {
		t.Fatal("preview must allow FRP when only agent is missing")
	}
	for _, reason := range processBlockers(preview["process"]) {
		if strings.Contains(strings.ToUpper(reason), "AGENT") {
			t.Fatal("monitoring agent must not block FRP preview")
		}
	}
}

func TestFrpTunnelPreviewForExcludesItsOwnName(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
				serverRuntimeCheck("swarm", serverRuntimeCheckSuccess, "Docker Swarm manager is active"),
				serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik gateway available"),
			},
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frps": true, "frpsStatus": "installed",
				"traefik": true, "traefikStatus": "running",
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)
	now := domain.Now()
	err := store.MutateState(ctx, func(state *domain.State) error {
		state.FrpTunnels = append(state.FrpTunnels, domain.FrpTunnel{
			ID: "tun_edit", Name: "Local API", ServerID: "srv_gateway", FrpsBindPort: 7000,
			Mappings: []domain.FrpTunnelMapping{{
				Name: "local-api", TargetType: domain.FrpTunnelMappingTargetLocalService, LocalHost: "127.0.0.1",
				LocalPort: port, FrpsProxyPort: 18081, PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
			}},
			CreatedAt: now, UpdatedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed tunnel: %v", err)
	}

	// 不带隧道 ID 时，同一名称会被判成重复；带自身 ID 时必须放行。
	blocked, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview without id: %v", err)
	}
	if !slices.Contains(processBlockers(blocked["process"]), "FRP_TUNNEL_NAME_DUPLICATE") {
		t.Fatal("preview without id should detect duplicate name")
	}
	allowed, err := runtime.PreviewFrpTunnelFor(ctx, "tun_edit", tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview with id: %v", err)
	}
	if !processDecisionAllowed(allowed["process"]) {
		t.Fatal("preview for the same tunnel must not report duplicate name")
	}
}

func TestFrpTunnelPreviewBlocksSwarmManagerRequired(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
				serverRuntimeCheck("swarm", serverRuntimeCheckDanger, "SWARM_NOT_INITIALIZED"),
				serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik gateway available"),
			},
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": false, "swarmRole": "none",
				"traefik": true, "traefikStatus": "running",
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if processDecisionAllowed(preview["process"]) {
		t.Fatal("preview should block non-manager Swarm")
	}
	reasons := processBlockers(preview["process"])
	if !slices.Contains(reasons, "FRP_SWARM_MANAGER_REQUIRED") {
		t.Fatal("missing swarm manager block reason")
	}
}

func TestFrpTunnelPreviewBlocksSwarmWorkerRole(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": true, "swarmRole": "worker",
				"traefik": true, "traefikStatus": "running",
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(port))
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if processDecisionAllowed(preview["process"]) {
		t.Fatal("preview should block Swarm worker")
	}
	reasons := processBlockers(preview["process"])
	if !slices.Contains(reasons, "FRP_SWARM_MANAGER_REQUIRED") {
		t.Fatal("missing swarm manager block reason")
	}
}
