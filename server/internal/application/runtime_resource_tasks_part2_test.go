package application

import (
	"strings"
	"testing"
)

func TestRuntimeResourceExecutorScriptsUseStableImagesAndDestructiveUninstall(t *testing.T) {
	traefikScript := traefikInstallScript(TraefikRuntimeExecutionInput{
		Service: "iquops-traefik",
		Image:   "traefik:v3.7",
		ImagePullCandidates: []string{
			"traefik:v3.7",
			"public.ecr.aws/docker/library/traefik:v3.7",
			"ghcr.io/traefik/traefik:3.7",
		},
		Network:   "iquops-public",
		HTTPPort:  80,
		HTTPSPort: 443,
	})
	for _, expected := range []string{
		"docker network inspect 'iquops-public'",
		"docker network create --driver overlay --attachable 'iquops-public'",
		"TRAEFIK_SWARM_NETWORK_INVALID",
		"docker service create --name 'iquops-traefik'",
		"--publish 'mode=host,published=80,target=80'",
		"--publish 'mode=host,published=443,target=443'",
		"docker image inspect 'traefik:v3.7'",
		"for candidate in 'traefik:v3.7' 'public.ecr.aws/docker/library/traefik:v3.7' 'ghcr.io/traefik/traefik:3.7'; do",
		"timeout 90 docker pull \"$candidate\"",
		"docker tag \"$resolved_image\" 'traefik:v3.7'",
		"traefik:v3.7",
		"--providers.swarm=true",
		"--providers.swarm.endpoint=unix:///var/run/docker.sock",
		"--providers.swarm.exposedbydefault=false",
		"--providers.swarm.constraints='Label(`iquops.owner`,`iquops`)'",
		"type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock,readonly",
		"/opt/iquops/traefik",
	} {
		if !strings.Contains(traefikScript, expected) {
			t.Fatalf("Traefik install script missing %q", expected)
		}
	}
	if strings.Contains(traefikScript, "traefik:v3.3") {
		t.Fatal("Traefik install script uses an outdated image")
	}
	if count := strings.Count(traefikScript, "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock,readonly"); count != 1 {
		t.Fatalf("Traefik service should mount the Docker socket exactly once, got %d", count)
	}

}
