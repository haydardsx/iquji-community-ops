package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) CreateCertificate(ctx context.Context, input domain.Object) (map[string]any, error) {
	certificate, err := r.CreateCertificateAsset(ctx, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"certificate": certificate}, nil
}

// 证书签发只在真实网关上执行，页面必须先确认再创建可执行操作。
func (r *Runtime) CreateCertificateVerification(ctx context.Context, id string) (domain.Task, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	certificate := findObject(state.Certificates, id)
	if certificate == nil {
		return domain.Task{}, errors.New("CERTIFICATE_NOT_FOUND")
	}
	return r.CreateTask(ctx, string(domain.TaskActionVerifyCertificate), "certificate", id,
		firstNonEmptyString(stringValue(certificate["name"]), "certificate"), nil)
}

func (r *Runtime) MonitoringProjects(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := []domain.Object{}
	for _, app := range state.Applications {
		project := findObject(state.Projects, stringValue(app["projectId"]))
		items = append(items, domain.Object{
			"projectId":         stringValue(app["projectId"]),
			"projectName":       firstNonEmptyString(stringValue(project["name"]), stringValue(app["projectId"])),
			"applicationId":     stringValue(app["id"]),
			"applicationName":   firstNonEmptyString(stringValue(app["name"]), stringValue(app["id"])),
			"status":            applicationHealthStatus(app, state),
			"runningContainers": 0,
			"alerts":            []domain.Object{},
		})
	}
	return map[string]any{"items": items, "agents": monitoringAgentViews(state, time.Now()), "refreshIntervalSeconds": 15, "generatedAt": domain.Now()}, nil
}

func (r *Runtime) MonitoringServers(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	latest := latestSwarmSnapshots(state)
	items := []domain.Object{}
	for _, server := range state.Servers {
		agent := monitoringAgentView(findObjectBy(state.MonitoringAgents, "serverId", stringValue(server["id"])), time.Now())
		serverID := stringValue(server["id"])
		item := domain.Object{
			"serverId":         stringValue(server["id"]),
			"serverName":       firstNonEmptyString(stringValue(server["name"]), stringValue(server["id"])),
			"connectionStatus": monitoringServerConnectionStatus(server, agent, time.Now()),
			"publicHost":       stringValue(server["publicHost"]),
			"agentStatus":      firstNonEmptyString(stringValue(agent["status"]), "not_installed"),
			"agentVersion":     stringValue(agent["version"]),
			"lastHeartbeatAt":  agent["lastHeartbeatAt"],
			"alerts":           []domain.Object{},
		}
		metrics := observedServerMetrics(objectMapFromAny(server["health"]), latest[serverID])
		for _, key := range []string{"swarm", "swarmClusterId", "swarmVerificationStatus", "swarmVerificationError", "swarmControlAvailable", "swarmCheckedAt", "swarmNodeId", "swarmRole", "swarmConnectionMode", "swarmAdvertiseAddr", "swarmManagerAddress"} {
			if value, ok := metrics[key]; ok {
				item[key] = value
			}
		}
		if sample := latestMetricSample(state, serverID, "server", serverID); len(sample) > 0 {
			values := objectMapFromAny(sample["values"])
			copyNumericMetric(item, values, "cpuPercent")
			copyNumericMetric(item, values, "memoryPercent")
			copyNumericMetric(item, values, "diskPercent")
			copyNumericMetric(item, values, "dockerDiskBytes")
			item["lastSampleAt"] = sample["sampledAt"]
		}
		item["sampleStatus"] = monitoringSampleStatus(stringValue(item["lastSampleAt"]), time.Now())
		items = append(items, item)
	}
	return map[string]any{"items": items, "agents": monitoringAgentViews(state, time.Now()), "refreshIntervalSeconds": 15, "generatedAt": domain.Now()}, nil
}

func (r *Runtime) MonitoringSwarm(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return monitoringSwarmOverview(state, time.Now()), nil
}

func (r *Runtime) UpsertMonitoringAgent(ctx context.Context, input domain.Object, status string) (domain.Object, error) {
	var result domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		result = upsertMonitoringAgentState(state, input, status, domain.Now())
		return nil
	})
	return result, err
}

func (r *Runtime) AcceptMonitoringReport(ctx context.Context, input domain.Object) (map[string]any, error) {
	var agent domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		agent = applyMonitoringReportToState(state, input)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "agent": agent}, nil
}

func applyMonitoringReportToState(state *domain.State, input domain.Object) domain.Object {
	now := domain.Now()
	serverID := stringValue(input["serverId"])
	sampledAt := firstNonEmptyString(stringValue(input["sampledAt"]), now)
	agent := upsertMonitoringAgentState(state, input, "online", now)
	if observedAfter(sampledAt, stringValue(agent["lastSampleAt"])) {
		agent["lastSampleAt"] = sampledAt
	}
	agent["lastHeartbeatAt"] = now
	agent["updatedAt"] = now
	for _, sample := range objectSlice(input["samples"]) {
		item := cloneObject(sample)
		item["id"] = domain.NewID("metric")
		item["serverId"] = serverID
		item["sampledAt"] = firstNonEmptyString(stringValue(item["sampledAt"]), sampledAt)
		state.MetricSamples = append(state.MetricSamples, item)
	}
	if snapshot := objectMapFromAny(input["swarmSnapshot"]); len(snapshot) > 0 {
		item := cloneObject(snapshot)
		item["id"] = domain.NewID("swarm")
		item["serverId"] = serverID
		item["sampledAt"] = sampledAt
		item["receivedAt"] = now
		item["summary"] = firstNonEmptyString(stringValue(item["summary"]), "Swarm snapshot accepted")
		item["status"] = firstNonEmptyString(stringValue(item["status"]), "idle")
		state.SwarmSnapshots = append(state.SwarmSnapshots, item)
	}
	return agent
}

func upsertMonitoringAgentState(state *domain.State, input domain.Object, status string, now string) domain.Object {
	serverID := stringValue(input["serverId"])
	index := -1
	for i, agent := range state.MonitoringAgents {
		if stringValue(agent["serverId"]) == serverID {
			index = i
			break
		}
	}
	result := cloneObject(input)
	if index >= 0 {
		for key, value := range state.MonitoringAgents[index] {
			if _, ok := result[key]; !ok {
				result[key] = value
			}
		}
	} else {
		result["id"] = domain.NewID("agent")
		result["installedAt"] = now
		result["createdAt"] = now
	}
	// 注册和心跳只保留 Agent 元数据。历史误存的采样正文在下次更新时一并清理。
	delete(result, "samples")
	delete(result, "swarmSnapshot")
	delete(result, "sampledAt")
	result["protocolVersion"] = numberOrDefault(result["protocolVersion"], 1)
	result["status"] = status
	result["updatedAt"] = now
	if status == "online" {
		result["lastHeartbeatAt"] = now
	}
	if index >= 0 {
		state.MonitoringAgents[index] = result
	} else {
		state.MonitoringAgents = append(state.MonitoringAgents, result)
	}
	return result
}

func collectionRef(state *domain.State, collection string) (*[]domain.Object, error) {
	switch collection {
	case "projects":
		return &state.Projects, nil
	case "applications":
		return &state.Applications, nil
	case "servers":
		return &state.Servers, nil
	case "secret_assets":
		return &state.SecretAssets, nil
	case "domains":
		return &state.Domains, nil
	case "certificates":
		return &state.Certificates, nil
	case "resolvers":
		return &state.Resolvers, nil
	case "ingresses":
		return &state.Ingresses, nil
	case "release_plans":
		return &state.ReleasePlans, nil
	case "routes":
		return &state.Routes, nil
	}
	return nil, errors.New("UNKNOWN_COLLECTION")
}

func cloneObject(input domain.Object) domain.Object {
	return domain.CloneObject(input)
}

func stringValue(value any) string {
	return domain.StringValue(value)
}

func findObject(items []domain.Object, id string) domain.Object {
	return domain.FindObject(items, id)
}

func findObjectBy(items []domain.Object, key string, value string) domain.Object {
	return domain.FindObjectBy(items, key, value)
}

func filterObjectsBy(items []domain.Object, key string, value string) []domain.Object {
	return domain.FilterObjectsBy(items, key, value)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func objectSlice(value any) []domain.Object {
	return domain.ObjectSliceFromAny(value)
}

func objectMapFromAny(value any) domain.Object {
	switch typed := value.(type) {
	case domain.Object:
		return typed
	case map[string]any:
		return domain.Object(typed)
	default:
		return domain.Object{}
	}
}

func objectStringSlice(value any) []string {
	return domain.StringSliceFromAny(value)
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func numberValue(value any) int {
	return domain.NumberValue(value)
}

func numberOrDefault(value any, fallback int) int {
	return domain.NumberOrDefault(value, fallback)
}

func jsonNumber(value int) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func objectSummary(item domain.Object, labelKey string) domain.Object {
	return domain.Object{
		"id":   stringValue(item["id"]),
		"name": firstNonEmptyString(stringValue(item[labelKey]), stringValue(item["displayName"]), stringValue(item["id"])),
	}
}

func hasServerTask(tasks []domain.Task, serverID string, actionFragment string) bool {
	for _, task := range tasks {
		if task.TargetKind == "server" && task.TargetID == serverID && strings.Contains(task.Action, actionFragment) && task.Status == "succeeded" {
			return true
		}
	}
	return false
}

func hasMonitoringAgent(agents []domain.Object, serverID string) bool {
	for _, agent := range agents {
		if stringValue(agent["serverId"]) == serverID && stringValue(agent["status"]) != "not_installed" {
			return true
		}
	}
	return false
}
