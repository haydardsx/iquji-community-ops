package application

import (
	"context"
	"errors"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// executeSecretAssetOperation 领取后生效本次材料，旧任务继续核验已保存的材料。
func (r *Runtime) executeSecretAssetOperation(ctx context.Context, task domain.Task) (string, error) {
	if stringValue(task.Payload["stagedSecretRef"]) != "" {
		return r.applyStagedSecretAssetTask(ctx, task)
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "凭据资产读取失败", err
	}
	asset := findObject(state.SecretAssets, task.TargetID)
	if task.TargetKind != "secret_asset" || asset == nil {
		return "凭据资产不存在", errors.New("SECRET_ASSET_NOT_FOUND")
	}
	assetType := stringValue(asset["assetType"])
	purpose, exists := secretAssetPurposes[assetType]
	if !exists {
		return "凭据资产类型无效", errors.New("SECRET_ASSET_TYPE_INVALID")
	}
	if stringValue(asset["purpose"]) != purpose {
		return "凭据资产用途无效", errors.New("SECRET_ASSET_PURPOSE_INVALID")
	}
	secretRef := stringValue(asset["secretRef"])
	if secretRef == "" {
		return "凭据材料不存在", errors.New("SECRET_MATERIAL_NOT_FOUND")
	}
	material, err := r.Store.GetSecretMaterial(ctx, secretRef)
	if err != nil {
		return "凭据材料读取失败", err
	}
	if strings.TrimSpace(material) == "" {
		return "凭据材料为空", errors.New("SECRET_MATERIAL_REQUIRED")
	}
	if err := validateSecretAssetMaterial(assetType, material); err != nil {
		return "SSH 私钥材料无效", errors.New("SSH_PRIVATE_KEY_INVALID")
	}
	if task.Action == string(domain.TaskActionRotateSecretAsset) {
		return "凭据轮换材料已保存并通过校验", nil
	}
	return "凭据资产与材料已保存并通过校验", nil
}
