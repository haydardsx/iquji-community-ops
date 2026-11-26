package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationReadVersionRequiresCurrentTerminalAndPersists(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.OperationRuns = []domain.OperationRun{{ID: "operation_example", Status: "running", UpdatedAt: "2026-01-01T00:00:00Z"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOperationRead(ctx, "operation_example", "running", "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("活动操作不能标记已读")
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.OperationRuns[0].Status = "succeeded"
		state.OperationRuns[0].UpdatedAt = "2026-01-01T00:01:00Z"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkOperationRead(ctx, "operation_example", "succeeded", "2026-01-01T00:00:00Z"); err == nil {
		t.Fatal("旧版本不能清除新结果")
	}
	if err := store.MarkOperationRead(ctx, "operation_example", "succeeded", "2026-01-01T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := store.OperationReadVersions(ctx)
	if err != nil || versions["operation_example"] != "succeeded:2026-01-01T00:01:00Z" {
		t.Fatalf("已读状态未持久化：%v %v", versions, err)
	}
	if err := store.ClearOperationalHistory(ctx); err != nil {
		t.Fatal(err)
	}
	versions, err = store.OperationReadVersions(ctx)
	if err != nil || len(versions) != 0 {
		t.Fatalf("清空日志未清理已读记录：%v %v", versions, err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.OperationRuns) != 0 {
		t.Fatalf("清空日志仍残留操作记录：%v", err)
	}
}

func TestOperationReadMigrationAcknowledgesOnlyExistingTerminal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store, err := Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	state := domain.EmptyState()
	state.OperationRuns = []domain.OperationRun{
		{ID: "operation_old", Status: "failed", UpdatedAt: "2026-01-01T00:00:00Z"},
		{ID: "operation_active", Status: "running", UpdatedAt: "2026-01-01T00:00:00Z"},
	}
	raw, _ := json.Marshal(state)
	if _, err := store.db.ExecContext(ctx, "UPDATE app_state SET state = ? WHERE id = ?", string(raw), currentStateID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "DROP TABLE operation_read_versions"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM _migrations WHERE id = ?", "0008_operation_read_versions.sql"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	versions, err := store.OperationReadVersions(ctx)
	if err != nil || len(versions) != 1 || versions["operation_old"] == "" {
		t.Fatalf("迁移基线不正确：%v %v", versions, err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.OperationRuns[1].Status = "succeeded"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	versions, err = store.OperationReadVersions(ctx)
	if err != nil || versions["operation_active"] != "" {
		t.Fatal("升级后完成的操作应保持未读")
	}
}
