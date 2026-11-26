package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func rebuildTaskCheckpoint(id, phase string) domain.Object {
	dataHash := "none"
	if phase == "prepared" {
		dataHash = ""
	}
	return domain.Object{"version": 1, "backupId": id, "manifestHash": strings.Repeat("a", 64), "phase": phase, "dataHash": dataHash, "newClusterId": "", "newNodeId": ""}
}

func TestSwarmRebuildRequiresConfirmationAndPassesTaskIdentity(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", " PUBLIC ", " 192.0.2.10 ")
	if err != nil || task.Status != "pending_confirmation" {
		t.Fatalf("重建未要求危险操作确认：%v", err)
	}
	calls := 0
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		rebuildSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
			calls++
			if input.SwarmRebuildID != task.ID || input.SwarmRebuildRestoreOnly || len(input.SwarmRebuildCheckpoint) != 0 || input.SwarmConnectionMode != "public" || input.SwarmAdvertiseAddr != "192.0.2.10" {
				t.Fatal("新重建未传递独立备份身份或目标地址")
			}
			for _, phase := range []string{"prepared", "backed_up", "verified"} {
				if err := input.SaveSwarmRebuildCheckpoint(executionCtx, rebuildTaskCheckpoint(task.ID, phase)); err != nil {
					t.Fatal(err)
				}
			}
			return nil, nil
		},
		probeResult: domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmNodeId": "node_new", "swarmClusterId": "cluster_new", "swarmVerificationStatus": "verified", "swarmAdvertiseAddr": "192.0.2.10", "swarmManagerAddress": "192.0.2.10:2377", "swarmConnectionMode": "public"}},
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil || calls != 0 {
		t.Fatal("确认前执行了重建")
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != "succeeded" || serverRuntimeMetrics(state.Servers[0])["swarmClusterId"] != "cluster_new" {
		t.Fatalf("重建未通过正式 Worker 保存真实探测：%v", err)
	}
}

func TestSwarmRebuildCancellationKeepsRecoveryCheckpointWritable(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		if err := input.SaveSwarmRebuildCheckpoint(ctx, rebuildTaskCheckpoint(task.ID, "prepared")); err != nil {
			t.Fatal(err)
		}
		cancelled, err := runtime.CancelTask(ctx, task.ID)
		if err != nil || !cancelled.CancelRequested || cancelled.Status != "executing" {
			t.Fatalf("取消未保留 Worker 清理：%v", err)
		}
		select {
		case <-executionCtx.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("执行器没有收到取消请求")
		}
		if err := input.SaveSwarmRebuildCheckpoint(ctx, rebuildTaskCheckpoint(task.ID, "recovered")); err != nil {
			t.Fatalf("取消后拒绝记录恢复结果：%v", err)
		}
		return nil, errSwarmRebuildRecovered
	}}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	current := findTask(state.Tasks, task.ID)
	if err != nil || current.Status != "cancelled" || objectMapFromAny(current.Payload["swarmRebuildCheckpoint"])["phase"] != "recovered" {
		t.Fatalf("恢复完成后取消未收敛：%v", err)
	}
}

func TestSwarmRebuildQueuedCheckpointAlwaysRestoresBeforeFinishing(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "中断恢复", true: "取消恢复"}[cancelled], func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			seedFrpServer(t, ctx, store, true)
			task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			if err := store.MutateState(ctx, func(state *domain.State) error {
				findTask(state.Tasks, task.ID).Payload["swarmRebuildCheckpoint"] = rebuildTaskCheckpoint(task.ID, "leaving")
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if cancelled {
				current, err := runtime.CancelTask(ctx, task.ID)
				if err != nil || current.Status != "queued" || !current.CancelRequested {
					t.Fatalf("排队中的备份恢复被直接终结：%v", err)
				}
			}
			calls := 0
			runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
				calls++
				if !input.SwarmRebuildRestoreOnly || executionCtx.Err() != nil || input.SwarmRebuildCheckpoint["phase"] != "leaving" || input.SwarmRebuildID != task.ID {
					t.Fatal("旧备份未先进入独立恢复阶段")
				}
				if err := input.SaveSwarmRebuildCheckpoint(ctx, rebuildTaskCheckpoint(task.ID, "recovered")); err != nil {
					t.Fatal(err)
				}
				return nil, errSwarmRebuildRecovered
			}}
			if _, err := runtime.RunWorkerOnce(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := store.LoadState(ctx)
			want := "failed"
			if cancelled {
				want = "cancelled"
			}
			if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != want {
				t.Fatalf("恢复终态错误：want=%s calls=%d err=%v", want, calls, err)
			}
		})
	}
}

func TestSwarmRebuildCheckpointRejectsSecretsAndBackupReplacement(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{rebuildSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		original := rebuildTaskCheckpoint(task.ID, "backed_up")
		if err := input.SaveSwarmRebuildCheckpoint(ctx, original); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(domain.Object){
			func(value domain.Object) { value["Spec"] = domain.Object{"Env": "敏感内容"} },
			func(value domain.Object) { value["backupId"] = "task_other" },
			func(value domain.Object) { value["manifestHash"] = strings.Repeat("b", 64) },
			func(value domain.Object) { value["dataHash"] = strings.Repeat("b", 64) },
		} {
			next := cloneObject(original)
			change(next)
			if err := input.SaveSwarmRebuildCheckpoint(ctx, next); err == nil {
				t.Fatal("检查点接受了秘密字段或替换原备份")
			}
		}
		return nil, errors.New("SWARM_REBUILD_RECOVERY_REQUIRED")
	}}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10"); err == nil || err.Error() != "SWARM_REBUILD_RECOVERY_REQUIRED" {
		t.Fatal("未恢复备份被新重建基线覆盖")
	}
	queued, err := runtime.CreateTask(ctx, "uninstall_traefik_gateway", "server", "srv_gateway", "网关", nil)
	if err != nil {
		t.Fatalf("未恢复备份期间应接受排队: %v", err)
	}
	if _, err := runtime.ConfirmTask(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimNextTask(ctx, -1); err != nil || claimed != nil {
		t.Fatal("原备份恢复前不能删除网关")
	}
	if _, err := runtime.CreateTask(ctx, "install_monitoring_agent", "server", "srv_gateway", "网关", nil); err != nil {
		t.Fatalf("无关 Agent 操作误阻断：%v", err)
	}
}

func TestSwarmRebuildBlocksGatewayWritersAndKeepsIndependentActions(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_example"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"id": "config_example", "applicationId": "app_example", "gatewayServerId": "srv_gateway"}}
		state.Routes = []domain.Object{{"id": "route_example", "applicationId": "app_example"}}
		state.Certificates = []domain.Object{{"id": "certificate_example", "issuer": "lets_encrypt", "challengeType": "http-01", "domains": []string{"app.example.test"}, "resolverId": "resolver_example"}}
		state.Resolvers = []domain.Object{{"id": "resolver_example", "ingressId": "ingress_example"}}
		state.Ingresses = []domain.Object{{"id": "ingress_example", "serverIds": []string{"srv_gateway"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.CreateSwarmRebuildOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	assertActive := func(err error) {
		t.Helper()
		var active *TaskAlreadyActiveError
		if !errors.As(err, &active) || active.ActiveTaskID != task.ID {
			t.Fatalf("网关写入未被维护任务阻止：%v", err)
		}
	}
	_, err = runtime.VerifyCertificate(ctx, "certificate_example")
	assertActive(err)
	if _, err = runtime.CreateTask(ctx, "apply_route", "route", "route_example", "示例路由", nil); err != nil {
		t.Fatalf("路由未能排队: %v", err)
	}
	if _, _, err = runtime.createFrpTunnelRun(ctx, &domain.FrpTunnel{ID: "tunnel_example", Name: "示例隧道", ServerID: "srv_gateway"}, true); err != nil {
		t.Fatalf("隧道未能排队: %v", err)
	}
	if _, err := runtime.CreateTask(ctx, "install_monitoring_agent", "server", "srv_gateway", "示例网关", nil); err != nil {
		t.Fatalf("重建误阻断独立 Agent 操作：%v", err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.FrpTunnelRuns) != 1 || state.FrpTunnelRuns[0].Status != frpTunnelStatusPendingConfirmation {
		t.Fatal("隧道排队未保留待确认状态")
	}
}
