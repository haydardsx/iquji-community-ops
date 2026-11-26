package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type routeTestExecutor struct {
	fail                    bool
	applyCalls, removeCalls int
	beforeApply             func()
}

func (f *routeTestExecutor) Apply(_ context.Context, input RouteRuntimeInput) (domain.Object, error) {
	f.applyCalls++
	if f.beforeApply != nil {
		f.beforeApply()
	}
	if f.fail {
		return nil, errors.New("ROUTE_REVISION_NOT_LOADED")
	}
	return routeRuntimeSnapshot(input), nil
}
func (f *routeTestExecutor) Remove(_ context.Context, input RouteRuntimeInput) (domain.Object, error) {
	f.removeCalls++
	if f.fail {
		return nil, errors.New("ROUTE_REVOCATION_NOT_VERIFIED")
	}
	return routeRuntimeSnapshot(input), nil
}

func TestStaleRouteCleanupRemovesOnlyFilesDerivedFromTheRouteID(t *testing.T) {
	command := staleRouteCleanupCommand("route_demo_1")
	path, staged, backup := routeRemotePaths("route_demo_1")
	for _, expected := range []string{shellQuote(path), shellQuote(staged), shellQuote(backup)} {
		if !strings.Contains(command, expected) {
			t.Fatal("过期路由清理缺少当前路由的文件")
		}
	}
	if strings.Contains(command, " -r ") || strings.Contains(command, "*") || strings.Contains(command, "..") || strings.Contains(command, ";") {
		t.Fatal("过期路由清理命令扩大了文件范围")
	}
}

func TestApplicationRouteDraftApplyAndRevoke(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	fake := &routeTestExecutor{}
	runtime.RouteExecutor = fake
	fake.beforeApply = func() {
		state, err := store.LoadState(ctx)
		if err != nil || len(state.Routes) == 0 || len(objectMapFromAny(state.Routes[0]["pendingRemote"])) == 0 {
			t.Fatal("远端执行前未持久化意图")
		}
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_one"}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one"}, {"id": "app_other"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "deploymentManagerServerId": "srv_gateway", "gatewayServerId": "srv_gateway", "swarmRuntime": domain.Object{"services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}}}}}
		state.Domains = []domain.Object{{"id": "domain_one", "name": "app.example.test"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := domain.Object{"domainIds": []string{"domain_one"}, "services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}}, "rules": []domain.Object{{"path": "/api", "pathHandling": "preserve"}}, "disabled": false}
	route, err := runtime.UpsertApplicationRoute(ctx, "app_one", "", input)
	if err != nil {
		t.Fatal(err)
	}
	id := stringValue(route["id"])
	if route["status"] != "pending" || route["lastApplied"] != nil {
		t.Fatal("保存草稿伪造了发布事实")
	}
	if _, err := runtime.UpsertApplicationRoute(ctx, "app_other", id, input); err == nil {
		t.Fatal("允许跨应用编辑路由")
	}
	apply := func(action string) domain.Task {
		t.Helper()
		task, err := runtime.CreateRouteOperation(ctx, "app_one", id, action)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != "pending_confirmation" {
			t.Fatal("危险动作没有确认")
		}
		if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.RunWorkerOnce(ctx); err != nil {
			t.Fatal(err)
		}
		return task
	}
	apply("apply_route")
	state, _ := store.LoadState(ctx)
	if state.Routes[0]["status"] != "active" || len(objectMapFromAny(state.Routes[0]["lastApplied"])) == 0 || fake.applyCalls != 1 {
		t.Fatal("真实执行结果没有保存")
	}
	fake.fail = true
	task := apply("delete_route")
	state, _ = store.LoadState(ctx)
	run, _ := runtime.GetOperationRun(ctx, task.ID)
	if len(state.Routes) != 1 || len(objectMapFromAny(state.Routes[0]["lastApplied"])) == 0 || len(objectMapFromAny(state.Routes[0]["pendingRemote"])) == 0 || run["status"] != "failed" {
		t.Fatal("远端撤销失败却删除了本地记录")
	}
	fake.fail = false
	apply("disable_route")
	state, _ = store.LoadState(ctx)
	if state.Routes[0]["lastApplied"] != nil || state.Routes[0]["disabled"] != true {
		t.Fatal("停用未清除远端快照")
	}
	apply("delete_route")
	state, _ = store.LoadState(ctx)
	if len(state.Routes) != 0 || fake.removeCalls != 2 {
		t.Fatal("草稿删除不应再次操作远端")
	}
}

func TestTraefikRouteUsesBoundServicesAndControlledRules(t *testing.T) {
	input := RouteRuntimeInput{Route: domain.Object{"id": "route/../unsafe"}, Revision: "revision-123", HTTPSPort: 8443, Domains: []domain.Object{{"name": "app.example.test", "tls": true}}, Rules: []domain.Object{{"path": "/api", "pathHandling": "rewrite", "rewriteTargetPath": "/health"}}}
	data, err := buildTraefikRoute(input, []domain.Object{{"name": "app-web", "port": 8080, "weight": 2}})
	if err != nil {
		t.Fatal(err)
	}
	var document domain.Object
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"http://app-web:8080", "iquops-le", "X-IQuOps-Route", "revision-123", "replacePath"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("生成配置缺少 %s", expected)
		}
	}
	if urls := appliedRouteURLs(input); len(urls) != 1 || urls[0] != "https://app.example.test:8443/api" {
		t.Fatalf("成功访问地址遗漏真实端口：%v", urls)
	}
	path, staged, backup := routeRemotePaths("route/../unsafe")
	if strings.Contains(path+staged+backup, "..") || strings.Contains(path, "unsafe") {
		t.Fatal("外部ID参与了远端路径拼接")
	}
}

func TestDomainAssetsRequireManagedCertificateBindings(t *testing.T) {
	state := domain.EmptyState()
	state.Servers = []domain.Object{{"id": "gateway"}}
	state.Ingresses = []domain.Object{{"id": "ingress", "serverIds": []string{"gateway"}}}
	state.Resolvers = []domain.Object{{"id": "resolver", "ingressId": "ingress"}}
	certificate, err := normalizeDomainAsset("certificates", domain.Object{"name": "证书", "issuer": "lets_encrypt", "challengeType": "http-01", "domains": []string{"app.example.test"}, "resolverId": "resolver"}, state, nil)
	if err != nil || certificate["status"] != "pending" {
		t.Fatalf("保存资产冒充签发：%v", err)
	}
	certificate["id"] = "certificate"
	state.Certificates = []domain.Object{certificate}
	asset := domain.Object{"id": "domain", "name": "app.example.test", "certificateId": "certificate"}
	if _, err := routeDomainSnapshot(state, asset, "other-gateway"); err == nil {
		t.Fatal("HTTPS 允许了未绑定网关")
	}
	if _, err := routeDomainSnapshot(state, asset, "gateway"); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeDomainAsset("certificates", domain.Object{"name": "上传", "issuer": "manual_upload"}, state, nil); err == nil {
		t.Fatal("重新启用了手工证书上传")
	}
}
