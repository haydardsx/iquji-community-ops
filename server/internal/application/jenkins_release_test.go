package application

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestJenkinsReleaseMaterialsContainOnlyGitSourceAndCallbackContract(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{
		{"id": "srv_worker", "platformProfile": domain.Object{"architecture": "arm64"}},
	}
	releasePlan := domain.Object{
		"id": "rel_one", "applicationId": "app_one", "projectId": "project_one",
		"artifactManifest":  domain.Object{"deployMode": "direct_load", "imageRef": "iquops/app:one", "builtAt": domain.Now()},
		"placementSnapshot": domain.Object{"eligibleServerIds": []string{"srv_worker"}},
		"configSnapshot":    []domain.Object{},
		"buildSnapshot": domain.Object{
			"sourceSnapshot": domain.Object{
				"sourceType": "git", "repositoryUrl": "https://example.com/repository.git", "authMode": "none",
				"sourcePath": ".", "resolvedRef": "main", "refType": "branch",
			},
			"buildConfig":          domain.Object{"environment": "jenkins", "buildStrategy": "image_build", "buildType": "dockerfile"},
			"resolvedSourceLayout": domain.Object{"sourcePath": ".", "buildContextPath": ".", "composeFilePath": "./docker-compose.yml"},
			"swarmRuntime": domain.Object{"services": []domain.Object{{
				"slotKey": "web", "serverId": "srv_worker", "serviceName": "app-one", "replicas": 1,
			}}},
			"deliveryConfig": domain.Object{"executor": "jenkins", "registrySync": domain.Object{"enabled": false}},
		},
	}

	snapshot, materials, err := jenkinsReleaseMaterials(state, releasePlan, "run_one", "http://127.0.0.1:49152")
	if err != nil {
		t.Fatalf("build Jenkins materials: %v", err)
	}
	if len(snapshot) != 3 || snapshot["artifactManifest"] != nil || snapshot["deploymentTargets"] != nil || snapshot["configSnapshot"] != nil {
		t.Fatal("private build and deployment materials were retained")
	}
	if materials["controlPlaneBaseUrl"] != nil || materials["build"] != nil {
		t.Fatal("unapproved Jenkins materials were returned")
	}
	source := objectMapFromAny(materials["source"])
	sourceSnapshot := objectMapFromAny(source["snapshot"])
	if len(sourceSnapshot) != 7 || sourceSnapshot["provider"] != "git" || sourceSnapshot["repositoryUrl"] != "https://example.com/repository.git" || sourceSnapshot["resolvedRef"] != "main" {
		t.Fatal("unexpected Git source materials")
	}
	deploy := objectMapFromAny(materials["deploy"])
	if deploy["callbackUrl"] != "http://127.0.0.1:49152/platform/jenkins/release-artifacts" {
		t.Fatal("unexpected callback URL")
	}
	if len(deploy) != 2 || deploy["callbackTokenHeader"] != "x-iquops-jenkins-run-token" {
		t.Fatal("unexpected deploy callback contract")
	}
}

func TestJenkinsReleaseCallbackRevokesRunTokenAndUpdatesOperationRun(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	token := "short-lived-token"
	const sample = "example-opaque-value"
	if err := store.MutateStateWithSecretMaterials(ctx, map[string]string{"example_ref": sample}, func(state *domain.State) error {
		state.ReleasePlans = []domain.Object{{
			"id": "rel_one", "applicationId": "app_one", "status": "jenkins_triggered", "jenkinsRunId": "run_one",
			"configSnapshot":   []domain.Object{{"valueSource": "secret_ref", "secretRef": "example_ref"}},
			"artifactManifest": domain.Object{"deployMode": "direct_load"},
			"buildSnapshot": domain.Object{"swarmRuntime": domain.Object{"services": []domain.Object{{
				"slotKey": "web", "serverId": "srv_one", "serviceName": "app-one", "replicas": 1,
			}}}},
		}}
		state.OperationRuns = []domain.OperationRun{{ID: "task_one", Action: "request_jenkins_release", Status: "running", CreatedAt: domain.Now(), UpdatedAt: domain.Now()}}
		state.JenkinsReleaseRuns = []domain.Object{{
			"id": "run_one", "releasePlanId": "rel_one", "applicationId": "app_one", "operationRunId": "task_one",
			"tokenHash": domain.HashSecret(token), "status": "triggered", "expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
			"createdAt": domain.Now(), "updatedAt": domain.Now(),
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed release state: %v", err)
	}
	if _, allowed, err := runtime.AuthorizeJenkinsRun(ctx, "run_one", token); err != nil || !allowed {
		t.Fatalf("valid run token was rejected: allowed=%v err=%v", allowed, err)
	}

	releasePlan, err := runtime.RecordJenkinsReleaseCallback(ctx, domain.Object{
		"runId": "run_one", "releasePlanId": "rel_one", "applicationId": "app_one", "status": "succeeded",
		"summary":     "release completed by project Jenkinsfile",
		"jenkinsRun":  domain.Object{"runId": "jenkins-one", "runNumber": 12, "runUrl": "https://jenkins.example/job/app/12/", "status": "succeeded"},
		"runtimeLogs": []domain.Object{{"source": "jenkins", "phase": "deploy", "level": "info", "message": "deployed " + sample, "timestamp": domain.Now()}},
	})
	if err != nil {
		t.Fatalf("record Jenkins callback: %v", err)
	}
	if releasePlan["status"] != "succeeded" || releasePlan["summary"] != "release completed by project Jenkinsfile" || releasePlan["releaseArtifact"] != nil {
		t.Fatal("Jenkinsfile terminal status was not recorded")
	}
	if _, allowed, err := runtime.AuthorizeJenkinsRun(ctx, "run_one", token); err != nil || allowed {
		t.Fatalf("terminal run token was not revoked: allowed=%v err=%v", allowed, err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load release state: %v", err)
	}
	if len(state.JenkinsReleaseRuns) != 1 || state.JenkinsReleaseRuns[0]["status"] != "succeeded" || state.JenkinsReleaseRuns[0]["tokenHash"] != "" {
		t.Fatal("run was not finalized safely")
	}
	if len(state.OperationRuns) != 1 || state.OperationRuns[0].Status != "succeeded" || state.OperationRuns[0].Summary != "release completed by project Jenkinsfile" {
		t.Fatal("operation run did not reach Jenkins terminal state")
	}
	if len(state.RuntimeLogs) != 1 || state.RuntimeLogs[0].OperationRunID != "task_one" || state.RuntimeLogs[0].Message != "deployed [REDACTED]" {
		t.Fatal("Jenkins runtime logs were not recorded")
	}
}

func TestLocalControlPlaneAddressNeverUsesWildcardHost(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::", "localhost"} {
		if actual := localControlPlaneAddress(host, 3001); actual != "127.0.0.1:3001" {
			t.Fatalf("host %q produced %q", host, actual)
		}
	}
}

func TestWorkerRecoveryRevokesCallbackTokenAndRetainsBrokerRun(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	token := "old-run-token"
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = []domain.Task{{
			ID: "task_release", Action: string(domain.TaskActionRequestJenkinsRelease), Status: "executing",
			TargetKind: "release_plan", TargetID: "release_one", Payload: map[string]any{}, CreatedAt: old, UpdatedAt: old,
		}}
		state.ReleasePlans = []domain.Object{{"id": "release_one", "applicationId": "app_one", "buildServerId": "srv_build", "status": "jenkins_triggered"}}
		state.JenkinsReleaseRuns = []domain.Object{{
			"id": "run_old", "operationRunId": "task_release", "releasePlanId": "release_one", "applicationId": "app_one",
			"status": "triggered", "tokenHash": domain.HashSecret(token), "expiresAt": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed stale Jenkins task: %v", err)
	}
	claimed, err := store.ClaimNextTask(ctx, 15*time.Minute)
	if err != nil || claimed == nil || claimed.ID != "task_release" {
		t.Fatal("recover stale task")
	}
	if _, allowed, err := runtime.AuthorizeJenkinsRun(ctx, "run_old", token); err != nil || allowed {
		t.Fatalf("old callback token remains authorized: allowed=%v err=%v", allowed, err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || state.JenkinsReleaseRuns[0]["status"] != "triggered" || state.JenkinsReleaseRuns[0]["tokenHash"] != "" {
		t.Fatal("old Jenkins run was not revoked")
	}
}

func TestJenkinsCallbackTerminalStatusDoesNotRunControlPlaneDeployment(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ReleasePlans = []domain.Object{{
			"id": "release_one", "applicationId": "app_one", "status": "jenkins_triggered", "jenkinsRunId": "run_one",
		}}
		state.OperationRuns = []domain.OperationRun{{ID: "operation_one", Action: "request_jenkins_release", Status: "running", CreatedAt: domain.Now(), UpdatedAt: domain.Now()}}
		state.JenkinsReleaseRuns = []domain.Object{{
			"id": "run_one", "releasePlanId": "release_one", "applicationId": "app_one", "operationRunId": "operation_one", "status": "triggered",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed release state: %v", err)
	}
	_, err := runtime.RecordJenkinsReleaseCallback(ctx, domain.Object{
		"runId": "run_one", "releasePlanId": "release_one", "applicationId": "app_one", "status": "succeeded",
		"summary":     "Jenkinsfile deployed the application",
		"jenkinsRun":  domain.Object{"runId": "jenkins-one", "runNumber": 1, "status": "succeeded"},
		"runtimeLogs": []domain.Object{{"source": "jenkins", "phase": "deploy", "level": "info", "message": "deployed", "timestamp": domain.Now()}},
	})
	if err != nil {
		t.Fatalf("record Jenkins callback: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load callback state: %v", err)
	}
	if state.ReleasePlans[0]["status"] != "succeeded" || state.OperationRuns[0].Status != "succeeded" || state.JenkinsReleaseRuns[0]["status"] != "succeeded" {
		t.Fatal("callback terminal state was not recorded")
	}
	if len(state.ApplicationInstances) != 0 {
		t.Fatal("Jenkins 回调后意外创建了应用实例记录")
	}
}

func TestApplicationConfigRejectsBuildServerOutsideProjectScope(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one", "name": "App"}}
		state.Servers = []domain.Object{{"id": "srv_other", "projectIds": []string{"project_other"}}}
		return nil
	}); err != nil {
		t.Fatalf("seed project scope: %v", err)
	}
	_, err := runtime.UpsertApplicationConfig(ctx, "app_one", domain.Object{"buildServerId": "srv_other"})
	if err == nil || err.Error() != "BUILD_SERVER_NOT_AVAILABLE_TO_PROJECT" {
		t.Fatalf("out-of-scope build server error = %v", err)
	}
}

func TestApplicationRebuildTaskCreatesAndReusesReleasePlan(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "project_one", "serverIds": []string{"srv_one"}}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one", "name": "App", "slug": "app"}}
		state.Servers = []domain.Object{readinessServer("srv_one", domain.Object{
			"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1", "swarm": true, "swarmRole": "manager",
			"swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one", "traefik": true,
		})}
		state.ApplicationDeploymentConfigs = []domain.Object{{
			"id": "config_one", "applicationId": "app_one", "buildServerId": "srv_one", "deploymentManagerServerId": "srv_one",
			"sourceConfig":   domain.Object{"provider": "git", "repositoryUrl": "https://example.com/repository.git", "sourcePath": "."},
			"deliveryConfig": defaultDeliveryConfig("app_one"),
			"buildConfig":    defaultBuildConfig(),
			"placement":      domain.Object{"eligibleServerIds": []string{"srv_one"}},
			"swarmRuntime": domain.Object{"services": []domain.Object{{
				"slotKey": "web", "serverId": "srv_one", "serviceName": "app-one", "replicas": 1,
			}}},
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed rebuild state: %v", err)
	}
	task := domain.Task{ID: "task_rebuild", Action: string(domain.TaskActionRequestJenkinsRelease), TargetKind: "application", TargetID: "app_one", Payload: map[string]any{"ref": "main", "refType": "branch"}}
	firstPlanID, err := runtime.resolveJenkinsReleasePlanForTask(ctx, task)
	if err != nil {
		t.Fatalf("create rebuild release plan: %v", err)
	}
	secondPlanID, err := runtime.resolveJenkinsReleasePlanForTask(ctx, task)
	if err != nil {
		t.Fatalf("reuse rebuild release plan: %v", err)
	}
	if firstPlanID == "" || secondPlanID != firstPlanID {
		t.Fatalf("rebuild plan was not reused: first=%q second=%q", firstPlanID, secondPlanID)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load rebuild state: %v", err)
	}
	if len(state.ReleasePlans) != 1 || state.ReleasePlans[0]["operationRunId"] != task.ID {
		t.Fatal("unexpected rebuild release plans")
	}
}

func TestJenkinsUninstallOnlyBlocksLiveReferences(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ReleasePlans = []domain.Object{{
			"id": "release_one", "buildServerId": "srv_build", "status": "succeeded",
		}}
		state.JenkinsReleaseRuns = []domain.Object{{
			"id": "run_one", "releasePlanId": "release_one", "status": "succeeded",
		}}
		return nil
	}); err != nil {
		t.Fatalf("seed terminal Jenkins history: %v", err)
	}
	if err := runtime.validateJenkinsUninstall(ctx, "srv_build"); err != nil {
		t.Fatalf("terminal Jenkins history should not block uninstall: %v", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ReleasePlans[0]["status"] = "executing"
		state.JenkinsReleaseRuns[0]["status"] = "triggered"
		return nil
	}); err != nil {
		t.Fatalf("seed active Jenkins run: %v", err)
	}
	if err := runtime.validateJenkinsUninstall(ctx, "srv_build"); err == nil || err.Error() != "JENKINS_UNINSTALL_BLOCKED_RELEASE_PLAN" {
		t.Fatalf("active release plan should block uninstall: %v", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ReleasePlans[0]["status"] = "succeeded"
		return nil
	}); err != nil {
		t.Fatalf("terminalize release plan: %v", err)
	}
	if err := runtime.validateJenkinsUninstall(ctx, "srv_build"); err == nil || err.Error() != "JENKINS_UNINSTALL_BLOCKED_ACTIVE_RUN" {
		t.Fatalf("active Jenkins run should block uninstall: %v", err)
	}
}

func TestDockerUninstallIsBlockedByManagedRuntimes(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{readinessServer("srv_one", domain.Object{
			"docker": true, "swarm": true, "traefik": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1",
		})}
		return nil
	}); err != nil {
		t.Fatalf("seed Docker uninstall guard: %v", err)
	}
	if err := runtime.validateDockerUninstall(ctx, "srv_one"); err == nil || err.Error() != "DOCKER_UNINSTALL_BLOCKED_SWARM" {
		t.Fatalf("Swarm should block Docker uninstall first: %v", err)
	}
}

func newJenkinsReleaseTestRuntime(t *testing.T, ctx context.Context) (*Runtime, *sqlite.Store) {
	t.Helper()
	tempDir := t.TempDir()
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = tempDir
	config.DatabasePath = filepath.Join(tempDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewRuntime(config, store), store
}
