package application

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestNormalizeLinuxArchForBundledFrps(t *testing.T) {
	for input, want := range map[string]string{
		"x86_64":  "amd64",
		"amd64":   "amd64",
		"aarch64": "arm64",
		"arm64":   "arm64",
	} {
		got, err := normalizeLinuxArch(input)
		if err != nil {
			t.Fatalf("normalize %s: %v", input, err)
		}
		if got != want {
			t.Fatalf("normalize %s = %s, want %s", input, got, want)
		}
	}
	if _, err := normalizeLinuxArch("riscv64"); err == nil {
		t.Fatal("expected unsupported arch to fail")
	}
}

func TestFrpsBinaryForArchSelectsBundledBinary(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "frps-linux-amd64")
	if err := os.WriteFile(want, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write frps: %v", err)
	}
	got, err := frpsBinaryForArch(dir, "amd64")
	if err != nil {
		t.Fatalf("select frps: %v", err)
	}
	if got != want {
		t.Fatal("selected FRPS binary differs from the expected file")
	}
}

func TestFrpsDockerignoreKeepsSecretsOutOfBuildContext(t *testing.T) {
	ignore := frpsDockerignore()
	if !strings.Contains(ignore, "!frps") || !strings.Contains(ignore, "!Dockerfile") {
		t.Fatal("dockerignore must include binary and Dockerfile")
	}
	if strings.Contains(ignore, "frps.env") || strings.Contains(ignore, "frps.toml") {
		t.Fatal("dockerignore must not include secret/config files")
	}
}

func TestFrpsActivationRoutesDomainAndDirectMappings(t *testing.T) {
	input := FrpTunnelExecutionInput{
		Run: domain.FrpTunnelRun{
			ID: "tunrun_test", TunnelID: "tun_test", FrpsBindPort: 7000, RemoteDir: "/opt/iquops/tunnels/tunrun_test",
			ManagedFrps: true,
			Mappings: []domain.FrpTunnelMapping{
				{
					Name: "probe", TargetType: domain.FrpTunnelMappingTargetHTTPProbe,
					LocalHost: "127.0.0.1", LocalPort: 17990, FrpsProxyPort: 17990, PublicPathPrefix: "/probe",
				},
				{
					Name: "local-api", TargetType: domain.FrpTunnelMappingTargetLocalService,
					LocalHost: "127.0.0.1", LocalPort: 3001, FrpsProxyPort: 18081,
					PublicDomain: "example.test", PublicPathPrefix: "/api", Entrypoint: "websecure", PublicScheme: "https",
				},
			},
		},
		Server: domain.Object{"publicHost": "203.0.113.10"},
	}
	service := managedFrps{ID: "service_test", labels: map[string]string{
		"traefik.enable": "true", "traefik.swarm.network": "iquops-public", "traefik.http.routers.control.rule": "example",
	}}
	command := strings.Join(frpsActivateArgs(input, service), " ")
	for _, expected := range []string{
		"docker service update --detach=true --replicas 1",
		"--network-add 'iquops-public'",
		"traefik.enable=true",
		"traefik.swarm.network=iquops-public",
		"iquops.tunnelId=tun_test",
		"iquops.tunnelRunId=tunrun_test",
		"PathPrefix(`/~!frp`)",
		"PathPrefix(`/api`)",
		"Host(`example.test`)",
		"loadbalancer.server.port=7000",
		"loadbalancer.server.port=18081",
		"published=17990,target=17990,protocol=tcp,mode=host",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("activation missing %q", expected)
		}
	}
	for _, marker := range []string{"published=7000", "published=18081", "PathPrefix(`/probe`)", "docker service rm"} {
		if strings.Contains(command, marker) {
			t.Fatalf("unexpected option %q", marker)
		}
	}
	stopped := strings.Join(frpsParkArgs(input, service), " ")
	if !strings.Contains(stopped, "--replicas 0") || !strings.Contains(stopped, "--publish-rm 17990") ||
		!strings.Contains(stopped, "--network-rm 'iquops-public'") || strings.Contains(stopped, "docker service rm") {
		t.Fatal("stop must retain the installed service and remove the tunnel routes")
	}

	probe := input.Run.Mappings[0]
	if actual := frpTunnelPublicURL(input.Server, probe, 80, 443, false); actual != "http://203.0.113.10:17990/probe" {
		t.Fatal("unexpected direct probe URL")
	}
	config := frpcConfig(input)
	for _, expected := range []string{
		`serverAddr = "example.test"`, `transport.protocol = "wss"`, `type = "http2http"`,
		`X-IQuOps-Tunnel-ID = "tun_test"`, `X-IQuOps-Tunnel-Run-ID = "tunrun_test"`,
		`X-IQuOps-Tunnel-Mapping = "probe"`, `localAddr = "127.0.0.1:17990"`,
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("frpc config missing %q", expected)
		}
	}

	input.Run.Mappings = []domain.FrpTunnelMapping{probe}
	command = strings.Join(frpsActivateArgs(input, service), " ")
	if strings.Contains(command, "--network-add") || !strings.Contains(command, "published=7000,target=7000") {
		t.Fatal("direct tunnel must publish the control port without Traefik")
	}
	config = frpcConfig(input)
	for _, expected := range []string{`serverAddr = "203.0.113.10"`, `serverPort = 7000`, `transport.protocol = "tcp"`, `transport.tls.enable = true`} {
		if !strings.Contains(config, expected) {
			t.Fatalf("direct frpc config missing %q", expected)
		}
	}
}

func TestFrpcConfigUsesConfiguredDNSServer(t *testing.T) {
	input := FrpTunnelExecutionInput{
		Run: domain.FrpTunnelRun{
			ID: "tunrun_test", TunnelID: "tun_test", FrpsBindPort: 7000,
			Mappings: []domain.FrpTunnelMapping{{
				Name: "local-api", TargetType: domain.FrpTunnelMappingTargetLocalService,
				LocalHost: "localhost", LocalPort: 5173, FrpsProxyPort: 18080,
				PublicDomain: "console.example.invalid", PublicPathPrefix: "/", Entrypoint: "web", PublicScheme: "http",
			}},
		},
		Server:        domain.Object{"publicHost": "198.51.100.7"},
		FrpcDNSServer: "192.0.2.53",
	}
	config := frpcConfig(input)
	if !strings.Contains(config, `dnsServer = "192.0.2.53"`) {
		t.Fatal("frpc config missing dnsServer")
	}
	if !strings.Contains(config, `serverAddr = "console.example.invalid"`) {
		t.Fatal("frpc config must keep the public domain as serverAddr")
	}
}

func TestFrpsCleanupOnlyUsesTheSwarmServiceLifecycle(t *testing.T) {
	command := frpsSwarmServiceRemoveCommand("iquops-frps-run")
	if !strings.Contains(command, "docker service rm 'iquops-frps-run'") || !strings.Contains(command, "docker service inspect 'iquops-frps-run'") {
		t.Fatal("FRPS cleanup must use the Swarm service lifecycle")
	}
	if strings.Contains(command, "docker rm") {
		t.Fatal("FRPS cleanup must not contain a standalone-container compatibility path")
	}
}

func TestFrpRequiresAnExistingSwarmOverlayNetwork(t *testing.T) {
	command := frpTraefikNetworkCheckCommand("iquops-public")
	if !strings.Contains(command, "--format '{{.Driver}}|{{.Scope}}'") || !strings.Contains(command, "= 'overlay|swarm'") {
		t.Fatal("FRP network check must require a Swarm overlay")
	}
	if strings.Contains(command, "network create") {
		t.Fatal("FRP must not create a local bridge network")
	}
}

func TestReadinessProbeTransportKeepsSystemProxyForHTTPS(t *testing.T) {
	for _, target := range []string{"http://example.test", "https://example.test"} {
		client := httpClientForReadinessProbe(target)
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("readiness transport for %s has type %T", target, client.Transport)
		}
		if transport.Proxy == nil {
			t.Fatalf("readiness transport for %s must preserve the system proxy resolver", target)
		}
		if strings.HasPrefix(target, "https://") && transport.TLSClientConfig == nil {
			t.Fatal("HTTPS readiness transport must retain its certificate issuance probe configuration")
		}
	}
}
