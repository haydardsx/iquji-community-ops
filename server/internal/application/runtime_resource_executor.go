package application

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

const (
	traefikRemoteRoot = "/opt/iquops/traefik"
)

type RuntimeResourceExecutor interface {
	InstallTraefikGateway(ctx context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error)
	UninstallTraefikGateway(ctx context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error)
}

func managedTraefikResolver(name string) string {
	return firstNonEmptyString(strings.TrimSpace(name), platform.DefaultTraefikCertResolver)
}

type TraefikRuntimeExecutionInput struct {
	CertResolver        string
	Server              domain.Object
	SSHKey              string
	Service             string
	Image               string
	ImagePullCandidates []string
	Network             string
	HTTPPort            int
	HTTPSPort           int
	AdvertiseAddr       string
	LogStep             func(source string, phase string, level string, message string)
}

type SystemRuntimeResourceExecutor struct{}

func NewSystemRuntimeResourceExecutor() *SystemRuntimeResourceExecutor {
	return &SystemRuntimeResourceExecutor{}
}

func (e *SystemRuntimeResourceExecutor) InstallTraefikGateway(ctx context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error) {
	input = withTraefikLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	swarm, err := readRemoteSwarmStatus(ctx, client, input.Network)
	if err != nil || !swarm.Active {
		return nil, errors.New("TRAEFIK_SWARM_REQUIRED")
	}
	if swarm.Role != "manager" {
		return nil, errors.New("TRAEFIK_SWARM_MANAGER_REQUIRED")
	}
	input.LogStep("docker", "install-traefik", "info", "正在创建 Traefik 网关服务 "+input.Service)
	if _, err := remoteOutput(ctx, client, traefikInstallScript(input)); err != nil {
		return nil, errors.New(stableTraefikExecutionError(err, "REMOTE_TRAEFIK_INSTALL_FAILED"))
	}
	return domain.Object{
		"metrics": domain.Object{
			"ssh":                        true,
			"traefik":                    true,
			"traefikStatus":              "running",
			"traefikService":             input.Service,
			"traefikImage":               input.Image,
			"traefikImagePullCandidates": input.ImagePullCandidates,
			"traefikHttpPort":            input.HTTPPort,
			"traefikHttpsPort":           input.HTTPSPort,
			"traefikDataRoot":            traefikRemoteRoot,
			"traefikNetwork":             input.Network,
			"traefikSwarmNetwork":        input.Network,
		},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik "+input.Image+" deployed"),
		},
	}, nil
}

func (e *SystemRuntimeResourceExecutor) UninstallTraefikGateway(ctx context.Context, input TraefikRuntimeExecutionInput) (domain.Object, error) {
	input = withTraefikLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	input.LogStep("docker", "uninstall-traefik", "info", "正在移除 Traefik 网关服务 "+input.Service)
	if _, err := remoteOutput(ctx, client, traefikUninstallScript(input.Service)); err != nil {
		return nil, errors.New(stableTraefikExecutionError(err, "REMOTE_TRAEFIK_UNINSTALL_FAILED"))
	}
	return domain.Object{
		"metrics": domain.Object{"ssh": true, "traefik": false},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("traefik", serverRuntimeCheckSuccess, "Traefik removed"),
		},
	}, nil
}

func traefikInstallScript(input TraefikRuntimeExecutionInput) string {
	resolver := "--certificatesresolvers." + managedTraefikResolver(input.CertResolver) + ".acme."
	input = withTraefikLogStep(input)
	service := shellQuote(input.Service)
	network := shellQuote(input.Network)
	image := shellQuote(input.Image)
	candidates := shellQuotedWords(input.ImagePullCandidates)
	pullFailureMessage := shellQuote("Unable to pull Traefik image from candidates: " + strings.Join(input.ImagePullCandidates, ", "))
	return strings.Join([]string{
		"set -e",
		"service_name=" + service,
		"test \"$(docker info --format '{{.Swarm.LocalNodeState}}')\" = active || { printf 'TRAEFIK_SWARM_REQUIRED\\n'; exit 2; }",
		"test \"$(docker info --format '{{.Swarm.ControlAvailable}}')\" = true || { printf 'TRAEFIK_SWARM_MANAGER_REQUIRED\\n'; exit 3; }",
		"manager_node=$(docker info --format '{{.Swarm.NodeID}}')",
		"test -n \"$manager_node\" || { printf 'TRAEFIK_SWARM_NODE_ID_REQUIRED\\n'; exit 4; }",
		"mkdir -p " + shellQuote(traefikRemoteRoot+"/dynamic") + " " + shellQuote(traefikRemoteRoot+"/certificates") + " " + shellQuote(traefikRemoteRoot+"/acme"),
		"touch " + shellQuote(traefikRemoteRoot+"/acme/acme.json"),
		"chmod 600 " + shellQuote(traefikRemoteRoot+"/acme/acme.json"),
		"network_spec=$(docker network inspect " + network + " --format '{{.Driver}}|{{.Scope}}' 2>/dev/null || true)",
		"if [ -z \"$network_spec\" ]; then docker network create --driver overlay --attachable " + network + "; elif [ \"$network_spec\" != 'overlay|swarm' ]; then printf 'TRAEFIK_SWARM_NETWORK_INVALID\\n'; exit 5; fi",
		"resolved_image=''",
		"if docker image inspect " + image + " >/dev/null 2>&1; then resolved_image=" + image + "; fi",
		"pull_traefik_image() {",
		"  candidate=\"$1\"",
		"  attempt=1",
		"  while [ \"$attempt\" -le 2 ]; do",
		"    printf 'Pulling Traefik image %s (attempt %s/2)\\n' \"$candidate\" \"$attempt\"",
		"    if command -v timeout >/dev/null 2>&1; then timeout 90 docker pull \"$candidate\" && return 0; else docker pull \"$candidate\" && return 0; fi",
		"    attempt=$((attempt + 1))",
		"    sleep 3",
		"  done",
		"  return 1",
		"}",
		"if [ -z \"$resolved_image\" ]; then",
		"  for candidate in " + candidates + "; do",
		"    if pull_traefik_image \"$candidate\"; then resolved_image=\"$candidate\"; break; fi",
		"  done",
		"fi",
		"if [ -z \"$resolved_image\" ]; then printf '%s\\n' " + pullFailureMessage + "; exit 1; fi",
		"if [ \"$resolved_image\" != " + image + " ]; then docker tag \"$resolved_image\" " + image + "; fi",
		"docker service inspect " + service + " >/dev/null 2>&1 && docker service rm " + service + " >/dev/null 2>&1 || true",
		"for attempt in $(seq 1 30); do docker service inspect " + service + " >/dev/null 2>&1 || break; sleep 1; done",
		"docker service inspect " + service + " >/dev/null 2>&1 && { printf 'TRAEFIK_SERVICE_REMOVE_TIMEOUT\\n'; exit 6; } || true",
		"docker rm -f " + service + " >/dev/null 2>&1 || true",
		"docker service create --name " + service +
			" --replicas 1" +
			" --constraint node.role==manager" +
			" --constraint \"node.id==$manager_node\"" +
			" --network " + network +
			" --publish " + shellQuote("mode=host,published="+strconv.Itoa(input.HTTPPort)+",target=80") +
			" --publish " + shellQuote("mode=host,published="+strconv.Itoa(input.HTTPSPort)+",target=443") +
			" --label " + shellQuote("iquops.owner=iquops") +
			" --label " + shellQuote("iquops.component=traefik_gateway") +
			" --label " + shellQuote("iquops.lifecycle=managed") +
			" --mount " + shellQuote("type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock,readonly") +
			" --mount " + shellQuote("type=bind,source="+traefikRemoteRoot+"/dynamic,target="+traefikRemoteRoot+"/dynamic") +
			" --mount " + shellQuote("type=bind,source="+traefikRemoteRoot+"/certificates,target=/certificates") +
			" --mount " + shellQuote("type=bind,source="+traefikRemoteRoot+"/acme,target="+traefikRemoteRoot+"/acme") +
			" " + image +
			" --providers.swarm=true" +
			" --providers.swarm.endpoint=unix:///var/run/docker.sock" +
			" --providers.swarm.exposedbydefault=false" +
			" " + shellQuote("--providers.swarm.network="+input.Network) +
			" --providers.swarm.constraints=" + shellQuote("Label(`iquops.owner`,`iquops`)") +
			" --providers.file.directory=" + traefikRemoteRoot + "/dynamic" +
			" --providers.file.watch=true" +
			" --entrypoints.web.address=:80" +
			" --entrypoints.websecure.address=:443" +
			" " + shellQuote(resolver+"storage="+traefikRemoteRoot+"/acme/acme.json") +
			" " + shellQuote(resolver+"httpchallenge.entrypoint=web") +
			" --api.dashboard=true",
		"docker service inspect " + service + " >/dev/null",
		"for attempt in $(seq 1 45); do replicas=$(docker service ls --filter name=" + service + " --format '{{.Name}}|{{.Replicas}}' | awk -F'|' -v expected=\"$service_name\" '$1 == expected { print $2 }'); test \"$replicas\" = \"1/1\" && break; sleep 2; done",
		"test \"$replicas\" = \"1/1\"",
		"printf 'deployed'",
	}, "\n")
}

func stableTraefikExecutionError(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	for _, code := range []string{
		"TRAEFIK_SWARM_REQUIRED",
		"TRAEFIK_SWARM_MANAGER_REQUIRED",
		"TRAEFIK_SWARM_NODE_ID_REQUIRED",
		"TRAEFIK_SWARM_NETWORK_INVALID",
		"TRAEFIK_SERVICE_REMOVE_TIMEOUT",
	} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return fallback
}

func traefikUninstallScript(service string) string {
	quoted := shellQuote(service)
	return strings.Join([]string{
		"set -e",
		"if docker service inspect " + quoted + " >/dev/null 2>&1; then docker service rm " + quoted + " >/dev/null; fi",
		"docker rm -f " + quoted + " >/dev/null 2>&1 || true",
		"rm -rf " + shellQuote(traefikRemoteRoot),
		"if docker service inspect " + quoted + " >/dev/null 2>&1; then printf 'Traefik service still exists\\n'; exit 1; fi",
		"if docker inspect " + quoted + " >/dev/null 2>&1; then printf 'Traefik container still exists\\n'; exit 1; fi",
		"printf 'removed'",
	}, "\n")
}

func shellQuotedWords(values []string) string {
	quoted := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		quoted = append(quoted, shellQuote(value))
	}
	return strings.Join(quoted, " ")
}

func withTraefikLogStep(input TraefikRuntimeExecutionInput) TraefikRuntimeExecutionInput {
	if input.LogStep == nil {
		input.LogStep = func(string, string, string, string) {}
	}
	if input.Service == "" {
		input.Service = "iquops-traefik"
	}
	if input.Image == "" {
		input.Image = "traefik:v3.7"
	}
	if len(input.ImagePullCandidates) == 0 {
		input.ImagePullCandidates = []string{input.Image}
	}
	if input.Network == "" {
		input.Network = "iquops-public"
	}
	if input.HTTPPort <= 0 {
		input.HTTPPort = 80
	}
	if input.HTTPSPort <= 0 {
		input.HTTPSPort = 443
	}
	return input
}
