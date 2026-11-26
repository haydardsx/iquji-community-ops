package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ServerRuntimeOverview(ctx context.Context) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := []domain.Object{}
	for _, server := range state.Servers {
		id := stringValue(server["id"])
		health, _ := r.ServerHealth(ctx, id)
		items = append(items, domain.Object{
			"serverId":   id,
			"serverName": firstNonEmptyString(stringValue(server["name"]), id),
			"publicHost": stringValue(server["publicHost"]),
			"health":     health,
		})
	}
	return map[string]any{"items": items}, nil
}

func (r *Runtime) UpdateServerSecretAssets(ctx context.Context, serverID string, secretAssetIDs []string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	current := findObject(state.Servers, serverID)
	if current == nil {
		return nil, errors.New("SERVER_NOT_FOUND")
	}
	existing := stringSet(objectStringSlice(current["secretAssetIds"]))
	for _, id := range secretAssetIDs {
		if existing[id] {
			continue
		}
		asset := findObject(state.SecretAssets, id)
		if asset == nil {
			return nil, errors.New("SECRET_ASSET_NOT_FOUND")
		}
		if stringValue(asset["assetType"]) != "ssh_key" || stringValue(asset["purpose"]) != "deployment_server" {
			return nil, errors.New("SERVER_SSH_SECRET_ASSET_REQUIRED")
		}
		available, err := r.Store.HasSecretMaterial(ctx, stringValue(asset["secretRef"]))
		if err != nil {
			return nil, err
		}
		if !available {
			return nil, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
		}
	}
	var server domain.Object
	var added []string
	err = r.Store.MutateState(ctx, func(state *domain.State) error {
		for _, assetID := range secretAssetIDs {
			if findObject(state.SecretAssets, assetID) == nil {
				return errors.New("SECRET_ASSET_NOT_FOUND")
			}
		}
		if err := rejectPendingServerCredentials(*state, secretAssetIDs); err != nil {
			return err
		}
		for index := range state.Servers {
			if stringValue(state.Servers[index]["id"]) != serverID {
				continue
			}
			existing := stringSet(objectStringSlice(state.Servers[index]["secretAssetIds"]))
			nextSet := stringSet(secretAssetIDs)
			for _, id := range secretAssetIDs {
				if !existing[id] {
					added = append(added, id)
				}
			}
			normalized := make([]string, 0, len(nextSet))
			for _, id := range secretAssetIDs {
				if nextSet[id] {
					normalized = append(normalized, id)
					delete(nextSet, id)
				}
			}
			state.Servers[index]["secretAssetIds"] = normalized
			state.Servers[index]["updatedAt"] = domain.Now()
			server = serverWithPublicMirror(state.Servers[index])
			state.OperationLogs = append(state.OperationLogs, newOperationLog("update_server_secret_assets", "server", serverID, "succeeded", "已更新服务器凭据绑定", ""))
			return nil
		}
		return errors.New("SERVER_NOT_FOUND")
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"server": server, "addedAssetIds": added}, nil
}

func (r *Runtime) DeletePreview(ctx context.Context, targetKind string, targetID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	target := findDeleteTarget(state, targetKind, targetID)
	if target == nil {
		return nil, errors.New("DELETE_TARGET_NOT_FOUND")
	}
	impact := map[string]int{
		"projects":      0,
		"applications":  0,
		"releasePlans":  0,
		"servers":       0,
		"secretAssets":  0,
		"routes":        0,
		"domains":       0,
		"certificates":  0,
		"resolvers":     0,
		"ingresses":     0,
		"frpTunnels":    0,
		"frpTunnelRuns": 0,
	}
	impactKey := deleteImpactKey(targetKind)
	if impactKey != "" {
		impact[impactKey] = 1
	}
	if targetKind == "project" {
		for _, item := range state.Applications {
			if stringValue(item["projectId"]) == targetID {
				impact["applications"]++
			}
		}
		for _, item := range state.ReleasePlans {
			if stringValue(item["projectId"]) == targetID {
				impact["releasePlans"]++
			}
		}
	}
	if targetKind == "application" {
		for _, item := range state.Routes {
			if stringValue(item["applicationId"]) == targetID {
				impact["routes"]++
			}
		}
		for _, item := range state.ReleasePlans {
			if stringValue(item["applicationId"]) == targetID {
				impact["releasePlans"]++
			}
		}
	}
	targetName := firstNonEmptyString(stringValue(target["name"]), stringValue(target["displayName"]), targetID)
	subject := resourceRef(targetKind, targetID, targetName)
	builder := domain.NewCheckReportBuilder(domain.CheckKindDeleteImpact, subject)
	if targetKind == "project" || targetKind == "application" {
		collection := "projects"
		if targetKind == "application" {
			collection = "applications"
		}
		if err := validateConfigurationDeletion(state, collection, targetID); err != nil {
			builder.Check(err.Error(), targetKind, "delete-preview", subject, domain.CheckOutcomeFailed, domain.CheckSeverityDanger, true, "请先清理关联应用、远端资源或活动操作", nil)
		}
	}
	serverImpact := serverReferenceImpact{}
	if targetKind == "server" {
		serverImpact = calculateServerReferenceImpact(state, targetID)
		for _, reason := range serverImpact.Reasons {
			builder.Check(
				stringValue(reason["code"]), "server", "delete-preview", subject,
				domain.CheckOutcomeFailed, domain.CheckSeverityDanger, true,
				"server deletion is blocked by a live reference or managed runtime", reason,
			)
		}
	}
	for key, count := range impact {
		if count > 0 {
			builder.Impact(domain.ResourceActionDelete, resourceRef(deleteResourceKind(key), key, key), count, domain.CheckSeverityDanger, "DELETE_"+processCheckCode(key))
		}
	}
	summaryCheck(builder, "DELETE_IMPACT_READY", "resource", "delete-preview", subject, true, false, "delete impact calculated")
	report := builder.Report()
	payload := map[string]any{
		"targetKind": targetKind,
		"targetId":   targetID,
		"targetName": targetName,
		"impact":     impact,
	}
	if targetKind == "server" {
		payload["serverImpact"] = serverImpact.Object()
		payload["blockers"] = serverImpact.Reasons
	}
	if targetKind == "secret_asset" {
		// 凭据删除需要展示真实引用位置，供确认弹窗说明级联解绑与阻断原因。
		usage := collectSecretAssetUsage(state, targetID, stringValue(target["secretRef"]))
		payload["secretUsage"] = usage.Object()
		payload["blocked"] = usage.Blocking()
	}
	return processResponse(report, "deleteImpact", payload), nil
}
