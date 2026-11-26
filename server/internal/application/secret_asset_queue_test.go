package application

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestQueuedCredentialCreationOnlyActivatesInWorker(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset, task, err := runtime.PrepareSecretAssetOperation(ctx, "create_secret_asset", domain.Object{
		"name": "示例凭据", "assetType": "git_token", "secretValue": "example-queued-material",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if available, err := store.HasSecretMaterial(ctx, stringValue(asset["secretRef"])); err != nil || available {
		t.Fatalf("排队期间正式材料不得可用：%v %v", available, err)
	}
	state := readState(t, ctx, runtime)
	data, _ := json.Marshal(state)
	if strings.Contains(string(data), "example-queued-material") {
		t.Fatal("普通任务状态泄露材料")
	}
	confirmAndRunTaskForTest(t, runtime, ctx, task.ID)
	actual, err := store.GetSecretMaterial(ctx, stringValue(asset["secretRef"]))
	if err != nil || actual != "example-queued-material" {
		t.Fatalf("正式材料未生效：%v", err)
	}
	if available, _ := store.HasSecretMaterial(ctx, stringValue(task.Payload["stagedSecretRef"])); available {
		t.Fatal("成功后未清理暂存材料")
	}
}

func TestQueuedCredentialCancellationKeepsExistingMaterial(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": "git_token", "secretValue": "example-old-material"})
	_, task, err := runtime.PrepareSecretAssetOperation(ctx, "rotate_secret_asset", domain.Object{"secretValue": "example-new-material"}, stringValue(asset["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CancelTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	actual, err := store.GetSecretMaterial(ctx, stringValue(asset["secretRef"]))
	if err != nil || actual != "example-old-material" {
		t.Fatalf("取消排队改变了原材料：%v", err)
	}
	if available, _ := store.HasSecretMaterial(ctx, stringValue(task.Payload["stagedSecretRef"])); available {
		t.Fatal("取消后未回收暂存材料")
	}
}

func TestQueuedCredentialRotationsKeepIndependentMaterials(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{"assetType": "git_token", "secretValue": "example-initial-material"})
	var ids []string
	for _, material := range []string{"example-first-material", "example-second-material"} {
		_, task, err := runtime.PrepareSecretAssetOperation(ctx, "rotate_secret_asset", domain.Object{"secretValue": material}, stringValue(asset["id"]))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	for i, expected := range []string{"example-first-material", "example-second-material"} {
		if result, err := runtime.RunWorkerOnce(ctx); err != nil || result.Processed != 1 {
			t.Fatal("执行轮换时未处理一个任务")
		}
		actual, err := store.GetSecretMaterial(ctx, stringValue(asset["secretRef"]))
		if err != nil || actual != expected {
			t.Fatalf("第%d次轮换读取了其他任务材料：%v", i+1, err)
		}
		run, _ := runtime.GetOperationRun(ctx, ids[i])
		if run["status"] != "succeeded" {
			t.Fatal("轮换未成功")
		}
	}
}

func TestCancelledCredentialCreationRemovesPendingAsset(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset, task, err := runtime.PrepareSecretAssetOperation(ctx, "create_secret_asset", domain.Object{"assetType": "git_token", "secretValue": "example-cancelled-material"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CancelTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if findObject(readState(t, ctx, runtime).SecretAssets, stringValue(asset["id"])) != nil {
		t.Fatal("取消后留下了不可用的凭据空壳")
	}
	if available, _ := store.HasSecretMaterial(ctx, stringValue(task.Payload["stagedSecretRef"])); available {
		t.Fatal("取消后暂存材料未删除")
	}
	run, err := runtime.GetOperationRun(ctx, task.ID)
	if err != nil || run["status"] != "cancelled" {
		t.Fatal("取消历史应继续可查")
	}
}

func TestPendingCredentialCannotBindThroughGenericServerWrites(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	asset, _, err := runtime.PrepareSecretAssetOperation(ctx, "create_secret_asset", domain.Object{"assetType": "ssh_key", "secretValue": validTestSSHPrivateKey}, "")
	if err != nil {
		t.Fatal(err)
	}
	input := domain.Object{"name": "示例服务器", "publicHost": "192.0.2.10", "secretAssetIds": []string{stringValue(asset["id"])}}
	if _, err := runtime.CreateObject(ctx, "servers", "server", input); err == nil || err.Error() != "SERVER_SSH_SECRET_MATERIAL_REQUIRED" {
		t.Fatalf("创建服务器绕过未生效凭据保护：%v", err)
	}
	server, err := runtime.CreateObject(ctx, "servers", "server", domain.Object{"name": "示例服务器", "publicHost": "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.UpdateObject(ctx, "servers", stringValue(server["id"]), input); err == nil || err.Error() != "SERVER_SSH_SECRET_MATERIAL_REQUIRED" {
		t.Fatalf("修改服务器绕过未生效凭据保护：%v", err)
	}
}
