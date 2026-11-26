package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	monitoringAgentRemoteDir       = "/opt/iquops/monitoring-agent"
	monitoringAgentLegacyRemoteDir = "/opt/iquops/monitoring-client/community/agent"
	monitoringAgentBinaryPath      = monitoringAgentRemoteDir + "/iquops-monitoring-agent"
	monitoringAgentConfigPath      = monitoringAgentRemoteDir + "/config.json"
)

func (e *SystemServerRuntimeExecutor) InstallMonitoringAgent(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	archOutput, err := remoteOutput(ctx, client, "uname -m")
	if err != nil {
		return nil, err
	}
	binaryPath, target, err := monitoringAgentBinary(input, archOutput)
	if err != nil {
		return nil, err
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("MONITORING_AGENT_BINARY_READ_FAILED:%s: %w", binaryPath, err)
	}
	report, err := installMonitoringAgentFiles(ctx, client, input, binary, target)
	if err != nil {
		return nil, err
	}
	return domain.Object{
		"agent": domain.Object{
			"serverId":        stringValue(input.Server["id"]),
			"version":         target,
			"protocolVersion": 1,
			"status":          "online",
			"capabilities":    []string{"host"},
		},
		"metrics": domain.Object{
			"ssh":   true,
			"agent": true,
		},
		"checks": []domain.Object{
			serverRuntimeCheck("ssh", serverRuntimeCheckSuccess, "SSH reachable"),
			serverRuntimeCheck("agent", serverRuntimeCheckSuccess, "iquops-monitoring-agent active"),
		},
		"monitoringReport": report,
	}, nil
}

func (e *SystemServerRuntimeExecutor) UninstallMonitoringAgent(ctx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
	input = withServerRuntimeLogStep(input)
	client, err := sshClient(ctx, input.Server, input.SSHKey)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()
	input.LogStep("agent", "uninstall", "info", "正在移除监控 Agent 服务")
	command := monitoringAgentUninstallCommand()
	if err := remoteRun(ctx, client, command); err != nil {
		return nil, err
	}
	return domain.Object{
		"metrics": domain.Object{"agent": false},
		"checks":  []domain.Object{serverRuntimeCheck("agent", serverRuntimeCheckSuccess, "iquops-monitoring-agent removed")},
	}, nil
}

func monitoringAgentInstallDirectoryCommand() string {
	return "mkdir -p " + shellQuote(monitoringAgentRemoteDir) + " && chmod 700 /opt/iquops " + shellQuote(monitoringAgentRemoteDir) + " && rm -rf " + shellQuote(monitoringAgentLegacyRemoteDir)
}

func monitoringAgentUninstallCommand() string {
	return "systemctl disable --now iquops-monitoring-agent >/dev/null 2>&1 || true; rm -f /etc/systemd/system/iquops-monitoring-agent.service; systemctl daemon-reload >/dev/null 2>&1 || true; rm -rf " + shellQuote(monitoringAgentRemoteDir) + " " + shellQuote(monitoringAgentLegacyRemoteDir)
}

func monitoringAgentUnit() string {
	return strings.Join([]string{
		"[Unit]",
		"Description=iQuji Ops Monitoring Agent",
		"After=network-online.target docker.service",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + monitoringAgentBinaryPath,
		"Restart=always",
		"RestartSec=5",
		"User=root",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func monitoringAgentBinary(input ServerRuntimeExecutionInput, archOutput string) (string, string, error) {
	arch := strings.TrimSpace(archOutput)
	targetArch := ""
	switch arch {
	case "x86_64", "amd64":
		targetArch = "amd64"
	case "aarch64", "arm64":
		targetArch = "arm64"
	default:
		return "", "", fmt.Errorf("MONITORING_AGENT_ARCH_UNSUPPORTED:%s", arch)
	}
	target := "linux-" + targetArch
	fileName := "iquops-monitoring-agent-" + target
	candidates := []string{}
	if strings.TrimSpace(input.AgentBundleDir) != "" {
		candidates = append(candidates, filepath.Join(input.AgentBundleDir, fileName))
	}
	if strings.TrimSpace(input.RepositoryRoot) != "" {
		candidates = append(candidates,
			filepath.Join(input.RepositoryRoot, "agent", "dist", fileName),
			filepath.Join(input.RepositoryRoot, "agent", fileName),
		)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, target, nil
		}
	}
	return "", "", fmt.Errorf("MONITORING_AGENT_BINARY_NOT_FOUND:%s", strings.Join(candidates, ","))
}

func portListening(ctx context.Context, client sshSessionClient, port int) bool {
	pattern := ":" + strconv.Itoa(port)
	output, err := remoteOutput(ctx, client, "ss -lnt 2>/dev/null || netstat -lnt 2>/dev/null || true")
	return err == nil && strings.Contains(output, pattern)
}

func applyRemoteTraefikProbe(ctx context.Context, client sshSessionClient, input ServerRuntimeExecutionInput, result domain.Object) {
	service := firstNonEmptyString(strings.TrimSpace(input.TraefikService), "iquops-traefik")
	network := firstNonEmptyString(strings.TrimSpace(input.TraefikNetwork), "iquops-public")
	httpPort := input.TraefikHTTPPort
	if httpPort <= 0 {
		httpPort = 80
	}
	httpsPort := input.TraefikHTTPSPort
	if httpsPort <= 0 {
		httpsPort = 443
	}
	setMetric(result, "traefikService", service)
	setMetric(result, "traefikImage", firstNonEmptyString(strings.TrimSpace(input.TraefikImage), "traefik:v3.7"))
	setMetric(result, "traefikHttpPort", httpPort)
	setMetric(result, "traefikHttpsPort", httpsPort)
	setMetric(result, "traefikNetwork", network)
	setMetric(result, "traefikSwarmNetwork", network)

	swarm, err := readRemoteSwarmStatus(ctx, client, network)
	if err != nil {
		setMetric(result, "traefikStatus", "unavailable")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckDanger, "TRAEFIK_PROBE_FAILED")
		return
	}
	if !swarm.Active || swarm.Role != "manager" {
		setMetric(result, "traefik", false)
		setMetric(result, "traefikStatus", "missing")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckSkipped, "TRAEFIK_NOT_INSTALLED")
		return
	}
	names, err := remoteOutput(ctx, client, "docker service ls --format '{{.Name}}'")
	if err != nil {
		setMetric(result, "traefikStatus", "unavailable")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckDanger, "TRAEFIK_PROBE_FAILED")
		return
	}
	if !containsString(nonEmptyLines(names), service) {
		setMetric(result, "traefik", false)
		setMetric(result, "traefikStatus", "missing")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckSkipped, "TRAEFIK_NOT_INSTALLED")
		return
	}
	format := `{{index .Spec.Labels "iquops.owner"}}|{{index .Spec.Labels "iquops.component"}}|{{.Spec.TaskTemplate.ContainerSpec.Image}}`
	output, err := remoteOutput(ctx, client, "docker service inspect -f "+shellQuote(format)+" "+shellQuote(service)+" 2>/dev/null")
	if err != nil {
		setMetric(result, "traefik", false)
		setMetric(result, "traefikStatus", "unavailable")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckDanger, "TRAEFIK_PROBE_FAILED")
		return
	}
	parts := strings.SplitN(strings.TrimSpace(output), "|", 3)
	if len(parts) != 3 {
		setMetric(result, "traefik", false)
		setMetric(result, "traefikStatus", "unavailable")
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckDanger, "TRAEFIK_GATEWAY_UNAVAILABLE:inspect_invalid")
		return
	}
	owner := strings.TrimSpace(parts[0])
	component := strings.TrimSpace(parts[1])
	image := strings.TrimSpace(parts[2])
	managed := owner == "iquops" && (component == "traefik_gateway" || component == "traefik")
	replicaOutput, replicaErr := remoteOutput(ctx, client, "docker service ls --filter "+shellQuote("name="+service)+" --format '{{.Name}}|{{.Replicas}}'")
	running := replicaErr == nil && serviceReplicasReady(replicaOutput, service)
	networkOutput, networkErr := remoteOutput(ctx, client, "docker network inspect "+shellQuote(network)+" --format '{{.Id}}|{{.Driver}}|{{.Scope}}'")
	networkID, networkReady := managedOverlayNetworkID(networkOutput, networkErr)
	serviceNetworks, serviceNetworksErr := remoteOutput(ctx, client, "docker service inspect -f '{{range .Spec.TaskTemplate.Networks}}{{println .Target}}{{end}}' "+shellQuote(service)+" 2>/dev/null")
	attached := networkReady && serviceNetworksErr == nil && serviceUsesNetwork(serviceNetworks, networkID)
	if running && managed && attached {
		setMetric(result, "traefik", true)
		setMetric(result, "traefikStatus", "running")
		setMetric(result, "traefikImage", image)
		appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckSuccess, "Traefik gateway available")
		return
	}
	reason := []string{}
	if !running {
		reason = append(reason, "not_running")
	}
	if !managed {
		reason = append(reason, "not_iquops_managed")
	}
	if !attached {
		reason = append(reason, "network_missing")
	}
	setMetric(result, "traefik", false)
	setMetric(result, "traefikStatus", "unavailable")
	appendServerRuntimeCheck(result, "traefik", serverRuntimeCheckDanger, "TRAEFIK_GATEWAY_UNAVAILABLE:"+strings.Join(reason, ","))
}

func managedOverlayNetworkID(output string, err error) (string, bool) {
	if err != nil {
		return "", false
	}
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 3 || strings.TrimSpace(parts[1]) != "overlay" || strings.TrimSpace(parts[2]) != "swarm" {
		return "", false
	}
	networkID := strings.TrimSpace(parts[0])
	return networkID, networkID != ""
}

func serviceUsesNetwork(output string, networkID string) bool {
	if networkID == "" {
		return false
	}
	for _, candidate := range strings.Fields(output) {
		if candidate == networkID {
			return true
		}
	}
	return false
}

func serviceReplicasReady(output string, service string) bool {
	for _, line := range strings.Split(output, "\n") {
		name, replicas, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || strings.TrimSpace(name) != service {
			continue
		}
		actualText, desiredText, ok := strings.Cut(strings.TrimSpace(replicas), "/")
		if !ok {
			return false
		}
		actual, actualErr := strconv.Atoi(strings.TrimSpace(actualText))
		desired, desiredErr := strconv.Atoi(strings.TrimSpace(desiredText))
		return actualErr == nil && desiredErr == nil && desired > 0 && actual >= desired
	}
	return false
}

func remoteMonitoringAgentReport(ctx context.Context, client sshSessionClient) (domain.Object, error) {
	command := strings.Join([]string{
		"token=$(/bin/sed -n 's/.*\"token\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' " + shellQuote(monitoringAgentConfigPath) + " 2>/dev/null | /usr/bin/head -n 1)",
		"if [ -z \"$token\" ]; then exit 2; fi",
		`port=$(/bin/sed -n 's/.*"port"[[:space:]]*:[[:space:]]*\([0-9][0-9]*\).*/\1/p' ` + shellQuote(monitoringAgentConfigPath) + ` | /usr/bin/head -n 1)`,
		fmt.Sprintf(`case "$port" in ""|0) port=%d;; esac`, defaultMonitoringAgentPort),
		`[ "$port" -ge 1 ] && [ "$port" -le 65535 ] || exit 2`,
		"if command -v curl >/dev/null 2>&1; then",
		"  curl -fsS -H \"Authorization: Bearer $token\" \"http://127.0.0.1:$port/report\"",
		"else",
		"  wget -qO- --header=\"Authorization: Bearer $token\" \"http://127.0.0.1:$port/report\"",
		"fi",
	}, "\n")
	return remoteMonitoringAgentReportFromCommand(ctx, client, command)
}

func remoteMonitoringAgentReportFromCommand(ctx context.Context, client sshSessionClient, command string) (domain.Object, error) {
	output, err := remoteOutput(ctx, client, command)
	if err != nil {
		return nil, err
	}
	var report domain.Object
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		return nil, fmt.Errorf("MONITORING_AGENT_REPORT_INVALID: %w", err)
	}
	return report, nil
}

func remotePlatformProfile(ctx context.Context, client sshSessionClient) (domain.Object, string, error) {
	osRelease, err := remoteOutput(ctx, client, "/bin/cat /etc/os-release 2>/dev/null || /usr/bin/cat /etc/os-release 2>/dev/null || /bin/cat /usr/lib/os-release 2>/dev/null || /usr/bin/cat /usr/lib/os-release 2>/dev/null || true")
	if err != nil {
		return nil, "", err
	}
	architecture, _ := remoteOutput(ctx, client, "/usr/bin/uname -m 2>/dev/null || /bin/uname -m 2>/dev/null || uname -m 2>/dev/null || true")
	packageManager := remoteDetectPackageManager(ctx, client)
	serviceManager := remoteDetectServiceManager(ctx, client)
	profile := platformProfileFromRemote(osRelease, architecture, packageManager, serviceManager)
	summary := strings.Join([]string{
		stringValue(profile["osId"]) + " " + stringValue(profile["osVersion"]),
		stringValue(profile["architecture"]),
		stringValue(profile["packageManager"]),
		stringValue(profile["serviceManager"]),
	}, " / ")
	return profile, summary, nil
}

func remoteDetectPackageManager(ctx context.Context, client sshSessionClient) string {
	output, err := remoteOutput(ctx, client, "if [ -x /usr/bin/yum ] || [ -x /bin/yum ]; then printf yum; elif [ -x /usr/bin/dnf ] || [ -x /bin/dnf ]; then printf dnf; elif [ -x /usr/bin/apt-get ] || [ -x /bin/apt-get ]; then printf apt; else printf unknown; fi")
	if err != nil || strings.TrimSpace(output) == "" {
		return "unknown"
	}
	return strings.TrimSpace(output)
}

func remoteDetectServiceManager(ctx context.Context, client sshSessionClient) string {
	output, err := remoteOutput(ctx, client, "if [ -x /usr/bin/systemctl ] || [ -x /bin/systemctl ]; then printf systemd; elif [ -x /usr/sbin/service ] || [ -x /sbin/service ] || [ -x /usr/bin/service ] || [ -x /bin/service ]; then printf service; else printf unknown; fi")
	if err != nil || strings.TrimSpace(output) == "" {
		return "unknown"
	}
	return strings.TrimSpace(output)
}

func platformProfileFromRemote(osRelease string, architecture string, packageManager string, serviceManager string) domain.Object {
	values := parseOSRelease(osRelease)
	return domain.Object{
		"osId":           firstNonEmptyString(values["ID"], "unknown"),
		"osVersion":      firstNonEmptyString(values["VERSION_ID"], "unknown"),
		"architecture":   normalizeServerArchitecture(architecture),
		"packageManager": normalizeServerPackageManager(packageManager),
		"serviceManager": normalizeServerServiceManager(serviceManager),
		"overrideSource": "detected",
		"detectedAt":     domain.Now(),
	}
}

func parseOSRelease(output string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		values[key] = value
	}
	return values
}

func normalizeServerArchitecture(value string) string {
	switch strings.TrimSpace(value) {
	case "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return firstNonEmptyString(strings.TrimSpace(value), "unknown")
	}
}

func normalizeServerPackageManager(value string) string {
	switch strings.TrimSpace(value) {
	case "apt", "dnf", "yum":
		return strings.TrimSpace(value)
	default:
		return "unknown"
	}
}

func normalizeServerServiceManager(value string) string {
	switch strings.TrimSpace(value) {
	case "systemd", "service":
		return strings.TrimSpace(value)
	default:
		return "unknown"
	}
}

func appendServerRuntimeCheck(result domain.Object, name string, status string, summary string) {
	checks := objectSlice(result["checks"])
	checks = append(checks, serverRuntimeCheck(name, status, summary))
	result["checks"] = checks
}

func serverRuntimeCheck(name string, status string, summary string) domain.Object {
	return domain.Object{
		"name":    name,
		"status":  status,
		"summary": domain.LogSummary(summary),
	}
}

func setMetric(result domain.Object, key string, value any) {
	metrics := objectMapFromAny(result["metrics"])
	metrics[key] = value
	result["metrics"] = metrics
}

func lastNonEmptyLine(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line != "" {
			return line
		}
	}
	return ""
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func withServerRuntimeLogStep(input ServerRuntimeExecutionInput) ServerRuntimeExecutionInput {
	if input.LogStep == nil {
		input.LogStep = func(string, string, string, string) {}
	}
	return input
}

func (r *Runtime) serverRuntimeExecutionInput(ctx context.Context, serverID string, task domain.Task) (ServerRuntimeExecutionInput, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return ServerRuntimeExecutionInput{}, err
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return ServerRuntimeExecutionInput{}, errors.New("SERVER_NOT_FOUND")
	}
	sshKey, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return ServerRuntimeExecutionInput{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	saveDeparture := r.swarmDepartureSaver(serverID, task.ID)
	input := ServerRuntimeExecutionInput{
		Server:                cloneObject(server),
		SSHKey:                sshKey,
		AgentBundleDir:        r.Config.AgentBundleDir,
		RepositoryRoot:        r.Config.RepositoryRoot,
		SwarmAdvertiseAddr:    r.Config.SwarmAdvertiseAddr,
		TraefikService:        r.Config.TraefikService,
		TraefikImage:          r.Config.TraefikImage,
		TraefikNetwork:        r.Config.SwarmNetwork,
		TraefikHTTPPort:       r.Config.TraefikHTTPPort,
		TraefikHTTPSPort:      r.Config.TraefikHTTPSPort,
		JenkinsBundleDir:      r.Config.JenkinsBundleDir,
		JenkinsPluginsArchive: r.Config.JenkinsPluginsArchive,
		JenkinsBundleVersion:  r.Config.JenkinsBundleVersion,
		JenkinsImage:          r.Config.JenkinsImage,
		JenkinsContainer:      r.Config.JenkinsContainer,
		JenkinsPort:           r.Config.JenkinsPort,
		LogStep: func(source string, phase string, level string, message string) {
			r.appendRuntimeMessage(task, source, phase, level, message)
		},
		SaveSwarmDeparture: func(saveCtx context.Context, record domain.Object) error {
			return saveDeparture(domain.WithTaskClaim(saveCtx, task), record)
		},
	}
	metrics := objectMapFromAny(objectMapFromAny(server["health"])["metrics"])
	managerServerID := strings.TrimSpace(stringValue(metrics["swarmManagerServerId"]))
	if managerServerID != "" && managerServerID != serverID {
		input.SwarmManagerServerID = managerServerID
		if manager := findObject(state.Servers, managerServerID); manager != nil {
			input.SwarmManagerServer = cloneObject(manager)
			if managerSSHKey, managerKeyAvailable := r.serverSSHSecretMaterial(ctx, state, manager); managerKeyAvailable {
				input.SwarmManagerSSHKey = managerSSHKey
			}
		}
	}
	return input, nil
}

func (r *Runtime) ServerRuntimeProbe(ctx context.Context, serverID string) (domain.Object, error) {
	if r.ServerRuntimeExecutor == nil {
		return nil, errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.serverRuntimeExecutionInput(ctx, serverID, domain.Task{
		Action:     "detect_server_runtime",
		TargetKind: "server",
		TargetID:   serverID,
	})
	if err != nil {
		return nil, err
	}
	startedAt := domain.Now()
	result, err := r.ServerRuntimeExecutor.ProbeServerRuntime(ctx, input)
	if err != nil {
		return nil, err
	}
	result["checkedAt"] = firstNonEmptyString(stringValue(result["checkedAt"]), startedAt)
	if err := r.applyServerRuntimeProbe(ctx, serverID, result); err != nil {
		return nil, err
	}
	subject := resourceRef("server", serverID, stringValue(input.Server["name"]))
	report := legacyRuntimeProcess(domain.CheckKindRuntimeProbe, subject, "probe", result)
	return processResponse(report, "serverRuntime", payloadWithoutWorkflowFields(result)), nil
}
