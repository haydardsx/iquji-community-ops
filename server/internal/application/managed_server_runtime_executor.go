package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	jenkinsRemoteRoot       = "/opt/iquops/jenkins"
	jenkinsManagedFolder    = domain.ManagedJenkinsFolder
	jenkinsManagedJob       = domain.ManagedJenkinsJob
	jenkinsManagedComponent = "jenkins"
)

type remoteSwarmInfo struct {
	NodeID           string `json:"NodeID"`
	NodeAddr         string `json:"NodeAddr"`
	LocalNodeState   string `json:"LocalNodeState"`
	ControlAvailable bool   `json:"ControlAvailable"`
	Error            string `json:"Error"`
	RemoteManagers   []struct {
		NodeID string `json:"NodeID"`
		Addr   string `json:"Addr"`
	} `json:"RemoteManagers"`
	Cluster *struct {
		ID string `json:"ID"`
	} `json:"Cluster"`
}

type remoteSwarmStatus struct {
	Active           bool
	Status           string
	Role             string
	ControlAvailable bool
	NodeID           string
	ClusterID        string
	AdvertiseAddr    string
	ManagerAddress   string
	FailureCode      string
	NetworkReady     bool
}

type jenkinsBundleFile struct {
	RelativePath string
	Content      []byte
}

func readRemoteSwarmStatus(ctx context.Context, client sshSessionClient, networkName string) (remoteSwarmStatus, error) {
	output, err := remoteOutput(ctx, client, "docker info --format '{{json .Swarm}}'")
	if err != nil {
		return remoteSwarmStatus{}, err
	}
	var info remoteSwarmInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return remoteSwarmStatus{}, fmt.Errorf("SWARM_INFO_INVALID: %w", err)
	}
	status := remoteSwarmStatus{
		Active:           strings.EqualFold(strings.TrimSpace(info.LocalNodeState), "active"),
		Status:           strings.ToLower(strings.TrimSpace(info.LocalNodeState)),
		Role:             "none",
		ControlAvailable: info.ControlAvailable,
		NodeID:           strings.TrimSpace(info.NodeID),
		AdvertiseAddr:    strings.TrimSpace(info.NodeAddr),
	}
	if strings.TrimSpace(info.Error) != "" {
		status.FailureCode = swarmJoinFailureCode(info.Error)
	}
	for _, manager := range info.RemoteManagers {
		if manager.NodeID == status.NodeID {
			status.ManagerAddress, _ = normalizeSwarmManagerEndpoint(manager.Addr)
			break
		}
	}
	if status.ManagerAddress == "" && !status.ControlAvailable && len(info.RemoteManagers) > 0 {
		status.ManagerAddress, _ = normalizeSwarmManagerEndpoint(info.RemoteManagers[0].Addr)
	}
	if status.ManagerAddress != "" && status.ControlAvailable {
		status.AdvertiseAddr, _, _ = net.SplitHostPort(status.ManagerAddress)
	}
	if status.Status == "" {
		status.Status = "unknown"
	}
	if info.Cluster != nil {
		status.ClusterID = strings.TrimSpace(info.Cluster.ID)
	}
	if status.Active {
		if status.ControlAvailable {
			status.Role = "manager"
			// 初始化后的 RemoteManagers 可能暂存旧地址，当前节点记录才是实际通告端点。
			status.ManagerAddress, _ = readSwarmManagerEndpoint(ctx, client, status.NodeID)
			if ctx.Err() != nil {
				return remoteSwarmStatus{}, ctx.Err()
			}
			status.AdvertiseAddr, _, _ = net.SplitHostPort(status.ManagerAddress)
		} else {
			status.Role = "worker"
			status.AdvertiseAddr = ""
		}
	}
	if status.ControlAvailable && strings.TrimSpace(networkName) != "" {
		networkOutput, networkErr := remoteOutput(ctx, client, "docker network inspect "+shellQuote(networkName)+" --format '{{.Driver}}|{{.Scope}}'")
		status.NetworkReady = networkErr == nil && managedOverlayNetworkReady(networkOutput)
	}
	return status, nil
}

func managedOverlayNetworkReady(output string) bool {
	driver, scope, ok := strings.Cut(strings.TrimSpace(output), "|")
	return ok && strings.TrimSpace(driver) == "overlay" && strings.TrimSpace(scope) == "swarm"
}

func applyRemoteJenkinsProbe(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, result domain.Object) {
	input = withManagedRuntimeDefaults(input)
	container := shellQuote(input.JenkinsContainer)
	names, err := remoteOutput(ctx, client, "docker container ls -a --format '{{.Names}}'")
	if err != nil {
		setMetric(result, "jenkinsStatus", "unavailable")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckDanger, "JENKINS_PROBE_FAILED")
		return
	}
	if !containsString(nonEmptyLines(names), input.JenkinsContainer) {
		setMetric(result, "jenkins", false)
		setMetric(result, "jenkinsStatus", "missing")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckSkipped, "JENKINS_NOT_INSTALLED")
		return
	}
	format := `{{.State.Running}}|{{.Config.Image}}|{{index .Config.Labels "iquops.owner"}}|{{index .Config.Labels "iquops.component"}}|{{index .Config.Labels "iquops.bundle-version"}}`
	output, err := remoteOutput(ctx, client, "docker inspect -f "+shellQuote(format)+" "+container+" 2>/dev/null")
	if err != nil {
		setMetric(result, "jenkins", false)
		setMetric(result, "jenkinsStatus", "unavailable")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckDanger, "JENKINS_PROBE_FAILED")
		return
	}
	parts := strings.SplitN(output, "|", 5)
	if len(parts) != 5 || strings.TrimSpace(parts[2]) != "iquops" || strings.TrimSpace(parts[3]) != jenkinsManagedComponent {
		setMetric(result, "jenkins", false)
		setMetric(result, "jenkinsStatus", "unavailable")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckDanger, "JENKINS_MANAGED_CONTAINER_INVALID")
		return
	}
	setMetric(result, "jenkinsImage", strings.TrimSpace(parts[1]))
	setMetric(result, "jenkinsContainer", input.JenkinsContainer)
	setMetric(result, "jenkinsBundleVersion", strings.TrimSpace(parts[4]))
	setMetric(result, "jenkinsPort", input.JenkinsPort)
	if strings.TrimSpace(parts[0]) != "true" || !remoteJenkinsReady(ctx, client, input.JenkinsContainer) {
		setMetric(result, "jenkins", false)
		setMetric(result, "jenkinsStatus", "unavailable")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckDanger, "JENKINS_HEALTHCHECK_FAILED")
		return
	}
	version, _ := remoteOutput(ctx, client, "docker exec "+container+" java -jar /usr/share/jenkins/jenkins.war --version 2>/dev/null")
	if version != "" {
		setMetric(result, "jenkinsVersion", lastNonEmptyLine(version))
	}
	setMetric(result, "jenkins", true)
	setMetric(result, "jenkinsStatus", "running")
	appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckSuccess, "Managed Jenkins is ready")
}

func remoteHTTPReady(ctx context.Context, client sshSessionClient, port int, path string) bool {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + path
	command := "if command -v curl >/dev/null 2>&1; then curl -fsS --max-time 5 " + shellQuote(url) + " >/dev/null; else wget -q -T 5 -O /dev/null " + shellQuote(url) + "; fi"
	_, err := remoteOutput(ctx, client, command)
	return err == nil
}

func (e *SystemServerRuntimeExecutor) InstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withManagedRuntimeDefaults(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	if _, err := remoteOutput(ctx, client, "docker version --format '{{.Server.Version}}'"); err != nil {
		return nil, swarmOperationError(ctx, err, "SWARM_DOCKER_REQUIRED")
	}

	current, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil {
		return nil, swarmOperationError(ctx, err, "SWARM_PROBE_FAILED")
	}
	input.LogStep("swarm", "install", "info", "正在配置 Docker Swarm "+input.SwarmMode)
	if input.SwarmMode == "worker" {
		if err := installSwarmWorker(ctx, client, input, current); err != nil {
			return nil, err
		}
	} else if err := installSwarmManager(ctx, client, input, current); err != nil {
		return nil, err
	}

	status, err := readRemoteSwarmStatus(ctx, client, input.TraefikNetwork)
	if err != nil || !status.Active || status.Role != input.SwarmMode || status.NodeID == "" {
		return nil, swarmOperationError(ctx, err, "SWARM_POST_INSTALL_PROBE_FAILED")
	}
	if input.SwarmMode == "manager" {
		if status.ClusterID == "" {
			return nil, errors.New("SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE")
		}
		if !status.NetworkReady {
			return nil, errors.New("SWARM_OVERLAY_NETWORK_FAILED")
		}
	}
	if input.SwarmMode == "worker" {
		managerClient, managerErr := sshClient(ctx, input.SwarmManagerServer, input.SwarmManagerSSHKey)
		if managerErr != nil {
			return nil, swarmOperationError(ctx, managerErr, "SWARM_MANAGER_SSH_UNAVAILABLE")
		}
		managerStatus, managerStatusErr := readRemoteSwarmStatus(ctx, managerClient, input.TraefikNetwork)
		if managerStatusErr == nil {
			status.ClusterID, managerStatusErr = verifiedSwarmWorkerCluster(ctx, managerClient, status, managerStatus)
			if managerStatusErr == nil {
				if address, addressErr := readSwarmWorkerAddress(ctx, managerClient, status.NodeID); addressErr == nil {
					status.AdvertiseAddr = address
				}
				status.ManagerAddress = managerStatus.ManagerAddress
			}
		}
		_ = managerClient.Close()
		if managerStatusErr != nil {
			return nil, managerStatusErr
		}
	}
	return swarmRuntimeResult(input, status, true), nil
}

func installSwarmManager(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, current remoteSwarmStatus) error {
	initialized := false
	if current.Active {
		if current.Role != "manager" {
			return errors.New("SWARM_ROLE_CONFLICT")
		}
		if input.SwarmConnectionMode != "" || input.SwarmAdvertiseAddr != "" {
			endpoint, err := readSwarmManagerEndpoint(ctx, client, current.NodeID)
			if err != nil {
				return err
			}
			current.ManagerAddress = endpoint
			if err := verifyRequestedSwarmConnection(input, current); err != nil {
				return err
			}
		}
	} else {
		advertiseAddr, err := swarmAdvertiseAddress(input)
		if err != nil {
			return err
		}
		command := "docker swarm init --advertise-addr " + shellQuote(advertiseAddr)
		if input.SwarmConnectionMode != "" {
			command += " --data-path-addr " + shellQuote(advertiseAddr)
		}
		if _, err := remoteOutput(ctx, client, command); err != nil {
			return errors.New("SWARM_MANAGER_INIT_FAILED")
		}
		initialized = true
	}
	manager, err := readRemoteSwarmStatus(ctx, client, "")
	if err != nil || !manager.Active || manager.Role != "manager" || manager.NodeID == "" {
		return rollbackNewSwarmManager(ctx, client, input, initialized, errors.New("SWARM_MANAGER_POST_INIT_PROBE_FAILED"))
	}
	serverID := stringValue(input.Server["id"])
	if serverID != "" {
		if _, err := remoteOutput(ctx, client, swarmNodeLabelCommand(serverID, manager.NodeID)); err != nil {
			return rollbackNewSwarmManager(ctx, client, input, initialized, errors.New("SWARM_NODE_LABEL_FAILED"))
		}
	}
	network := firstNonEmptyString(input.TraefikNetwork, "iquops-public")
	if err := ensureManagedSwarmOverlayNetwork(ctx, client, network); err != nil {
		return rollbackNewSwarmManager(ctx, client, input, initialized, err)
	}
	verified, err := readRemoteSwarmStatus(ctx, client, network)
	if err != nil || !verified.Active || verified.Role != "manager" || verified.NodeID == "" {
		return rollbackNewSwarmManager(ctx, client, input, initialized, errors.New("SWARM_POST_INSTALL_PROBE_FAILED"))
	}
	if verified.ClusterID == "" {
		return rollbackNewSwarmManager(ctx, client, input, initialized, errors.New("SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE"))
	}
	if !verified.NetworkReady {
		return rollbackNewSwarmManager(ctx, client, input, initialized, errors.New("SWARM_OVERLAY_NETWORK_FAILED"))
	}
	return nil
}

func swarmNodeLabelCommand(serverID string, nodeID string) string {
	return "docker node update --label-add " + shellQuote("iquops.server-id="+serverID) + " " + shellQuote(nodeID)
}

func rollbackNewSwarmManager(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, initialized bool, cause error) error {
	if !initialized {
		return cause
	}
	input.LogStep("swarm", "rollback", "warn", "rolling back incomplete Docker Swarm manager initialization")
	if _, err := remoteOutput(ctx, client, "docker swarm leave --force"); err != nil {
		input.LogStep("swarm", "rollback", "error", "SWARM_MANAGER_ROLLBACK_FAILED")
		return errors.New("SWARM_MANAGER_ROLLBACK_FAILED")
	}
	return cause
}

func ensureManagedSwarmOverlayNetwork(ctx context.Context, client sshSessionClient, network string) error {
	output, err := remoteOutput(ctx, client, "docker network inspect "+shellQuote(network)+" --format '{{.Driver}}|{{.Scope}}' 2>/dev/null")
	if err == nil {
		if strings.TrimSpace(output) != "overlay|swarm" {
			return errors.New("SWARM_NETWORK_CONFLICT")
		}
		return nil
	}
	if _, err := remoteOutput(ctx, client, swarmOverlayNetworkEnsureCommand(network)); err != nil {
		return errors.New("SWARM_OVERLAY_NETWORK_FAILED")
	}
	return nil
}

func swarmOverlayNetworkEnsureCommand(network string) string {
	quotedNetwork := shellQuote(network)
	return "network_spec=$(docker network inspect " + quotedNetwork + " --format '{{.Driver}}|{{.Scope}}' 2>/dev/null || true)" +
		"; if [ -z \"$network_spec\" ]; then docker network create --driver overlay --attachable --label " + shellQuote("iquops.owner=iquops") + " --label " + shellQuote("iquops.component=swarm_network") + " " + quotedNetwork +
		"; elif [ \"$network_spec\" != 'overlay|swarm' ]; then exit 1; fi"
}

func verifiedSwarmWorkerCluster(ctx context.Context, managerClient sshSessionClient, worker remoteSwarmStatus, manager remoteSwarmStatus) (string, error) {
	if !manager.Active || manager.Role != "manager" {
		return "", errors.New("SWARM_MANAGER_NOT_READY")
	}
	if err := validateSwarmClusterIDs(worker.ClusterID, manager.ClusterID); err != nil {
		return "", err
	}
	if worker.NodeID == "" {
		return "", errors.New("SWARM_WORKER_IDENTITY_REQUIRED")
	}
	output, err := remoteOutput(ctx, managerClient, "docker node ls -q")
	if err != nil {
		return "", swarmOperationError(ctx, err, "SWARM_CLUSTER_UNVERIFIED")
	}
	if !containsString(nonEmptyLines(output), worker.NodeID) {
		return "", errors.New("SWARM_CLUSTER_MISMATCH")
	}
	return manager.ClusterID, nil
}

func validateSwarmClusterIDs(workerClusterID string, managerClusterID string) error {
	if strings.TrimSpace(managerClusterID) == "" {
		return errors.New("SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE")
	}
	workerClusterID = strings.TrimSpace(workerClusterID)
	if workerClusterID != "" && workerClusterID != strings.TrimSpace(managerClusterID) {
		return errors.New("SWARM_CLUSTER_MISMATCH")
	}
	return nil
}

func stableSwarmProbeError(err error) string {
	if err == nil {
		return "SWARM_CLUSTER_UNVERIFIED"
	}
	for _, code := range []string{
		"SWARM_MANAGER_NOT_READY",
		"SWARM_MANAGER_CLUSTER_ID_UNAVAILABLE",
		"SWARM_CLUSTER_MISMATCH",
		"SWARM_WORKER_IDENTITY_REQUIRED",
	} {
		if strings.Contains(err.Error(), code) {
			return code
		}
	}
	return "SWARM_CLUSTER_UNVERIFIED"
}

func labelSwarmWorker(ctx context.Context, managerClient sshSessionClient, input ServerRuntimeExecutionInput, nodeID string) error {
	serverID := stringValue(input.Server["id"])
	if serverID == "" || strings.TrimSpace(nodeID) == "" {
		return errors.New("SWARM_WORKER_IDENTITY_REQUIRED")
	}
	command := swarmNodeLabelCommand(serverID, nodeID)
	if _, err := remoteOutput(ctx, managerClient, command); err != nil {
		return swarmOperationError(ctx, err, "SWARM_NODE_LABEL_FAILED")
	}
	return nil
}
