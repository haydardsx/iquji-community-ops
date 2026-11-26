package application

import (
	"context"
	"slices"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type fakeRuntimeResourceExecutor struct {
	installTraefikCalls   int
	uninstallTraefikCalls int

	installTraefikInput   TraefikRuntimeExecutionInput
	uninstallTraefikInput TraefikRuntimeExecutionInput

	installTraefikResult   domain.Object
	uninstallTraefikResult domain.Object

	err error
}

func (f *fakeRuntimeResourceExecutor) InstallTraefikGateway(_ context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error) {
	f.installTraefikCalls++
	f.installTraefikInput = input
	if f.err != nil {
		return nil, f.err
	}
	if f.installTraefikResult != nil {
		return f.installTraefikResult, nil
	}
	return domain.Object{"metrics": domain.Object{"ssh": true, "traefik": true, "traefikImage": input.Image}}, nil
}

func (f *fakeRuntimeResourceExecutor) UninstallTraefikGateway(_ context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error) {
	f.uninstallTraefikCalls++
	f.uninstallTraefikInput = input
	if f.err != nil {
		return nil, f.err
	}
	if f.uninstallTraefikResult != nil {
		return f.uninstallTraefikResult, nil
	}
	return domain.Object{"metrics": domain.Object{"ssh": true, "traefik": false}}, nil
}

func TestTraefikRuntimeWorkerUsesExecutorAndClearsStaleMetrics(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	fake := &fakeRuntimeResourceExecutor{}
	runtime.RuntimeResourceExecutor = fake
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{probeResult: domain.Object{
		"metrics": domain.Object{
			"ssh":           true,
			"docker":        true,
			"swarm":         true,
			"swarmStatus":   "active",
			"swarmRole":     "manager",
			"traefik":       false,
			"traefikStatus": "missing",
			"jenkins":       false,
			"jenkinsStatus": "missing",
		},
		"checks": []domain.Object{},
		"remote": domain.Object{},
	}}
	runtime.Config.TraefikImagePullCandidates = []string{"public.ecr.aws/docker/library/traefik:v3.7"}
	err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{
			"status":  "idle",
			"summary": "Server runtime verified",
			"metrics": domain.Object{
				"ssh":                        true,
				"traefik":                    true,
				"traefikService":             "iquops-traefik",
				"traefikImage":               "traefik:v3.3",
				"traefikImagePullCandidates": []string{"old-registry/traefik:v3.3"},
				"traefikHttpPort":            8080,
				"traefikHttpsPort":           8443,
				"traefikDataRoot":            "/opt/iquops/traefik",
				"traefikSwarmNetwork":        "iquops-public",
			},
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed stale traefik health: %v", err)
	}
	task, err := runtime.CreateTask(ctx, "uninstall_traefik_gateway", "server", "srv_gateway", "Gateway", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm task: %v", err)
	}

	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatalf("worker: %v", err)
	}
	if fake.uninstallTraefikCalls != 1 {
		t.Fatalf("Traefik uninstall executor calls = %d", fake.uninstallTraefikCalls)
	}
	if fake.uninstallTraefikInput.Image != "traefik:v3.7" {
		t.Fatal("Traefik executor did not receive the configured image")
	}
	if !slices.Equal(fake.uninstallTraefikInput.ImagePullCandidates, []string{"public.ecr.aws/docker/library/traefik:v3.7"}) {
		t.Fatal("Traefik executor did not receive the configured image pull candidates")
	}
	updated, err := runtime.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated["status"] != "succeeded" {
		t.Fatal("Traefik uninstall task did not succeed")
	}
	health, err := runtime.ServerHealth(ctx, "srv_gateway")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	metrics := objectMapFromAny(objectMapFromAny(health["serverHealth"])["metrics"])
	if metrics["traefik"] != false {
		t.Fatal("Traefik is still reported as active after uninstall")
	}
	for _, key := range []string{"traefikService", "traefikImage", "traefikImagePullCandidates", "traefikHttpPort", "traefikHttpsPort", "traefikDataRoot", "traefikSwarmNetwork"} {
		if _, ok := metrics[key]; ok {
			t.Fatalf("stale Traefik metric %s should be cleared", key)
		}
	}
}
