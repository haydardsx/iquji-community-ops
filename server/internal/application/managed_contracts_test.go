package application

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestConfiguredTraefikResolverUsedByGatewayAndRoutes(t *testing.T) {
	const resolver = "example-acme"
	text := traefikInstallScript(TraefikRuntimeExecutionInput{CertResolver: resolver})
	for _, flag := range []string{"storage=", "httpchallenge.entrypoint=web"} {
		if !strings.Contains(text, "--certificatesresolvers."+resolver+".acme."+flag) {
			t.Fatalf("缺少签发器参数 %s", flag)
		}
	}
	in := RouteRuntimeInput{
		CertResolver: resolver, Route: domain.Object{"id": "route_demo"},
		Domains:  []domain.Object{{"name": "source.example.test", "tls": true}},
		Rules:    []domain.Object{{"path": "/"}},
		Redirect: domain.Object{"targetHost": "target.example.test"},
	}
	app, err := buildTraefikRoute(in, []domain.Object{{"name": "service", "port": 8080, "weight": 1}})
	if err != nil {
		t.Fatal(err)
	}
	redirect, err := buildDomainRedirectRoute(in)
	if err != nil {
		t.Fatal(err)
	}
	jenkins, err := jenkinsAccessRoute(in, "certs", "192.0.2.1", 8443)
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range [][]byte{app, redirect, jenkins} {
		var doc struct {
			HTTP struct {
				Routers map[string]struct {
					TLS *struct{ CertResolver string }
				}
			}
		}
		if err := json.Unmarshal(cfg, &doc); err != nil {
			t.Fatal(err)
		}
		secure := 0
		for _, router := range doc.HTTP.Routers {
			if router.TLS != nil {
				secure++
				if router.TLS.CertResolver != resolver {
					t.Fatal("TLS 路由没有引用配置的签发器")
				}
			}
		}
		if secure == 0 {
			t.Fatal("缺少 HTTPS 路由")
		}
	}
	labels := traefikHTTPRouterLabels("example", "example", "Host(`example.test`)", "websecure", resolver, 8080, 100)
	if !strings.Contains(strings.Join(labels, "\n"), ".tls.certresolver="+resolver) {
		t.Fatal("隧道路由缺少签发器")
	}
	f := newSwarmRebuildFixture()
	raw, _ := json.Marshal(f.services[0])
	if err := validateSwarmRebuildGateway(bytes.ReplaceAll(raw, []byte("iquops-le"), []byte(resolver)), f.input(), "node_old"); err != nil {
		t.Fatal("重建不能识别已安装的自定义签发器", err)
	}
}

func TestAgentPresenceAndReportsShareMetadataContract(t *testing.T) {
	r, store, ctx := newTaskTestRuntime(t)
	server := "server_example"
	input := domain.Object{"serverId": server, "version": "example", "samples": []domain.Object{{"targetKind": "server"}}, "swarmSnapshot": domain.Object{"status": "not_enabled"}, "sampledAt": domain.Now()}
	registered, err := r.UpsertMonitoringAgent(ctx, input, "online")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"samples", "swarmSnapshot", "sampledAt"} {
		if _, found := registered[k]; found {
			t.Fatal("注册保存了采样正文", k)
		}
	}
	if _, err = r.AcceptMonitoringReport(ctx, input); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := r.UpsertMonitoringAgent(ctx, domain.Object{"serverId": server}, "online")
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if registered["id"] != heartbeat["id"] || heartbeat["version"] != "example" || len(state.MetricSamples) != 1 || len(state.SwarmSnapshots) != 1 || heartbeat["lastSampleAt"] == nil {
		t.Fatal("注册、心跳和报告的身份、时间或采样不一致")
	}
}
