package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSourceConfigRejectsCredentialsInRepositoryURL(t *testing.T) {
	for _, suffix := range []string{"?token=example", "#example"} {
		source := domain.Object{"provider": "git", "repositoryUrl": "https://example.test/sample.git" + suffix}
		if saved, err := validateApplicationSourceConfig(source); err == nil || saved != nil || err.Error() != "APPLICATION_GIT_URL_INVALID" {
			t.Fatal("repository URL with query or fragment was accepted")
		}
	}
	source := domain.Object{"provider": "git", "repositoryUrl": "https://example.test/sample.git"}
	if _, err := validateApplicationSourceConfig(source); err != nil {
		t.Fatal("plain repository URL was rejected")
	}
}

func TestGitSnapshotMaterialChecksAssetPurposeAtBothBoundaries(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	git := seedSecretAsset(t, ctx, r, domain.Object{"name": "示例 Git 凭据", "assetType": "git_token", "secretValue": "example-git-token"})
	app := seedSecretAsset(t, ctx, r, domain.Object{"name": "示例应用凭据", "assetType": "application_secret", "secretValue": "example-application-material"})
	ssh := seedSecretAsset(t, ctx, r, domain.Object{"name": "示例部署密钥", "assetType": "ssh_key", "secretValue": validTestSSHPrivateKey})
	state := readState(t, ctx, r)
	state.Servers = append(state.Servers, domain.Object{"id": "node", "secretAssetIds": []string{stringValue(ssh["id"])}})
	source := domain.Object{"provider": "git", "repositoryUrl": "https://example.test/demo.git", "authMode": "git_token"}
	build := domain.Object{"buildServerId": "node", "sourceSnapshot": source}
	plan := domain.Object{"buildServerId": "node", "deploymentManagerServerId": "node", "buildSnapshot": build}
	run := domain.Object{"materialsSnapshot": build}
	for _, test := range []struct{ label, mode, ref string }{{"部署密钥误作 Git token", "git_token", stringValue(ssh["secretRef"])}, {"应用凭据误作 Git token", "git_token", stringValue(app["secretRef"])}, {"历史 SSH 模式", "ssh_key", stringValue(ssh["secretRef"])}, {"跳过鉴权的引用", "none", stringValue(git["secretRef"])}} {
		t.Run(test.label, func(t *testing.T) {
			source["authMode"], source["authSecretRef"] = test.mode, test.ref
			if value, err := r.JenkinsGitCredentialMaterial(ctx, run); err == nil || value != nil {
				t.Fatal("运行令牌端点接受了错误用途的材料")
			}
			if value, err := r.trustedJenkinsMaterials(ctx, state, plan); err == nil || value != nil {
				t.Fatal("受信执行材料通道接受了错误用途的材料")
			}
		})
	}
	source["authMode"], source["authSecretRef"] = "git_token", git["secretRef"]
	v, err := r.JenkinsGitCredentialMaterial(ctx, run)
	if err != nil || v["token"] != "example-git-token" {
		t.Fatal("合法 Git 凭据无法解析", err)
	}
	if err := store.MutateState(ctx, func(s *domain.State) error {
		a := findObject(s.SecretAssets, stringValue(git["id"]))
		a["purpose"] = "deployment_server"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if v, err := r.JenkinsGitCredentialMaterial(ctx, run); err == nil || v != nil {
		t.Fatal("类型正确但用途被篡改仍可以读取")
	}
	fake := domain.Object{"key": "SHOULD_FAIL", "stage": "build", "valueSource": "secret_ref", "secretRef": app["secretRef"], "materialRevisionRef": ssh["secretRef"]}
	for _, mutate := range []bool{false, true} {
		if mutate {
			if err := store.MutateState(ctx, func(s *domain.State) error {
				a := findObject(s.SecretAssets, stringValue(app["id"]))
				a["revisionSecretRefs"] = append(objectStringSlice(a["revisionSecretRefs"]), stringValue(ssh["secretRef"]))
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if v, err := r.resolvedStageValues(ctx, []domain.Object{fake}, "build", ""); err == nil || v != nil {
			t.Fatal("历史运行输入可冒用其他资产的材料修订")
		}
	}
}
