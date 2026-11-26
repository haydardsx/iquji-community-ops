package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSecretAssetWorkerValidatesPersistedAssets(t *testing.T) {
	for _, action := range []domain.TaskAction{domain.TaskActionCreateSecretAsset, domain.TaskActionRotateSecretAsset} {
		for _, assetType := range []string{"ssh_key", "git_token"} {
			t.Run(string(action)+"/"+assetType, func(t *testing.T) {
				runtime, store, ctx := newFrpRuntime(t)
				material := "example-git-token"
				if assetType == "ssh_key" {
					material = validTestSSHPrivateKey
				}
				asset := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": assetType, "secretValue": material})
				task, err := runtime.CreateTask(ctx, string(action), "secret_asset", stringValue(asset["id"]), "凭据", nil)
				if err != nil {
					t.Fatal(err)
				}
				if result, err := runtime.RunWorkerOnce(ctx); err != nil || result.Processed != 0 {
					t.Fatal("未确认操作不应执行")
				}
				confirmAndRunTaskForTest(t, runtime, ctx, task.ID)
				run, err := runtime.GetOperationRun(ctx, task.ID)
				if err != nil || run["status"] != "succeeded" || stringValue(run["errorCode"]) != "" {
					t.Fatal("凭据操作未成功")
				}
				actual, err := store.GetSecretMaterial(ctx, stringValue(asset["secretRef"]))
				if err != nil || actual != material {
					t.Fatal("凭据校验改变了材料")
				}
				state := readState(t, ctx, runtime)
				data, err := json.Marshal(state)
				encodedMaterial, materialErr := json.Marshal(material)
				if err != nil || materialErr != nil || strings.Contains(string(data), string(encodedMaterial[1:len(encodedMaterial)-1])) {
					t.Fatal("普通状态不得包含凭据材料")
				}
			})
		}
	}
}

func TestSecretAssetWorkerFailsWhenPersistedAssetIsUnavailable(t *testing.T) {
	for _, scenario := range []struct {
		name string
		code string
		edit func(*domain.State)
	}{
		{"deleted", "SECRET_ASSET_NOT_FOUND", func(state *domain.State) { state.SecretAssets = nil }},
		{"missing-material", "SECRET_MATERIAL_NOT_FOUND", func(state *domain.State) { state.SecretAssets[0]["secretRef"] = "missing" }},
		{"missing-ref", "SECRET_MATERIAL_NOT_FOUND", func(state *domain.State) { delete(state.SecretAssets[0], "secretRef") }},
		{"invalid-type", "SECRET_ASSET_TYPE_INVALID", func(state *domain.State) { state.SecretAssets[0]["assetType"] = "certificate" }},
		{"invalid-purpose", "SECRET_ASSET_PURPOSE_INVALID", func(state *domain.State) { state.SecretAssets[0]["purpose"] = "git_auth" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			asset := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": "ssh_key", "secretValue": validTestSSHPrivateKey})
			task, err := runtime.CreateTask(ctx, string(domain.TaskActionCreateSecretAsset), "secret_asset", stringValue(asset["id"]), "凭据", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MutateState(ctx, func(state *domain.State) error { scenario.edit(state); return nil }); err != nil {
				t.Fatal(err)
			}
			confirmAndRunTaskForTest(t, runtime, ctx, task.ID)
			run, err := runtime.GetOperationRun(ctx, task.ID)
			if err != nil || run["status"] != "failed" || run["errorCode"] != scenario.code {
				t.Fatal("凭据失效时不得写入成功")
			}
			state := readState(t, ctx, runtime)
			lastLog := state.OperationLogs[len(state.OperationLogs)-1]
			if lastLog.OperationRunID != task.ID || lastLog.Status != "failed" || lastLog.Message != scenario.code {
				t.Fatal("凭据校验失败未进入操作日志")
			}
		})
	}
}

func TestSecretAssetWorkerRejectsInvalidStoredMaterial(t *testing.T) {
	for _, scenario := range []struct{ material, code string }{
		{"not-a-private-key", "SSH_PRIVATE_KEY_INVALID"},
		{" ", "SECRET_MATERIAL_REQUIRED"},
	} {
		t.Run(scenario.code, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			asset := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": "ssh_key", "secretValue": validTestSSHPrivateKey})
			if err := store.UpsertSecretMaterial(ctx, stringValue(asset["secretRef"]), scenario.material); err != nil {
				t.Fatal(err)
			}
			task, err := runtime.CreateTask(ctx, string(domain.TaskActionRotateSecretAsset), "secret_asset", stringValue(asset["id"]), "凭据", nil)
			if err != nil {
				t.Fatal(err)
			}
			confirmAndRunTaskForTest(t, runtime, ctx, task.ID)
			run, err := runtime.GetOperationRun(ctx, task.ID)
			if err != nil || run["status"] != "failed" || run["errorCode"] != scenario.code {
				t.Fatal("材料无效时不得写入成功")
			}
		})
	}
}
