package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

const (
	serverRuntimeCheckSuccess = "success"
	serverRuntimeCheckDanger  = "danger"
	serverRuntimeCheckWarning = "warning"
	serverRuntimeCheckSkipped = "skipped"
)

type ServerRuntimeExecutor interface {
	ProbeServerRuntime(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	InstallDockerEngine(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	UninstallDockerEngine(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	InstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	UninstallDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	ReconfigureDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	RebuildDockerSwarm(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	InstallJenkins(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	UninstallJenkins(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	InstallMonitoringAgent(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
	UninstallMonitoringAgent(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error)
}

type ServerRuntimeExecutionInput struct {
	Server                        domain.Object
	SSHKey                        string
	AgentBundleDir                string
	RepositoryRoot                string
	SwarmMode                     string
	SwarmAdvertiseAddr            string
	SwarmConnectionMode           string
	SwarmConnectionCheckpoint     domain.Object
	SwarmConnectionRestoreOnly    bool
	SaveSwarmConnectionCheckpoint func(context.Context, domain.Object) error
	SwarmRebuildID                string
	SwarmRebuildCheckpoint        domain.Object
	SwarmRebuildRestoreOnly       bool
	SaveSwarmRebuildCheckpoint    func(context.Context, domain.Object) error
	SaveSwarmDeparture            func(context.Context, domain.Object) error
	SwarmManagerServer            domain.Object
	SwarmManagerSSHKey            string
	SwarmManagerServerID          string
	TraefikService                string
	TraefikImage                  string
	TraefikNetwork                string
	TraefikHTTPPort               int
	TraefikHTTPSPort              int
	JenkinsBundleDir              string
	JenkinsPluginsArchive         string
	JenkinsBundleVersion          string
	JenkinsImage                  string
	JenkinsContainer              string
	JenkinsPort                   int
	LogStep                       func(source string, phase string, level string, message string)
}

type SystemServerRuntimeExecutor struct{}

func NewSystemServerRuntimeExecutor() *SystemServerRuntimeExecutor {
	return &SystemServerRuntimeExecutor{}
}

func (e *SystemServerRuntimeExecutor) ProbeServerRuntime(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withServerRuntimeLogStep(input)
	result := domain.Object{
		"serverId":  stringValue(input.Server["id"]),
		"checkedAt": domain.Now(),
		"checks":    []domain.Object{},
		"metrics": domain.Object{
			"ssh":                false,
			"docker":             false,
			"dockerStatus":       "unknown",
			"swarm":              false,
			"swarmStatus":        "unknown",
			"traefik":            false,
			"traefikStatus":      "unknown",
			"frps":               false,
			"frpsStatus":         "unknown",
			"frpsPresent":        false,
			"frpsManagedPresent": false,
			"frpsExternal":       false,
			"frpsProbeError":     "",
			"jenkins":            false,
			"jenkinsStatus":      "unknown",
			"agent":              false,
		},
		"remote": domain.Object{},
	}
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		appendServerRuntimeCheck(result, "ssh", serverRuntimeCheckDanger, stableErrorCode(err, "SSH_UNAVAILABLE"))
		return result, nil
	}
	defer func() {
		_ = client.Close()
	}()
	setMetric(result, "ssh", true)
	appendServerRuntimeCheck(result, "ssh", serverRuntimeCheckSuccess, "SSH reachable")

	remote := objectMapFromAny(result["remote"])
	if profile, summary, err := remotePlatformProfile(ctx, client); err == nil {
		remote["platformProfile"] = profile
		appendServerRuntimeCheck(result, "platform", serverRuntimeCheckSuccess, summary)
	} else {
		appendServerRuntimeCheck(result, "platform", serverRuntimeCheckWarning, stableErrorCode(err, "REMOTE_PLATFORM_PROFILE_UNAVAILABLE"))
	}
	if dockerVersion, err := remoteOutput(ctx, client, "docker version --format '{{.Server.Version}}'"); err == nil && dockerVersion != "" {
		setMetric(result, "docker", true)
		setMetric(result, "dockerVersion", dockerVersion)
		setMetric(result, "dockerStatus", "ready")
		appendServerRuntimeCheck(result, "docker", serverRuntimeCheckSuccess, "Docker available")
		applyRemoteDockerRegistryMirrorProbe(ctx, client, input.Server, result)
		applyRemoteSwarmProbe(ctx, client, input, result)
		applyRemoteTraefikProbe(ctx, client, input, result)
		applyRemoteJenkinsProbe(ctx, client, input, result)
	} else {
		setMetric(result, "docker", false)
		setMetric(result, "swarm", false)
		setMetric(result, "swarmStatus", "unknown")
		setMetric(result, "traefik", false)
		setMetric(result, "traefikStatus", "docker_unavailable")
		setMetric(result, "jenkins", false)
		setMetric(result, "jenkinsStatus", "docker_unavailable")
		dockerStatus, code := remoteDockerFailureStatus(ctx, client)
		setMetric(result, "dockerStatus", dockerStatus)
		appendServerRuntimeCheck(result, "docker", serverRuntimeCheckDanger, code)
		appendServerRuntimeCheck(result, "swarm", serverRuntimeCheckSkipped, "SWARM_DOCKER_REQUIRED")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckSkipped, "TRAEFIK_DOCKER_REQUIRED")
		appendServerRuntimeCheck(result, "jenkins", serverRuntimeCheckSkipped, "JENKINS_DOCKER_REQUIRED")
	}
	applyRemoteFrpsProbe(ctx, client, result)
	if agentStatus, err := remoteOutput(ctx, client, "systemctl is-active iquops-monitoring-agent 2>&1 || true"); err == nil && strings.TrimSpace(agentStatus) == "active" {
		setMetric(result, "agent", true)
		appendServerRuntimeCheck(result, "agent", serverRuntimeCheckSuccess, "iquops-monitoring-agent active")
		if report, err := remoteMonitoringAgentReport(ctx, client); err == nil {
			result["monitoringReport"] = report
		}
	} else if report, err := remoteMonitoringAgentReport(ctx, client); err == nil && stringValue(report["serverId"]) == stringValue(input.Server["id"]) {
		setMetric(result, "agent", true)
		appendServerRuntimeCheck(result, "agent", serverRuntimeCheckWarning, "Agent 已报告，systemd 状态待核对")
		result["monitoringReport"] = report
	} else {
		setMetric(result, "agent", false)
		appendServerRuntimeCheck(result, "agent", serverRuntimeCheckDanger, "IQUOPS_MONITORING_AGENT_NOT_ACTIVE")
	}
	result["remote"] = remote
	return result, nil
}

// 守护进程停止或权限不足时仍有安装痕迹，只有远端明确返回缺失才判定未安装。
const dockerPresenceCommand = "if command -v docker >/dev/null || command -v dockerd >/dev/null || [ -S /var/run/docker.sock ] || [ -e /lib/systemd/system/docker.service ] || [ -e /usr/lib/systemd/system/docker.service ] || [ -e /etc/systemd/system/docker.service ]; then printf present; else printf missing; fi"

func remoteDockerFailureStatus(ctx context.Context, client sshSessionClient) (string, string) {
	output, err := remoteOutput(ctx, client, dockerPresenceCommand)
	if err == nil && output == "missing" {
		return "missing", "REMOTE_DOCKER_NOT_INSTALLED"
	}
	return "unavailable", "REMOTE_DOCKER_UNAVAILABLE"
}

func (e *SystemServerRuntimeExecutor) InstallDockerEngine(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	input.LogStep("ssh", "probe-runtime", "info", "正在检查远端软件包与服务管理器")
	if err := remoteRun(ctx, client, "command -v systemctl >/dev/null && (command -v yum >/dev/null || command -v dnf >/dev/null || command -v apt-get >/dev/null)"); err != nil {
		return nil, fmt.Errorf("SERVER_RUNTIME_UNSUPPORTED:systemd_package_manager_required: %w", err)
	}
	input.LogStep("docker", "install", "info", "正在远端服务器安装 Docker Engine")
	version, err := remoteOutput(ctx, client, dockerInstallScript(input.Server))
	if err != nil {
		return nil, fmt.Errorf("REMOTE_DOCKER_INSTALL_FAILED: %w", err)
	}
	version = lastNonEmptyLine(version)
	if version == "" {
		version = "installed"
	}
	if err := ensureDockerRegistryMirror(ctx, client, input.Server, input.LogStep); err != nil {
		return nil, err
	}
	return domain.Object{
		"metrics": domain.Object{
			"ssh":                       true,
			"docker":                    true,
			"dockerVersion":             version,
			"dockerStatus":              "ready",
			"dockerRegistryMirrorReady": true,
		},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker "+version),
		},
	}, nil
}

func applyRemoteDockerRegistryMirrorProbe(ctx context.Context, client sshSessionClient, server domain.Object, result domain.Object) {
	desired, err := desiredDockerRegistryMirrorURL(server)
	if err != nil {
		setMetric(result, "dockerRegistryMirrorReady", false)
		appendServerRuntimeCheck(result, "docker-registry", serverRuntimeCheckWarning, err.Error())
		return
	}
	mirrors, err := remoteDockerRegistryMirrors(ctx, client)
	ready := desired == "" || len(mirrors) > 0 && mirrors[0] == desired
	if err == nil {
		setMetric(result, "dockerRegistryMirrors", mirrors)
	}
	setMetric(result, "dockerRegistryMirrorReady", err == nil && ready)
	if err != nil {
		appendServerRuntimeCheck(result, "docker-registry", serverRuntimeCheckWarning, "DOCKER_REGISTRY_MIRROR_PROBE_FAILED")
		return
	}
	if !ready {
		appendServerRuntimeCheck(result, "docker-registry", serverRuntimeCheckWarning, "DOCKER_REGISTRY_MIRROR_DRIFT")
		return
	}
	appendServerRuntimeCheck(result, "docker-registry", serverRuntimeCheckSuccess, "Docker registry mirror is ready")
}

func ensureDockerRegistryMirror(
	ctx context.Context,
	client *ssh.Client,
	server domain.Object,
	logStep func(source string, phase string, level string, message string),
) error {
	desired, err := desiredDockerRegistryMirrorURL(server)
	if err != nil {
		return err
	}
	if desired == "" {
		return nil
	}
	current, err := remoteOutput(ctx, client, "if [ -f /etc/docker/daemon.json ]; then cat /etc/docker/daemon.json; fi")
	if err != nil {
		return errors.New("DOCKER_DAEMON_CONFIG_READ_FAILED")
	}
	next, changed, err := mergeDockerRegistryMirrorConfig(current, desired)
	if err != nil {
		return err
	}
	if !changed {
		ready, probeErr := remoteDockerRegistryMirrorReady(ctx, client, desired)
		if probeErr == nil && ready {
			return nil
		}
		next = []byte(current)
		changed = true
	}
	if changed {
		if logStep != nil {
			logStep("docker", "configure-registry", "info", "configuring Docker registry mirror")
		}
		const temporaryPath = "/etc/docker/daemon.json.iquops.tmp"
		if err := writeRemoteFileBytes(ctx, client, temporaryPath, next); err != nil {
			return errors.New("DOCKER_REGISTRY_MIRROR_CONFIG_WRITE_FAILED")
		}
		if _, err := remoteOutput(ctx, client, dockerRegistryMirrorApplyCommand(temporaryPath)); err != nil {
			return errors.New("DOCKER_REGISTRY_MIRROR_CONFIG_FAILED")
		}
	}
	ready, err := remoteDockerRegistryMirrorReady(ctx, client, desired)
	if err != nil || !ready {
		return errors.New("DOCKER_REGISTRY_MIRROR_VERIFY_FAILED")
	}
	return nil
}

func desiredDockerRegistryMirrorURL(server domain.Object) (string, error) {
	profile := objectMapFromAny(server["platformProfile"])
	mirror, ok := domain.DockerRegistryMirrorURL(profile["dockerRegistryMirrorUrl"])
	if !ok {
		return "", errors.New("DOCKER_REGISTRY_MIRROR_URL_INVALID")
	}
	return mirror, nil
}

func mergeDockerRegistryMirrorConfig(current string, desired string) ([]byte, bool, error) {
	config := map[string]any{}
	if strings.TrimSpace(current) != "" {
		if err := json.Unmarshal([]byte(current), &config); err != nil || config == nil {
			return nil, false, errors.New("DOCKER_DAEMON_CONFIG_INVALID")
		}
	}
	existing := []string{}
	if rawMirrors, ok := config["registry-mirrors"]; ok {
		items, ok := rawMirrors.([]any)
		if !ok {
			return nil, false, errors.New("DOCKER_DAEMON_CONFIG_INVALID")
		}
		for _, item := range items {
			mirror, ok := item.(string)
			if !ok || strings.TrimSpace(mirror) == "" {
				return nil, false, errors.New("DOCKER_DAEMON_CONFIG_INVALID")
			}
			existing = append(existing, mirror)
		}
	}
	if len(existing) > 0 && normalizeDockerRegistryMirrorURL(existing[0]) == desired {
		return nil, false, nil
	}
	mirrors := []string{desired}
	seen := map[string]bool{desired: true}
	for _, mirror := range existing {
		normalized := normalizeDockerRegistryMirrorURL(mirror)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		mirrors = append(mirrors, strings.TrimSpace(mirror))
	}
	config["registry-mirrors"] = mirrors
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, false, errors.New("DOCKER_DAEMON_CONFIG_INVALID")
	}
	return append(encoded, '\n'), true, nil
}

func remoteDockerRegistryMirrorReady(ctx context.Context, client sshSessionClient, desired string) (bool, error) {
	mirrors, err := remoteDockerRegistryMirrors(ctx, client)
	return err == nil && (desired == "" || len(mirrors) > 0 && mirrors[0] == desired), err
}

func remoteDockerRegistryMirrors(ctx context.Context, client sshSessionClient) ([]string, error) {
	output, err := remoteOutput(ctx, client, "docker info --format '{{json .RegistryConfig.Mirrors}}'")
	if err != nil {
		return nil, err
	}
	var mirrors []string
	if err := json.Unmarshal([]byte(output), &mirrors); err != nil {
		return nil, err
	}
	result := []string{}
	for _, address := range mirrors {
		mirror, ok := domain.DockerRegistryMirrorURL(address)
		if !ok || mirror == "" {
			return nil, errors.New("DOCKER_REGISTRY_MIRROR_URL_INVALID")
		}
		result = append(result, mirror)
	}
	return result, nil
}

func normalizeDockerRegistryMirrorURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func dockerRegistryMirrorApplyCommand(temporaryPath string) string {
	configPath := "/etc/docker/daemon.json"
	backupPath := configPath + ".iquops.backup"
	return strings.Join([]string{
		"set -e",
		"config=" + shellQuote(configPath),
		"temporary=" + shellQuote(temporaryPath),
		"backup=" + shellQuote(backupPath),
		"had_config=0",
		"install -d -m 0755 /etc/docker",
		"if command -v dockerd >/dev/null 2>&1; then dockerd --validate --config-file \"$temporary\" >/dev/null; fi",
		"if [ -f \"$config\" ]; then cp -p \"$config\" \"$backup\"; had_config=1; else rm -f \"$backup\"; fi",
		"install -m 0600 \"$temporary\" \"$config\"",
		"if systemctl restart docker && docker version --format '{{.Server.Version}}' >/dev/null; then rm -f \"$temporary\" \"$backup\"; exit 0; fi",
		"if [ \"$had_config\" = 1 ]; then mv -f \"$backup\" \"$config\"; else rm -f \"$config\"; fi",
		"systemctl restart docker >/dev/null 2>&1 || true",
		"rm -f \"$temporary\" \"$backup\"",
		"exit 1",
	}, "\n")
}

func dockerInstallScript(server ...domain.Object) string {
	conflicts := "docker docker-client docker-client-latest docker-common docker-latest docker-latest-logrotate docker-logrotate docker-engine docker.io docker-doc docker-compose docker-compose-v2 podman-docker containerd runc"
	repoBases := dockerRepoBaseURLs(nil)
	if len(server) > 0 {
		repoBases = dockerRepoBaseURLs(server[0])
	}
	strategies := dockerPackageManagerStrategies()
	lines := []string{
		"set -e",
		"conflicts=" + shellQuote(conflicts),
		"docker_ce_packages='docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin'",
		"supported_package_managers=" + shellQuote(dockerPackageManagerNames(strategies)),
		"docker_repo_bases=$(cat <<'IQUOPS_DOCKER_REPOS'",
		strings.Join(repoBases, "\n"),
		"IQUOPS_DOCKER_REPOS",
		")",
		"write_yum_docker_repo() {",
		"  repo_base=\"$1\"",
		"  cat > /etc/yum.repos.d/docker-ce.repo <<EOF",
		"[docker-ce-stable]",
		"name=Docker CE Stable - \\$basearch",
		"baseurl=${repo_base}/linux/centos/\\$releasever/\\$basearch/stable",
		"enabled=1",
		"gpgcheck=1",
		"gpgkey=${repo_base}/linux/centos/gpg",
		"EOF",
		"}",
		"install_yum_docker() {",
		"  pm=\"$1\"",
		"  for repo_base in $docker_repo_bases; do",
		"    echo \"Trying Docker CE repo: ${repo_base}/linux/centos\"",
		"    write_yum_docker_repo \"$repo_base\"",
		"    if $pm makecache -y --disablerepo='*' --enablerepo='docker-ce-stable' && $pm install -y $docker_ce_packages; then",
		"      return 0",
		"    fi",
		"  done",
		"  echo DOCKER_CE_INSTALL_FAILED",
		"  return 1",
		"}",
		"install_apt_docker() {",
		"  . /etc/os-release",
		"  codename=${VERSION_CODENAME:-${UBUNTU_CODENAME:-stable}}",
		"  repo_id=$ID",
		"  case \"$repo_id\" in ubuntu|debian) ;; *) repo_id=ubuntu ;; esac",
		"  for repo_base in $docker_repo_bases; do",
		"    echo \"Trying Docker CE repo: ${repo_base}/linux/${repo_id}\"",
		"    rm -f /etc/apt/keyrings/docker.asc",
		"    if curl -fsSL \"${repo_base}/linux/${repo_id}/gpg\" -o /etc/apt/keyrings/docker.asc; then",
		"      chmod a+r /etc/apt/keyrings/docker.asc",
		"      echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] ${repo_base}/linux/${repo_id} ${codename} stable\" > /etc/apt/sources.list.d/docker.list",
		"      if apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y $docker_ce_packages; then",
		"        return 0",
		"      fi",
		"    fi",
		"  done",
		"  echo DOCKER_CE_INSTALL_FAILED",
		"  return 1",
		"}",
	}
	lines = append(lines, dockerInstallDispatchScript(strategies)...)
	lines = append(lines,
		"systemctl enable --now docker",
		"docker version --format '{{.Server.Version}}'",
	)
	return strings.Join(lines, "\n")
}

func dockerRepoBaseURLs(server domain.Object) []string {
	official := "https://download.docker.com"
	mirrors := map[string]string{
		"aliyun":   "https://mirrors.aliyun.com/docker-ce",
		"tencent":  "https://mirrors.cloud.tencent.com/docker-ce",
		"tsinghua": "https://mirrors.tuna.tsinghua.edu.cn/docker-ce",
	}
	profile := objectMapFromAny(server["platformProfile"])
	choice := stringValue(profile["dockerRepoMirror"])
	switch choice {
	case "official":
		return []string{official}
	case "aliyun", "tencent", "tsinghua":
		return uniqueStrings([]string{mirrors[choice], official})
	default:
		return uniqueStrings([]string{official, mirrors["tencent"], mirrors["aliyun"], mirrors["tsinghua"]})
	}
}

func (e *SystemServerRuntimeExecutor) UninstallDockerEngine(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	startedAt := domain.Now()
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	input.LogStep("docker", "uninstall", "info", "正在远端服务器卸载 Docker Engine")
	if _, err := remoteOutput(ctx, client, dockerUninstallScript()); err != nil {
		return nil, fmt.Errorf("REMOTE_DOCKER_UNINSTALL_FAILED: %w", err)
	}
	presence, err := remoteOutput(ctx, client, dockerPresenceCommand)
	if err != nil || presence != "missing" {
		return nil, errors.New("REMOTE_DOCKER_POST_UNINSTALL_UNVERIFIED")
	}
	return domain.Object{"checkedAt": startedAt,
		"metrics": domain.Object{
			"ssh":          true,
			"docker":       false,
			"dockerStatus": "missing",
		},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("docker", serverRuntimeCheckSuccess, "Docker Engine removed"),
		},
	}, nil
}

func dockerUninstallScript() string {
	packages := "docker docker-client docker-client-latest docker-common docker-latest docker-latest-logrotate docker-logrotate docker-engine docker-ce docker-ce-cli docker-ce-rootless-extras containerd.io docker-buildx-plugin docker-compose-plugin docker.io docker-compose docker-compose-v2 podman-docker"
	strategies := dockerPackageManagerStrategies()
	lines := []string{
		"set -e",
		"if command -v systemctl >/dev/null 2>&1; then",
		"  systemctl disable --now docker docker.socket containerd >/dev/null 2>&1 || true",
		"else",
		"  service docker stop >/dev/null 2>&1 || true",
		"  service containerd stop >/dev/null 2>&1 || true",
		"  chkconfig docker off >/dev/null 2>&1 || true",
		"fi",
		"packages=" + shellQuote(packages),
	}
	lines = append(lines, dockerRemoveDispatchScript(strategies)...)
	lines = append(lines,
		"if version=$(docker version --format '{{.Server.Version}}' 2>/dev/null); then",
		"  printf 'Docker daemon still available: %s\\n' \"$version\"",
		"  exit 1",
		"fi",
		"printf 'removed'",
	)
	return strings.Join(lines, "\n")
}
