package sqlite

import (
	"context"
	"database/sql"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"strings"
)

// 旧版套件会生成独立材料引用而不建凭据资产。仅迁入应用已经引用的套件材料，
// 已登记的 Git 和 SSH 凭据继续保留原用途。没有正文的引用保持缺失状态。
func migrateReferencedApplicationCredentials(ctx context.Context, tx *sql.Conn, state *domain.State) error {
	assets := map[string]domain.Object{}
	for _, a := range state.SecretAssets {
		assets[domain.StringValue(a["secretRef"])] = a
	}
	for _, cfg := range state.ApplicationDeploymentConfigs {
		for _, v := range domain.ObjectSliceFromAny(cfg["environmentVariables"]) {
			ref := domain.StringValue(v["secretRef"])
			if v["valueSource"] != "secret_ref" || !strings.HasPrefix(ref, "secret_env_") || assets[ref] != nil {
				continue
			}
			a := domain.Object{"id": domain.NewID("secretasset"), "assetType": "application_secret", "purpose": "application_variables", "name": domain.StringValue(v["key"]), "secretRef": ref, "maskedSummary": "********", "createdAt": domain.Now(), "updatedAt": domain.Now()}
			revision := domain.NewID("appmaterial")
			inserted, err := tx.ExecContext(ctx, "INSERT INTO secret_materials (secret_ref,value,created_at,updated_at) SELECT ?,value,created_at,updated_at FROM secret_materials WHERE secret_ref=?", revision, ref)
			if err != nil {
				return err
			}
			count, err := inserted.RowsAffected()
			if err != nil {
				return err
			}
			if count == 1 {
				a["materialRef"], a["revisionSecretRefs"] = revision, []string{revision}
			}
			assets[ref] = a
			state.SecretAssets = append(state.SecretAssets, a)
		}
	}
	return nil
}
