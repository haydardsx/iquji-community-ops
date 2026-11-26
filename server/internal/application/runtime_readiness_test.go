package application

import (
	"slices"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestDeploymentRuntimeReadinessRequiresManagedBuildAndSharedSwarmCluster(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{
		readinessServer("srv_build", domain.Object{"ssh": true, "docker": true, "jenkins": true}),
		readinessServer("srv_gateway", domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager",
			"swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one", "traefik": true,
		}),
		readinessServer("srv_worker", domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "worker",
			"swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one",
		}),
	}
	config := domain.Object{
		"applicationId":             "app_one",
		"buildServerId":             "srv_build",
		"deploymentManagerServerId": "srv_gateway",
		"swarmRuntime": domain.Object{"services": []domain.Object{{
			"slotKey": "web", "serverId": "srv_worker", "serviceName": "app-one",
		}}},
	}

	checks := deploymentRuntimeReadiness(state, config)
	for _, check := range checks {
		if !check.Passed {
			t.Fatalf("unexpected failed readiness check: %s", check.Code)
		}
	}

	worker := findObject(state.Servers, "srv_worker")
	serverRuntimeMetrics(worker)["swarmClusterId"] = "cluster_other"
	checks = deploymentRuntimeReadiness(state, config)
	failed := failedReadinessCodes(checks)
	if !slices.Contains(failed, "SWARM_CLUSTER_MISMATCH") {
		t.Fatalf("cluster mismatch was not blocked: %#v", failed)
	}
}

func TestDeploymentRuntimeReadinessReportsStableMissingCapabilityCodes(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{
		readinessServer("srv_build", domain.Object{"ssh": true, "docker": true, "jenkins": false}),
		readinessServer("srv_gateway", domain.Object{"ssh": true, "docker": true, "swarm": false, "swarmStatus": "inactive", "traefik": false}),
	}
	config := domain.Object{
		"applicationId":             "app_one",
		"buildServerId":             "srv_build",
		"deploymentManagerServerId": "srv_gateway",
		"swarmRuntime": domain.Object{"services": []domain.Object{{
			"slotKey": "web", "serverId": "srv_gateway", "serviceName": "app-one",
		}}},
	}

	failed := failedReadinessCodes(deploymentRuntimeReadiness(state, config))
	for _, code := range []string{
		"BUILD_SERVER_JENKINS_UNAVAILABLE",
		"DEPLOYMENT_MANAGER_SWARM_MANAGER_REQUIRED",
		"DEPLOYMENT_MANAGER_ROLE_REQUIRED",
		"SWARM_NOT_INITIALIZED",
	} {
		if !slices.Contains(failed, code) {
			t.Fatalf("missing blocker %s in %#v", code, failed)
		}
	}
}

func TestGitApplicationVolumesRequireVolumeAwareJenkinsBundle(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{
		readinessServer("srv_build", domain.Object{"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v10-jenkins-2.568.1"}),
	}
	config := domain.Object{
		"applicationId": "app_one",
		"sourceConfig":  domain.Object{"provider": "git"},
		"buildServerId": "srv_build",
		"swarmRuntime":  domain.Object{"volumes": []domain.Object{{"name": "data", "target": "/data"}}},
	}
	failed := failedReadinessCodes(deploymentRuntimeReadiness(state, config))
	if !slices.Contains(failed, "JENKINS_APPLICATION_BUNDLE_UPDATE_REQUIRED") {
		t.Fatalf("old bundle must block data-volume publishing: %#v", failed)
	}
	serverRuntimeMetrics(state.Servers[0])["jenkinsBundleVersion"] = "v12-jenkins-2.568.1"
	failed = failedReadinessCodes(deploymentRuntimeReadiness(state, config))
	if slices.Contains(failed, "JENKINS_APPLICATION_BUNDLE_UPDATE_REQUIRED") {
		t.Fatalf("volume-aware bundle must pass the version check: %#v", failed)
	}
}

func readinessServer(id string, metrics domain.Object) domain.Object {
	return domain.Object{
		"id":   id,
		"name": id,
		"health": domain.Object{
			"metrics": metrics,
		},
	}
}

func failedReadinessCodes(checks []runtimeReadinessCheck) []string {
	codes := []string{}
	for _, check := range checks {
		if !check.Passed {
			codes = append(codes, check.Code)
		}
	}
	return codes
}

func TestRuntimeReadinessAllPassed(t *testing.T) {
	if !runtimeReadinessAllPassed(nil) {
		t.Fatal("empty readiness should pass")
	}
	if !runtimeReadinessAllPassed([]runtimeReadinessCheck{{Code: "A", Passed: true}, {Code: "B", Passed: true}}) {
		t.Fatal("all passed checks should pass")
	}
	if runtimeReadinessAllPassed([]runtimeReadinessCheck{{Code: "A", Passed: true}, {Code: "B", Passed: false}}) {
		t.Fatal("any failed check should fail readiness")
	}
}
