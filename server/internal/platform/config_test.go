package platform

import "testing"

func TestResolveProvidesTraefikImagePullFallbacks(t *testing.T) {
	t.Setenv("IQUOPS_TRAEFIK_IMAGE", "")
	t.Setenv("IQUOPS_TRAEFIK_IMAGE_PULL_CANDIDATES", "")

	config := Resolve(RoleWorker)
	expected := []string{
		"traefik:v3.7",
		"public.ecr.aws/docker/library/traefik:v3.7",
		"ghcr.io/traefik/traefik:3.7",
	}
	if len(config.TraefikImagePullCandidates) != len(expected) {
		t.Fatalf("traefik image pull candidates = %#v", config.TraefikImagePullCandidates)
	}
	for index, candidate := range expected {
		if config.TraefikImagePullCandidates[index] != candidate {
			t.Fatalf("traefik image pull candidates = %#v", config.TraefikImagePullCandidates)
		}
	}
	summary := config.RuntimeSummary()
	if candidates, ok := summary["traefikImagePullCandidates"].([]string); !ok || len(candidates) != len(expected) {
		t.Fatalf("runtime summary candidates = %#v", summary["traefikImagePullCandidates"])
	}
}

func TestResolveAllowsTraefikImagePullCandidateOverride(t *testing.T) {
	t.Setenv("IQUOPS_TRAEFIK_IMAGE_PULL_CANDIDATES", "registry.example.test/traefik:v3.7, public.ecr.aws/docker/library/traefik:v3.7")

	config := Resolve(RoleWorker)
	expected := []string{"registry.example.test/traefik:v3.7", "public.ecr.aws/docker/library/traefik:v3.7"}
	if len(config.TraefikImagePullCandidates) != len(expected) {
		t.Fatalf("traefik image pull candidates = %#v", config.TraefikImagePullCandidates)
	}
	for index, candidate := range expected {
		if config.TraefikImagePullCandidates[index] != candidate {
			t.Fatalf("traefik image pull candidates = %#v", config.TraefikImagePullCandidates)
		}
	}
}

func TestResolveProvidesFrpcDNSServerDefaultAndOverride(t *testing.T) {
	t.Setenv("IQUOPS_FRPC_DNS_SERVER", "")

	config := Resolve(RoleWorker)
	if config.FrpcDNSServer != "" {
		t.Fatalf("frpc dns server default = %q", config.FrpcDNSServer)
	}
	summary := config.RuntimeSummary()
	if _, set := summary["frpcDnsServer"]; set {
		t.Fatalf("runtime summary frpc dns server = %#v", summary["frpcDnsServer"])
	}

	t.Setenv("IQUOPS_FRPC_DNS_SERVER", "192.0.2.53")
	config = Resolve(RoleWorker)
	if config.FrpcDNSServer != "192.0.2.53" {
		t.Fatalf("frpc dns server override = %q", config.FrpcDNSServer)
	}
}
