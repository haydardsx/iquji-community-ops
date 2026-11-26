package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestConcurrentCredentialRotationsMaskCrossProjectHistory(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	random := func() string {
		raw := make([]byte, 24)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(raw)
	}
	old, one, two := random(), random(), random()
	asset := seedSecretAsset(t, ctx, r, domain.Object{"assetType": "application_secret", "name": "示例凭据", "secretValue": old})
	owner := domain.Object{"id": "producer", "projectId": "p1"}
	consumer := domain.Object{"id": "consumer", "projectId": "p2"}
	cfg := domain.Object{"id": "cfg_consumer", "applicationId": "consumer", "environmentVariables": []domain.Object{{"stage": "runtime", "key": "VALUE", "valueSource": "output_ref", "outputApplicationId": "producer", "outputId": "out"}}}
	if err := store.MutateState(ctx, func(s *domain.State) error {
		s.Applications = []domain.Object{owner, consumer}
		s.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "producer", "outputVariables": []domain.Object{{"id": "out", "key": "SHARED_VALUE", "valueSource": "secret_ref", "secretRef": asset["secretRef"], "visibility": "cross_project"}}}, cfg}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state := readState(t, ctx, r)
	frozen, err := applicationInputSnapshot(state, "consumer", cfg)
	if err != nil {
		t.Fatal(err)
	}
	type prepared struct {
		task domain.Task
		err  error
	}
	ready := make(chan prepared, 2)
	for _, value := range []string{one, two} {
		go func() {
			_, task, err := r.PrepareSecretAssetOperation(ctx, "rotate_secret_asset", domain.Object{"secretValue": value}, stringValue(asset["id"]))
			ready <- prepared{task, err}
		}()
	}
	ops := []string{}
	for i := 0; i < 2; i++ {
		res := <-ready
		if res.err != nil {
			t.Fatal(res.err)
		}
		ops = append(ops, res.task.ID)
	}
	for _, id := range ops {
		if _, err := r.ConfirmTask(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	for range ops {
		if _, err := r.RunWorkerOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ops {
		result, err := r.GetOperationRun(ctx, id)
		if err != nil || result["status"] != "succeeded" {
			t.Fatal("同资源并发轮换未串行完成", err, result["errorCode"])
		}
	}
	state = readState(t, ctx, r)
	revisions := objectStringSlice(findObject(state.SecretAssets, stringValue(asset["id"]))["revisionSecretRefs"])
	if len(revisions) != 3 {
		t.Fatal("轮换覆盖了已发生的材料修订")
	}
	prior, err := r.resolvedStageValues(ctx, frozen, "runtime", "")
	if err != nil || prior["VALUE"] != old {
		t.Fatal("并发轮换改变了已冻结发布材料")
	}
	mask, err := r.applicationLogSecrets(ctx, "consumer", "")
	if err != nil {
		t.Fatal(err)
	}
	raw := "2026-01-01T00:00:00Z " + strings.Join([]string{old, one, two}, " ")
	items, _ := parseApplicationServiceLogs(raw, ApplicationServiceState{ServiceSlotKey: "main"}, mask, false)
	if len(items) != 1 {
		t.Fatal("应用日志读取失败")
	}
	for _, secret := range []string{old, one, two} {
		if strings.Contains(items[0].Message, secret) {
			t.Fatal("跨项目应用日志泄露旧修订或当前材料")
		}
	}
	rev, err := r.resolvedStageValues(context.Background(), frozen, "runtime", "")
	if err != nil || rev["VALUE"] != old {
		t.Fatal("旧任务重试未使用冻结的版本")
	}
}
