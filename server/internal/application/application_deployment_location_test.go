package application

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestTCPOnlyDeploymentUsesManagerAndWorkerWithoutGateway(t *testing.T) {
	state := domain.EmptyState()
	state.Applications = []domain.Object{{"id": "database", "projectId": "project"}}
	for _, role := range []string{"manager", "worker"} {
		state.Servers = append(state.Servers, readinessServer(role, domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": role, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster-a"}))
	}
	cfg := domain.Object{"applicationId": "database", "sourceConfig": domain.Object{"provider": "image"}, "deploymentManagerServerId": "manager", "swarmRuntime": domain.Object{"services": []domain.Object{{"slotKey": "db", "serverId": "worker"}}, "ports": []domain.Object{{"name": "db", "protocol": "tcp", "targetPort": 5432}}}}
	state.ApplicationDeploymentConfigs = []domain.Object{cfg}
	if failed := failedReadinessCodes(deploymentRuntimeReadiness(state, cfg)); len(failed) > 0 {
		t.Fatal("纯 TCP 应用无 HTTP 网关或构建机时被拦截", failed)
	}
	plan := domain.Object{"applicationId": "database", "deploymentManagerServerId": "manager", "deploymentClusterId": "cluster-a"}
	if err := pinReleaseLocation(&state, plan); err != nil {
		t.Fatal(err)
	}
	for _, server := range state.Servers {
		serverRuntimeMetrics(server)["swarmClusterId"] = "cluster-b"
	}
	if failed := failedReadinessCodes(deploymentRuntimeReadiness(state, cfg)); !slices.Contains(failed, "APPLICATION_SWARM_CLUSTER_MISMATCH") {
		t.Fatal("管理节点重入其他集群后仍可向错误现场发布", failed)
	}
}

func TestRuntimeRejectsUnknownOrChangedClusterBeforeReportingMissingService(t *testing.T) {
	remote := &fakeApplicationRemote{run: func(command string) (string, bool, error) {
		if strings.HasPrefix(command, "docker info") {
			return `{"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"cluster-b"}}`, false, nil
		}
		if strings.HasPrefix(command, "docker service ls") {
			return "", false, nil
		}
		t.Fatal("查询空服务时执行了额外命令")
		return "", false, nil
	}}
	for _, test := range []struct {
		input ApplicationServiceInput
		code  string
	}{
		{ApplicationServiceInput{ApplicationID: "app", ClusterID: "cluster-a"}, "APPLICATION_SWARM_CLUSTER_MISMATCH"},
		{ApplicationServiceInput{ApplicationID: "app", VerifyLegacyLocation: true}, "APPLICATION_RUNTIME_CHECK_REQUIRED"},
	} {
		_, err := inspectApplicationServices(context.Background(), remote, test.input)
		if err == nil || err.Error() != test.code {
			t.Fatal("空服务响应掩盖了现场身份异常", err)
		}
	}
}

func TestEmptyApplicationHasContainerPortAndLegacySlotsKeepTheirPublishedPorts(t *testing.T) {
	cfg := defaultDeploymentConfig("app", "project", nil)
	swarm := objectMapFromAny(cfg["swarmRuntime"])
	port := objectSlice(swarm["ports"])[0]
	if port["name"] != "web" || port["protocol"] != "http" || port["targetPort"] != 80 || port["publishedPort"] != nil || cfg["gatewayServerId"] != nil || cfg["deploymentManagerServerId"] != "" || objectSlice(swarm["services"])[0]["serverId"] != "" {
		t.Fatal("新应用端口或部署节点默认值错误")
	}
	legacy := domain.Object{"containerPort": 5432, "publishedPort": 15432}
	if configuredServicePorts(legacy, domain.Object{"publishedPort": 25432})[0]["publishedPort"] != 25432 || configuredServicePorts(legacy, nil)[0]["publishedPort"] != 15432 {
		t.Fatal("旧应用的槽位端口覆盖丢失")
	}
}

func TestLegacyApplicationCanVerifyAndRebindOriginalDeploymentManager(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app", "projectId": "project", "deploymentLocation": domain.Object{"deploymentManagerServerId": "srv_gateway", "deploymentClusterId": ""}}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app", "deploymentManagerServerId": "", "gatewayServerId": "srv_gateway"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input, err := r.applicationServiceInput(ctx, "app", nil)
	if err != nil || input.Server["id"] != "srv_gateway" || !input.VerifyLegacyLocation {
		t.Fatal("无法回读历史应用现场", err)
	}
	r.ApplicationRuntimeExecutor = &fakeApplicationRuntimeExecutor{snapshot: ApplicationRuntimeSnapshot{Availability: "available", Status: "running", CheckedAt: domain.Now(), DeploymentManagerServerID: "srv_gateway", DeploymentClusterID: "cluster-old", Services: []ApplicationServiceState{{ServiceID: "service-old"}}}}
	cfg, err := r.UpsertApplicationConfig(ctx, "app", domain.Object{"deploymentManagerServerId": "srv_gateway"})
	if err != nil || cfg["deploymentManagerServerId"] != "srv_gateway" {
		t.Fatal("原管理节点已回读受管服务后仍不能重新绑定", err)
	}
}
