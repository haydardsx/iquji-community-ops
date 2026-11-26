package application

import (
	"context"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildRecoveryKeepsClaimAndRejectsReplacedAttemptCheckpoint(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimNextTask(ctx, -1)
	if err != nil || old == nil {
		t.Fatalf("首次领取失败: %v", err)
	}
	f := newSwarmRebuildFixture()
	f.failBackup = true
	input := f.input()
	input.SwarmRebuildID = old.ID
	save := runtime.swarmRebuildCheckpointSaver(old.ID, old.ID)
	var replacement *domain.Task
	recoveryWrites := 0
	input.SaveSwarmRebuildCheckpoint = func(saveCtx context.Context, checkpoint domain.Object) error {
		if checkpoint["phase"] != "prepared" {
			recoveryWrites++
		}
		claim, ok := domain.TaskClaimFromContext(saveCtx)
		if !ok || claim.ClaimToken != old.ClaimToken {
			t.Error("实际恢复回调丢失了原执行身份")
		}
		if err := save(saveCtx, checkpoint); err != nil {
			return err
		}
		if checkpoint["phase"] == "prepared" {
			if err := store.ExpirePreviousWorkerLeases(ctx); err != nil {
				t.Fatal(err)
			}
			var err error
			replacement, err = store.ClaimNextTask(ctx, 15*time.Minute)
			if err != nil || replacement == nil || replacement.Attempt <= old.Attempt {
				t.Fatalf("恢复领取失败: %v", err)
			}
		}
		return nil
	}
	_, err = executeSwarmRebuild(domain.WithTaskClaim(ctx, *old), input, f.remote())
	if err == nil || err.Error() != "SWARM_REBUILD_ROLLBACK_FAILED" || recoveryWrites == 0 {
		t.Fatalf("没有执行并阻断旧恢复写回: writes=%d err=%v", recoveryWrites, err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current := findTask(state.Tasks, old.ID)
	if current.Attempt != replacement.Attempt || current.ClaimToken != replacement.ClaimToken || objectMapFromAny(current.Payload["swarmRebuildCheckpoint"])["phase"] != "prepared" {
		t.Fatal("旧执行的实际恢复分支覆盖了新 attempt 检查点")
	}
}

func TestSwarmDepartureCleanupKeepsClaimAfterCancellationAndRejectsOldCallback(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateTask(ctx, "uninstall_docker_swarm", "server", "srv_gateway", "示例节点", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimNextTask(ctx, -1)
	if err != nil || old == nil {
		t.Fatalf("首次领取失败: %v", err)
	}
	input, err := runtime.serverRuntimeExecutionInput(domain.WithTaskClaim(ctx, *old), old.TargetID, *old)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ExpirePreviousWorkerLeases(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ClaimNextTask(ctx, 15*time.Minute)
	if err != nil || replacement == nil {
		t.Fatalf("恢复领取失败: %v", err)
	}
	record := domain.Object{"nodeId": "node_example", "clusterId": "cluster_example", "cleanupStatus": "pending"}
	oldCtx, cancelOld := context.WithCancel(domain.WithTaskClaim(ctx, *old))
	cancelOld()
	if err := saveSwarmWorkerDepartureAfterLeave(oldCtx, input, record); err == nil {
		t.Fatal("取消后的旧执行清理绕过了租约检查")
	}
	// 即使执行器错误使用 Background，正式回调也必须坚持创建输入时的执行身份。
	if err := input.SaveSwarmDeparture(context.Background(), record); err == nil {
		t.Fatal("丢失上下文的旧回调仍能写回")
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(objectSlice(state.Servers[0]["swarmDepartures"])) != 0 {
		t.Fatal("被替换的执行污染了离群事实")
	}
	freshCtx, cancelFresh := context.WithCancel(domain.WithTaskClaim(ctx, *replacement))
	fresh, err := runtime.serverRuntimeExecutionInput(freshCtx, replacement.TargetID, *replacement)
	if err != nil {
		t.Fatal(err)
	}
	saveFresh := fresh.SaveSwarmDeparture
	fresh.SaveSwarmDeparture = func(saveCtx context.Context, record domain.Object) error {
		claim, ok := domain.TaskClaimFromContext(saveCtx)
		if !ok || claim.ClaimToken != replacement.ClaimToken {
			t.Error("离群清理脱离取消时丢失执行身份")
		}
		return saveFresh(saveCtx, record)
	}
	cancelFresh()
	if err := saveSwarmWorkerDepartureAfterLeave(freshCtx, fresh, record); err != nil {
		t.Fatalf("有效执行在取消后无法保存必要清理事实: %v", err)
	}
	state, err = store.LoadState(ctx)
	departures := objectSlice(state.Servers[0]["swarmDepartures"])
	if err != nil || len(departures) != 1 || departures[0]["operationRunId"] != replacement.ID {
		t.Fatal("当前执行的取消清理未正确落库")
	}
}
