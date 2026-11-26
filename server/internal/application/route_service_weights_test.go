package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRouteBackendInspectionSkipsZeroWeightServices(t *testing.T) {
	gateway := `[{"Spec":{"Labels":{"iquops.owner":"iquops","iquops.component":"traefik_gateway"},"TaskTemplate":{"Networks":[{"Target":"net"}]}}}]`
	backend := `[{"Spec":{"Labels":{"iquops.owner":"iquops","iquops.application-id":"app","iquops.service-slot":"blue","iquops.port.web":"http/8080"},"TaskTemplate":{"Networks":[{"Target":"net"}]}}}]`
	client, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case strings.Contains(command, "docker info"):
			return `{"LocalNodeState":"active","ControlAvailable":true}`, 0
		case strings.Contains(command, "docker network inspect"):
			if strings.Contains(command, "{{.Id}}") {
				return "net|overlay|swarm", 0
			}
			return "overlay|swarm", 0
		case strings.Contains(command, "'gateway'"):
			return gateway, 0
		case strings.Contains(command, "'app-blue'"):
			return backend, 0
		}
		return "", 1
	})
	input := RouteRuntimeInput{Network: "net", TraefikName: "gateway", ApplicationID: "app", Services: []domain.Object{
		{"serviceName": "app-green", "slotKey": "green", "portName": "web", "weight": 0},
		{"serviceName": "app-blue", "slotKey": "blue", "portName": "web"},
	}}
	backends, err := inspectRouteBackends(context.Background(), client, input)
	if err != nil || len(backends) != 1 || backends[0]["name"] != "app-blue" || backends[0]["weight"] != 1 {
		t.Fatal("零权重应跳过探测，缺省权重应为 1")
	}
}

func routeWeightTestState() domain.State {
	state := domain.EmptyState()
	state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "swarmRuntime": domain.Object{"services": []domain.Object{
		{"slotKey": "one", "serviceName": "app-one"}, {"slotKey": "two", "serviceName": "app-two"},
	}}}}
	state.Domains = []domain.Object{{"id": "domain_one", "name": "app.example.test"}}
	return state
}

func routeWeightTestInput(first, second any) domain.Object {
	return domain.Object{"domainIds": []string{"domain_one"}, "services": []domain.Object{
		{"slotKey": "one", "serviceName": "app-one", "weight": first}, {"slotKey": "two", "serviceName": "app-two", "weight": second},
	}, "rules": []domain.Object{{"path": "/", "pathHandling": "preserve"}}, "disabled": false}
}

func TestEachRouteStoresItsOwnServiceWeights(t *testing.T) {
	state := routeWeightTestState()
	for _, weights := range [][2]int{{1, 3}, {7, 1}, {0, 1}} {
		config, err := validatedRouteConfig(state, "app_one", routeWeightTestInput(weights[0], weights[1]))
		if err != nil {
			t.Fatal(err)
		}
		services := objectSlice(config["services"])
		if services[0]["weight"] != weights[0] || services[1]["weight"] != weights[1] {
			t.Fatal("入口权重丢失")
		}
	}
	for _, service := range objectSlice(objectMapFromAny(state.ApplicationDeploymentConfigs[0]["swarmRuntime"])["services"]) {
		if service["weight"] != nil || service["accessWeight"] != nil {
			t.Fatal("入口保存污染了运行服务配置")
		}
	}
}

func TestRouteRuntimeUsesBindingWeightsAndDistinctRevisions(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one"}}
		configured := routeWeightTestState()
		state.Applications = []domain.Object{{"id": "app_one"}}
		state.ApplicationDeploymentConfigs = configured.ApplicationDeploymentConfigs
		state.ApplicationDeploymentConfigs[0]["gatewayServerId"] = "srv_gateway"
		state.ApplicationDeploymentConfigs[0]["deploymentManagerServerId"] = "srv_gateway"
		state.Domains = configured.Domains
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	revisions := map[string]bool{}
	for _, weights := range [][2]int{{1, 3}, {3, 1}} {
		route, err := validatedRouteConfig(state, "app_one", routeWeightTestInput(weights[0], weights[1]))
		if err != nil {
			t.Fatal(err)
		}
		route["id"], route["applicationId"] = "route_weighted", "app_one"
		input, err := runtime.routeRuntimeInput(ctx, state, route, false, domain.Task{})
		if err != nil {
			t.Fatal(err)
		}
		if input.Services[0]["weight"] != weights[0] || input.Services[1]["weight"] != weights[1] {
			t.Fatal("运行输入没有读取入口绑定比例")
		}
		revisions[input.Revision] = true
	}
	if len(revisions) != 2 {
		t.Fatal("修改入口权重没有生成新发布修订")
	}
}

func TestRouteWeightsRejectInvalidAndAllZeroTargets(t *testing.T) {
	for _, value := range []any{-1, 1.5, "3", true, nil, 2147483648} {
		if _, err := validatedRouteConfig(routeWeightTestState(), "app_one", routeWeightTestInput(value, 1)); err == nil || err.Error() != "ROUTE_SERVICE_WEIGHT_INVALID" {
			t.Fatalf("非法权重未拒绝 %v: %v", value, err)
		}
	}
	if _, err := validatedRouteConfig(routeWeightTestState(), "app_one", routeWeightTestInput(0, 0)); err == nil || err.Error() != "ROUTE_SERVICE_WEIGHT_REQUIRED" {
		t.Fatalf("全部零权重未拒绝：%v", err)
	}
}

func TestOptionalGatewaySharesDeploymentClusterAndRevokesAtOriginalLocation(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	gateway := state.Servers[0]
	gateway["health"] = domain.Object{"metrics": domain.Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster-a"}}
	state.Servers = append(state.Servers, readinessServer("manager", domain.Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster-a"}))
	state.Applications = []domain.Object{{"id": "app_one"}}
	fixture := routeWeightTestState()
	state.ApplicationDeploymentConfigs, state.Domains = fixture.ApplicationDeploymentConfigs, fixture.Domains
	cfg := state.ApplicationDeploymentConfigs[0]
	cfg["deploymentManagerServerId"] = "manager"
	route, err := validatedRouteConfig(state, "app_one", routeWeightTestInput(1, 3))
	if err != nil {
		t.Fatal(err)
	}
	route["id"], route["applicationId"] = "route-one", "app_one"
	if _, err := runtime.routeRuntimeInput(ctx, state, route, false, domain.Task{}); err == nil || err.Error() != "APPLICATION_GATEWAY_SERVER_REQUIRED" {
		t.Fatal("未选择 HTTP 网关仍可发布入口", err)
	}
	cfg["gatewayServerId"] = gateway["id"]
	objectMapFromAny(cfg["swarmRuntime"])["overlayNetwork"] = "test-overlay"
	applied, err := runtime.routeRuntimeInput(ctx, state, route, false, domain.Task{})
	if err != nil || applied.ClusterID != "cluster-a" || applied.Gateway["id"] != gateway["id"] || applied.Network != "test-overlay" {
		t.Fatal("同群 HTTP 网关无法使用部署服务", err)
	}
	route["lastApplied"] = routeRuntimeSnapshot(applied)
	serverRuntimeMetrics(gateway)["swarmClusterId"] = "cluster-b"
	if _, err := runtime.routeRuntimeInput(ctx, state, route, false, domain.Task{}); err == nil || err.Error() != "APPLICATION_SWARM_CLUSTER_MISMATCH" {
		t.Fatal("跨集群网关没有被阻止", err)
	}
	delete(cfg, "gatewayServerId")
	objectMapFromAny(cfg["swarmRuntime"])["overlayNetwork"] = "other-overlay"
	removed, err := runtime.routeRuntimeInput(ctx, state, route, true, domain.Task{})
	if err != nil || removed.Gateway["id"] != gateway["id"] || removed.ClusterID != applied.ClusterID || removed.Network != applied.Network || removed.Revision != applied.Revision {
		t.Fatal("撤销使用了草稿现场而非已生效入口", err)
	}
}

func TestSingleRouteTargetDefaultsToFullTrafficWithoutRuntimeWeights(t *testing.T) {
	for _, previous := range []any{nil, 0, 7} {
		input := routeWeightTestInput(1, 1)
		bound := domain.Object{"slotKey": "one", "serviceName": "app-one"}
		if previous != nil {
			bound["weight"] = previous
		}
		input["services"] = []domain.Object{bound}
		config, err := validatedRouteConfig(routeWeightTestState(), "app_one", input)
		if err != nil || objectSlice(config["services"])[0]["weight"] != 1 {
			t.Fatal("单服务入口权重错误")
		}
	}
}

func TestRuntimeServiceConfigurationRejectsRouteWeightFields(t *testing.T) {
	for _, key := range []string{"accessWeight", "weight"} {
		service := domain.Object{"slotKey": "one", "serviceName": "app-one", key: 3}
		if err := normalizeApplicationSwarm(domain.EmptyState(), "", domain.Object{"services": []domain.Object{service}}); err == nil || err.Error() != "APPLICATION_SERVICE_CONFIG_INVALID" {
			t.Fatalf("运行服务仍接受入口权重 %s: %v", key, err)
		}
	}
}

func TestTraefikConfigurationKeepsTheSelectedRouteRatio(t *testing.T) {
	input := RouteRuntimeInput{Route: domain.Object{"id": "route_weighted"}, Domains: []domain.Object{{"name": "app.example.test"}}, Rules: []domain.Object{{"path": "/", "pathHandling": "preserve"}}}
	data, err := buildTraefikRoute(input, []domain.Object{{"name": "app-one", "port": 3000, "weight": 1}, {"name": "app-two", "port": 3000, "weight": 3}})
	if err != nil {
		t.Fatal(err)
	}
	document := domain.Object{}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	services := objectMapFromAny(objectMapFromAny(document["http"])["services"])
	for _, service := range services {
		weighted := objectSlice(objectMapFromAny(objectMapFromAny(service)["weighted"])["services"])
		if len(weighted) == 2 && numberValue(weighted[0]["weight"]) == 1 && numberValue(weighted[1]["weight"]) == 3 {
			return
		}
	}
	t.Fatal("Traefik 未保留入口比例")
}
