package application

import (
	"context"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestCreateApplicationReleaseOperationReturnsReadinessDetailsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	clusterMetrics := func(clusterID string, role string) domain.Object {
		return domain.Object{"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1", "swarm": true, "swarmRole": role, "swarmVerificationStatus": "verified", "swarmClusterId": clusterID, "traefik": true}
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_one", "name": "示例服务", "projectId": "project_one"}}
		state.Servers = []domain.Object{
			{"id": "srv_build", "name": "构建机", "health": domain.Object{"metrics": clusterMetrics("", "")}},
			{"id": "srv_gateway", "name": "网关", "health": domain.Object{"metrics": clusterMetrics("cluster_gateway", "manager")}},
			{"id": "srv_worker", "name": "部署节点", "health": domain.Object{"metrics": clusterMetrics("cluster_other", "worker")}},
		}
		state.ApplicationDeploymentConfigs = []domain.Object{{
			"applicationId": "app_one", "buildServerId": "srv_build", "deploymentManagerServerId": "srv_gateway",
			"sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.com/app.git", "ref": "main", "refType": "branch", "jenkinsfilePath": "Jenkinsfile"},
			"swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "srv_worker", "slotKey": "web", "serviceName": "orders-web"}}},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err := runtime.CreateApplicationReleaseOperation(ctx, "app_one", "")
	previewErr, ok := err.(*ReleasePlanPreviewBlockedError)
	if !ok || previewErr.Error() != "RELEASE_PLAN_PREVIEW_BLOCKED" {
		t.Fatalf("expected structured preview error, got %T %v", err, err)
	}
	if len(previewErr.Blockers) == 0 || len(previewErr.Checks) == 0 {
		t.Fatal("structured blockers missing")
	}
	foundClusterMismatch := false
	for _, check := range previewErr.Checks {
		if check.Code != "SWARM_CLUSTER_MISMATCH" {
			continue
		}
		foundClusterMismatch = true
		if check.Target.Name != "部署节点" || check.Details["deploymentManagerServerName"] != "网关" || check.Details["deploymentClusterId"] != "cluster_gateway" || check.Details["serverClusterId"] != "cluster_other" {
			t.Fatal("cluster mismatch details missing names or IDs")
		}
	}
	if !foundClusterMismatch {
		t.Fatal("cluster mismatch blocker missing")
	}
	state, loadErr := store.LoadState(ctx)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(state.ReleasePlans) != 0 || len(state.Tasks) != 0 || len(state.OperationRuns) != 0 {
		t.Fatalf("blocked preflight mutated execution state: plans=%d tasks=%d runs=%d", len(state.ReleasePlans), len(state.Tasks), len(state.OperationRuns))
	}
}
