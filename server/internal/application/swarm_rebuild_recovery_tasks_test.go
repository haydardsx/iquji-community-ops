package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildExplicitRecoveryKeepsBackupOwnerAndOriginalHistory(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	original, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := rebuildTaskCheckpoint(original.ID, "backed_up")
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		if err := input.SaveSwarmRebuildCheckpoint(ctx, checkpoint); err != nil {
			t.Fatal(err)
		}
		return nil, errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
	}}
	if _, err := runtime.ConfirmTask(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertRecovery := func(operationID string, needsRecovery bool) {
		t.Helper()
		run, err := runtime.GetOperationRun(ctx, operationID)
		if err != nil {
			t.Fatal(err)
		}
		public := objectMapFromAny(objectMapFromAny(run["details"])["swarmRebuildRecovery"])
		if public["needsRecovery"] != needsRecovery || public["operationRunId"] != original.ID {
			t.Fatal("恢复入口投影错误")
		}
		raw, err := json.Marshal(run)
		if err != nil || strings.Contains(string(raw), "manifestHash") || strings.Contains(string(raw), "swarmRebuildCheckpoint") {
			t.Fatal("操作详情泄露了内部备份字段")
		}
	}
	assertRecovery(original.ID, true)
	if _, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "server_other", original.ID); err == nil || err.Error() != "SWARM_REBUILD_RECOVERY_INVALID" {
		t.Fatal("恢复入口允许使用其他服务器的备份")
	}
	recovery, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", original.ID)
	if err != nil || recovery.ID == original.ID || recovery.Status != "pending_confirmation" {
		t.Fatalf("显式恢复未创建独立确认操作：%v", err)
	}
	assertRecovery(original.ID, false)
	assertRecovery(recovery.ID, false)
	repeated, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", original.ID)
	if err != nil || repeated.ID == recovery.ID {
		t.Fatal("重复确认意图应独立排队")
	}
	if _, err := runtime.CancelTask(ctx, repeated.ID); err != nil {
		t.Fatal(err)
	}
	if state, err := store.LoadState(ctx); err != nil || findTask(state.Tasks, recovery.ID).Status != "pending_confirmation" {
		t.Fatalf("取消后项影响原恢复: %v", err)
	}
	calls := 0
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		rebuildSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
			calls++
			if !input.SwarmRebuildRestoreOnly || input.SwarmRebuildID != original.ID || input.SwarmRebuildCheckpoint["manifestHash"] != checkpoint["manifestHash"] {
				t.Fatal("显式恢复改用了新任务备份或新基线")
			}
			next := cloneObject(checkpoint)
			next["phase"] = "recovered"
			if err := input.SaveSwarmRebuildCheckpoint(ctx, next); err != nil {
				t.Fatal(err)
			}
			return nil, errSwarmRebuildRecovered
		},
		probeResult: domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmNodeId": "node_restored", "swarmClusterId": "cluster_restored", "swarmVerificationStatus": "verified"}},
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil || calls != 0 {
		t.Fatal("显式恢复确认前执行了远端恢复")
	}
	if _, err := runtime.ConfirmTask(ctx, recovery.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || calls != 1 || findTask(state.Tasks, recovery.ID).Status != "succeeded" || findTask(state.Tasks, original.ID).Status != "failed" {
		t.Fatalf("显式恢复成功终态或原历史被改写：%v", err)
	}
	originalRun, err := runtime.GetOperationRun(ctx, original.ID)
	if err != nil || originalRun["errorCode"] != "SWARM_REBUILD_ROLLBACK_FAILED" {
		t.Fatalf("原操作错误被恢复成功覆盖：%v", err)
	}
	assertRecovery(original.ID, false)
	assertRecovery(recovery.ID, false)
	if _, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", recovery.ID); err == nil || err.Error() != "SWARM_REBUILD_RECOVERY_NOT_REQUIRED" {
		t.Fatal("已恢复备份重复执行了恢复")
	}
	if _, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10"); err != nil {
		t.Fatalf("原始检查点恢复后仍阻断新维护：%v", err)
	}
}

func TestSwarmRebuildRecoveryRetryUsesLatestOriginalCheckpointAndFinishesCancellation(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	original := newTask(string(domain.TaskActionRebuildDockerSwarm), "server", "srv_gateway", "示例网关", domain.Object{"connectionMode": "public", "advertiseAddr": "192.0.2.10"})
	original.Status = "failed"
	original.Payload["swarmRebuildCheckpoint"] = rebuildTaskCheckpoint(original.ID, "leaving")
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Tasks = append(state.Tasks, original)
		state.OperationRuns = append(state.OperationRuns, domain.OperationRun{ID: original.ID, Action: original.Action, Status: "failed", TargetKind: "server", TargetID: "srv_gateway", ErrorCode: "SWARM_REBUILD_ROLLBACK_FAILED"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", original.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		next := cloneObject(input.SwarmRebuildCheckpoint)
		next["phase"] = "recovering"
		if err := input.SaveSwarmRebuildCheckpoint(ctx, next); err != nil {
			t.Fatal(err)
		}
		return nil, errors.New("SWARM_REBUILD_ROLLBACK_FAILED")
	}}
	if _, err := runtime.ConfirmTask(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", first.ID)
	if err != nil {
		t.Fatalf("失败恢复记录未指向原始备份继续恢复：%v", err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		if input.SwarmRebuildID != original.ID || input.SwarmRebuildCheckpoint["phase"] != "recovering" {
			t.Fatal("继续恢复重新创建了备份或丢失最新阶段")
		}
		if _, err := runtime.CancelTask(ctx, second.ID); err != nil {
			t.Fatal(err)
		}
		if executionCtx.Err() != nil {
			t.Fatal("取消中断了必须完成的恢复")
		}
		next := cloneObject(input.SwarmRebuildCheckpoint)
		next["phase"] = "recovered"
		if err := input.SaveSwarmRebuildCheckpoint(ctx, next); err != nil {
			t.Fatal(err)
		}
		return nil, errSwarmRebuildRecovered
	}}
	if _, err := runtime.ConfirmTask(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || findTask(state.Tasks, second.ID).Status != "cancelled" || objectMapFromAny(findTask(state.Tasks, original.ID).Payload["swarmRebuildCheckpoint"])["phase"] != "recovered" {
		t.Fatalf("取消恢复未同步原始检查点：%v", err)
	}
	if _, err := runtime.CreateTask(ctx, "install_traefik_gateway", "server", "srv_gateway", "示例网关", nil); err != nil {
		t.Fatalf("旧失败恢复记录在原备份恢复后仍错误持有锁：%v", err)
	}
}

func TestSwarmRebuildIOCancellationNeverCountsAsRecoveryComplete(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	original, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		if err := input.SaveSwarmRebuildCheckpoint(ctx, rebuildTaskCheckpoint(original.ID, "prepared")); err != nil {
			t.Fatal(err)
		}
		return nil, context.Canceled
	}}
	if _, err := runtime.ConfirmTask(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertIncomplete := func(operationID string) {
		t.Helper()
		run, err := runtime.GetOperationRun(ctx, operationID)
		if err != nil || run["status"] != "failed" || run["errorCode"] != "SWARM_REBUILD_ROLLBACK_FAILED" {
			t.Fatal("普通 I/O 取消误报恢复成功")
		}
		public := objectMapFromAny(objectMapFromAny(run["details"])["swarmRebuildRecovery"])
		if public["needsRecovery"] != true {
			t.Fatal("未恢复的检查点丢失继续恢复入口")
		}
	}
	assertIncomplete(original.ID)
	recovery, err := runtime.CreateSwarmRebuildRecoveryOperation(ctx, "srv_gateway", original.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(context.Context, ServerRuntimeExecutionInput) (domain.Object, error) {
		return nil, context.Canceled
	}}
	if _, err := runtime.ConfirmTask(ctx, recovery.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	assertIncomplete(recovery.ID)
	state, err := store.LoadState(ctx)
	if err != nil || objectMapFromAny(findTask(state.Tasks, original.ID).Payload["swarmRebuildCheckpoint"])["phase"] != "prepared" {
		t.Fatal("I/O 取消改写了未完成检查点")
	}
}
