package application

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

const managedFrpsImagePrefix = "iquops/frps:node-"

var frpsResourceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type managedFrps struct {
	ID          string
	Service     string
	Installed   bool
	Replicas    int
	RunID       string
	Image       string
	labels      map[string]string
	constraints []string
}

func managedFrpsServiceName(nodeID string) string { return "iquops-frps-node-" + nodeID }

func inspectManagedFrps(ctx context.Context, client sshSessionClient, nodeID string) (managedFrps, error) {
	return probeManagedFrps(func(command string) (string, error) { return remoteOutput(ctx, client, command) }, nodeID)
}

func probeManagedFrps(run func(string) (string, error), nodeID string) (managedFrps, error) {
	if !frpsResourceIDPattern.MatchString(nodeID) {
		return managedFrps{}, errors.New("FRP_SWARM_MANAGER_REQUIRED")
	}
	name := managedFrpsServiceName(nodeID)
	services, err := listFrpsServices(run, "name="+name)
	if err != nil {
		return managedFrps{}, err
	}
	for _, service := range services {
		if service.Service != name {
			continue
		}
		verified, err := readFrpsService(run, service)
		if err != nil {
			return managedFrps{}, err
		}
		if !verified.ownedByNode(nodeID) || verified.labels["iquops.nodeId"] != nodeID || verified.labels["iquops.lifecycle"] != "persistent" {
			return managedFrps{}, errors.New("FRPS_SERVICE_OWNERSHIP_MISMATCH")
		}
		verified.Installed = true
		return verified, nil
	}
	return managedFrps{}, nil
}

func (s managedFrps) ownedByNode(nodeID string) bool {
	for _, constraint := range s.constraints {
		if strings.ReplaceAll(constraint, " ", "") == "node.id=="+nodeID {
			return s.labels["iquops.owner"] == "iquops" && s.labels["iquops.component"] == "frps"
		}
	}
	return false
}

func listFrpsServices(run func(string) (string, error), filters ...string) ([]managedFrps, error) {
	command := "docker service ls --no-trunc --format '{{.ID}}|{{.Name}}'"
	for _, filter := range filters {
		command += " --filter " + shellQuote(filter)
	}
	output, err := run(command)
	if err != nil {
		return nil, errors.New("FRPS_SERVICE_PROBE_FAILED")
	}
	var services []managedFrps
	for _, line := range nonEmptyLines(output) {
		id, name, ok := strings.Cut(line, "|")
		if !ok || !frpsResourceIDPattern.MatchString(id) || name == "" {
			return nil, errors.New("FRPS_SERVICE_PROBE_FAILED")
		}
		services = append(services, managedFrps{ID: id, Service: name})
	}
	return services, nil
}

func readFrpsService(run func(string) (string, error), service managedFrps) (managedFrps, error) {
	const format = `{{json .Spec.Labels}}{{println}}{{json .Spec.TaskTemplate.Placement.Constraints}}{{println}}{{json .Spec.Mode.Replicated}}{{println}}{{.Spec.TaskTemplate.ContainerSpec.Image}}`
	output, err := run("docker service inspect --format " + shellQuote(format) + " " + shellQuote(service.ID))
	parts := strings.Split(output, "\n")
	var replicaSpec *struct{ Replicas int }
	if err != nil || len(parts) != 4 || json.Unmarshal([]byte(parts[0]), &service.labels) != nil ||
		json.Unmarshal([]byte(parts[1]), &service.constraints) != nil || json.Unmarshal([]byte(parts[2]), &replicaSpec) != nil ||
		replicaSpec == nil || parts[3] == "" {
		return managedFrps{}, errors.New("FRPS_SERVICE_PROBE_FAILED")
	}
	service.Replicas = replicaSpec.Replicas
	service.Image = strings.TrimSpace(parts[3])
	service.RunID = service.labels["iquops.tunnelRunId"]
	return service, nil
}

func inspectLegacyFrps(ctx context.Context, client sshSessionClient, nodeID string) ([]managedFrps, error) {
	run := func(command string) (string, error) { return remoteOutput(ctx, client, command) }
	if !frpsResourceIDPattern.MatchString(nodeID) {
		return nil, errors.New("FRP_SWARM_MANAGER_REQUIRED")
	}
	services, err := listFrpsServices(run, "label=iquops.lifecycle=temporary", "label=iquops.owner=iquops", "label=iquops.component=frps")
	if err != nil {
		return nil, err
	}
	legacy := []managedFrps{}
	for _, service := range services {
		if !strings.HasPrefix(service.Service, "iquops-frps-") || service.Service == managedFrpsServiceName(nodeID) {
			continue
		}
		verified, err := readFrpsService(run, service)
		if err != nil {
			return nil, err
		}
		if verified.ownedByNode(nodeID) && verified.labels["iquops.lifecycle"] == "temporary" &&
			frpsResourceIDPattern.MatchString(verified.RunID) && verified.Service == frpTunnelContainerName(verified.RunID) {
			legacy = append(legacy, verified)
		}
	}
	return legacy, nil
}

func applyRemoteFrpsProbe(ctx context.Context, client sshSessionClient, result domain.Object) {
	metrics := objectMapFromAny(result["metrics"])
	if metrics["swarmRole"] == "manager" && metrics["swarmControlAvailable"] == true {
		applyRemoteManagedFrpsProbe(ctx, client, result, stringValue(metrics["swarmNodeId"]))
	}
	if metrics["frpsPresent"] != true && externalFrpsPresent(ctx, client) {
		setMetric(result, "frpsPresent", true)
		setMetric(result, "frpsExternal", true)
	}
	if metrics["frpsStatus"] == "unknown" {
		code := "FRP_SWARM_MANAGER_REQUIRED"
		if metrics["docker"] != true {
			code = "FRPS_DOCKER_REQUIRED"
		}
		appendServerRuntimeCheck(result, "frps", serverRuntimeCheckSkipped, code)
	}
}

func applyRemoteManagedFrpsProbe(ctx context.Context, client sshSessionClient, result domain.Object, nodeID string) {
	service, err := inspectManagedFrps(ctx, client, nodeID)
	legacy, legacyErr := inspectLegacyFrps(ctx, client, nodeID)
	if service.Installed || len(legacy) > 0 {
		setMetric(result, "frpsPresent", true)
		setMetric(result, "frpsManagedPresent", true)
	}
	if service.Installed {
		status := "installed"
		if service.Replicas > 0 {
			status = "running"
		}
		setMetric(result, "frps", true)
		setMetric(result, "frpsStatus", status)
		setMetric(result, "frpsImage", service.Image)
		setMetric(result, "frpsService", service.Service)
		appendServerRuntimeCheck(result, "frps", serverRuntimeCheckSuccess, "FRPS 已安装")
		return
	}
	if err != nil || legacyErr != nil {
		setMetric(result, "frpsStatus", "unavailable")
		code := "FRPS_SERVICE_PROBE_FAILED"
		if err != nil {
			code = stableErrorCode(err, code)
		}
		setMetric(result, "frpsProbeError", code)
		appendServerRuntimeCheck(result, "frps", serverRuntimeCheckWarning, code)
		return
	}
	setMetric(result, "frpsStatus", "missing")
	appendServerRuntimeCheck(result, "frps", serverRuntimeCheckSkipped, "FRPS_NOT_INSTALLED")
}

func externalFrpsPresent(ctx context.Context, client sshSessionClient) bool {
	const command = `if command -v frps >/dev/null 2>&1 || pgrep -x frps >/dev/null 2>&1; then printf present; elif command -v systemctl >/dev/null 2>&1; then case "$(systemctl show -p LoadState --value frps.service 2>/dev/null)" in loaded|masked) printf present;; esac; fi`
	output, err := remoteOutput(ctx, client, command)
	return err == nil && output == "present"
}

type FrpsRuntimeExecutor interface {
	InstallFrps(ctx context.Context, input ServerRuntimeExecutionInput, binaryPath, image string) error
	UninstallFrps(ctx context.Context, input ServerRuntimeExecutionInput) error
}

func (e *SystemServerRuntimeExecutor) InstallFrps(ctx context.Context, input ServerRuntimeExecutionInput, binaryPath, image string) error {
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	nodeID, err := frpSwarmManagerNodeID(ctx, client)
	if err != nil {
		return err
	}
	current, err := inspectManagedFrps(ctx, client, nodeID)
	if err != nil || current.Installed {
		return err
	}
	input.LogStep("swarm", "prepare-frps-image", "info", "准备 FRPS 镜像")
	if strings.TrimSpace(binaryPath) != "" {
		image = managedFrpsImagePrefix + nodeID
		if err := buildManagedFrpsImage(ctx, client, nodeID, binaryPath, image); err != nil {
			return err
		}
	} else {
		image = strings.TrimSpace(image)
		if image == "" {
			return errors.New("FRPS_IMAGE_REQUIRED")
		}
		registry, _, hasRepository := strings.Cut(image, "/")
		if !hasRepository || (!strings.ContainsAny(registry, ".:") && registry != "localhost") || registry == "docker.io" {
			if err := ensureDockerRegistryMirror(ctx, client, input.Server, input.LogStep); err != nil {
				return err
			}
		}
		if err := remoteRun(ctx, client, "docker pull "+shellQuote(image)); err != nil {
			return errors.New("FRPS_IMAGE_PULL_FAILED")
		}
	}
	input.LogStep("swarm", "install-frps", "info", "创建待机的受管 FRPS 服务")
	if err := remoteRun(ctx, client, managedFrpsCreateCommand(nodeID, image)); err != nil {
		return errors.New("FRPS_SWARM_SERVICE_CREATE_FAILED")
	}
	return nil
}

func managedFrpsCreateCommand(nodeID string, image string) string {
	return strings.Join([]string{
		"docker service create --replicas 0 --restart-condition none --no-resolve-image",
		"--name " + shellQuote(managedFrpsServiceName(nodeID)),
		"--constraint " + shellQuote("node.id=="+nodeID),
		"--label iquops.owner=iquops --label iquops.component=frps --label iquops.lifecycle=persistent",
		"--label " + shellQuote("iquops.nodeId="+nodeID),
		"--label traefik.enable=false",
		shellQuote(image),
	}, " ")
}

func buildManagedFrpsImage(ctx context.Context, client *ssh.Client, nodeID, binaryPath, image string) error {
	path, err := frpsBinaryForRemote(ctx, client, binaryPath)
	if err != nil {
		return errors.New(stableErrorCode(err, "FRPS_BINARY_NOT_FOUND"))
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		return errors.New("FRPS_BINARY_READ_FAILED")
	}
	buildDir := "/opt/iquops/frps/" + nodeID + "/build"
	if err := remoteRun(ctx, client, "install -d -m 700 "+shellQuote(buildDir)); err != nil {
		return errors.New("FRPS_IMAGE_BUILD_FAILED")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = remoteRun(cleanup, client, "rm -rf "+shellQuote(buildDir))
	}()
	if err := writeRemoteFileBytes(ctx, client, buildDir+"/frps", binary); err != nil {
		return errors.New("FRPS_BINARY_UPLOAD_FAILED")
	}
	if err := writeRemoteFile(ctx, client, buildDir+"/Dockerfile", frpsDockerfile()); err != nil {
		return errors.New("FRPS_IMAGE_BUILD_FAILED")
	}
	if err := writeRemoteFile(ctx, client, buildDir+"/.dockerignore", frpsDockerignore()); err != nil {
		return errors.New("FRPS_IMAGE_BUILD_FAILED")
	}
	if err := remoteRun(ctx, client, "chmod 755 "+shellQuote(buildDir+"/frps")+" && docker build -t "+shellQuote(image)+" "+shellQuote(buildDir)); err != nil {
		return errors.New("FRPS_IMAGE_BUILD_FAILED")
	}
	return nil
}

func (e *SystemServerRuntimeExecutor) UninstallFrps(ctx context.Context, input ServerRuntimeExecutionInput) error {
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	nodeID, err := frpSwarmManagerNodeID(ctx, client)
	if err != nil {
		return err
	}
	stable, err := inspectManagedFrps(ctx, client, nodeID)
	if err != nil {
		return err
	}
	legacy, err := inspectLegacyFrps(ctx, client, nodeID)
	if err != nil {
		return err
	}
	input.LogStep("swarm", "uninstall-frps", "info", "卸载该节点的受管 FRPS 服务")
	if stable.Installed {
		if frpsResourceIDPattern.MatchString(stable.RunID) {
			if err := remoteRun(ctx, client, "rm -f -- "+shellQuote(frpTunnelRemoteDir(stable.RunID)+"/token")); err != nil {
				return errors.New("FRPS_TOKEN_REVOCATION_FAILED")
			}
		}
		if err := remoteRun(ctx, client, frpsSwarmServiceRemoveCommand(stable.ID)); err != nil {
			return errors.New("FRPS_SWARM_SERVICE_REMOVE_FAILED")
		}
		_ = remoteRun(ctx, client, "docker image rm "+shellQuote(managedFrpsImagePrefix+nodeID)+" >/dev/null 2>&1 || true")
	}
	for _, service := range legacy {
		if err := remoteRun(ctx, client, frpsSwarmServiceRemoveCommand(service.ID)); err != nil {
			return errors.New("FRPS_SWARM_SERVICE_REMOVE_FAILED")
		}
		_ = remoteRun(ctx, client, "docker image rm "+shellQuote("iquops/frps:"+service.RunID)+" >/dev/null 2>&1 || true")
		if err := remoteRun(ctx, client, "rm -rf "+shellQuote(frpTunnelRemoteDir(service.RunID))); err != nil {
			return errors.New("FRPS_LEGACY_FILES_REMOVE_FAILED")
		}
	}
	return nil
}
