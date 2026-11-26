package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestWorkerStartupCannotExpireLiveWorkerLeaseBeforeLock(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	config := platform.Config{StateDir: directory, DatabasePath: filepath.Join(directory, "state.sqlite"), RepositoryRoot: t.TempDir()}
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	expiry := time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = []domain.Task{{ID: "task_running", Action: "refresh_inventory", Status: "executing", TargetKind: "server", TargetID: "server_example", QueueOrder: 1, Attempt: 1, ClaimToken: "claim_previous", LeaseExpiresAt: expiry, CreatedAt: domain.Now(), UpdatedAt: domain.Now()}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	release, err := platform.AcquireWorkerLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	if opened, unlock, err := openWorkerState(ctx, config); err == nil {
		_ = opened.Close()
		unlock()
		release()
		t.Fatal("未取得进程锁仍打开了抢占入口")
	}
	state, err := store.LoadState(ctx)
	if err != nil || state.Tasks[0].LeaseExpiresAt != expiry {
		release()
		t.Fatal("第二个Worker改写了活动执行租约")
	}
	release()
	restarted, unlock, err := openWorkerState(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	defer restarted.Close()
	claimed, err := restarted.ClaimNextTask(ctx, 15*time.Minute)
	if err != nil || claimed == nil || claimed.ID != "task_running" || claimed.Attempt != 2 || claimed.ClaimToken == "claim_previous" {
		t.Fatal("持锁重启没有立即恢复新执行身份")
	}
}
