package application

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestDomainRedirectDraftPublishAndWithdraw(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	fake := &routeTestExecutor{}
	r.RouteExecutor = fake
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"traefik": true}}
		state.Domains = []domain.Object{{"id": "apex", "name": "example.test"}, {"id": "web", "name": "www.example.test"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	draft := domain.Object{"gatewayServerId": "srv_gateway", "targetDomainId": "web", "scheme": "https", "mode": "permanent"}
	if _, err := r.SaveDomainRedirect(ctx, "apex", draft); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateObject(ctx, "domains", "apex", domain.Object{"redirectRuntime": domain.Object{"status": "active"}}); err == nil {
		t.Fatal("允许客户端伪造发布状态")
	}
	if _, err := r.SaveDomainRedirect(ctx, "web", domain.Object{"gatewayServerId": "srv_gateway", "targetDomainId": "apex", "scheme": "http", "mode": "temporary"}); err == nil {
		t.Fatal("允许循环跳转")
	}
	run := func(action string) domain.State {
		t.Helper()
		task, err := r.CreateDomainRedirectOperation(ctx, "apex", action)
		if err != nil || task.Status != "pending_confirmation" {
			t.Fatalf("创建任务 %v %v", task.Status, err)
		}
		if _, err := r.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := r.RunWorkerOnce(ctx); err != nil {
			t.Fatal(err)
		}
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	fake.beforeApply = func() {
		state, _ := store.LoadState(ctx)
		if !redirectOccupiesHost(state.Domains[0]) {
			t.Fatal("远端发布前未记录意图")
		}
	}
	state := run(applyDomainRedirect)
	if objectMapFromAny(state.Domains[0]["redirectRuntime"])["status"] != "active" {
		t.Fatal("跳转规则未发布")
	}
	if err := r.DeleteObject(ctx, "domains", "web"); err == nil {
		t.Fatal("可删除已引用的目标")
	}
	if !redirectBlocksBackend(state.Domains[0], "http") || !redirectBlocksBackend(state.Domains[0], "https") {
		t.Fatal("整域入口未保留")
	}
	fake.fail = true
	state = run(removeDomainRedirect)
	if !redirectOccupiesHost(state.Domains[0]) {
		t.Fatal("撤销失败丢失远端事实")
	}
	fake.fail = false
	state = run(removeDomainRedirect)
	if fake.removeCalls != 2 || state.Domains[0]["redirect"] != nil || redirectOccupiesHost(state.Domains[0]) {
		t.Fatal("未完整撤销")
	}
}

func TestRedirectPreservesRequestURIAndSharesHTTPSBackend(t *testing.T) {
	for _, item := range []struct {
		target  string
		wantTLS bool
		port    int
	}{
		{"www.example.test", true, 0}, {"example.test", false, 8443},
	} {
		input := RouteRuntimeInput{Route: domain.Object{"id": "owned"}, Revision: "rev", Domains: []domain.Object{{"name": "example.test", "tls": true}}, Redirect: domain.Object{"targetHost": item.target, "targetPort": item.port, "scheme": "https", "mode": "permanent"}}
		data, err := buildDomainRedirectRoute(input)
		if err != nil {
			t.Fatal(err)
		}
		var file domain.Object
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatal(err)
		}
		config := objectMapFromAny(file["http"])
		if (len(objectMapFromAny(config["routers"])) == 2) != item.wantTLS {
			t.Fatal("入口协议错误")
		}
		const uri = "/docs/a%2Fb?q=a%2Fb&x=1"
		for _, middleware := range objectMapFromAny(config["middlewares"]) {
			rule := objectMapFromAny(objectMapFromAny(middleware)["redirectRegex"])
			if len(rule) == 0 {
				continue
			}
			loc := regexp.MustCompile(stringValue(rule["regex"])).ReplaceAllString("http://example.test:8080"+uri, stringValue(rule["replacement"]))
			res := &http.Response{StatusCode: 301, Header: http.Header{"Location": {loc}}}
			if !verifiedDomainRedirect(input.Redirect, uri, res) || !strings.HasSuffix(loc, uri) {
				t.Fatal("路径、端口或查询串不符")
			}
			res.StatusCode = 200
			if verifiedDomainRedirect(input.Redirect, uri, res) {
				t.Fatal("错误状态码通过校验")
			}
		}
	}
	asset := domain.Object{"id": "same", "redirectRuntime": domain.Object{"lastApplied": domain.Object{"redirect": domain.Object{"targetDomainId": "same"}}}}
	if redirectBlocksBackend(asset, "https") || !redirectBlocksBackend(asset, "http") {
		t.Fatal("同域升级未保留 HTTPS 业务入口")
	}
}

func TestLegacyRedirectCleanupPreservesApplicationRouter(t *testing.T) {
	id, host := "route", "example.test"
	prefix := "iquops-" + routeRevision(id)
	base := prefix + "-d0-r0"
	marker := prefix + "-revision"
	document := domain.Object{"http": domain.Object{"routers": domain.Object{base + "-http": domain.Object{"rule": `Host("example.test") && PathPrefix("/")`, "middlewares": []string{marker, base + "-https"}}, base + "-https": domain.Object{"service": "web"}}, "middlewares": domain.Object{marker: domain.Object{"headers": domain.Object{"customResponseHeaders": domain.Object{"X-IQuOps-Route": routeRevision(id) + ":before"}}}, base + "-https": domain.Object{"redirectScheme": domain.Object{"scheme": "https"}}, base + "-path": domain.Object{"stripPrefix": domain.Object{"prefixes": []string{"/prefix"}}}}}}
	raw, _ := json.Marshal(document)
	next, old, revision, err := retireLegacyDocument(raw, id, host)
	if err != nil || old != "before" || old == revision || strings.Contains(string(next), "redirectScheme") || !strings.Contains(string(next), "stripPrefix") || !strings.Contains(string(next), `"service":"web"`) {
		t.Fatal("旧中间件清理有误")
	}
	again, _, second, err := retireLegacyDocument(next, id, host)
	if err != nil || len(again) > 0 || second != revision {
		t.Fatal("无法重复迁移", err)
	}
}

func TestDomainRedirectMigrationBackupAndRestore(t *testing.T) {
	state := domain.EmptyState()
	state.Domains = []domain.Object{{"id": "source", "name": "example.test", "certificateId": "cert", "httpsRedirectEnabled": true}}
	state.Certificates = []domain.Object{{"id": "cert", "resolverId": "resolver"}}
	state.Resolvers = []domain.Object{{"id": "resolver", "ingressId": "ingress"}}
	state.Ingresses = []domain.Object{{"id": "ingress", "serverIds": []string{"gateway"}}}
	state.Servers = []domain.Object{{"id": "gateway", "publicHost": "192.0.2.1"}}
	domain.MigrateDomainRedirects(&state)
	if state.Domains[0]["httpsRedirectEnabled"] != nil || objectMapFromAny(state.Domains[0]["redirect"])["targetDomainId"] != "source" {
		t.Fatal("旧开关未迁移")
	}
	runtime := domain.Object{"lastApplied": domain.Object{"gatewayServerId": "gateway", "revision": "actual"}}
	state.Domains[0]["redirectRuntime"] = runtime
	scopes, reasons, err := domain.ResolveBackupScopeClosure(state, []domain.BackupScope{domain.BackupScopeDomainsCertificates})
	if err != nil {
		t.Fatal(err)
	}
	hasGateway := false
	for _, reason := range reasons {
		if reason.Reason == "DOMAIN_REDIRECT_GATEWAY_REFERENCE" {
			hasGateway = true
		}
	}
	if !hasGateway {
		t.Fatal("缺少网关备份依赖")
	}
	payload := domain.BuildBackupPayload(state, "2", scopes, false)
	if asset := payload.DomainsCertificates.Domains[0]; asset["redirectRuntime"] != nil || asset["redirect"] == nil {
		t.Fatal("归档中的跳转事实错误")
	}
	if err := validateBackupPayloadStructure(payload, scopes); err != nil {
		t.Fatal(err)
	}
	plan := buildRestoreObjectPlan(state, payload, scopes, "test", t.TempDir())
	if err := applyRestoreObjectPlan(&state, plan, domain.RestoreConflictOverwriteSelected); err != nil {
		t.Fatal(err)
	}
	if restored := objectMapFromAny(state.Domains[0]["redirectRuntime"]); restored["status"] != "pending" || objectMapFromAny(restored["lastApplied"])["revision"] != "actual" {
		t.Fatal("覆盖恢复丢失本机远端事实")
	}
	payload.Servers.Servers = nil
	if err := validateBackupPayloadStructure(payload, scopes); err == nil {
		t.Fatal("接受了缺少网关的归档")
	}
}
