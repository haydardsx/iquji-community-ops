package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// PrepareSecretAssetOperation 只暂存本次材料，确认并领取任务后才改变正式凭据。
func (r *Runtime) PrepareSecretAssetOperation(ctx context.Context, action string, input domain.Object, assetID string) (domain.Object, domain.Task, error) {
	if action != string(domain.TaskActionCreateSecretAsset) && action != string(domain.TaskActionRotateSecretAsset) {
		return nil, domain.Task{}, errors.New("TASK_ACTION_UNSUPPORTED")
	}
	material := stringValue(input["secretValue"])
	if strings.TrimSpace(material) == "" {
		return nil, domain.Task{}, errors.New("SECRET_MATERIAL_REQUIRED")
	}
	task := newTask(action, "secret_asset", assetID, "凭据", nil)
	materials := map[string]string{}
	var result domain.Object
	err := r.Store.MutateStateWithSecretMaterials(ctx, materials, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); err != nil {
			return err
		} else if reused {
			result = cloneObject(findObject(state.SecretAssets, task.TargetID))
			if result == nil {
				result = domain.Object{"id": task.TargetID, "name": task.TargetName, "materialStatus": "missing"}
			}
			return nil
		}
		asset := findObject(state.SecretAssets, assetID)
		if action == string(domain.TaskActionCreateSecretAsset) {
			assetType, purpose, err := normalizeSecretAssetType(stringValue(input["assetType"]))
			if err != nil {
				return err
			}
			if declared := stringValue(input["purpose"]); declared != "" && declared != purpose {
				return errors.New("SECRET_ASSET_PURPOSE_INVALID")
			}
			if err := validateSecretAssetMaterial(assetType, material); err != nil {
				return err
			}
			now := domain.Now()
			asset = domain.Object{
				"id": domain.NewID("asset"), "name": firstNonEmptyString(strings.TrimSpace(stringValue(input["name"])), assetType),
				"assetType": assetType, "purpose": purpose, "description": stringValue(input["description"]),
				"provider": "manual", "bindingScope": "global", "secretRef": domain.NewID("secret"),
				"createdAt": now, "updatedAt": now, "pendingCreationOperationId": task.ID,
			}
			state.SecretAssets = append(state.SecretAssets, asset)
		} else {
			if asset == nil {
				return errors.New("SECRET_ASSET_NOT_FOUND")
			}
			if stringValue(asset["pendingCreationOperationId"]) != "" {
				return errors.New("SECRET_MATERIAL_NOT_FOUND")
			}
			if err := validateSecretAssetMaterial(stringValue(asset["assetType"]), material); err != nil {
				return err
			}
		}
		serverID := strings.TrimSpace(stringValue(input["bindServerId"]))
		if serverID != "" {
			if asset["assetType"] != "ssh_key" {
				return errors.New("SERVER_SSH_SECRET_ASSET_REQUIRED")
			}
			if findObject(state.Servers, serverID) == nil {
				return errors.New("SERVER_NOT_FOUND")
			}
		}
		task.TargetID, task.TargetName = stringValue(asset["id"]), stringValue(asset["name"])
		stagedRef := domain.NewID("pending_secret")
		task.Payload = map[string]any{"assetType": asset["assetType"], "purpose": asset["purpose"], "stagedSecretRef": stagedRef}
		if serverID != "" {
			task.Payload["bindServerId"] = serverID
		}
		if err := enqueueTaskRequest(ctx, state, &task); err != nil {
			return err
		}
		materials[stagedRef] = material
		result = cloneObject(asset)
		if action == string(domain.TaskActionCreateSecretAsset) {
			result["materialStatus"] = "pending"
		}
		return nil
	})
	if result["assetType"] == "application_secret" || result["assetType"] == "registry_secret" {
		delete(result, "fingerprint")
		delete(result, "materialRef")
		delete(result, "revisionSecretRefs")
	}
	return result, task, err
}

// applyStagedSecretAssetTask 原子生效材料与元数据；检查点避免重启后重复应用旧轮换。
func (r *Runtime) applyStagedSecretAssetTask(ctx context.Context, task domain.Task) (string, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "凭据资产读取失败", err
	}
	asset := findObject(state.SecretAssets, task.TargetID)
	if asset == nil {
		return "凭据资产不存在", errors.New("SECRET_ASSET_NOT_FOUND")
	}
	if stringValue(asset["appliedOperationRunId"]) == task.ID {
		return "凭据材料已生效并通过核验", nil
	}
	stagedRef := stringValue(task.Payload["stagedSecretRef"])
	material, err := r.Store.GetSecretMaterial(ctx, stagedRef)
	if err != nil {
		return "本次凭据暂存材料不可用", err
	}
	assetType := stringValue(asset["assetType"])
	if purpose, exists := secretAssetPurposes[assetType]; !exists {
		return "凭据资产类型无效", errors.New("SECRET_ASSET_TYPE_INVALID")
	} else if stringValue(asset["purpose"]) != purpose {
		return "凭据资产用途无效", errors.New("SECRET_ASSET_PURPOSE_INVALID")
	}
	if err := validateSecretAssetMaterial(assetType, material); err != nil {
		return "凭据材料无效", err
	}
	secretRef := stringValue(asset["secretRef"])
	if secretRef == "" {
		return "凭据材料引用缺失", errors.New("SECRET_REF_REQUIRED")
	}
	materials := map[string]string{secretRef: material}
	err = r.Store.MutateStateWithSecretChanges(ctx, materials, []string{stagedRef}, func(current *domain.State) error {
		target := findObject(current.SecretAssets, task.TargetID)
		if target == nil {
			return errors.New("SECRET_ASSET_NOT_FOUND")
		}
		if stringValue(target["secretRef"]) != secretRef || stringValue(target["assetType"]) != assetType {
			return errors.New("SECRET_ASSET_CHANGED")
		}
		if serverID := stringValue(task.Payload["bindServerId"]); serverID != "" {
			server := findObject(current.Servers, serverID)
			if server == nil {
				return errors.New("SERVER_NOT_FOUND")
			}
			ids := objectStringSlice(server["secretAssetIds"])
			if !containsString(ids, task.TargetID) {
				server["secretAssetIds"] = append(ids, task.TargetID)
				server["updatedAt"] = domain.Now()
			}
		}
		fingerprint := shortHash(material + task.TargetID)
		if (assetType == "application_secret" || assetType == "registry_secret") && (target["fingerprint"] != fingerprint || target["materialRef"] == nil) {
			ref := domain.NewID("appmaterial")
			materials[ref] = material
			target["revisionSecretRefs"] = append(objectStringSlice(target["revisionSecretRefs"]), ref)
			target["materialRef"] = ref
		}
		target["fingerprint"] = fingerprint
		target["maskedSummary"] = secretSummary(assetType, material, fingerprint)
		target["rotatedAt"], target["updatedAt"] = domain.Now(), domain.Now()
		target["appliedOperationRunId"] = task.ID
		delete(target, "pendingCreationOperationId")
		return nil
	})
	return "凭据材料已生效并通过核验", err
}

// finalizeSecretAssetTask 回收未生效材料；失败轮换保留原正式材料。
func (r *Runtime) finalizeSecretAssetTask(ctx context.Context, task domain.Task, status string) error {
	stagedRef := stringValue(task.Payload["stagedSecretRef"])
	if domain.TaskAction(task.Action) == domain.TaskActionBindJenkinsAccess && stagedRef != "" {
		return r.Store.DeleteSecretMaterial(ctx, stagedRef)
	}
	if task.TargetKind != "secret_asset" || stagedRef == "" {
		return nil
	}
	return r.Store.MutateStateWithSecretChanges(ctx, nil, []string{stagedRef}, func(state *domain.State) error {
		if status != "succeeded" {
			asset := findObject(state.SecretAssets, task.TargetID)
			if asset != nil && stringValue(asset["pendingCreationOperationId"]) == task.ID {
				state.SecretAssets = domain.DeleteObjectByID(state.SecretAssets, task.TargetID)
			}
		}
		return nil
	})
}

// rejectPendingServerCredentials 防止通过通用服务器保存入口提前绑定未生效的凭据。
func rejectPendingServerCredentials(state domain.State, assetIDs []string) error {
	for _, id := range assetIDs {
		asset := findObject(state.SecretAssets, id)
		if asset != nil && stringValue(asset["pendingCreationOperationId"]) != "" {
			return errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
		}
	}
	return nil
}
