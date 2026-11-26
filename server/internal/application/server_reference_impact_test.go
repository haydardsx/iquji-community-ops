package application

import (
	"context"
	"errors"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestServerDeleteRechecksReferencesAndManagedRuntimes(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{readinessServer("srv_one", domain.Object{"docker": true, "jenkins": true})}
		state.ApplicationDeploymentConfigs = []domain.Object{{
			"id": "config_one", "applicationId": "app_one", "buildServerId": "srv_one",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed server impact: %v", err)
	}

	preview, err := runtime.DeletePreview(ctx, "server", "srv_one")
	if err != nil {
		t.Fatalf("server delete preview: %v", err)
	}
	process := preview["process"].(domain.CheckReport)
	if process.Decision.Allowed || len(process.Decision.Blockers) == 0 {
		t.Fatal("referenced server delete preview should be blocked")
	}
	impact := objectMapFromAny(preview["deleteImpact"])
	serverImpact := objectMapFromAny(impact["serverImpact"])
	if serverImpact["blocked"] != true || len(objectSlice(serverImpact["reasons"])) < 2 {
		t.Fatal("server impact is missing expected blockers")
	}

	err = runtime.DeleteObject(ctx, "servers", "srv_one")
	var blocked *ServerDeleteBlockedError
	if !errors.As(err, &blocked) || blocked.Error() != "SERVER_DELETE_BLOCKED" {
		t.Fatalf("delete should be transactionally blocked: %v", err)
	}
	state, loadErr := store.LoadState(ctx)
	if loadErr != nil || findObject(state.Servers, "srv_one") == nil {
		t.Fatal("referenced server missing after blocked deletion")
	}
}

func TestUnreferencedServerDeleteSucceedsWithoutRemoteUninstall(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_unused", "name": "Unused"}}
		return nil
	}); err != nil {
		t.Fatalf("seed unused server: %v", err)
	}
	if err := runtime.DeleteObject(ctx, "servers", "srv_unused"); err != nil {
		t.Fatalf("delete unused server: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || findObject(state.Servers, "srv_unused") != nil {
		t.Fatal("unused server was not deleted")
	}
}

func TestServerDeletePrunesOrphanApplicationConfiguration(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_orphan", "name": "Orphan"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{
			"id": "config_orphan", "applicationId": "app_missing",
			"swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "srv_orphan"}}},
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed orphan configuration: %v", err)
	}
	if err := runtime.DeleteObject(ctx, "servers", "srv_orphan"); err != nil {
		t.Fatalf("orphan configuration should not block server deletion: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.ApplicationDeploymentConfigs) != 0 {
		t.Fatal("orphan configuration was retained")
	}
}

func TestCleanupOrphanMonitoringAgentsKeepsLiveRegistrations(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_live"}}
		state.MonitoringAgents = []domain.Object{
			{"id": "agent_live", "serverId": "srv_live"},
			{"id": "agent_orphan", "serverId": "srv_missing"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.CleanupOrphanMonitoringAgents(ctx)
	if err != nil || result["removed"] != 1 {
		t.Fatal("cleanup did not remove exactly one orphan registration")
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.MonitoringAgents) != 1 || stringValue(state.MonitoringAgents[0]["id"]) != "agent_live" {
		t.Fatal("cleanup did not retain the expected active agent")
	}
}
