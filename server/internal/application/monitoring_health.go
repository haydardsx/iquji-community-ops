package application

import (
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const monitoringFreshness = 90 * time.Second

func observedAfter(candidate, previous string) bool {
	current, err := time.Parse(time.RFC3339Nano, candidate)
	if err != nil {
		return false
	}
	older, err := time.Parse(time.RFC3339Nano, previous)
	return err != nil || !current.Before(older)
}

func monitoringSampleStatus(value string, now time.Time) string {
	observed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "unknown"
	}
	age := now.Sub(observed)
	if age < -monitoringFreshness || age > monitoringFreshness {
		return "stale"
	}
	return "fresh"
}

func monitoringAgentView(agent domain.Object, now time.Time) domain.Object {
	result := cloneObject(agent)
	if result == nil {
		result = domain.Object{}
	}
	status := firstNonEmptyString(stringValue(result["status"]), "not_installed")
	if status == "online" {
		switch monitoringSampleStatus(stringValue(result["lastHeartbeatAt"]), now) {
		case "stale":
			status = "stale"
		case "unknown":
			status = "unknown"
		}
	}
	result["status"] = status
	result["sampleStatus"] = monitoringSampleStatus(stringValue(result["lastSampleAt"]), now)
	delete(result, "pollToken")
	delete(result, "pollLeaseUntil")
	return result
}

func monitoringAgentViews(state domain.State, now time.Time) []domain.Object {
	items := []domain.Object{}
	for _, agent := range state.MonitoringAgents {
		if findObject(state.Servers, stringValue(agent["serverId"])) != nil {
			items = append(items, monitoringAgentView(agent, now))
		}
	}
	return items
}

func monitoringServerConnectionStatus(server, agent domain.Object, now time.Time) string {
	health := objectMapFromAny(server["health"])
	metrics := serverRuntimeMetrics(server)
	checkedAt := firstNonEmptyString(stringValue(health["checkedAt"]), stringValue(metrics["checkedAt"]))
	if monitoringSampleStatus(checkedAt, now) == "fresh" {
		if metrics["ssh"] == true {
			return "online"
		}
		if metrics["ssh"] == false {
			return "offline"
		}
	}
	// 新收到的已鉴权采样也能证明主机可达；Agent 的离线不推导主机离线。
	if agent["status"] == "online" {
		return "online"
	}
	return "unknown"
}

// 完整探测可能跨越 Agent 安装或卸载，较早开始的结果不能覆盖新实例。
func filterSupersededAgentProbe(state domain.State, serverID string, result domain.Object) domain.Object {
	agent := findObjectBy(state.MonitoringAgents, "serverId", serverID)
	startedAt := stringValue(result["checkedAt"])
	if startedAt == "" || !observedAfter(stringValue(agent["installedAt"]), startedAt) {
		return result
	}
	filtered := cloneObject(result)
	metrics := cloneObject(objectMapFromAny(result["metrics"]))
	delete(metrics, "agent")
	filtered["metrics"] = metrics
	delete(filtered, "monitoringReport")
	checks := []domain.Object{}
	for _, check := range objectSlice(result["checks"]) {
		if check["name"] != "agent" {
			checks = append(checks, check)
		}
	}
	filtered["checks"] = checks
	return filtered
}
