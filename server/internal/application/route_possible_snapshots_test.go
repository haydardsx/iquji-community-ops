package application

import (
	"fmt"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRouteFailureSnapshotsKeepDomainRuleAndPortPairs(t *testing.T) {
	old := domain.Object{"revision": "old", "httpPort": 8080, "httpsPort": 8443,
		"domains": []domain.Object{{"name": "old.example.test", "tls": true}},
		"rules":   []domain.Object{{"path": "/api"}}}
	pending := domain.Object{"revision": "pending", "httpPort": 9090,
		"domains": []domain.Object{{"name": "new.example.test"}},
		"rules":   []domain.Object{{"path": "/new"}}}
	route := domain.Object{"id": "route_one", "lastApplied": old, "pendingRemote": pending}
	input := RouteRuntimeInput{Route: route, HTTPPort: 80, HTTPSPort: 443,
		Domains: []domain.Object{{"name": "desired.example.test"}}, Rules: []domain.Object{{"path": "/desired"}},
		PossibleSnapshots: possibleRouteSnapshots(route)}
	targets, err := routeAccessTargets(input, true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"old.example.test/api/https/8443": true, "old.example.test/api/http/8080": true, "new.example.test/new/http/9090": true}
	for _, target := range targets {
		scheme, port := "http", target.input.HTTPPort
		if target.domain["tls"] == true {
			scheme, port = "https", target.input.HTTPSPort
		}
		key := fmt.Sprintf("%s%s/%s/%d", stringValue(target.domain["name"]), stringValue(target.rule["path"]), scheme, port)
		if !want[key] {
			t.Fatalf("撤销混用了新旧域名、路径或端口：%s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("撤销遗漏可能仍生效的入口：%v", want)
	}
	applied, err := routeAccessTargets(input, false)
	if err != nil || len(applied) != 1 || applied[0].domain["name"] != "desired.example.test" || applied[0].rule["path"] != "/desired" {
		t.Fatal("发布探测使用了旧快照")
	}
	state := domain.EmptyState()
	state.Routes = []domain.Object{route}
	for _, snapshot := range []domain.Object{old, pending} {
		if err := rejectConflictingRoute(state, domain.Object{"id": "another"}, objectSlice(snapshot["domains"]), objectSlice(snapshot["rules"])); err == nil {
			t.Fatal("冲突检查遗漏故障快照")
		}
	}
	if err := rejectConflictingRoute(state, domain.Object{"id": "another"}, objectSlice(old["domains"]), objectSlice(pending["rules"])); err != nil {
		t.Fatal("冲突检查混用了两个快照的组合")
	}
}
