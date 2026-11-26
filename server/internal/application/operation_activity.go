package application

import (
	"context"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// OperationScope 只含公开资源身份，前端无需读取执行 payload 即可逐层定位日志。
type OperationScope struct {
	Navigation    string   `json:"navigation"`
	TargetKind    string   `json:"targetKind"`
	TargetID      string   `json:"targetId"`
	ProjectID     string   `json:"projectId,omitempty"`
	ApplicationID string   `json:"applicationId,omitempty"`
	ServerIDs     []string `json:"serverIds"`
	TunnelID      string   `json:"tunnelId,omitempty"`
	Category      string   `json:"category,omitempty"`
	Section       string   `json:"section,omitempty"`
	Reachable     bool     `json:"reachable"`
}

func operationScope(state domain.State, run domain.OperationRun) OperationScope {
	scope := OperationScope{TargetKind: run.TargetKind, TargetID: run.TargetID, ServerIDs: []string{}}
	// 同步配置操作使用集合名保存日志；归属查询统一使用资源身份。
	for plural, singular := range map[string]string{
		"projects": "project", "applications": "application", "servers": "server",
		"routes": "route", "secret_assets": "secret_asset", "domains": "domain",
		"certificates": "certificate", "resolvers": "resolver", "ingresses": "ingress",
	} {
		if scope.TargetKind == plural {
			scope.TargetKind = singular
			break
		}
	}
	task := findTask(state.Tasks, run.ID)
	if task != nil {
		scope.ProjectID = stringValue(task.Payload["projectId"])
		scope.ApplicationID = taskApplicationID(state, *task)
		scope.TunnelID = stringValue(task.Payload["tunnelId"])
		scope.ServerIDs = append(scope.ServerIDs, stringValue(task.Payload["serverId"]), stringValue(task.Payload["bindServerId"]))
		for _, resource := range task.QueueResources {
			if id, ok := strings.CutPrefix(resource.Key, "server:"); ok {
				scope.ServerIDs = append(scope.ServerIDs, id)
			}
		}
	}
	switch scope.TargetKind {
	case "project":
		scope.ProjectID = run.TargetID
	case "application":
		scope.ApplicationID = run.TargetID
	case "route":
		route := findObject(state.Routes, run.TargetID)
		scope.ApplicationID = firstNonEmptyString(scope.ApplicationID, stringValue(route["applicationId"]))
	case "release_plan":
		plan := findObject(state.ReleasePlans, run.TargetID)
		scope.ApplicationID = firstNonEmptyString(scope.ApplicationID, stringValue(plan["applicationId"]))
		scope.ProjectID = firstNonEmptyString(scope.ProjectID, stringValue(plan["projectId"]))
	case "server":
		scope.ServerIDs = append(scope.ServerIDs, run.TargetID)
	case "frp_tunnel":
		scope.TunnelID = run.TargetID
	case "frp_tunnel_run":
		for _, tunnelRun := range state.FrpTunnelRuns {
			if tunnelRun.ID == run.TargetID {
				scope.TunnelID = tunnelRun.TunnelID
				scope.ServerIDs = append(scope.ServerIDs, tunnelRun.ServerID)
			}
		}
	case "secret_asset":
		scope.Category = "secrets"
		for _, server := range state.Servers {
			if containsString(objectStringSlice(server["secretAssetIds"]), run.TargetID) {
				scope.ServerIDs = append(scope.ServerIDs, stringValue(server["id"]))
			}
		}
	case "domain", "certificate", "resolver", "ingress":
		scope.Category = "domain-certificates"
	case "backup_restore":
		scope.Category = "backup"
	}
	if scope.ApplicationID != "" {
		app := findObject(state.Applications, scope.ApplicationID)
		scope.ProjectID = firstNonEmptyString(scope.ProjectID, stringValue(app["projectId"]))
		config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", scope.ApplicationID)
		for _, key := range []string{"buildServerId", "deploymentManagerServerId", "gatewayServerId"} {
			scope.ServerIDs = append(scope.ServerIDs, stringValue(config[key]))
		}
		for _, service := range objectSlice(objectMapFromAny(config["swarmRuntime"])["services"]) {
			scope.ServerIDs = append(scope.ServerIDs, stringValue(service["serverId"]))
		}
	}
	if scope.TunnelID != "" {
		for _, tunnel := range state.FrpTunnels {
			if tunnel.ID == scope.TunnelID {
				scope.ServerIDs = append(scope.ServerIDs, tunnel.ServerID)
			}
		}
	}
	scope.Section = operationSection(scope.TargetKind, run.Action)
	if scope.Section == "server-runtime" {
		// 区域归属只认操作目标，不把参与资源锁的其他服务器混入本机日志。
		scope.ServerIDs = []string{run.TargetID}
	}
	switch {
	case scope.ProjectID != "" || scope.ApplicationID != "":
		scope.Navigation = "projects"
	case scope.TunnelID != "":
		scope.Navigation = "tunnels"
	case scope.Category != "":
		scope.Navigation = "settings"
	case run.Action == "restore_backup" || run.Action == "restore_backup_overwrite":
		scope.Navigation, scope.Category = "settings", "backup"
	default:
		scope.Navigation = "servers"
	}
	unique := map[string]bool{}
	serverIDs := []string{}
	for _, id := range scope.ServerIDs {
		if id != "" && !unique[id] {
			unique[id] = true
			serverIDs = append(serverIDs, id)
		}
	}
	sort.Strings(serverIDs)
	scope.ServerIDs = serverIDs
	scope.Reachable = operationScopeReachable(state, scope, run.Action)
	return scope
}

func operationMatchesScope(scope OperationScope, query TaskQuery) bool {
	return (query.TargetKind == "" || scope.TargetKind == query.TargetKind) &&
		(query.TargetID == "" || scope.TargetID == query.TargetID) &&
		(query.ProjectID == "" || scope.ProjectID == query.ProjectID) &&
		(query.ApplicationID == "" || scope.ApplicationID == query.ApplicationID) &&
		(query.ServerID == "" || containsString(scope.ServerIDs, query.ServerID)) &&
		(query.TunnelID == "" || scope.TunnelID == query.TunnelID) &&
		(query.Category == "" || scope.Category == query.Category) &&
		(query.Section == "" || scope.Section == query.Section) &&
		(query.Navigation == "" || scope.Navigation == query.Navigation || query.Navigation == "servers" && len(scope.ServerIDs) > 0)
}

func (r *Runtime) MarkOperationRead(ctx context.Context, id, status, updatedAt string) error {
	return r.Store.MarkOperationRead(ctx, id, status, updatedAt)
}

func operationActivityResponse(state domain.State, run domain.OperationRun) map[string]any {
	response := operationRunResponseWithRecovery(state, run)
	response["scope"] = operationScope(state, run)
	response["readVersion"] = run.Status + ":" + run.UpdatedAt
	return response
}

func (r *Runtime) OperationActivity(ctx context.Context, query TaskQuery) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	readVersions, err := r.Store.OperationReadVersions(ctx)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	activeCount, unreadCount := 0, 0
	for _, run := range state.OperationRuns {
		if !operationMatchesScope(operationScope(state, run), query) {
			continue
		}
		terminal := domain.IsOperationRunStatusTerminal(run.Status)
		if !terminal {
			if task := findTask(state.Tasks, run.ID); task != nil && task.Status == "pending_confirmation" {
				continue
			}
		}
		unread := terminal && readVersions[run.ID] != run.Status+":"+run.UpdatedAt
		if terminal && !unread {
			continue
		}
		item := operationActivityResponse(state, run)
		item["active"], item["unread"] = !terminal, unread
		items = append(items, item)
		if terminal {
			unreadCount++
		} else {
			activeCount++
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return stringValue(items[i]["updatedAt"]) > stringValue(items[j]["updatedAt"]) })
	return map[string]any{"items": items, "activeCount": activeCount, "unreadCount": unreadCount}, nil
}
