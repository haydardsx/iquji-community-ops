package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 获取材料前按正式资产用途校验；归档、旧任务和重试与配置保存使用相同边界。
func credentialForPurpose(state domain.State, ref, kind string) domain.Object {
	if ref == "" {
		return nil
	}
	var found domain.Object
	for _, asset := range state.SecretAssets {
		if asset["secretRef"] != ref {
			if asset["materialRef"] == ref || containsString(objectStringSlice(asset["revisionSecretRefs"]), ref) {
				return nil
			}
			continue
		}
		if found != nil || asset["assetType"] != kind || asset["purpose"] != secretAssetPurposes[kind] || stringValue(asset["pendingCreationOperationId"]) != "" {
			return nil
		}
		found = asset
	}
	return found
}

func (r *Runtime) readGitSourceToken(ctx context.Context, state domain.State, source domain.Object) (string, error) {
	mode, ref := stringValue(source["authMode"]), stringValue(source["authSecretRef"])
	if ref == "" && (mode == "" || mode == "none") {
		return "", nil
	}
	if mode != "" && mode != "git_token" {
		return "", errors.New("JENKINS_GIT_CREDENTIAL_MODE_INVALID")
	}
	if ref == "" {
		return "", errors.New("JENKINS_GIT_CREDENTIAL_REFERENCE_REQUIRED")
	}
	if credentialForPurpose(state, ref, "git_token") == nil {
		return "", errors.New("APPLICATION_GIT_SECRET_INVALID")
	}
	value, err := r.Store.GetSecretMaterial(ctx, ref)
	if err != nil || value == "" {
		return "", errors.New("JENKINS_GIT_CREDENTIAL_MATERIAL_REQUIRED")
	}
	return value, nil
}

func (r *Runtime) readApplicationMaterial(ctx context.Context, state domain.State, variable domain.Object) (string, error) {
	return r.readPurposeMaterial(ctx, state, variable, "application_secret")
}

func (r *Runtime) readPurposeMaterial(ctx context.Context, state domain.State, variable domain.Object, kind string) (string, error) {
	ref := stringValue(variable["secretRef"])
	asset := credentialForPurpose(state, ref, kind)
	if asset == nil {
		return "", errors.New("APPLICATION_CREDENTIAL_INVALID")
	}
	rev := firstNonEmptyString(stringValue(variable["materialRevisionRef"]), ref)
	if rev != ref && !containsString(objectStringSlice(asset["revisionSecretRefs"]), rev) {
		return "", errors.New("APPLICATION_CREDENTIAL_INVALID")
	}
	// 修订引用不能借用任何其他资产的正式材料或不可变修订。
	for _, other := range state.SecretAssets {
		if other["id"] != asset["id"] && (other["secretRef"] == rev || other["materialRef"] == rev || containsString(objectStringSlice(other["revisionSecretRefs"]), rev)) {
			return "", errors.New("APPLICATION_CREDENTIAL_INVALID")
		}
	}
	value, err := r.Store.GetSecretMaterial(ctx, rev)
	if err != nil || value == "" {
		return "", errors.New("APPLICATION_CREDENTIAL_MATERIAL_MISSING")
	}
	return value, nil
}
