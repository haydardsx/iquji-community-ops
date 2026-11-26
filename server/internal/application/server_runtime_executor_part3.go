package application

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

func (r *Runtime) applyServerRuntimeProbe(ctx context.Context, serverID string, result domain.Object) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		health := objectMapFromAny(findObject(state.Servers, serverID)["health"])
		if checkedAt := stringValue(result["checkedAt"]); checkedAt != "" && !observedAfter(checkedAt, stringValue(health["checkedAt"])) {
			return nil
		}
		result = filterSupersededAgentProbe(*state, serverID, result)
		if metrics := objectMapFromAny(result["metrics"]); len(metrics) > 0 {
			metrics = cloneObject(metrics)
			if _, ok := metrics["docker"]; ok {
				metrics["dockerStatus"] = dockerProbeStatus(metrics, objectSlice(result["checks"]))
			}
			if metrics["swarm"] != nil || metrics["docker"] == false || metrics["ssh"] == false {
				metrics["swarmCheckedAt"] = firstNonEmptyString(stringValue(result["checkedAt"]), stringValue(metrics["swarmCheckedAt"]), domain.Now())
			}
			applyServerRuntimeMetrics(state, serverID, metrics)
		}
		persistServerProbeSnapshot(state, serverID, result)
		remote := objectMapFromAny(result["remote"])
		if profile := objectMapFromAny(remote["platformProfile"]); len(profile) > 0 {
			applyServerPlatformProfile(state, serverID, profile)
		}
		if report := objectMapFromAny(result["monitoringReport"]); len(report) > 0 {
			applyMonitoringReportToState(state, report)
		}
		return nil
	})
}

func (r *Runtime) executeInstallDockerEngine(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare docker install", err
	}
	r.appendRuntimeMessage(task, "docker", "install", "info", "正在安装 Docker Engine")
	result, err := r.ServerRuntimeExecutor.InstallDockerEngine(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "docker", err)
		return "failed to install Docker Engine", err
	}
	_ = r.applyServerRuntimeProbe(context.WithoutCancel(ctx), task.TargetID, result)
	return "Docker Engine installed and verified", nil
}

func (r *Runtime) executeUninstallDockerEngine(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	if err := r.validateDockerUninstall(ctx, task.TargetID); err != nil {
		return "Docker Engine uninstall blocked", err
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare docker uninstall", err
	}
	r.appendRuntimeMessage(task, "docker", "uninstall", "info", "正在卸载 Docker Engine")
	result, err := r.ServerRuntimeExecutor.UninstallDockerEngine(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "docker", err)
		return "failed to uninstall Docker Engine", err
	}
	_ = r.applyServerRuntimeProbe(context.WithoutCancel(ctx), task.TargetID, result)
	return "Docker Engine uninstalled and verified", nil
}

func (r *Runtime) validateDockerUninstall(ctx context.Context, serverID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	server := findObject(state.Servers, serverID)
	if server == nil {
		return errors.New("SERVER_NOT_FOUND")
	}
	impact := calculateServerReferenceImpact(state, serverID)
	for _, guard := range []struct {
		reason string
		code   string
	}{
		{reason: "SERVER_HAS_MANAGED_SWARM_RUNTIME", code: "DOCKER_UNINSTALL_BLOCKED_SWARM"},
		{reason: "SERVER_HAS_MANAGED_TRAEFIK_RUNTIME", code: "DOCKER_UNINSTALL_BLOCKED_TRAEFIK"},
		{reason: "SERVER_HAS_MANAGED_FRPS_RUNTIME", code: "DOCKER_UNINSTALL_BLOCKED_FRPS"},
		{reason: "SERVER_HAS_MANAGED_JENKINS_RUNTIME", code: "DOCKER_UNINSTALL_BLOCKED_JENKINS"},
	} {
		if impact.HasReason(guard.reason) {
			return errors.New(guard.code)
		}
	}
	if impact.HasReason("SERVER_REFERENCED_AS_BUILD_SERVER", "SERVER_REFERENCED_AS_GATEWAY", "SERVER_REFERENCED_BY_ROUTE_GATEWAY", "SERVER_REFERENCED_AS_DEPLOYMENT_MANAGER", "SERVER_REFERENCED_BY_PLACEMENT", "SERVER_REFERENCED_BY_SWARM_SERVICE") {
		return errors.New("DOCKER_UNINSTALL_BLOCKED_APPLICATION_CONFIG")
	}
	if impact.HasReason("SERVER_HAS_ENVIRONMENT_RUNTIME") {
		return errors.New("DOCKER_UNINSTALL_BLOCKED_ENVIRONMENT")
	}
	if impact.HasReason("SERVER_HAS_APPLICATION_INSTANCE", "SERVER_REFERENCED_BY_TUNNEL", "SERVER_HAS_ACTIVE_TUNNEL_RUN", "SERVER_REFERENCED_BY_ACTIVE_RELEASE_BUILD", "SERVER_REFERENCED_BY_ACTIVE_RELEASE_MANAGER", "SERVER_REFERENCED_BY_ACTIVE_RELEASE_SERVICE", "SERVER_HAS_ACTIVE_JENKINS_RUN") {
		return errors.New("DOCKER_UNINSTALL_BLOCKED_MANAGED_RESOURCE")
	}
	return nil
}

func (r *Runtime) executeInstallMonitoringAgent(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare monitoring agent install", err
	}
	r.appendRuntimeMessage(task, "agent", "install", "info", "正在安装监控 Agent")
	result, err := r.ServerRuntimeExecutor.InstallMonitoringAgent(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "agent", err)
		return "failed to install monitoring agent", err
	}
	if err := r.applyMonitoringAgentInstallResult(context.WithoutCancel(ctx), task.TargetID, result); err != nil {
		return "保存监控 Agent 安装结果失败", err
	}
	return "monitoring agent installed and verified", nil
}

func (r *Runtime) executeUninstallMonitoringAgent(ctx context.Context, task domain.Task) (string, error) {
	if r.ServerRuntimeExecutor == nil {
		return "server runtime executor is not configured", errors.New("SERVER_RUNTIME_EXECUTOR_NOT_CONFIGURED")
	}
	input, err := r.serverRuntimeExecutionInput(ctx, task.TargetID, task)
	if err != nil {
		return "failed to prepare monitoring agent uninstall", err
	}
	result, err := r.ServerRuntimeExecutor.UninstallMonitoringAgent(ctx, input)
	if err != nil {
		_ = r.markServerRuntimeError(context.WithoutCancel(ctx), task.TargetID, "agent", err)
		return "failed to uninstall monitoring agent", err
	}
	_ = r.applyMonitoringAgentUninstallResult(context.WithoutCancel(ctx), task.TargetID, result)
	return "monitoring agent uninstalled", nil
}

func (r *Runtime) applyMonitoringAgentInstallResult(ctx context.Context, serverID string, result domain.Object) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		agent := objectMapFromAny(result["agent"])
		report := objectMapFromAny(result["monitoringReport"])
		if server := findObject(state.Servers, serverID); server == nil {
			return errors.New("SERVER_NOT_FOUND")
		}
		if report["serverId"] != serverID || stringValue(report["sampledAt"]) == "" {
			return errors.New("MONITORING_AGENT_REPORT_INVALID")
		}
		if len(agent) == 0 {
			agent = domain.Object{
				"serverId":        serverID,
				"version":         "local",
				"protocolVersion": 1,
				"capabilities":    []string{"host"},
			}
		}
		agent["serverId"] = serverID
		agent["status"] = "online"
		agent["lastHeartbeatAt"] = now
		agent["updatedAt"] = now
		if stringValue(agent["version"]) == "" {
			agent["version"] = "local"
		}
		if _, ok := agent["capabilities"]; !ok {
			agent["capabilities"] = []string{"host"}
		}
		prior := findObjectBy(state.MonitoringAgents, "serverId", serverID)
		upsertStateObjectByKey(&state.MonitoringAgents, "serverId", serverID, domain.Object{
			"id":              firstNonEmptyString(stringValue(prior["id"]), stringValue(agent["id"]), domain.NewID("agent")),
			"serverId":        serverID,
			"version":         agent["version"],
			"protocolVersion": numberOrDefault(agent["protocolVersion"], 1),
			"status":          "online",
			"capabilities":    objectStringSlice(agent["capabilities"]),
			"installedAt":     now,
			"lastHeartbeatAt": now,
			"createdAt":       firstNonEmptyString(stringValue(prior["createdAt"]), now),
			"updatedAt":       now,
		})
		clearMonitoringPoll(findObjectBy(state.MonitoringAgents, "serverId", serverID))
		delete(findObjectBy(state.MonitoringAgents, "serverId", serverID), "lastSampleAt")
		metrics := objectMapFromAny(result["metrics"])
		metrics["agent"] = true
		applyServerRuntimeMetrics(state, serverID, metrics)
		applyMonitoringReportToState(state, report)
		return nil
	})
}

func (r *Runtime) applyMonitoringAgentUninstallResult(ctx context.Context, serverID string, result domain.Object) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		upsertStateObjectByKey(&state.MonitoringAgents, "serverId", serverID, domain.Object{
			"id":              domain.NewID("agent"),
			"serverId":        serverID,
			"version":         "local",
			"protocolVersion": 1,
			"status":          "not_installed",
			"capabilities":    []string{},
			"installedAt":     now,
			"createdAt":       now,
			"updatedAt":       now,
		})
		clearMonitoringPoll(findObjectBy(state.MonitoringAgents, "serverId", serverID))
		metrics := objectMapFromAny(result["metrics"])
		metrics["agent"] = false
		applyServerRuntimeMetrics(state, serverID, metrics)
		return nil
	})
}

func (r *Runtime) markServerRuntimeError(ctx context.Context, serverID string, component string, err error) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		for index := range state.Servers {
			if stringValue(state.Servers[index]["id"]) != serverID {
				continue
			}
			health := objectMapFromAny(state.Servers[index]["health"])
			metrics := objectMapFromAny(health["metrics"])
			metrics[component] = false
			if component == "swarm" {
				reconcileSwarmIdentityMetrics(metrics, domain.Object{"swarm": false})
			} else {
				clearServerRuntimeMetricDetails(metrics, component)
			}
			metrics["checkedAt"] = domain.Now()
			health["metrics"] = metrics
			health["summary"] = stableErrorCode(err, strings.ToUpper(component)+"_RUNTIME_FAILED")
			state.Servers[index]["health"] = health
			state.Servers[index]["updatedAt"] = domain.Now()
			return nil
		}
		return nil
	})
}

func applyServerRuntimeMetrics(state *domain.State, serverID string, metrics domain.Object) {
	now := domain.Now()
	for index := range state.Servers {
		if stringValue(state.Servers[index]["id"]) != serverID {
			continue
		}
		health := objectMapFromAny(state.Servers[index]["health"])
		existing := objectMapFromAny(health["metrics"])
		for key, value := range metrics {
			if !strings.HasPrefix(key, "swarm") {
				existing[key] = value
			}
		}
		if docker, ok := metrics["docker"].(bool); ok && !docker {
			clearServerRuntimeMetricDetails(existing, "docker")
			clearServerRuntimeMetricDetails(existing, "traefik")
			clearServerRuntimeMetricDetails(existing, "jenkins")
			existing["traefik"] = false
			existing["jenkins"] = false
		}
		if metrics["swarm"] != nil || metrics["swarmCheckedAt"] != nil || metrics["docker"] == false || metrics["ssh"] == false {
			mergeSwarmProbeMetrics(existing, metrics, firstNonEmptyString(stringValue(metrics["swarmCheckedAt"]), now))
		}
		if traefik, ok := metrics["traefik"].(bool); ok && !traefik {
			clearServerRuntimeMetricDetails(existing, "traefik")
		}
		if frps, ok := metrics["frps"].(bool); ok && !frps {
			clearServerRuntimeMetricDetails(existing, "frps")
		}
		if jenkins, ok := metrics["jenkins"].(bool); ok && !jenkins {
			clearServerRuntimeMetricDetails(existing, "jenkins")
		}
		existing["checkedAt"] = now
		health["metrics"] = existing
		health["summary"] = "Server runtime verified"
		state.Servers[index]["health"] = health
		state.Servers[index]["updatedAt"] = now
		break
	}
	if agentValue, ok := metrics["agent"].(bool); ok && !agentValue {
		agent := findObjectBy(state.MonitoringAgents, "serverId", serverID)
		if monitoringAgentInstalled(agent) {
			agent["status"] = "offline"
			agent["updatedAt"] = now
			clearMonitoringPoll(agent)
			return
		}
		upsertStateObjectByKey(&state.MonitoringAgents, "serverId", serverID, domain.Object{
			"id":              domain.NewID("agent"),
			"serverId":        serverID,
			"version":         "local",
			"protocolVersion": 1,
			"status":          "not_installed",
			"capabilities":    []string{},
			"installedAt":     now,
			"createdAt":       now,
			"updatedAt":       now,
		})
	}
}

func clearServerRuntimeMetricDetails(metrics map[string]any, component string) {
	switch component {
	case "docker":
		delete(metrics, "dockerVersion")
		delete(metrics, "dockerRegistryMirrorReady")
		delete(metrics, "dockerRegistryMirrors")
	case "swarm":
		delete(metrics, "swarmControlAvailable")
		delete(metrics, "swarmNodeId")
		delete(metrics, "swarmClusterId")
		delete(metrics, "swarmVerifiedAt")
		delete(metrics, "swarmManagerAddress")
		delete(metrics, "swarmAdvertiseAddr")
		delete(metrics, "swarmNetwork")
	case "traefik":
		delete(metrics, "traefikService")
		delete(metrics, "traefikImage")
		delete(metrics, "traefikImagePullCandidates")
		delete(metrics, "traefikHttpPort")
		delete(metrics, "traefikHttpsPort")
		delete(metrics, "traefikDataRoot")
		delete(metrics, "traefikNetwork")
		delete(metrics, "traefikSwarmNetwork")
	case "jenkins":
		delete(metrics, "jenkinsVersion")
		delete(metrics, "jenkinsImage")
		delete(metrics, "jenkinsContainer")
		delete(metrics, "jenkinsBundleVersion")
		delete(metrics, "jenkinsPort")
	case "frps":
		delete(metrics, "frpsService")
		delete(metrics, "frpsImage")
	}
}

func applyServerPlatformProfile(state *domain.State, serverID string, detected domain.Object) {
	now := domain.Now()
	for index := range state.Servers {
		if stringValue(state.Servers[index]["id"]) != serverID {
			continue
		}
		profile := defaultServerPlatformProfile()
		if existing := objectMapFromAny(state.Servers[index]["platformProfile"]); len(existing) > 0 {
			for key, value := range existing {
				profile[key] = value
			}
		}
		for _, key := range []string{"osId", "osVersion", "architecture", "packageManager", "serviceManager", "overrideSource", "detectedAt"} {
			if value, ok := detected[key]; ok && stringValue(value) != "" {
				profile[key] = value
			}
		}
		if stringValue(profile["detectedAt"]) == "" {
			profile["detectedAt"] = now
		}
		state.Servers[index]["platformProfile"] = profile
		state.Servers[index]["updatedAt"] = now
		return
	}
}

type sshSessionClient interface {
	NewSession() (*ssh.Session, error)
}

func verifyTCPPort(ctx context.Context, host string, port int, timeout time.Duration) error {
	endpoint := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}
