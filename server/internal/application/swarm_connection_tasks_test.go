package application

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmPublicJoinRejectsPrivateManagerBeforeMutation(t *testing.T) {
	_, manager, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_example"}}`, 0
		case "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
			return netip.AddrPortFrom(fixtureManagerIP, 3377).String(), 0
		default:
			t.Error("方式不匹配仍执行额外命令")
			return "", 1
		}
	})
	workerClient, worker, workerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
		t.Error("方式不匹配仍调用 Worker")
		return "", 1
	})
	input := withManagedRuntimeDefaults(ServerRuntimeExecutionInput{Server: worker, SSHKey: workerKey, SwarmConnectionMode: "public", SwarmAdvertiseAddr: "192.0.2.24", SwarmManagerServer: manager, SwarmManagerSSHKey: key})
	input.TraefikNetwork = ""
	err := installSwarmWorker(context.Background(), workerClient, input, remoteSwarmStatus{Status: "inactive"})
	if err == nil || err.Error() != "SWARM_MANAGER_CONNECTION_MODE_MISMATCH" {
		t.Fatalf("公网选择仍尝试连接私网 Manager：%v", err)
	}
}

func TestSwarmConnectionOperationUsesConfirmationAndWorker(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	calls := 0
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		reconfigureSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
			calls++
			if err := input.SaveSwarmConnectionCheckpoint(ctx, domain.Object{"nodeId": "node_example"}); err != nil {
				t.Fatal(err)
			}
			if input.SwarmConnectionMode != "public" || input.SwarmAdvertiseAddr != "192.0.2.10" {
				t.Fatal("连接设置没有传递到执行器")
			}
			return nil, nil
		},
		probeResult: domain.Object{"metrics": domain.Object{"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmNodeId": "node_example", "swarmClusterId": "cluster_example", "swarmVerificationStatus": "verified", "swarmAdvertiseAddr": "192.0.2.10", "swarmManagerAddress": "192.0.2.10:3377", "swarmConnectionMode": "public"}},
	}
	task, err := runtime.CreateSwarmConnectionOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil || task.Status != "pending_confirmation" {
		t.Fatalf("连接设置未经过危险操作确认：%v", err)
	}
	if _, err = runtime.RunWorkerOnce(ctx); err != nil || calls != 0 {
		t.Fatal("确认前执行了迁移")
	}
	if _, err = runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != "succeeded" {
		t.Fatalf("连接设置未通过正式 Worker 完成：%v", err)
	}
	if objectMapFromAny(findTask(state.Tasks, task.ID).Payload["swarmConnectionCheckpoint"])["nodeId"] != "node_example" {
		t.Fatal("迁移前的恢复基线没有写入任务")
	}
	if serverRuntimeMetrics(state.Servers[0])["swarmConnectionMode"] != "public" {
		t.Fatal("真实复探测结果未保存")
	}
}

func TestSwarmMaintenanceConflictsWithDeploymentButAllowsIndependentOperations(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_example"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_example", "deploymentManagerServerId": "srv_gateway"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	maintenance, err := runtime.CreateSwarmConnectionOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	app, err := runtime.CreateApplicationOperation(ctx, "app_example", "restart", false)
	if err != nil {
		t.Fatalf("维护期间必须接受应用排队: %v", err)
	}
	join, err := runtime.CreateTask(ctx, "install_docker_swarm", "server", "other_worker", "示例节点", domain.Object{"managerServerId": "srv_gateway", "mode": "worker"})
	if err != nil {
		t.Fatal(err)
	}
	independent, err := runtime.CreateTask(ctx, "install_monitoring_agent", "server", "independent_server", "其他节点", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []domain.Task{maintenance, app, join, independent} {
		if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ClaimNextTask(ctx, -1)
	if err != nil || first == nil || first.ID != maintenance.ID {
		t.Fatal("维护未先领取")
	}
	next, err := store.ClaimNextTask(ctx, -1)
	if err != nil || next == nil || next.ID != independent.ID {
		t.Fatal("无关节点未并行")
	}
	if task, err := store.ClaimNextTask(ctx, -1); err != nil || task != nil {
		t.Fatal("关联部署或加入未等待维护")
	}
}

func TestSwarmConnectionExecutingCancellationWaitsForCleanup(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	var taskID string
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{reconfigureSwarm: func(executionCtx context.Context, _ ServerRuntimeExecutionInput) (domain.Object, error) {
		if _, err := runtime.CancelTask(ctx, taskID); err != nil {
			t.Fatal(err)
		}
		<-executionCtx.Done()
		return nil, executionCtx.Err()
	}}
	task, err := runtime.CreateSwarmConnectionOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	taskID = task.ID
	if _, err = runtime.ConfirmTask(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || findTask(state.Tasks, taskID).Status != "cancelled" {
		t.Fatalf("执行中取消未收敛：%v", err)
	}
}

func TestSwarmConnectionQueuedRecoveryCancellationStillRunsRestore(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmConnectionOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		findTask(state.Tasks, task.ID).Payload["swarmConnectionCheckpoint"] = domain.Object{"nodeId": "node_example"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := runtime.CancelTask(ctx, task.ID)
	if err != nil || cancelled.Status != "queued" || !cancelled.CancelRequested {
		t.Fatal("中断维护的取消跳过了待执行恢复")
	}
	calls := 0
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{reconfigureSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		calls++
		if !input.SwarmConnectionRestoreOnly || executionCtx.Err() != nil || len(input.SwarmConnectionCheckpoint) == 0 {
			t.Fatal("取消恢复没有通过正式执行器继续清理")
		}
		return nil, context.Canceled
	}}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != "cancelled" {
		t.Fatalf("取消恢复未收敛到终态：calls=%d err=%v", calls, err)
	}
}

func TestSwarmConnectionQueuedLegacyRecoveryRunsWithoutCancellation(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	task, err := runtime.CreateSwarmConnectionOperation(ctx, "srv_gateway", "public", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		findTask(state.Tasks, task.ID).Payload["swarmConnectionCheckpoint"] = domain.Object{"nodeId": "node_example"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{reconfigureSwarm: func(executionCtx context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
		calls++
		if !input.SwarmConnectionRestoreOnly || executionCtx.Err() != nil || len(input.SwarmConnectionCheckpoint) == 0 {
			t.Fatal("未取消的旧任务没有通过正式执行器先恢复")
		}
		return nil, context.Canceled
	}}
	if _, err := runtime.RunWorkerOnce(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != "failed" {
		t.Fatalf("未取消的旧任务恢复后误报为取消或成功：calls=%d err=%v", calls, err)
	}
}

func TestSwarmFactsUseObservedManagerAndWorkerAddresses(t *testing.T) {
	input := ServerRuntimeExecutionInput{}
	for _, test := range []struct{ role, address, endpoint, mode string }{{"manager", fixtureManagerIP.String(), "192.0.2.10:3377", "public"}, {"worker", fixtureWorkerIP.String(), "192.0.2.10:3377", "private"}} {
		result := domain.Object{}
		applySwarmIdentityProbe(input, result, remoteSwarmStatus{Active: true, Status: "active", Role: test.role, NodeID: "node_example", ClusterID: "cluster_example", AdvertiseAddr: test.address, ManagerAddress: test.endpoint}, "")
		if objectMapFromAny(result["metrics"])["swarmConnectionMode"] != test.mode {
			t.Fatal("请求参数覆盖了实际连接方式")
		}
	}
	if err := verifyRequestedSwarmConnection(ServerRuntimeExecutionInput{SwarmConnectionMode: "public", SwarmAdvertiseAddr: "192.0.2.24"}, remoteSwarmStatus{Role: "worker", AdvertiseAddr: fixtureWorkerIP.String()}); err == nil {
		t.Fatal("已有 Worker 地址变更被误报为成功")
	}
	// 原始集群配置只在内存中传递，错误通过稳定分类输出。
	if err := ValidateSwarmConnectionInput("public", fixtureManagerIP.String(), true); err == nil || strings.Contains(err.Error(), fixtureManagerIP.String()) {
		t.Fatal("输入错误包含不必要的环境地址")
	}
}

func TestSwarmWorkerAddressUnknownDoesNotFallBackToNATLocalAddress(t *testing.T) {
	_, manager, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_example"}}`, 0
		case "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
			return "192.0.2.10:2377", 0
		case "docker node ls -q":
			return "manager_node\nworker_node", 0
		case "docker node inspect 'worker_node' --format '{{.Status.Addr}}'":
			return "", 1
		default:
			t.Error("非预期地址核验命令")
			return "", 1
		}
	})
	status := remoteSwarmStatus{Active: true, Status: "active", Role: "worker", NodeID: "worker_node", AdvertiseAddr: fixtureWorkerIP.String()}
	input := ServerRuntimeExecutionInput{SwarmManagerServer: manager, SwarmManagerSSHKey: key, SwarmManagerServerID: "manager"}
	code := verifyRemoteSwarmMembership(context.Background(), input, &status)
	result := domain.Object{}
	applySwarmIdentityProbe(input, result, status, code)
	metrics := objectMapFromAny(result["metrics"])
	if code != "SWARM_NODE_ADDRESS_UNVERIFIED" || metrics["swarmConnectionMode"] != "unknown" || metrics["swarmAdvertiseAddr"] != "" {
		t.Fatal("本机NAT地址被冒充为已核验的通告地址")
	}
}
