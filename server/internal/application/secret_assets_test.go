package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 测试调用正式的暂存与生效逻辑，避免另外维护绕过 Worker 的凭据实现。
func (r *Runtime) runTestSecretChange(ctx context.Context, action, id string, input domain.Object) (domain.Object, error) {
	_, task, err := r.PrepareSecretAssetOperation(ctx, action, input, id)
	if err != nil {
		return nil, err
	}
	if _, err = r.ConfirmTask(ctx, task.ID); err != nil {
		return nil, err
	}
	if _, err = r.RunWorkerOnce(ctx); err != nil {
		return nil, err
	}
	run, err := r.GetOperationRun(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if run["status"] != "succeeded" {
		return nil, errors.New(stringValue(run["errorCode"]))
	}
	state, err := r.Store.LoadState(ctx)
	return findObject(state.SecretAssets, task.TargetID), err
}
func (r *Runtime) CreateSecretAsset(ctx context.Context, input domain.Object) (domain.Object, error) {
	return r.runTestSecretChange(ctx, "create_secret_asset", "", input)
}
func (r *Runtime) RotateSecretAsset(ctx context.Context, id string, input domain.Object) (domain.Object, error) {
	return r.runTestSecretChange(ctx, "rotate_secret_asset", id, input)
}

func seedSecretAsset(t *testing.T, ctx context.Context, runtime *Runtime, input domain.Object) domain.Object {
	t.Helper()
	asset, err := runtime.CreateSecretAsset(ctx, input)
	if err != nil {
		t.Fatalf("create secret asset: %v", err)
	}
	return asset
}

func readState(t *testing.T, ctx context.Context, runtime *Runtime) domain.State {
	t.Helper()
	state, err := runtime.Store.LoadState(ctx)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	return state
}

func TestSecretAssetCreateDerivesPurposeAndRejectsUnknownType(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)

	token := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "GitHub token",
		"assetType":   "git_token",
		"secretValue": "sample-git-value",
		"description": "读取示例仓库",
	})
	if stringValue(token["purpose"]) != "git_auth" {
		t.Fatalf("git token purpose = %q", stringValue(token["purpose"]))
	}
	if stringValue(token["description"]) != "读取示例仓库" || stringValue(token["id"]) == "" {
		t.Fatal("token should keep description and id")
	}
	if _, err := runtime.CreateSecretAsset(ctx, domain.Object{"assetType": "certificate", "secretValue": "x"}); err == nil || err.Error() != "SECRET_ASSET_TYPE_INVALID" {
		t.Fatalf("unknown type error = %v", err)
	}
	if _, err := runtime.CreateSecretAsset(ctx, domain.Object{"assetType": "ssh_key", "purpose": "git_auth", "secretValue": validTestSSHPrivateKey}); err == nil || err.Error() != "SECRET_ASSET_PURPOSE_INVALID" {
		t.Fatalf("mismatched purpose error = %v", err)
	}
	if _, err := runtime.CreateSecretAsset(ctx, domain.Object{"assetType": "git_token"}); err == nil || err.Error() != "SECRET_MATERIAL_REQUIRED" {
		t.Fatalf("empty material error = %v", err)
	}
}

func TestSecretAssetRotateRequiresMaterial(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "Deploy key",
		"assetType":   "ssh_key",
		"secretValue": validTestSSHPrivateKey,
	})
	id := stringValue(asset["id"])

	if _, err := runtime.RotateSecretAsset(ctx, id, domain.Object{}); err == nil || err.Error() != "SECRET_MATERIAL_REQUIRED" {
		t.Fatalf("empty rotation error = %v", err)
	}
	rotated, err := runtime.RotateSecretAsset(ctx, id, domain.Object{"secretValue": validTestSSHPrivateKey})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if stringValue(rotated["fingerprint"]) != stringValue(asset["fingerprint"]) {
		t.Fatal("同一材料的指纹应保持稳定")
	}
	update := func(mutate func(*domain.State)) {
		if err := store.MutateState(ctx, func(state *domain.State) error {
			mutate(state)
			return nil
		}); err != nil {
			t.Fatalf("mutate: %v", err)
		}
	}
	update(func(state *domain.State) {
		for index := range state.SecretAssets {
			if stringValue(state.SecretAssets[index]["id"]) == id {
				state.SecretAssets[index]["secretRef"] = "secret_rotated"
			}
		}
	})
	if available, err := store.HasSecretMaterial(ctx, "secret_rotated"); err != nil || available {
		t.Fatalf("manual ref rewrite should not create material: %v %v", available, err)
	}
}

func TestSecretAssetDeleteCascadesBindingsAndMaterial(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "Deploy key",
		"assetType":   "ssh_key",
		"secretValue": validTestSSHPrivateKey,
	})
	assetID := stringValue(asset["id"])
	secretRef := stringValue(asset["secretRef"])
	gitToken := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "Git token",
		"assetType":   "git_token",
		"secretValue": "sample-git-value",
	})
	gitRef := stringValue(gitToken["secretRef"])
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = append(state.Servers, domain.Object{"id": "srv_one", "name": "Gateway", "secretAssetIds": []string{assetID}})
		state.Applications = append(state.Applications, domain.Object{
			"id": "app_one", "name": "API", "projectId": "prj_one",
			"sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/a.git", "authSecretRef": gitRef},
		})
		return nil
	}); err != nil {
		t.Fatalf("seed references: %v", err)
	}

	preview, err := runtime.SecretAssetDeletePreview(ctx, assetID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview["blocked"] != false {
		t.Fatal("preview should not be blocked")
	}
	if err := runtime.DeleteSecretAsset(ctx, assetID); err != nil {
		t.Fatalf("delete asset: %v", err)
	}
	if available, err := store.HasSecretMaterial(ctx, secretRef); err != nil || available {
		t.Fatalf("secret material should be removed: %v %v", available, err)
	}
	state := readState(t, ctx, runtime)
	if len(state.SecretAssets) != 1 || stringValue(state.SecretAssets[0]["id"]) != stringValue(gitToken["id"]) {
		t.Fatal("only the git token should remain")
	}
	if ids := objectStringSlice(state.Servers[0]["secretAssetIds"]); len(ids) != 0 {
		t.Fatal("server binding should be cleared")
	}

	if err := runtime.DeleteSecretAsset(ctx, stringValue(gitToken["id"])); err != nil {
		t.Fatalf("delete git token: %v", err)
	}
	state = readState(t, ctx, runtime)
	source := objectMapFromAny(state.Applications[0]["sourceConfig"])
	if stringValue(source["authSecretRef"]) != "" {
		t.Fatal("git auth reference should be cleared")
	}
	if stringValue(source["repositoryUrl"]) == "" {
		t.Fatal("repository url must be preserved")
	}
}

func TestSecretAssetDeleteBlocksOutputReferences(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "Env password",
		"assetType":   "git_token",
		"secretValue": "sample-git-value",
	})
	assetID := stringValue(asset["id"])
	secretRef := stringValue(asset["secretRef"])
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, domain.Object{
			"applicationId":   "app_demo",
			"outputVariables": []domain.Object{{"key": "PASSWORD", "valueSource": "secret_ref", "secretRef": secretRef}},
		})
		return nil
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	preview, err := runtime.SecretAssetDeletePreview(ctx, assetID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview["blocked"] != true {
		t.Fatal("preview should be blocked")
	}
	if err := runtime.DeleteSecretAsset(ctx, assetID); err == nil || err.Error() != "SECRET_ASSET_REFERENCED_BY_APPLICATION_VARIABLE" {
		t.Fatalf("delete error = %v", err)
	}
	if available, err := store.HasSecretMaterial(ctx, secretRef); err != nil || !available {
		t.Fatalf("blocked delete must keep material: %v %v", available, err)
	}
}

func TestSecretAssetUpdateIsLimitedToMetadata(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{
		"name":        "Deploy key",
		"assetType":   "ssh_key",
		"secretValue": validTestSSHPrivateKey,
	})
	id := stringValue(asset["id"])
	updated, err := runtime.UpdateSecretAsset(ctx, id, domain.Object{"name": "Deploy key 2", "description": "轮换后使用"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if stringValue(updated["name"]) != "Deploy key 2" || stringValue(updated["description"]) != "轮换后使用" {
		t.Fatal("metadata not applied")
	}
	if _, err := runtime.UpdateSecretAsset(ctx, id, domain.Object{"purpose": "git_auth"}); err == nil || err.Error() != "SECRET_ASSET_PURPOSE_INVALID" {
		t.Fatalf("purpose change error = %v", err)
	}
	if _, err := runtime.UpdateSecretAsset(ctx, id, domain.Object{}); err == nil || !strings.Contains(err.Error(), "SECRET_ASSET_UPDATE_EMPTY") {
		t.Fatalf("empty update error = %v", err)
	}
}
