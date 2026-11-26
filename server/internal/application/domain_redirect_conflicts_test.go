package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestDomainRedirectDraftChecksBackendOccupancy(t *testing.T) {
	const gateway = "srv_gateway"
	for _, test := range []struct {
		name          string
		routeSnapshot string
		frpStatus     domain.FrpTunnelStatus
		tls           bool
		upgrade       bool
		wantError     bool
	}{
		{"已生效的 HTTPS 应用阻止跨域覆盖", "lastApplied", "", true, false, true},
		{"正在发布的 HTTP 应用阻止同域升级", "pendingRemote", "", false, true, true},
		{"正在发布的 HTTPS 应用可以共存", "pendingRemote", "", true, true, false},
		{"正在启动的 HTTP 映射阻止同域升级", "", domain.FrpTunnelStatusStarting, false, true, true},
		{"正在运行的 HTTPS 映射可以共存", "", domain.FrpTunnelStatusRunning, true, true, false},
		{"正在撤销的 HTTPS 映射仍占用整域", "", domain.FrpTunnelStatusStopping, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, store, ctx := newFrpRuntime(t)
			seedFrpServer(t, ctx, store, true)
			const host = "source.example.test"
			const originalTarget = "prior"
			if err := store.MutateState(ctx, func(state *domain.State) error {
				state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{"traefik": true}}
				state.Domains = []domain.Object{
					{"id": "source", "name": host, "certificateId": "cert", "redirect": domain.Object{"targetDomainId": originalTarget}},
					{"id": "target", "name": "target.example.test"}, {"id": originalTarget, "name": "prior.example.test"},
				}
				state.Certificates = []domain.Object{{"id": "cert", "domains": []string{host}, "issuer": "lets_encrypt", "challengeType": "http-01", "resolverId": "resolver"}}
				state.Resolvers = []domain.Object{{"id": "resolver", "ingressId": "ingress"}}
				state.Ingresses = []domain.Object{{"id": "ingress", "serverIds": []string{gateway}}}
				if test.routeSnapshot != "" {
					state.Routes = []domain.Object{{test.routeSnapshot: domain.Object{"domains": []domain.Object{{"name": host, "tls": test.tls}}}}}
				}
				if test.frpStatus != "" {
					entry := "web"
					if test.tls {
						entry = "websecure"
					}
					state.FrpTunnelRuns = []domain.FrpTunnelRun{{Status: test.frpStatus, Mappings: []domain.FrpTunnelMapping{{PublicDomain: host, Entrypoint: entry}}}}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			nextTarget := "target"
			if test.upgrade {
				nextTarget = "source"
			}
			_, err := r.SaveDomainRedirect(ctx, "source", domain.Object{"targetDomainId": nextTarget, "gatewayServerId": gateway, "scheme": "https", "mode": "permanent"})
			if test.wantError {
				if err == nil || err.Error() != "DOMAIN_REDIRECT_HOST_IN_USE" {
					t.Fatalf("未按占用拒绝草稿：%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			state, err := store.LoadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := nextTarget
			if test.wantError {
				want = originalTarget
			}
			if stored := objectMapFromAny(findObject(state.Domains, "source")["redirect"]); stored["targetDomainId"] != want {
				t.Fatal("草稿目标错误")
			}
		})
	}
}

func TestDomainRedirectCycleAcrossRuntimeAndDraft(t *testing.T) {
	edge := func(target string) domain.Object { return domain.Object{"targetDomainId": target} }
	state := domain.EmptyState()
	state.Domains = []domain.Object{
		{"id": "a", "name": "a.example.test"},
		{"id": "b", "name": "b.example.test", "redirect": edge("e"), "redirectRuntime": domain.Object{"lastApplied": domain.Object{"redirect": edge("c")}}},
		{"id": "c", "name": "c.example.test", "redirectRuntime": domain.Object{"pendingRemote": domain.Object{"redirect": edge("d")}}},
		{"id": "d", "name": "d.example.test", "redirect": edge("a")},
		{"id": "e", "name": "e.example.test"},
	}
	draft := domain.Object{"targetDomainId": "b", "gatewayServerId": "", "scheme": "http", "mode": "temporary"}
	if err := validateRedirectDefinition(state, "a", draft); err == nil || err.Error() != "DOMAIN_REDIRECT_CYCLE" {
		t.Fatalf("漏判跨越生效、待核验与草稿版本的环路：%v", err)
	}
	delete(state.Domains[2], "redirectRuntime")
	if err := validateRedirectDefinition(state, "a", draft); err != nil {
		t.Fatalf("撤销中间版本后仍报告环路：%v", err)
	}
}
