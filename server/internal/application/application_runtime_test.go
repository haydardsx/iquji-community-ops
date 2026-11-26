package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type fakeApplicationRemote struct {
	run func(string) (string, bool, error)
}

func (f *fakeApplicationRemote) Run(_ context.Context, command string, _ int) (string, bool, error) {
	return f.run(command)
}
func (f *fakeApplicationRemote) RunLogs(_ context.Context, command string, _ int) (string, bool, error) {
	return f.run(command)
}
func (f *fakeApplicationRemote) Close() error { return nil }

func TestApplicationServiceActionsPreserveSavedReplicasAndDoNotBuild(t *testing.T) {
	before := ApplicationRuntimeSnapshot{Services: []ApplicationServiceState{{ServiceID: "svc1", ServiceName: "api", ExpectedReplicas: 3, DesiredReplicas: 2, RunningReplicas: 2}}}
	input := ApplicationServiceInput{ApplicationID: "app1", Services: []ApplicationServiceTarget{{ServiceName: "api", Replicas: 3}}}
	for _, test := range []struct {
		action   string
		want     int
		contains string
	}{
		{"stop", 0, "svc1=0"}, {"start", 3, "svc1=3"}, {"restart", 2, "--force"}, {"uninstall", 0, "docker service rm"},
	} {
		targets, err := applicationServiceOperationTargets(before, input, test.action)
		if err != nil || targets["svc1"] != test.want {
			t.Fatalf("%s targets = %v, %v", test.action, targets, err)
		}
		command, err := applicationServiceActionCommand("app1", before.Services[0], test.action, targets["svc1"])
		if err != nil || !strings.Contains(command, test.contains) {
			t.Fatalf("%s generated an unexpected command", test.action)
		}
		for _, forbidden := range []string{"--image", "service create", "jenkins", "volume rm"} {
			if strings.Contains(command, forbidden) {
				t.Fatal("操作越过运行服务边界")
			}
		}
	}
	before.Services[0].DesiredReplicas = 0
	if _, err := applicationServiceOperationTargets(before, input, "restart"); err == nil || err.Error() != "APPLICATION_STOPPED_USE_START" {
		t.Fatalf("停止服务不可通过重启隐式启动: %v", err)
	}
	executor := &SystemApplicationRuntimeExecutor{dial: func(context.Context, ApplicationServiceInput) (applicationServiceRemote, error) {
		t.Fatal("删卷应在连接前阻断")
		return nil, nil
	}}
	if _, err := executor.Apply(context.Background(), input, "uninstall", true); err == nil || err.Error() != "APPLICATION_VOLUME_REMOVAL_UNSUPPORTED" {
		t.Fatalf("删卷未失败关闭: %v", err)
	}
}

func TestApplicationRemoteInspectAndPartialFailureKeepActualFacts(t *testing.T) {
	firstStopped := false
	remote := &fakeApplicationRemote{run: func(command string) (string, bool, error) {
		switch {
		case strings.HasPrefix(command, "docker info"):
			return `{"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"cluster1"}}`, false, nil
		case strings.HasPrefix(command, "docker service ls"):
			return "svc1\nsvc2", false, nil
		case strings.HasPrefix(command, "docker service inspect"):
			items := []domain.Object{}
			for _, id := range []string{"svc1", "svc2"} {
				replicas := 1
				if id == "svc1" && firstStopped {
					replicas = 0
				}
				items = append(items, domain.Object{"ID": id, "Spec": domain.Object{"Name": id, "Labels": domain.Object{"iquops.owner": "iquops", "iquops.application-id": "app1", "iquops.service-slot": id}, "Mode": domain.Object{"Replicated": domain.Object{"Replicas": replicas}}}})
			}
			encoded, _ := json.Marshal(items)
			return string(encoded), false, nil
		case strings.HasPrefix(command, "docker service ps"):
			if !strings.Contains(command, "--filter 'desired-state=running'") {
				t.Fatal("运行态查询必须过滤历史任务")
			}
			if strings.Contains(command, "svc1") {
				return "task1", false, nil
			}
			return "task2", false, nil
		case strings.HasPrefix(command, "docker inspect --type task"):
			if strings.Contains(command, "task1") {
				state := "running"
				if firstStopped {
					state = "shutdown"
				}
				return `[{"ID":"task1","NodeID":"node1","Status":{"State":"` + state + `"}}]`, false, nil
			}
			return `[{"ID":"task2","NodeID":"node2","Status":{"State":"running"}}]`, false, nil
		case strings.Contains(command, "docker service scale"):
			if strings.Contains(command, "svc1=0") {
				firstStopped = true
				return "", false, nil
			}
			return "", false, errors.New("SSH_DISCONNECTED")
		default:
			t.Fatal("意外命令")
			return "", false, nil
		}
	}}
	input := ApplicationServiceInput{ApplicationID: "app1", ClusterID: "cluster1", Services: []ApplicationServiceTarget{{ServiceName: "svc1", Replicas: 1}, {ServiceName: "svc2", Replicas: 1}}}
	executor := &SystemApplicationRuntimeExecutor{dial: func(context.Context, ApplicationServiceInput) (applicationServiceRemote, error) { return remote, nil }}
	before, err := executor.Inspect(context.Background(), input)
	if err != nil || before.Status != "running" || before.Services[0].Tasks[0].ID != "task1" {
		t.Fatal("真实状态读取失败")
	}
	after, err := executor.Apply(context.Background(), input, "stop", false)
	if err == nil || err.Error() != "APPLICATION_RUNTIME_ACTION_PARTIALLY_FAILED" || after.Status != "degraded" || after.Services[0].DesiredReplicas != 0 || after.Services[1].RunningReplicas != 1 {
		t.Fatal("部分失败被伪造成成功或回滚")
	}
	input.ClusterID = "other-cluster"
	if _, err := executor.Inspect(context.Background(), input); err == nil || err.Error() != "APPLICATION_SWARM_CLUSTER_MISMATCH" {
		t.Fatalf("集群边界未生效: %v", err)
	}
	badService := domain.Object{"ID": "foreign", "Spec": domain.Object{"Labels": domain.Object{"iquops.owner": "other"}}}
	if _, err := inspectApplicationService(context.Background(), remote, input, badService); err == nil || err.Error() != "APPLICATION_SERVICE_OWNERSHIP_MISMATCH" {
		t.Fatalf("归属错误不可操作: %v", err)
	}
}

func TestApplicationFactsAndMutualExclusion(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = append(state.Applications, domain.Object{"id": "app1", "name": "API", "projectId": "project1"})
		state.Routes = append(state.Routes, domain.Object{"id": "route1", "applicationId": "app1"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := ApplicationRuntimeSnapshot{Availability: "available", Status: "running", CheckedAt: domain.Now(), Services: []ApplicationServiceState{{ServiceID: "svc1", ServiceSlotKey: "web", Tasks: []ApplicationServiceTask{{ID: "actual-task", NodeID: "actual-node", Status: "running"}, {ID: "old-task", Status: "shutdown"}}}}}
	if err := runtime.recordApplicationRuntime(ctx, "app1", snapshot); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	instances := filterObjectsBy(state.ApplicationInstances, "applicationId", "app1")
	if len(instances) != 1 || instances[0]["id"] != "actual-task" || instances[0]["nodeId"] != "actual-node" {
		t.Fatal("实例必须来自实际task")
	}
	_, err := runtime.CreateTask(ctx, "apply_route", "route", "route1", "入口", map[string]any{"applicationId": "app1"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := runtime.CreateApplicationOperation(ctx, "app1", "stop", false)
	if err != nil || queued.Status != "pending_confirmation" {
		t.Fatal("关联操作应接受并等待确认")
	}
	snapshot.Availability, snapshot.Status, snapshot.Services, snapshot.CheckedAt = "not_deployed", "not_deployed", []ApplicationServiceState{}, domain.Now()
	if err := runtime.recordApplicationRuntime(ctx, "app1", snapshot); err != nil {
		t.Fatal(err)
	}
	state, _ = store.LoadState(ctx)
	if len(filterObjectsBy(state.ApplicationInstances, "applicationId", "app1")) != 0 {
		t.Fatal("卸载后仍保留虚构实例")
	}
	writer := &applicationBoundedOutput{limit: 8}
	_, _ = writer.Write([]byte("0123456789"))
	if writer.buffer.Len() != 8 || !writer.truncated {
		t.Fatal("单行日志必须受到字节上限约束")
	}
}

type fakeApplicationRuntimeExecutor struct {
	snapshot ApplicationRuntimeSnapshot
	err      error
}

func TestRuntimeSnapshotSurvivesEndpointProbeDeadline(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	const id = "app_example"
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = append(state.Applications, domain.Object{"id": id})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := ApplicationServiceInput{ApplicationID: id, Outputs: []domain.Object{{"valueSource": "service_port", "serviceSlotKey": "main"}}}
	service := ApplicationServiceState{ServiceSlotKey: "main", ServerID: "server_example", RunningReplicas: 1, Ports: []domain.Object{{"name": "web", "protocol": "http", "containerPort": 80}}}
	queryCtx, endQuery := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer endQuery()
	for _, status := range []string{"running", "degraded"} {
		snapshot := ApplicationRuntimeSnapshot{Availability: "available", Status: status, CheckedAt: domain.Now(), Services: []ApplicationServiceState{service}}
		if err := r.observeAndRecordRuntime(ctx, queryCtx, input, snapshot); err != nil {
			t.Fatal(err)
		}
		state, err := store.LoadState(ctx)
		if err != nil || findObject(state.Applications, id)["status"] != status || service.Ports[0]["internalVerified"] == true {
			t.Fatalf("端点超时须保留实际副本状态并留空端点验证：%s，%v", status, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.observeAndRecordRuntime(cancelled, queryCtx, input, ApplicationRuntimeSnapshot{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("调用者取消后仍写入运行快照：%v", err)
	}
}

func (f *fakeApplicationRuntimeExecutor) Inspect(context.Context, ApplicationServiceInput) (ApplicationRuntimeSnapshot, error) {
	return f.snapshot, f.err
}
func (f *fakeApplicationRuntimeExecutor) Apply(context.Context, ApplicationServiceInput, string, bool) (ApplicationRuntimeSnapshot, error) {
	return f.snapshot, f.err
}
func (f *fakeApplicationRuntimeExecutor) Logs(context.Context, ApplicationServiceInput, string) (ApplicationServiceLogs, error) {
	return ApplicationServiceLogs{}, f.err
}

func TestApplicationDeletionRequiresCurrentRemoteProof(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = append(state.Applications, domain.Object{"id": "app1", "name": "API"})
		state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, domain.Object{"id": "config1", "applicationId": "app1", "deploymentManagerServerId": "srv_gateway"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeApplicationRuntimeExecutor{snapshot: ApplicationRuntimeSnapshot{DeploymentManagerServerID: "srv_gateway", DeploymentClusterID: "cluster_demo", Availability: "available", Status: "stopped", CheckedAt: domain.Now(), Services: []ApplicationServiceState{{ServiceID: "actual-stopped-service", Tasks: []ApplicationServiceTask{}}}}}
	runtime.ApplicationRuntimeExecutor = fake
	if err := runtime.DeleteObject(ctx, "applications", "app1"); err == nil || err.Error() != "APPLICATION_RUNTIME_UNINSTALL_REQUIRED" {
		t.Fatalf("未打开详情的真实停止服务仍必须阻断删除: %v", err)
	}
	if _, err := runtime.checkedApplicationManager(ctx, "app1", domain.Object{"deploymentManagerServerId": "new_gateway"}); err == nil || err.Error() != "APPLICATION_RUNTIME_UNINSTALL_REQUIRED" {
		t.Fatalf("不应丢失现有服务管理位置: %v", err)
	}
	fake.err = errors.New("SSH_CONNECT_FAILED")
	if err := runtime.DeleteObject(ctx, "applications", "app1"); err == nil || err.Error() != "APPLICATION_RUNTIME_CHECK_REQUIRED" {
		t.Fatalf("读取失败不可沿用旧快照删除: %v", err)
	}
	fake.err = nil
	fake.snapshot = ApplicationRuntimeSnapshot{DeploymentManagerServerID: "srv_gateway", DeploymentClusterID: "cluster_demo", Availability: "not_deployed", Status: "not_deployed", CheckedAt: domain.Now(), Services: []ApplicationServiceState{}}
	if err := runtime.DeleteObject(ctx, "applications", "app1"); err != nil {
		t.Fatalf("真实确认卸载后应可删除配置: %v", err)
	}
	state, _ := store.LoadState(ctx)
	if findObject(state.Applications, "app1") != nil || findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", "app1") != nil {
		t.Fatal("删除应清理应用和配置")
	}
}

func TestApplicationServiceLogsReportUnsupportedDriverAndTruncation(t *testing.T) {
	driver := "syslog"
	remote := &fakeApplicationRemote{run: func(command string) (string, bool, error) {
		switch {
		case strings.HasPrefix(command, "docker info"):
			return `{"LocalNodeState":"active","ControlAvailable":true,"Cluster":{"ID":"cluster1"}}`, false, nil
		case strings.HasPrefix(command, "docker service ls"):
			return "svc1", false, nil
		case strings.HasPrefix(command, "docker service inspect"):
			return `[{"ID":"svc1","Spec":{"Name":"web","Labels":{"iquops.owner":"iquops","iquops.application-id":"app1","iquops.service-slot":"web"},"Mode":{"Replicated":{"Replicas":0}},"TaskTemplate":{"LogDriver":{"Name":"` + driver + `"}}}}]`, false, nil
		case strings.HasPrefix(command, "docker service ps"):
			return "", false, nil
		case strings.HasPrefix(command, "docker service logs --raw --timestamps --tail 200 --since 30m"):
			return "2026-10-30T00:00:00Z 实际输出", true, nil
		default:
			t.Fatal("意外日志命令")
			return "", false, nil
		}
	}}
	executor := &SystemApplicationRuntimeExecutor{dial: func(context.Context, ApplicationServiceInput) (applicationServiceRemote, error) { return remote, nil }}
	input := ApplicationServiceInput{ApplicationID: "app1", ClusterID: "cluster1"}
	if _, err := executor.Logs(context.Background(), input, "web"); err == nil || err.Error() != "APPLICATION_LOG_DRIVER_UNSUPPORTED" {
		t.Fatalf("驱动不支持不可伪装空日志: %v", err)
	}
	driver = "json-file"
	result, err := executor.Logs(context.Background(), input, "web")
	if err != nil || result.Availability != "available" || !result.Truncated || len(result.Items) != 1 || !strings.Contains(result.Items[0].Message, "实际输出") {
		t.Fatal("有界日志读取失败")
	}
}
