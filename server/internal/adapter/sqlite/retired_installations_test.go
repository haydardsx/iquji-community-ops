package sqlite

import (
	"context"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"path/filepath"
	"testing"
)

func TestUpgradePreservesRetiredInstallations(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	file := filepath.Join(root, "state.sqlite")
	store, err := Open(ctx, file, root)
	if err != nil {
		t.Fatal(err)
	}
	const installations = `[{"id":"instance_example","volume":"volume_example"}]`
	if _, err = store.db.ExecContext(ctx, `UPDATE app_state SET state=json_set(state,'$.environmentInstallations',json(?),'$.projects',json('[{"id":"project_example"}]'))`, installations); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, "DELETE FROM _migrations WHERE id='0009_retired_installations.sql'"); err != nil {
		t.Fatal(err)
	}
	const material = "example-value"
	if err = store.UpsertSecretMaterial(ctx, "secret_env_example", material); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `UPDATE app_state SET state=json_set(state,'$.applicationDeploymentConfigs',json('[{"applicationId":"app_example","environmentVariables":[{"key":"ACCESS","valueSource":"secret_ref","secretRef":"secret_env_example"},{"key":"ADDRESS","valueSource":"environment_value","plainValue":"example.test"}]}]'))`); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		store, err = Open(ctx, file, root)
		if err != nil {
			t.Fatal(err)
		}
		var archive string
		if err = store.db.QueryRowContext(ctx, "SELECT installations FROM retired_environment_state WHERE id=?", currentStateID).Scan(&archive); err != nil || archive != installations {
			t.Fatalf("旧实例归档发生变化: %v", err)
		}
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.SecretAssets) != 1 || state.SecretAssets[0]["assetType"] != "application_secret" {
			t.Fatal("旧引用应只生成一次应用凭据")
		}
		revision := domain.StringValue(state.SecretAssets[0]["materialRef"])
		actual, err := store.GetSecretMaterial(ctx, revision)
		if revision == "" || err != nil || actual != material {
			t.Fatal("凭据的初始修订材料不可用")
		}
		vars := domain.ObjectSliceFromAny(state.ApplicationDeploymentConfigs[0]["environmentVariables"])
		if vars[1]["stage"] != "runtime" || vars[1]["valueSource"] != "plain_text" || vars[1]["plainValue"] != "example.test" {
			t.Fatal("旧变量未能保留实际值与阶段")
		}
		var n int
		if err = store.db.QueryRowContext(ctx, `SELECT count(*) FROM app_state,json_each(state,'$.projects') WHERE json_extract(value,'$.id')='project_example'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("常规升级丢失原项目: %v", err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
