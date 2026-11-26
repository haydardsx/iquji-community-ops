package application

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestFrpTunnelStartFailureRollsBackRemoteFrpsAndDeletesToken(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	fake := &fakeFrpExecutor{startErr: errors.New("frpc failed")}
	runtime.FrpExecutor = fake
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)

	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil {
		t.Fatalf("created run missing")
	}
	tokenRef := run.TokenSecretRef
	confirmAndRunTaskForTest(t, runtime, ctx, created.TaskID)
	if fake.cleanups != 1 {
		t.Fatalf("cleanup executor calls = %d", fake.cleanups)
	}
	state, err = store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run = domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil || run.Status != frpTunnelStatusFailed {
		t.Fatal("run should be failed after rollback")
	}
	if _, err := store.GetSecretMaterial(ctx, tokenRef); err == nil {
		t.Fatalf("token secret material should be deleted after rollback")
	}
}

func TestFrpTunnelFailedRunCannotBeStopped(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	fake := &fakeFrpExecutor{startErr: errors.New("frpc failed")}
	runtime.FrpExecutor = fake
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)

	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	confirmAndRunTaskForTest(t, runtime, ctx, created.TaskID)
	if _, err := runtime.StopFrpTunnel(ctx, created.TunnelID); err == nil {
		t.Fatalf("failed tunnel should not be stoppable")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil || run.Status != frpTunnelStatusFailed {
		t.Fatal("failed run should not transition to stopping")
	}
}

func TestFrpTunnelCanStopEditAndRestartWithNewRunAndToken(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	fake := &fakeFrpExecutor{}
	runtime.FrpExecutor = fake
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)

	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	firstRun := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if firstRun == nil {
		t.Fatalf("first run missing")
	}
	firstTokenRef := firstRun.TokenSecretRef
	confirmAndRunTaskForTest(t, runtime, ctx, created.TaskID)
	stopTask, err := runtime.StopFrpTunnel(ctx, created.TunnelID)
	if err != nil {
		t.Fatalf("stop tunnel: %v", err)
	}
	if stopTask.TargetID != created.RunID || stopTask.TargetKind != "frp_tunnel_run" {
		t.Fatal("stop task must target the active run")
	}
	if stopTask.Status != "pending_confirmation" {
		t.Fatal("stop task must require confirmation")
	}
	if _, err := runtime.ConfirmTask(ctx, stopTask.ID); err != nil {
		t.Fatalf("confirm stop task: %v", err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("run stop worker: %v", err)
	}
	if fake.stops != 1 {
		t.Fatalf("stop executor calls = %d", fake.stops)
	}
	if _, err := runtime.StopFrpTunnel(ctx, created.TunnelID); err == nil {
		t.Fatalf("stopped tunnel should reject a duplicate stop")
	}
	request := tunnelRequest(port)
	request.Name = "Renamed API"
	request.Mappings[0].FrpsProxyPort = 18082
	if _, err := runtime.UpdateFrpTunnel(ctx, created.TunnelID, request); err != nil {
		t.Fatalf("edit stopped tunnel: %v", err)
	}
	state, err = store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load edited state: %v", err)
	}
	firstRun = domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if firstRun == nil || firstRun.Mappings[0].FrpsProxyPort != 18081 {
		t.Fatal("editing tunnel changed historical run snapshot")
	}
	restarted, err := runtime.StartFrpTunnel(ctx, created.TunnelID)
	if err != nil {
		t.Fatalf("restart tunnel: %v", err)
	}
	secondRunResponse := objectMapFromAny(restarted["run"])
	secondTaskResponse := objectMapFromAny(restarted["task"])
	secondRunID := stringValue(secondRunResponse["id"])
	if secondRunID == "" || secondRunID == created.RunID {
		t.Fatal("restart must create a new run")
	}
	state, err = store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load restarted state: %v", err)
	}
	secondRun := domain.FindFrpTunnelRun(state.FrpTunnelRuns, secondRunID)
	if secondRun == nil || secondRun.TokenSecretRef == "" || secondRun.TokenSecretRef == firstTokenRef {
		t.Fatal("restart must create a new token reference")
	}
	if len(state.FrpTunnelRuns) != 2 {
		t.Fatal("expected two run history entries")
	}
	confirmAndRunTaskForTest(t, runtime, ctx, stringValue(secondTaskResponse["id"]))
	if fake.starts != 2 {
		t.Fatalf("restart executor calls = %d", fake.starts)
	}
}

func TestFrpTunnelConcurrentRestartAllowsOnlyOneActiveRun(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.FrpExecutor = &fakeFrpExecutor{}
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)
	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	if _, err := runtime.CancelTask(ctx, created.TaskID); err != nil {
		t.Fatalf("cancel initial start: %v", err)
	}

	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			_, err := runtime.StartFrpTunnel(ctx, created.TunnelID)
			results <- err
		}()
	}
	succeeded := 0
	conflicted := 0
	for index := 0; index < 2; index++ {
		err := <-results
		if err == nil {
			succeeded++
		} else if strings.Contains(err.Error(), "SERVER_ALREADY_HAS_ACTIVE_FRP_TUNNEL") {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrent start error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("expected one success and one conflict, got success=%d conflict=%d", succeeded, conflicted)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	active := 0
	for _, run := range state.FrpTunnelRuns {
		if run.ServerID == "srv_gateway" && run.Status.IsActive() {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("expected one active run after race, got %d", active)
	}
}

func TestFrpTunnelCleanupRequiredBlocksStartEditAndDelete(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.FrpExecutor = &fakeFrpExecutor{startErr: errors.New("frpc failed"), cleanupErr: errors.New("cleanup failed")}
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)
	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	confirmAndRunTaskForTest(t, runtime, ctx, created.TaskID)

	for action, call := range map[string]func() error{
		"start": func() error { _, err := runtime.StartFrpTunnel(ctx, created.TunnelID); return err },
		"edit": func() error {
			_, err := runtime.UpdateFrpTunnel(ctx, created.TunnelID, tunnelRequest(port))
			return err
		},
		"delete": func() error { _, err := runtime.DeleteFrpTunnel(ctx, created.TunnelID); return err },
	} {
		if err := call(); err == nil {
			t.Fatalf("cleanup-required tunnel should reject %s", action)
		}
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID)
	if run == nil || run.Status != frpTunnelStatusCleanupRequired {
		t.Fatal("run should require cleanup")
	}
}

func TestFrpTunnelDeleteRemovesTunnelRunsAndProbeDataButRetainsTaskAudit(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	runtime.FrpExecutor = &fakeFrpExecutor{}
	seedFrpServer(t, ctx, store, true)
	port := openLocalTCPPort(t)
	created := createFrpTunnelForTest(t, runtime, ctx, tunnelRequest(port))
	if _, err := runtime.CancelTask(ctx, created.TaskID); err != nil {
		t.Fatalf("cancel initial start: %v", err)
	}
	if _, err := store.RecordFrpProbeHit(ctx, domain.FrpProbeHit{
		ID: "probehit_delete", TunnelID: created.TunnelID, RunID: created.RunID, MappingName: "local-api",
		Method: "GET", Path: "/delete", RemoteAddr: "127.0.0.1:1234", ReceivedAt: domain.Now(),
	}, sqlite.DefaultFrpProbeMaxHits); err != nil {
		t.Fatalf("record probe hit: %v", err)
	}
	deleteTask, err := runtime.DeleteFrpTunnel(ctx, created.TunnelID)
	if err != nil {
		t.Fatalf("create delete task: %v", err)
	}
	if deleteTask.Status != "pending_confirmation" || deleteTask.TargetKind != "frp_tunnel" {
		t.Fatal("delete task must require confirmation")
	}
	confirmAndRunTaskForTest(t, runtime, ctx, deleteTask.ID)
	if _, err := runtime.GetFrpTunnel(ctx, created.TunnelID); err == nil {
		t.Fatalf("deleted tunnel should not be returned")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if domain.FindFrpTunnel(state.FrpTunnels, created.TunnelID) != nil || domain.FindFrpTunnelRun(state.FrpTunnelRuns, created.RunID) != nil {
		t.Fatal("tunnel resources were not deleted")
	}
	if findTask(state.Tasks, created.TaskID) == nil || findTask(state.Tasks, deleteTask.ID) == nil {
		t.Fatal("historical tasks must be retained")
	}
	if len(state.OperationLogs) == 0 {
		t.Fatalf("audit history must be retained")
	}
	hits, err := store.ListFrpProbeHits(ctx, created.TunnelID, 1, 20)
	if err != nil || len(hits.Items) != 0 {
		t.Fatal("probe data should be deleted")
	}
}

func TestFrpTunnelStartupRecoveryMarksManagedRunForExplicitCleanup(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	fake := &fakeFrpExecutor{}
	runtime.FrpExecutor = fake
	seedFrpServer(t, ctx, store, true)
	if err := store.UpsertSecretMaterial(ctx, "secret_frp_token", "token"); err != nil {
		t.Fatalf("seed frp token: %v", err)
	}
	err := store.MutateState(ctx, func(state *domain.State) error {
		mapping := domain.FrpTunnelMapping{
			Name: "local-api", TargetType: domain.FrpTunnelMappingTargetLocalService, LocalHost: "127.0.0.1",
			LocalPort: 3001, FrpsProxyPort: 18081, PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
		}
		now := domain.Now()
		state.FrpTunnels = append(state.FrpTunnels, domain.FrpTunnel{
			ID: "tun_recover", Name: "Recovered", ServerID: "srv_gateway", FrpsBindPort: 7000,
			Mappings: []domain.FrpTunnelMapping{mapping}, CreatedAt: now, UpdatedAt: now,
		})
		state.FrpTunnelRuns = append(state.FrpTunnelRuns, domain.FrpTunnelRun{
			ID: "tunrun_recover", TunnelID: "tun_recover", ServerID: "srv_gateway", Status: frpTunnelStatusRunning,
			FrpsBindPort: 7000, FrpsImage: "ghcr.io/fatedier/frps:v0.69.1", Mappings: []domain.FrpTunnelMapping{mapping},
			ManagedFrps: true, TokenSecretRef: "secret_frp_token", ContainerName: frpTunnelContainerName("tunrun_recover"),
			RemoteDir: frpTunnelRemoteDir("tunrun_recover"), CreatedAt: now, UpdatedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed tunnel: %v", err)
	}

	if err := runtime.RecoverFrpTunnels(ctx); err != nil {
		t.Fatalf("recover tunnels: %v", err)
	}
	if fake.cleanups != 0 {
		t.Fatalf("cleanup executor calls = %d", fake.cleanups)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, "tunrun_recover")
	if run == nil || run.Status != frpTunnelStatusCleanupRequired || run.ErrorSummary != "FRPC_WORKER_RESTARTED" {
		t.Fatal("recovered run is missing its cleanup status or restart reason")
	}
	if domain.FindFrpTunnel(state.FrpTunnels, "tun_recover") == nil {
		t.Fatalf("recovery must retain the tunnel definition")
	}
	if value, err := store.GetSecretMaterial(ctx, "secret_frp_token"); err != nil || value != "token" {
		t.Fatal("explicit cleanup lost its run credential")
	}
}

func TestFrpTunnelPreviewBlocksMissingTraefikGateway(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeResult: domain.Object{
			"checks": []domain.Object{
				serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
				serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker available"),
				serverRuntimeCheck("traefik", serverRuntimeCheckDanger, "TRAEFIK_GATEWAY_REQUIRED"),
			},
			"metrics": domain.Object{
				"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frps": true, "frpsStatus": "installed",
				"traefik": false, "traefikStatus": "missing", "agent": false,
			},
			"remote": domain.Object{},
		},
	}
	port := openLocalTCPPort(t)

	direct := tunnelRequest(port)
	preview, err := runtime.PreviewFrpTunnel(ctx, direct)
	if err != nil || !processDecisionAllowed(preview["process"]) {
		t.Fatal("direct entry should not require Traefik")
	}
	direct.Mappings[0].PublicDomain = "example.test"
	preview, err = runtime.PreviewFrpTunnel(ctx, direct)
	if err != nil || !slices.Contains(processBlockers(preview["process"]), "FRP_TUNNEL_PUBLIC_DOMAIN_NOT_IN_ASSETS") {
		t.Fatal("unregistered domain must be blocked")
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Domains = append(state.Domains, domain.Object{"id": "dom_example", "name": "example.test"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview, err = runtime.PreviewFrpTunnel(ctx, direct)
	if err != nil || !slices.Contains(processBlockers(preview["process"]), "TRAEFIK_GATEWAY_REQUIRED") {
		t.Fatal("domain entry should require Traefik")
	}
}

func TestFrpTunnelPreviewBlocksMissingFrps(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{probeResult: domain.Object{
		"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "frpsStatus": "missing"},
	}}
	preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(openLocalTCPPort(t)))
	if err != nil || !slices.Contains(processBlockers(preview["process"]), "FRPS_NOT_INSTALLED") {
		t.Fatal("direct entry should require FRPS")
	}
}

func TestFrpTunnelPreviewRefreshesServerRuntimeSnapshot(t *testing.T) {
	for _, tt := range []struct {
		name       string
		observed   string
		wantStatus string
		oldProbe   bool
	}{
		{name: "FRPS 无法核验", observed: "unavailable", wantStatus: "unavailable"},
		{name: "FRPS 状态未知", observed: "unknown", wantStatus: "unknown"},
		{name: "较早探测不覆盖新快照", observed: "unavailable", wantStatus: "installed", oldProbe: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			seedFrpServer(t, ctx, store, true)
			const serverID = "srv_gateway"
			if _, err := runtime.ServerRuntimeProbe(ctx, serverID); err != nil {
				t.Fatal(err)
			}

			executor := runtime.ServerRuntimeExecutor.(*fakeServerRuntimeExecutor)
			executor.probeResult = domain.Object{
				"metrics": domain.Object{
					"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager",
					"frps": false, "frpsStatus": tt.observed,
				},
			}
			if tt.oldProbe {
				executor.probeResult["checkedAt"] = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
			}

			preview, err := runtime.PreviewFrpTunnel(ctx, tunnelRequest(openLocalTCPPort(t)))
			if err != nil {
				t.Fatal(err)
			}
			blockers := processBlockers(preview["process"])
			if !slices.Contains(blockers, "FRPS_INSTALLATION_UNAVAILABLE") || slices.Contains(blockers, "FRPS_NOT_INSTALLED") {
				t.Fatalf("FRPS 无法核验应报告不可用：%v", blockers)
			}

			servers, err := runtime.ListObjects(ctx, "servers")
			if err != nil {
				t.Fatal(err)
			}
			selected := findObject(objectSlice(servers["items"]), serverID)
			if got := serverRuntimeMetrics(selected)["frpsStatus"]; got != tt.wantStatus {
				t.Fatalf("服务器列表的 FRPS 状态 = %v，预期 %s", got, tt.wantStatus)
			}
			health, err := runtime.ServerHealth(ctx, serverID)
			if err != nil {
				t.Fatal(err)
			}
			metrics := objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"])
			if metrics["frpsStatus"] != tt.wantStatus || metrics["frps"] != (tt.wantStatus == "installed") {
				t.Fatalf("服务器健康快照中的 FRPS 状态错误：%v", metrics)
			}
		})
	}
}
