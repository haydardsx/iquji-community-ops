package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRegistrySyncConfigurationAndReleaseFacts(t *testing.T) {
	sync := domain.Object{"enabled": true, "imageRepository": "registry.example.org:5000/team/app"}
	if err := validateRegistrySync(sync); err != nil {
		t.Fatal(err)
	}
	for _, repository := range []string{"team/app", "registry.example.org/app:latest", "https://registry.example.org/app", "registry.example.org:99999/app"} {
		if err := validateRegistrySync(domain.Object{"enabled": true, "imageRepository": repository}); err == nil {
			t.Fatal("无效目标通过了校验", repository)
		}
	}
	sync["registryUsername"] = "demo"
	if err := validateRegistrySync(sync); err == nil {
		t.Fatal("缺少推送凭据")
	}
	sync["registrySecretRef"] = "secret_example"
	if err := validateRegistryCredential(domain.State{}, sync); err == nil {
		t.Fatal("无效的凭据引用")
	}
	snapshot := domain.Object{"slotPlatforms": domain.Object{"web": "linux/amd64"}, "swarmRuntime": domain.Object{"services": []domain.Object{{"slotKey": "web", "serverId": "server"}}}, "deliveryConfig": domain.Object{"registrySync": sync}}
	plan := domain.Object{"applicationId": "app", "buildSnapshot": snapshot}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fact := domain.Object{"image": "iquops/app:run-0-amd64", "serverId": "server", "serviceSlotKey": "web", "platform": "linux/amd64", "configDigest": digest, "manifestDigest": digest, "imageDigest": digest}
	facts := []domain.Object{fact}
	if _, err := verifiedGitArtifactFacts(facts, plan, "run"); err == nil {
		t.Fatal("缺少仓库回读证据")
	}
	fact["registryImage"] = "registry.example.org:5000/team/app:run-0-amd64"
	fact["registryDigest"] = digest
	if _, err := verifiedGitArtifactFacts(facts, plan, "run"); err != nil {
		t.Fatal(err)
	}
	fact["platform"], fact["image"], fact["registryImage"] = "linux/arm64", "iquops/app:run-0-arm64", "registry.example.org:5000/team/app:run-0-arm64"
	if _, err := verifiedGitArtifactFacts(facts, plan, "run"); err == nil {
		t.Fatal("槽位接受了其他架构的镜像")
	}
	fact["platform"], fact["image"] = "linux/amd64", "iquops/app:run-0-amd64"
	fact["registryImage"] = "registry.example.org/team/other:run-0-amd64"
	if _, err := verifiedGitArtifactFacts(facts, plan, "run"); err == nil {
		t.Fatal("接受了非本次发布目标")
	}
}

func TestRegistrySyncCredentialRotationAndDelete(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	asset := seedSecretAsset(t, ctx, runtime, domain.Object{"name": "示例仓库凭据", "assetType": "registry_secret", "secretValue": "test-first-value"})
	ref := asset["secretRef"]
	cfg := domain.Object{"applicationId": "app", "deliveryConfig": domain.Object{"registrySync": domain.Object{
		"enabled": true, "imageRepository": "registry.example.test/team/image", "registryUsername": "demo", "registrySecretRef": ref,
	}}}
	plan := domain.Object{"buildSnapshot": cfg, "registryRevisionSecretRef": asset["materialRef"]}
	if err := store.MutateState(ctx, func(state *domain.State) error { state.ApplicationDeploymentConfigs = []domain.Object{cfg}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RotateSecretAsset(ctx, stringValue(asset["id"]), domain.Object{"secretValue": "test-second-value"}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	materials, err := runtime.registryPushMaterials(ctx, state, plan)
	if err != nil || materials["password"] != "test-first-value" {
		t.Fatal("已创建发布计划须读取原凭据修订", err)
	}
	if err := runtime.DeleteSecretAsset(ctx, stringValue(asset["id"])); err == nil || err.Error() != "SECRET_ASSET_REFERENCED_BY_REGISTRY" {
		t.Fatal("仓库引用未阻止删除", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.ApplicationDeploymentConfigs = nil
		plan["applicationId"], plan["status"] = "app", "queued"
		state.ReleasePlans = []domain.Object{plan}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteSecretAsset(ctx, stringValue(asset["id"])); err == nil || err.Error() != "SECRET_ASSET_REFERENCED_BY_REGISTRY" {
		t.Fatal("配置已改动时待执行计划未保住固定的凭据修订", err)
	}
}

func TestRegistryCredentialsRemainPurposeBoundAcrossUpgrades(t *testing.T) {
	r, _, ctx := newFrpRuntime(t)
	app := seedSecretAsset(t, ctx, r, domain.Object{"name": "示例变量", "assetType": "application_secret", "secretValue": "example-variable"})
	registry := seedSecretAsset(t, ctx, r, domain.Object{"name": "示例推送", "assetType": "registry_secret", "secretValue": "example-push"})
	state := readState(t, ctx, r)
	sync := domain.Object{"enabled": true, "imageRepository": "registry.example.test/team/app", "registrySecretRef": app["secretRef"]}
	cfg := domain.Object{"applicationId": "app", "sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/sample.git"}, "deliveryConfig": domain.Object{"registrySync": sync}}
	state.Applications = []domain.Object{{"id": "app"}}
	state.ApplicationDeploymentConfigs = []domain.Object{cfg}
	plan := domain.Object{"buildSnapshot": cfg, "registryRevisionSecretRef": app["materialRef"]}
	for _, username := range []string{"", "example"} {
		sync["registryUsername"] = username
		if _, err := applicationReleaseConfig(state, "app", nil); err == nil {
			t.Fatal("旧引用在发布预检时未阻断")
		}
		if out, err := r.registryPushMaterials(ctx, state, plan); out != nil || err == nil {
			t.Fatal("旧引用被解释为推送凭据或降级至匿名推送")
		}
		if sync["registrySecretRef"] != app["secretRef"] {
			t.Fatal("旧配置未保留原引用")
		}
	}
	if material, err := r.readApplicationMaterial(ctx, state, domain.Object{"secretRef": registry["secretRef"]}); err == nil || material != "" {
		t.Fatal("应用变量能够读取镜像仓库专用凭据")
	}
	sync["registrySecretRef"] = registry["secretRef"]
	for _, revision := range []any{nil, registry["secretRef"], app["materialRef"]} {
		plan["registryRevisionSecretRef"] = revision
		if out, err := r.registryPushMaterials(ctx, state, plan); out != nil || err == nil {
			t.Fatal("发布接受了未固定或其他用途的凭据修订")
		}
	}
	if material, err := r.readApplicationMaterial(ctx, state, domain.Object{"secretRef": app["secretRef"]}); err != nil || material != "example-variable" {
		t.Fatal("旧引用影响了原应用变量")
	}
}
