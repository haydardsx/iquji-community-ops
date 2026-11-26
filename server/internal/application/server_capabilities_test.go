package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestServerHealthKeepsActualProbeChecksAndTimestamp(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	checkedAt := "2026-10-30T01:02:03Z"
	if err := runtime.applyServerRuntimeProbe(ctx, "srv_gateway", domain.Object{
		"checkedAt": checkedAt, "metrics": domain.Object{"ssh": true, "docker": false},
		"checks": []domain.Object{{"name": "docker", "status": "danger", "summary": "REMOTE_DOCKER_UNAVAILABLE"}},
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		response, err := runtime.ServerHealth(ctx, "srv_gateway")
		if err != nil {
			t.Fatal(err)
		}
		health := objectMapFromAny(response["serverHealth"])
		report := response["process"].(domain.CheckReport)
		if health["checkedAt"] != checkedAt || len(report.Checks) != 1 || report.Checks[0].ObservedAt != checkedAt || report.Checks[0].Summary != "REMOTE_DOCKER_UNAVAILABLE" || report.Decision.Allowed {
			t.Fatal("stored health check has an unexpected timestamp or status")
		}
	}
}

func TestServerResourceSummaryUsesActualReferences(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_empty"}, {"id": "srv_used"}}
		state.Projects = []domain.Object{{"id": "prj_one", "name": "One"}, {"id": "prj_other", "name": "Other"}}
		state.Applications = []domain.Object{{"id": "app_used", "projectId": "prj_one"}, {"id": "app_unused", "projectId": "prj_one"}, {"id": "app_other", "projectId": "prj_other"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_used", "placement": domain.Object{"eligibleServerIds": []string{"srv_used"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	empty, err := runtime.ServerResources(ctx, "srv_empty")
	if err != nil || len(objectSlice(empty["projects"])) != 0 || len(objectSlice(empty["applications"])) != 0 {
		t.Fatal("server without references returned an unexpected resource summary")
	}
	used, err := runtime.ServerResources(ctx, "srv_used")
	if err != nil || len(objectSlice(used["projects"])) != 1 || len(objectSlice(used["applications"])) != 1 || objectSlice(used["applications"])[0]["id"] != "app_used" {
		t.Fatal("server resource summary is missing a referenced application or project")
	}
}

func TestServerPlatformPatchPreservesDetectedFacts(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_one", "platformProfile": domain.Object{"osId": "debian", "osVersion": "13", "detectedAt": "earlier", "packageManager": "apt"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := runtime.UpdateObject(ctx, "servers", "srv_one", domain.Object{"platformProfile": domain.Object{"dockerRepoMirror": "official"}})
	if err != nil {
		t.Fatal(err)
	}
	profile := objectMapFromAny(updated["platformProfile"])
	if profile["osId"] != "debian" || profile["detectedAt"] != "earlier" || profile["dockerRepoMirror"] != "official" {
		t.Fatal("platform patch replaced detected facts")
	}
}
