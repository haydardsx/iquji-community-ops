package application

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestServerRuntimeProbeUpdatesDetectedPlatformProfile(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("platform", serverRuntimeCheckSuccess, "centos 7.9 / x86_64 / yum / systemd"),
			},
			"metrics": domain.Object{"ssh": true, "docker": false, "agent": false},
			"remote": domain.Object{
				"platformProfile": domain.Object{
					"osId":           "centos",
					"osVersion":      "7.9",
					"architecture":   "x86_64",
					"packageManager": "yum",
					"serviceManager": "systemd",
					"overrideSource": "detected",
					"detectedAt":     "2026-07-07T10:00:00Z",
				},
			},
		},
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["platformProfile"] = domain.Object{
			"osId":                    "unknown",
			"osVersion":               "unknown",
			"architecture":            "x86_64",
			"packageManager":          "unknown",
			"serviceManager":          "systemd",
			"overrideSource":          "manual",
			"dockerRepoMirror":        "auto",
			"dockerRegistryMirrorUrl": "https://mirror.ccs.tencentyun.com",
		}
		return nil
	}); err != nil {
		t.Fatalf("seed platform profile: %v", err)
	}

	if _, err := runtime.ServerRuntimeProbe(ctx, "srv_gateway"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	profile := objectMapFromAny(state.Servers[0]["platformProfile"])
	if profile["osId"] != "centos" || profile["packageManager"] != "yum" || profile["overrideSource"] != "detected" {
		t.Fatal("platform profile was not detected")
	}
	if profile["dockerRegistryMirrorUrl"] != "https://mirror.ccs.tencentyun.com" {
		t.Fatal("docker mirror should be preserved")
	}
}

func TestPlatformProfileFromRuntimeOutput(t *testing.T) {
	profile := platformProfileFromRemote(strings.Join([]string{
		`ID="tencentos"`,
		`VERSION_ID="3.1"`,
	}, "\n"), "amd64", "yum", "systemd")

	if profile["osId"] != "tencentos" || profile["architecture"] != "x86_64" || profile["packageManager"] != "yum" {
		t.Fatal("unexpected platform profile")
	}
}

func TestDockerInstallScriptUsesOfficialStableRepoWithoutLegacyFallback(t *testing.T) {
	script := dockerInstallScript()

	if strings.Contains(script, "yum-config-manager --add-repo") || strings.Contains(script, "dnf config-manager --add-repo") {
		t.Fatal("docker repo setup should not fetch docker-ce.repo over the network")
	}
	if !strings.Contains(script, "cat > /etc/yum.repos.d/docker-ce.repo <<EOF") {
		t.Fatal("docker repo setup should write yum repo file directly")
	}
	if !strings.Contains(script, "baseurl=${repo_base}/linux/centos/\\$releasever/\\$basearch/stable") {
		t.Fatal("docker repo setup should use Docker CE stable baseurl")
	}
	if !strings.Contains(script, "https://download.docker.com") {
		t.Fatal("docker repo setup should include official stable repo")
	}
	if !strings.Contains(script, "docker_ce_packages='docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin'") ||
		!strings.Contains(script, "$pm install -y $docker_ce_packages") {
		t.Fatal("docker install should install Docker CE packages")
	}
	if strings.Contains(script, "yum install -y docker\n") || strings.Contains(script, "yum install -y docker\"") {
		t.Fatal("docker install should not fall back to legacy distro docker package")
	}
}

func TestDockerInstallScriptUsesConfiguredRepoMirrorBeforeOfficialFallback(t *testing.T) {
	script := dockerInstallScript(domain.Object{
		"platformProfile": domain.Object{"dockerRepoMirror": "tencent"},
	})
	tencentIndex := strings.Index(script, "https://mirrors.cloud.tencent.com/docker-ce")
	officialIndex := strings.Index(script, "https://download.docker.com")
	if tencentIndex < 0 {
		t.Fatal("docker install should include configured Tencent Docker CE mirror")
	}
	if officialIndex < 0 {
		t.Fatal("docker install should keep official stable repo as fallback")
	}
	if tencentIndex > officialIndex {
		t.Fatal("configured Docker CE mirror should be tried before official fallback")
	}
}

func TestSecretAssetSummaryIsBoundedForLongSSHKey(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)

	asset, err := runtime.CreateSecretAsset(ctx, domain.Object{
		"name":        "Deploy SSH",
		"assetType":   "ssh_key",
		"purpose":     "deployment_server",
		"secretValue": validTestSSHPrivateKey,
	})
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if summary := stringValue(asset["maskedSummary"]); len(summary) > 64 || strings.Count(summary, "*") > 8 {
		t.Fatalf("summary should be bounded, got %q (%d chars)", summary, len(summary))
	}

	secrets, err := runtime.ListObjects(ctx, "secretAssets")
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	items := objectSlice(secrets["items"])
	if len(items) != 1 {
		t.Fatal("expected one secret")
	}
	if summary := stringValue(items[0]["maskedSummary"]); len(summary) > 64 {
		t.Fatalf("listed summary should be bounded, got %q", summary)
	}
}

func TestSecretAssetRejectsInvalidSSHPrivateKey(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)

	_, err := runtime.CreateSecretAsset(ctx, domain.Object{
		"name":        "Broken SSH",
		"assetType":   "ssh_key",
		"purpose":     "deployment_server",
		"secretValue": strings.Repeat("a", 1600),
	})
	if err == nil || !strings.Contains(err.Error(), "SSH_PRIVATE_KEY_INVALID") {
		t.Fatalf("expected invalid ssh key error, got %v", err)
	}
}

func TestDockerInstallFailureDoesNotMarkServerHealthy(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		installDockerErr: errors.New("REMOTE_DOCKER_INSTALL_FAILED"),
	}
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateTask(ctx, "install_docker_engine", "server", "srv_gateway", "Gateway", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm task: %v", err)
	}

	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
	updated, err := runtime.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated["status"] != "failed" {
		t.Fatal("docker install task should fail")
	}
	health, err := runtime.ServerHealth(ctx, "srv_gateway")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	metrics := objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"])
	if metrics["docker"] == true {
		t.Fatal("docker should not be marked healthy after failed install")
	}
}

func TestServerRuntimeProbeClearsStaleDockerVersionWhenUnavailable(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{
			"status":  "idle",
			"summary": "Server runtime verified",
			"metrics": domain.Object{
				"ssh":           true,
				"docker":        true,
				"dockerVersion": "1.13.1",
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed stale docker health: %v", err)
	}
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

	if _, err := runtime.ServerRuntimeProbe(ctx, "srv_gateway"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	health, err := runtime.ServerHealth(ctx, "srv_gateway")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	metrics := objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"])
	if metrics["docker"] != false {
		t.Fatal("docker should be false after unavailable probe")
	}
	if _, ok := metrics["dockerVersion"]; ok {
		t.Fatal("stale dockerVersion should be cleared")
	}
}

func TestDockerUninstallWorkerClearsRemoteDockerVersion(t *testing.T) {
	executor := &fakeServerRuntimeExecutor{
		uninstallDockerResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker Engine removed"),
			},
			"metrics": domain.Object{"ssh": true, "docker": false},
		},
	}
	runtime, store, ctx := newFrpRuntime(t)
	runtime.ServerRuntimeExecutor = executor
	seedFrpServer(t, ctx, store, true)
	err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{
			"status":  "idle",
			"summary": "Server runtime verified",
			"metrics": domain.Object{
				"ssh":           true,
				"docker":        true,
				"dockerVersion": "1.13.1",
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed stale docker health: %v", err)
	}
	task, err := runtime.CreateTask(ctx, "uninstall_docker_engine", "server", "srv_gateway", "Gateway", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm task: %v", err)
	}

	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
	if executor.uninstallDockerCalls != 1 {
		t.Fatalf("docker uninstall executor should be called once, got %d", executor.uninstallDockerCalls)
	}
	updated, err := runtime.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated["status"] != "succeeded" {
		t.Fatal("docker uninstall task should succeed")
	}
	health, err := runtime.ServerHealth(ctx, "srv_gateway")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	metrics := objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"])
	if metrics["docker"] != false {
		t.Fatal("docker should be false after uninstall")
	}
	if _, ok := metrics["dockerVersion"]; ok {
		t.Fatal("stale dockerVersion should be cleared after uninstall")
	}
}

func TestMonitoringReportFeedsServerRows(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)

	_, err := runtime.AcceptMonitoringReport(ctx, domain.Object{
		"serverId":        "srv_gateway",
		"version":         "linux-amd64",
		"protocolVersion": 1,
		"sampledAt":       "2026-07-07T11:10:00Z",
		"capabilities":    []string{"host", "docker"},
		"samples": []domain.Object{
			{
				"targetKind": "server",
				"targetId":   "srv_gateway",
				"sampledAt":  "2026-07-07T11:10:00Z",
				"values": domain.Object{
					"cpuPercent":      12.5,
					"memoryPercent":   34.5,
					"diskPercent":     56.5,
					"dockerDiskBytes": 1024.0,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("accept report: %v", err)
	}

	result, err := runtime.MonitoringServers(ctx)
	if err != nil {
		t.Fatalf("monitoring servers: %v", err)
	}
	items := objectSlice(result["items"])
	if len(items) != 1 {
		t.Fatal("expected one server row")
	}
	row := items[0]
	if row["cpuPercent"] != 12.5 || row["memoryPercent"] != 34.5 || row["diskPercent"] != 56.5 {
		t.Fatal("server metrics were not returned")
	}
	if row["lastSampleAt"] != "2026-07-07T11:10:00Z" {
		t.Fatal("last sample was not updated")
	}
}

func TestFrpTunnelStartUsesWorkerExecutorAndHidesRunSecrets(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	fake := &fakeFrpExecutor{}
	runtime.FrpExecutor = fake
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)

	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	confirmAndRunTaskForTest(t, runtime, ctx, created.TaskID)
	if fake.starts != 1 {
		t.Fatalf("start executor calls = %d", fake.starts)
	}
	tunnel, err := runtime.GetFrpTunnel(ctx, created.TunnelID)
	if err != nil {
		t.Fatalf("get tunnel: %v", err)
	}
	if tunnel["status"] != frpTunnelStatusRunning {
		t.Fatal("tunnel not running")
	}
	if _, ok := tunnel["tokenSecretRef"]; ok {
		t.Fatal("public tunnel leaked token secret ref")
	}
	latestRun := objectMapFromAny(tunnel["latestRun"])
	for _, secretField := range []string{"tokenSecretRef", "containerName", "remoteDir", "frpsImage"} {
		if _, ok := latestRun[secretField]; ok {
			t.Fatal("public run included a restricted field")
		}
	}
}

func TestFrpTunnelBackupContainsDefinitionButExcludesRunsAndTokens(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.FrpExecutor = &fakeFrpExecutor{}
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)
	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil || run.TokenSecretRef == "" {
		t.Fatal("pending run token reference missing")
	}
	service, err := runtime.backupArchiveService()
	if err != nil {
		t.Fatalf("create backup service: %v", err)
	}
	packageData, err := service.BuildPackage(ctx, domain.BackupOptions{
		RequestedScopes: []domain.BackupScope{domain.BackupScopeFrpTunnels},
	})
	if err != nil {
		t.Fatalf("build FRP backup: %v", err)
	}
	encoded, err := json.Marshal(packageData)
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	if !strings.Contains(string(encoded), created.TunnelID) {
		t.Fatalf("backup omitted FRP definition: %s", encoded)
	}
	for _, forbidden := range []string{"frpTunnelRuns", created.RunID, run.TokenSecretRef} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatal("FRP backup included tunnel runtime data")
		}
	}
}

func TestFrpTunnelCancelPendingStartAtomicallyReleasesServerSlotAndToken(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.FrpExecutor = &fakeFrpExecutor{}
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)

	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil {
		t.Fatal("created run missing")
	}
	tokenRef := run.TokenSecretRef
	if _, err := runtime.CancelTask(ctx, created.TaskID); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	state, err = store.LoadState(ctx)
	if err != nil {
		t.Fatalf("reload state: %v", err)
	}
	run = domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil || run.Status != frpTunnelStatusCancelled {
		t.Fatal("cancelled run not persisted")
	}
	task := findTask(state.Tasks, created.TaskID)
	if task == nil || task.Status != "cancelled" {
		t.Fatal("cancelled task not persisted")
	}
	tunnel, err := runtime.GetFrpTunnel(ctx, created.TunnelID)
	if err != nil {
		t.Fatalf("get tunnel: %v", err)
	}
	if tunnel["status"] != frpTunnelStatusStopped {
		t.Fatal("cancelled tunnel should derive stopped")
	}
	if _, err := store.GetSecretMaterial(ctx, tokenRef); err == nil {
		t.Fatalf("cancelled tunnel token material should be deleted")
	}
	if _, err := runtime.StartFrpTunnel(ctx, created.TunnelID); err != nil {
		t.Fatalf("server slot should be available after cancellation: %v", err)
	}
}

func TestFrpTunnelCancelMigratedPendingStartReleasesLegacyRunToken(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	now := domain.Now()
	err := store.MutateStateWithSecretMaterials(ctx, map[string]string{"secret_legacy_frp": "legacy-token"}, func(state *domain.State) error {
		state.LegacyFrpTunnelSessions = append(state.LegacyFrpTunnelSessions, domain.Object{
			"id": "tun_legacy", "name": "Legacy", "serverId": "srv_gateway", "status": "pending_confirmation",
			"frpsBindPort": 7000, "tokenSecretRef": "secret_legacy_frp", "createdAt": now, "updatedAt": now,
			"mappings": []domain.Object{{
				"name": "local-api", "localHost": "127.0.0.1", "localPort": 3001, "frpsProxyPort": 18081,
			}},
		})
		state.Tasks = append(state.Tasks, domain.Task{
			ID: "task_legacy_start", Action: string(domain.TaskActionStartFrpTunnel), Status: "pending_confirmation",
			TargetKind: "frp_tunnel_session", TargetID: "tun_legacy", TargetName: "Legacy", Payload: map[string]any{"tunnelId": "tun_legacy"},
			CreatedAt: now, UpdatedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed legacy start: %v", err)
	}
	if _, err := runtime.CancelTask(ctx, "task_legacy_start"); err != nil {
		t.Fatalf("cancel migrated start: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load migrated state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, "tun_legacy")
	if run == nil || run.Status != frpTunnelStatusCancelled {
		t.Fatal("legacy run was not cancelled")
	}
	if _, err := store.GetSecretMaterial(ctx, "secret_legacy_frp"); err == nil {
		t.Fatalf("legacy run token should be deleted")
	}
}
