package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// secretAssetPurposes 固定凭据类型与用途的对应关系，前端只选择类型。
var secretAssetPurposes = map[string]string{
	"application_secret": "application_variables",
	"registry_secret":    "registry_push",
	"git_token":          "git_auth",
	"ssh_key":            "deployment_server",
}

func normalizeSecretAssetType(assetType string) (string, string, error) {
	normalizedType := strings.ToLower(strings.TrimSpace(assetType))
	if normalizedType == "" {
		normalizedType = "ssh_key"
	}
	purpose, ok := secretAssetPurposes[normalizedType]
	if !ok {
		return "", "", errors.New("SECRET_ASSET_TYPE_INVALID")
	}
	return normalizedType, purpose, nil
}

// UpdateSecretAsset 只允许修改展示信息，材料必须通过轮换接口变更。
func (r *Runtime) UpdateSecretAsset(ctx context.Context, id string, input domain.Object) (domain.Object, error) {
	patch := domain.Object{}
	if name := strings.TrimSpace(stringValue(input["name"])); name != "" {
		patch["name"] = name
	}
	if _, exists := input["description"]; exists {
		patch["description"] = strings.TrimSpace(stringValue(input["description"]))
	}
	if purpose := strings.TrimSpace(stringValue(input["purpose"])); purpose != "" {
		state, err := r.Store.LoadState(ctx)
		if err != nil {
			return nil, err
		}
		asset := findObject(state.SecretAssets, id)
		if asset == nil {
			return nil, errors.New("SECRET_ASSET_NOT_FOUND")
		}
		expected := secretAssetPurposes[stringValue(asset["assetType"])]
		if purpose != expected {
			return nil, errors.New("SECRET_ASSET_PURPOSE_INVALID")
		}
		patch["purpose"] = purpose
	}
	if len(patch) == 0 {
		return nil, errors.New("SECRET_ASSET_UPDATE_EMPTY")
	}
	asset, err := r.UpdateObject(ctx, "secret_assets", id, patch)
	if err != nil {
		return nil, err
	}
	return r.publicSecretAsset(ctx, asset), nil
}

// SecretAssetUsage 汇总一个凭据资产的引用位置，用于删除前影响说明与阻断判断。
type SecretAssetUsage struct {
	Servers              []string
	GitApplications      []string
	RegistryApplications []string
	ApplicationVariables []string
}

// Blocking 表示配置或待执行发布仍需读取该材料。
func (u SecretAssetUsage) Blocking() bool {
	return u.blockingCode() != ""
}

func (u SecretAssetUsage) blockingCode() string {
	if len(u.RegistryApplications) > 0 {
		return "SECRET_ASSET_REFERENCED_BY_REGISTRY"
	}
	if len(u.ApplicationVariables) > 0 {
		return "SECRET_ASSET_REFERENCED_BY_APPLICATION_VARIABLE"
	}
	return ""
}

func (u SecretAssetUsage) Object() map[string]any {
	result := map[string]any{
		"servers":              u.Servers,
		"gitApplications":      u.GitApplications,
		"applicationVariables": u.ApplicationVariables,
	}
	if len(u.RegistryApplications) > 0 {
		result["registryApplications"] = u.RegistryApplications
	}
	if u.Blocking() {
		result["blockingCode"] = u.blockingCode()
	}
	return result
}

func collectSecretAssetUsage(state domain.State, assetID string, secretRef string) SecretAssetUsage {
	usage := SecretAssetUsage{Servers: []string{}, GitApplications: []string{}, ApplicationVariables: []string{}}
	for _, server := range state.Servers {
		if containsString(objectStringSlice(server["secretAssetIds"]), assetID) {
			usage.Servers = append(usage.Servers, firstNonEmptyString(stringValue(server["name"]), stringValue(server["id"])))
		}
	}
	if secretRef != "" {
		for _, application := range state.Applications {
			if stringValue(objectMapFromAny(application["sourceConfig"])["authSecretRef"]) == secretRef {
				usage.GitApplications = append(usage.GitApplications, firstNonEmptyString(stringValue(application["name"]), stringValue(application["id"])))
			}
		}
		recordRegistry := func(appID string, config domain.Object) {
			if sync := registrySyncConfig(config); sync["enabled"] == true && sync["registrySecretRef"] == secretRef {
				app := findObject(state.Applications, appID)
				usage.RegistryApplications = append(usage.RegistryApplications, firstNonEmptyString(stringValue(app["name"]), appID))
			}
		}
		for _, plan := range state.ReleasePlans {
			if !releasePlanTerminal(plan) {
				recordRegistry(stringValue(plan["applicationId"]), objectMapFromAny(plan["buildSnapshot"]))
			}
		}
		for _, config := range state.ApplicationDeploymentConfigs {
			recordRegistry(stringValue(config["applicationId"]), config)
			if stringValue(objectMapFromAny(config["sourceConfig"])["authSecretRef"]) == secretRef {
				usage.GitApplications = append(usage.GitApplications, firstNonEmptyString(stringValue(config["applicationId"]), "application"))
			}
			for _, variable := range append(objectSlice(config["environmentVariables"]), objectSlice(config["outputVariables"])...) {
				if stringValue(variable["secretRef"]) == secretRef {
					usage.ApplicationVariables = append(usage.ApplicationVariables, firstNonEmptyString(stringValue(variable["key"]), "variable"))
				}
			}
		}
	}
	usage.RegistryApplications = uniqueStrings(usage.RegistryApplications)
	usage.GitApplications = uniqueStrings(usage.GitApplications)
	usage.ApplicationVariables = uniqueStrings(usage.ApplicationVariables)
	return usage
}

// SecretAssetDeletePreview 返回删除凭据前的影响面，供确认弹窗展示。
func (r *Runtime) SecretAssetDeletePreview(ctx context.Context, assetID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	asset := findObject(state.SecretAssets, assetID)
	if asset == nil {
		return nil, errors.New("SECRET_ASSET_NOT_FOUND")
	}
	usage := collectSecretAssetUsage(state, assetID, stringValue(asset["secretRef"]))
	return map[string]any{
		"assetId":    assetID,
		"targetName": firstNonEmptyString(stringValue(asset["name"]), assetID),
		"usage":      usage.Object(),
		"blocked":    usage.Blocking(),
	}, nil
}

// DeleteSecretAsset 解除可解绑引用、阻断运行期引用并同步清理秘密材料。
func (r *Runtime) DeleteSecretAsset(ctx context.Context, assetID string) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	asset := findObject(state.SecretAssets, assetID)
	if asset == nil {
		return errors.New("SECRET_ASSET_NOT_FOUND")
	}
	secretRef := stringValue(asset["secretRef"])
	usage := collectSecretAssetUsage(state, assetID, secretRef)
	if usage.Blocking() {
		return errors.New(usage.blockingCode())
	}
	deleteRefs := objectStringSlice(asset["revisionSecretRefs"])
	if secretRef != "" {
		deleteRefs = append(deleteRefs, secretRef)
	}
	return r.Store.MutateStateWithSecretChanges(ctx, nil, deleteRefs, func(state *domain.State) error {
		current := findObject(state.SecretAssets, assetID)
		if current == nil {
			return errors.New("SECRET_ASSET_NOT_FOUND")
		}
		if current["materialRef"] != asset["materialRef"] {
			return errors.New("SECRET_ASSET_CHANGED")
		}
		if usage := collectSecretAssetUsage(*state, assetID, secretRef); usage.Blocking() {
			return errors.New(usage.blockingCode())
		}
		now := domain.Now()
		for index := range state.SecretAssets {
			if stringValue(state.SecretAssets[index]["id"]) != assetID {
				continue
			}
			state.SecretAssets = append(state.SecretAssets[:index], state.SecretAssets[index+1:]...)
			break
		}
		for index := range state.Servers {
			ids := objectStringSlice(state.Servers[index]["secretAssetIds"])
			if !containsString(ids, assetID) {
				continue
			}
			next := make([]string, 0, len(ids))
			for _, id := range ids {
				if id != assetID {
					next = append(next, id)
				}
			}
			state.Servers[index]["secretAssetIds"] = next
			state.Servers[index]["updatedAt"] = now
		}
		if secretRef != "" {
			for index := range state.Applications {
				source := objectMapFromAny(state.Applications[index]["sourceConfig"])
				if stringValue(source["authSecretRef"]) != secretRef {
					continue
				}
				delete(source, "authSecretRef")
				state.Applications[index]["sourceConfig"] = source
				state.Applications[index]["updatedAt"] = now
			}
			for index := range state.ApplicationDeploymentConfigs {
				source := objectMapFromAny(state.ApplicationDeploymentConfigs[index]["sourceConfig"])
				if stringValue(source["authSecretRef"]) != secretRef {
					continue
				}
				delete(source, "authSecretRef")
				state.ApplicationDeploymentConfigs[index]["sourceConfig"] = source
				state.ApplicationDeploymentConfigs[index]["updatedAt"] = now
			}
		}
		state.OperationLogs = append(state.OperationLogs, newOperationLog("delete_secret_assets", "secret_assets", assetID, "succeeded", "已删除凭据并清除绑定", ""))
		return nil
	})
}
