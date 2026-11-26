package application

import (
	"encoding/json"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func applicationHealthStatus(application domain.Object, state domain.State) string {
	status := stringValue(application["status"])
	if status == "running" || status == "success" {
		return "success"
	}
	if status == "failed" || status == "danger" {
		return "danger"
	}
	instances := filterObjectsBy(state.ApplicationInstances, "applicationId", stringValue(application["id"]))
	for _, instance := range instances {
		if stringValue(instance["status"]) == "running" || stringValue(instance["status"]) == "success" {
			return "success"
		}
	}
	return "idle"
}

func latestSwarmSample(snapshots []domain.Object) any {
	latest := ""
	for _, snapshot := range snapshots {
		if sampledAt := stringValue(snapshot["sampledAt"]); observedAfter(sampledAt, latest) {
			latest = sampledAt
		}
	}
	if latest == "" {
		return nil
	}
	return latest
}

func latestMetricSample(state domain.State, serverID string, targetKind string, targetID string) domain.Object {
	var latest domain.Object
	latestAt := ""
	for _, sample := range state.MetricSamples {
		if stringValue(sample["serverId"]) != serverID {
			continue
		}
		if stringValue(sample["targetKind"]) != targetKind {
			continue
		}
		if targetID != "" && stringValue(sample["targetId"]) != targetID {
			continue
		}
		sampledAt := stringValue(sample["sampledAt"])
		if sampledAt >= latestAt {
			latestAt = sampledAt
			latest = sample
		}
	}
	return latest
}

func copyNumericMetric(target domain.Object, values domain.Object, key string) {
	if value, ok := numericValue(values[key]); ok {
		target[key] = value
	}
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	default:
		return 0, false
	}
}

func placementServerIDs(config domain.Object) []string {
	switch placement := config["placement"].(type) {
	case domain.Object:
		return objectStringSlice(placement["eligibleServerIds"])
	case map[string]any:
		return objectStringSlice(placement["eligibleServerIds"])
	default:
		return []string{}
	}
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func safeName(value string) string {
	normalized := strings.Trim(strings.ToLower(value), "-_ ")
	normalized = strings.ReplaceAll(normalized, "_", "-")
	if normalized == "" {
		return "application"
	}
	return normalized
}

func SlugPrefix(name string) string {
	normalized := strings.ReplaceAll(strings.ToLower(name), "_", "-")
	switch normalized {
	case "projects":
		return "proj"
	case "applications":
		return "app"
	case "servers":
		return "srv"
	case "secret-assets", "secret_assets":
		return "secret"
	case "domains":
		return "domain"
	case "certificates":
		return "cert"
	case "resolvers":
		return "resolver"
	case "ingresses":
		return "ingress"
	case "release-plans", "release_plans":
		return "rel"
	case "routes":
		return "route"
	}
	return "obj"
}

func deploymentsLast7Days(tasks []domain.Task) int {
	count := 0
	for _, task := range tasks {
		if task.Action == "deploy_application" || task.Action == "request_jenkins_release" {
			count++
		}
	}
	return count
}
