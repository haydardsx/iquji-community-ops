package application

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/adapter/sqlite"
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"github.com/haydardsx/iquji-community-ops/server/internal/platform"
)

func newTaskTestRuntime(t *testing.T) (*Runtime, *sqlite.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	config := platform.Resolve(platform.RoleAPI)
	config.RepositoryRoot = filepath.Join("..", "..", "..")
	config.StateDir = t.TempDir()
	config.DatabasePath = filepath.Join(config.StateDir, "state.sqlite")
	store, err := sqlite.Open(ctx, config.DatabasePath, config.RepositoryRoot)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewRuntime(config, store), store, ctx
}

func TestListOperationRunsFiltersByActionTargetAndStatus(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	first, err := runtime.CreateTask(ctx, "Deploy_Application", "application", "app_one", "Example API A", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimNextTask(ctx, -1); err != nil || claimed == nil || claimed.ID != first.ID {
		t.Fatal("could not claim the first queued task")
	}
	if err := store.CompleteTask(ctx, first.ID, "failed", "deployment failed"); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.CreateTask(ctx, "deploy_application", "application", "app_two", "Example API B", nil)
	if err != nil {
		t.Fatal(err)
	}
	third, err := runtime.CreateTask(ctx, "rotate_secret_asset", "secret_asset", "secret_one", "Deploy Key", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	_ = third

	page, err := runtime.ListOperationRuns(ctx, TaskQuery{Action: "DEPLOY", Target: "api", TargetKind: "application", Page: 1, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page["total"] != 2 {
		t.Fatal("operation filter returned an unexpected total")
	}
	items := page["items"].([]map[string]any)
	if len(items) != 1 || items[0]["id"] != second.ID {
		t.Fatal("first page did not contain the expected operation")
	}
	secondPage, err := runtime.ListOperationRuns(ctx, TaskQuery{Action: "DEPLOY", Target: "api", TargetKind: "application", Page: 2, PageSize: 1})
	if err != nil || len(secondPage["items"].([]map[string]any)) != 1 || secondPage["items"].([]map[string]any)[0]["id"] != first.ID {
		t.Fatal("second page did not contain the expected operation")
	}

	failed, err := runtime.ListOperationRuns(ctx, TaskQuery{Status: "failed", TargetID: "app_one", Page: 1, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	failedItems := failed["items"].([]map[string]any)
	if len(failedItems) != 1 || failedItems[0]["id"] != first.ID {
		t.Fatal("failed operation filter returned unexpected results")
	}
	if failedItems[0]["summary"] != "deployment failed" || failedItems[0]["errorCode"] != "OPERATION_FAILED" {
		t.Fatal("operation result is incomplete")
	}
	if _, exists := failedItems[0]["progress"]; exists {
		t.Fatal("operation result must not expose progress")
	}
}

func TestTaskCreationAndTransitionsAreGuarded(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	pending, err := runtime.CreateTask(ctx, "install_docker_engine", "server", "srv_one", "Server 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, duplicateErr := runtime.CreateTask(ctx, pending.Action, pending.TargetKind, pending.TargetID, pending.TargetName, nil)
	if duplicateErr != nil || duplicate.ID == pending.ID || duplicate.Status != "pending_confirmation" {
		t.Fatal("明确重复提交应创建独立待确认操作")
	}
	confirmed, err := runtime.ConfirmTask(ctx, pending.ID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "queued" {
		t.Fatal("confirmed task was not queued")
	}
	if _, err := runtime.ConfirmTask(ctx, pending.ID); err == nil || err.Error() != "TASK_NOT_CONFIRMABLE" {
		t.Fatalf("second confirmation error = %v", err)
	}
	claimed, err := store.ClaimNextTask(ctx, -1)
	if err != nil || claimed == nil {
		t.Fatal("could not claim a confirmed task")
	}
	if _, err := runtime.CancelTask(ctx, pending.ID); err == nil || err.Error() != "TASK_NOT_CANCELLABLE" {
		t.Fatalf("executing cancellation error = %v", err)
	}

	cancellable, err := runtime.CreateTask(ctx, "install_monitoring_agent", "server", "srv_two", "Server 2", nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := runtime.CancelTask(ctx, cancellable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != "cancelled" {
		t.Fatal("cancelled task returned an unexpected state")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cancelledRun domain.OperationRun
	for _, run := range state.OperationRuns {
		if run.ID == cancellable.ID {
			cancelledRun = run
		}
	}
	if cancelledRun.Status != "cancelled" || cancelledRun.Summary != "操作已取消" {
		t.Fatal("cancelled operation returned an unexpected state")
	}
	queued, err := runtime.CreateTask(ctx, "refresh_server_inventory", "server", "srv_three", "Server 3", nil)
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" {
		t.Fatal("safe task should queue immediately")
	}
	if _, err := runtime.CancelTask(ctx, queued.ID); err != nil {
		t.Fatalf("cancel queued task: %v", err)
	}
}

func TestCreateTaskUsesServerConfirmationPolicy(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	for index, action := range []string{"request_jenkins_release", "REQUEST_JENKINS_RELEASE", "verify_certificate"} {
		task, err := runtime.CreateTask(ctx, action, "policy_target", fmt.Sprintf("danger_%d", index), action, nil)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != "pending_confirmation" || task.ConfirmationTokenID == "" {
			t.Fatal("dangerous action bypassed confirmation policy")
		}
	}
	safe, err := runtime.CreateTask(ctx, "refresh_inventory", "policy_target", "safe", "safe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if safe.Status != "queued" || safe.ConfirmationTokenID != "" {
		t.Fatal("safe task returned an unexpected confirmation policy")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ConfirmationTokens) != 3 {
		t.Fatal("confirmation tokens")
	}
}

func TestCompleteTaskRejectsInvalidTransitions(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	task, err := runtime.CreateTask(ctx, "refresh_inventory", "server", "srv_one", "Server 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTask(ctx, task.ID, "succeeded", "done"); err == nil || err.Error() != "TASK_NOT_COMPLETABLE" {
		t.Fatalf("queued completion error = %v", err)
	}
	if _, err := store.ClaimNextTask(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTask(ctx, task.ID, "unknown", "done"); err == nil || err.Error() != "TASK_TERMINAL_STATUS_INVALID" {
		t.Fatalf("invalid terminal status error = %v", err)
	}
	if err := store.CompleteTask(ctx, task.ID, "succeeded", "done"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCanCompleteExecutingTaskAsCancelled(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	task, err := runtime.CreateTask(ctx, "refresh_inventory", "server", "srv_one", "Server 1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTask(ctx, task.ID, "cancelled", "清理完成"); err == nil || err.Error() != "TASK_NOT_COMPLETABLE" {
		t.Fatalf("未领取的任务不应直接完成取消: %v", err)
	}
	if _, err := store.ClaimNextTask(ctx, -1); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTask(ctx, task.ID, "cancelled", "清理完成"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || state.Tasks[0].Status != "cancelled" || state.OperationRuns[0].Status != "cancelled" {
		t.Fatal("任务与操作终态应一致")
	}
}
