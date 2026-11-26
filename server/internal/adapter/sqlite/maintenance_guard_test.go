package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestMaintenanceTransactionRechecksActivitiesAndPreviewState(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTestStore(t, ctx)
	defer store.Close()
	digest := func() string {
		t.Helper()
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(encoded)
		return hex.EncodeToString(hash[:])
	}
	guarded := WithMaintenanceGuard(ctx, digest())
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = append(state.Tasks, domain.Task{ID: "task_after_preview", Status: "queued"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearOperationalHistory(guarded); err == nil || err.Error() != "DATA_MAINTENANCE_ACTIVE_TASK" {
		t.Fatalf("预检后入队任务被擦除: %v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.Tasks) != 1 {
		t.Fatal("维护失败后任务没有保留")
	}
	if err := store.MutateState(ctx, func(state *domain.State) error { state.Tasks[0].Status = "succeeded"; return nil }); err != nil {
		t.Fatal(err)
	}
	guarded = WithMaintenanceGuard(ctx, digest())
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = append(state.Projects, domain.Object{"id": "project_after_preview"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.FactoryReset(guarded, domain.EmptyState()); err == nil || err.Error() != "DATA_MAINTENANCE_STATE_CHANGED" {
		t.Fatalf("预检后新增配置被擦除: %v", err)
	}
	state, err = store.LoadState(ctx)
	if err != nil || len(state.Projects) != 2 {
		t.Fatal("事务内拒绝维护后配置没有保留")
	}
	if err := store.ClearOperationalHistory(WithMaintenanceGuard(ctx, digest())); err != nil {
		t.Fatalf("没有变化时维护应成功: %v", err)
	}
}
