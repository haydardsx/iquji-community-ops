package application

import (
	"errors"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmUninstallAllowsConfigurationOnlyServerReferences(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "project_one"}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one", "name": "示例应用"}}
		state.Servers = []domain.Object{readinessServer("srv_independent", domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmClusterId": "cluster_independent",
		})}
		state.ApplicationDeploymentConfigs = []domain.Object{{
			"id": "config_one", "applicationId": "app_one", "gatewayServerId": "srv_independent",
			"placement":    domain.Object{"eligibleServerIds": []string{"srv_independent"}},
			"swarmRuntime": domain.Object{"services": []domain.Object{{"serverId": "srv_independent", "serviceName": "web"}}},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.validateSwarmUninstall(ctx, "srv_independent", ""); err != nil {
		t.Fatalf("仅配置引用不应阻断 Swarm 退出：%v", err)
	}
	impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "srv_independent", "swarm")
	if err != nil {
		t.Fatalf("读取卸载影响失败：%v", err)
	}
	if impact["blocked"] != false {
		t.Fatal("仅配置引用不应标记为阻断")
	}
}

func TestSwarmUninstallAllowsBoundWorkerAndProtectsManagersAndWorkloads(t *testing.T) {
	for _, test := range []struct {
		name, role, managerID, expectedCode string
		running                             bool
	}{
		{"空闲 Worker", "worker", "manager", "", false},
		{"Manager 仍有成员", "manager", "", "SWARM_UNINSTALL_BLOCKED_CLUSTER_MEMBER", false},
		{"未知角色", "", "manager", "SWARM_UNINSTALL_BLOCKED_CLUSTER_MEMBER", false},
		{"Worker 未绑定 Manager", "worker", "", "", false},
		{"Worker 缓存实例由执行器实时核验", "worker", "manager", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			if err := store.MutateState(ctx, func(state *domain.State) error {
				state.Servers = []domain.Object{
					readinessServer("target", domain.Object{"swarm": true, "swarmRole": test.role, "swarmClusterId": "cluster", "swarmManagerServerId": test.managerID}),
					readinessServer("manager", domain.Object{"swarm": true, "swarmRole": "manager", "swarmClusterId": "cluster"}),
				}
				if test.running {
					state.ApplicationInstances = []domain.Object{{"id": "instance", "serverId": "target"}}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "target", "swarm")
			code := ""
			if err != nil {
				code = err.Error()
			}
			if code != test.expectedCode || impact["blocked"] != (code != "") {
				t.Fatal("角色与工作负载保护错误")
			}
			if test.role == "worker" && test.managerID != "" {
				for _, reason := range objectSlice(impact["reasons"]) {
					if reason["code"] == "SERVER_IS_SWARM_CLUSTER_MEMBER" {
						t.Fatal("Worker 影响列表仍错误要求先退出同群成员")
					}
				}
			}
		})
	}
}

func TestSwarmUninstallStillBlocksRuntimeReferencesAndActiveTasks(t *testing.T) {
	for _, scenario := range []struct {
		name string
		seed func(*domain.State)
		code string
	}{
		{
			name: "应用实例",
			seed: func(state *domain.State) {
				state.ApplicationInstances = []domain.Object{{"id": "instance_one", "applicationId": "app_one", "serverId": "srv_target"}}
			},
			code: "SWARM_UNINSTALL_BLOCKED_APPLICATION_RUNTIME",
		},
		{
			name: "活动服务器任务",
			seed: func(state *domain.State) {
				state.Tasks = []domain.Task{{ID: "task_other", Action: "install_docker_swarm", Status: "executing", TargetKind: "server", TargetID: "srv_target", TargetName: "安装 Swarm"}}
			},
			code: "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK",
		},
		{
			name: "受管网关",
			seed: func(state *domain.State) {
				state.Servers[0]["health"].(domain.Object)["metrics"].(domain.Object)["traefik"] = true
			},
			code: "SWARM_UNINSTALL_BLOCKED_TRAEFIK",
		},
		{
			name: "运行中的内网穿透",
			seed: func(state *domain.State) {
				state.FrpTunnels = []domain.FrpTunnel{{ID: "tunnel_one", Name: "公网入口", ServerID: "srv_target"}}
				state.FrpTunnelRuns = []domain.FrpTunnelRun{{ID: "run_one", TunnelID: "tunnel_one", ServerID: "srv_target", Status: domain.FrpTunnelStatusRunning}}
			},
			code: "SWARM_UNINSTALL_BLOCKED_TUNNEL",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			if err := store.MutateState(ctx, func(state *domain.State) error {
				state.Projects = []domain.Object{{"id": "project_one"}}
				state.Applications = []domain.Object{{"id": "app_one", "projectId": "project_one", "name": "示例应用"}}
				state.Servers = []domain.Object{readinessServer("srv_target", domain.Object{
					"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmClusterId": "cluster_one",
				})}
				scenario.seed(state)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := runtime.validateSwarmUninstall(ctx, "srv_target", ""); err == nil || err.Error() != scenario.code {
				t.Fatalf("阻断码 = %v，应为 %s", err, scenario.code)
			}
		})
	}
}

func TestSwarmUninstallIgnoresItsOwnActiveTaskButImpactReportsOtherTask(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{readinessServer("srv_target", domain.Object{
			"ssh": true, "docker": true, "swarm": true, "swarmRole": "manager", "swarmClusterId": "cluster_one",
		})}
		state.Tasks = []domain.Task{
			{ID: "task_self", Action: "uninstall_docker_swarm", Status: "executing", TargetKind: "server", TargetID: "srv_target"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.validateSwarmUninstall(ctx, "srv_target", "task_self"); err != nil {
		t.Fatalf("卸载 Worker 不应被自身任务阻断：%v", err)
	}
	impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "srv_target", "swarm")
	if err == nil || err.Error() != "SWARM_UNINSTALL_BLOCKED_ACTIVE_TASK" {
		t.Fatal("活动任务影响分析未阻断")
	}
	reasons := objectSlice(impact["reasons"])
	foundTask := false
	for _, reason := range reasons {
		if reason["resourceId"] == "task_self" && reason["code"] == "SERVER_HAS_ACTIVE_TASK" {
			foundTask = true
		}
	}
	if !foundTask {
		t.Fatal("活动任务影响详情缺少任务信息")
	}
}

func TestSwarmUninstallPostProbeErrorsAreNotReportedAsSuccess(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
		probeErr: errors.New("remote probe unavailable"),
	}
	_, err := runtime.refreshServerRuntimeAfterAction(ctx, ServerRuntimeExecutionInput{
		Server: domain.Object{"id": "srv_target"},
	})
	if err == nil || err.Error() != "remote probe unavailable" {
		t.Fatalf("远端探测失败不得伪造成功：%v", err)
	}
	for _, probe := range []domain.Object{
		{},
		{"metrics": domain.Object{}},
		{"metrics": domain.Object{"swarm": true}},
		{"metrics": domain.Object{"swarm": false}},
		{"metrics": domain.Object{"swarm": false, "swarmStatus": "pending"}},
		{"metrics": domain.Object{"swarm": false, "swarmStatus": "error"}},
		{"metrics": domain.Object{"swarm": false, "swarmStatus": "inactive", "swarmVerificationStatus": "not_joined", "swarmNodeId": "old_node"}},
	} {
		if err := verifySwarmUninstallProbe(probe); err == nil || err.Error() != "SWARM_POST_UNINSTALL_PROBE_FAILED" {
			t.Fatal("缺少明确退出探测结果不得报告成功")
		}
	}
	if err := verifySwarmUninstallProbe(domain.Object{"metrics": domain.Object{"swarm": false, "swarmStatus": "inactive", "swarmVerificationStatus": "not_joined"}}); err != nil {
		t.Fatalf("明确返回 inactive 且成员身份清空应通过：%v", err)
	}
}
