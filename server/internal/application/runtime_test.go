package application

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func TestLocalConfigContainsOnlyControlPlaneInformation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = tempDir
	config.DatabasePath = filepath.Join(tempDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = store.Close() }()

	value, err := NewRuntime(config, store).LocalConfig(ctx)
	if err != nil {
		t.Fatalf("local config: %v", err)
	}
	if _, ok := value["controlPlane"]; !ok {
		t.Fatal("control plane missing")
	}
	for _, forbidden := range []string{"runtime", "dependencies", "jenkins", "traefik", "dockerSwarm"} {
		if _, ok := value[forbidden]; ok {
			t.Fatalf("local config retained %s", forbidden)
		}
	}
}

func TestSingleUserRuntimeTaskLifecycle(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = tempDir
	config.DatabasePath = filepath.Join(tempDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() {
		_ = store.Close()
	}()
	runtime := NewRuntime(config, store)
	task, err := runtime.CreateTask(ctx, "restart_application", "application", "app_demo", "Demo", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	result, err := runtime.RunWorkerOnce(ctx)
	if err != nil {
		t.Fatalf("worker: %v", err)
	}
	if result.Processed != 1 {
		t.Fatalf("processed = %d", result.Processed)
	}
	updated, err := runtime.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if updated["status"] != "failed" {
		t.Fatal("不存在的应用必须执行失败")
	}
	operation, err := runtime.GetOperationRun(ctx, task.ID)
	if err != nil || operation["errorCode"] != "APPLICATION_NOT_FOUND" {
		t.Fatal("操作应保留应用不存在的错误码")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.OperationLogs) == 0 {
		t.Fatal("操作终态应写入操作日志")
	}
}
