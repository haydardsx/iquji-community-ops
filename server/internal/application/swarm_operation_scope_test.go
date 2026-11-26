package application

import (
	"context"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmLeaveIgnoresIndependentBuildDependenciesThroughWorker(t *testing.T) {
	for _, localState := range []string{"pending", "error", "active"} {
		t.Run(localState, func(t *testing.T) {
			runtime, store, ctx := newFrpRuntime(t)
			seedFrpServer(t, ctx, store, true)
			if err := store.MutateState(ctx, func(state *domain.State) error {
				state.Servers[0]["health"] = domain.Object{"metrics": domain.Object{
					"docker": true, "jenkins": true, "swarmStatus": localState,
				}}
				if localState == "active" {
					metrics := serverRuntimeMetrics(state.Servers[0])
					metrics["swarm"], metrics["swarmRole"], metrics["swarmClusterId"] = true, "manager", "cluster_one"
				}
				state.ApplicationDeploymentConfigs = []domain.Object{{
					"id": "config_one", "buildServerId": "srv_gateway", "gatewayServerId": "srv_gateway",
					"placement": domain.Object{"eligibleServerIds": []string{"srv_gateway"}},
				}}
				state.FrpTunnels = []domain.FrpTunnel{{ID: "tunnel_one", ServerID: "srv_gateway"}}
				state.ReleasePlans = []domain.Object{{"id": "release_elsewhere", "buildServerId": "srv_gateway", "deploymentManagerServerId": "other_gateway", "status": "building"}}
				state.Tasks = []domain.Task{{ID: "agent_confirmation", Action: "install_monitoring_agent", Status: "pending_confirmation", TargetKind: "server", TargetID: "srv_gateway"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "srv_gateway", "swarm")
			if err != nil || impact["blocked"] != false || len(objectSlice(impact["reasons"])) != 0 {
				t.Fatal("无关依赖仍出现在 Swarm 退出预览中")
			}
			calls := 0
			runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{
				uninstallSwarm: func(_ context.Context, input ServerRuntimeExecutionInput) (domain.Object, error) {
					calls++
					if input.SSHKey == "" || input.SwarmManagerSSHKey != "" || input.SwarmManagerServerID != "" {
						t.Fatal("本机退出错误要求了外部 Manager")
					}
					return nil, nil
				},
				probeResult: domain.Object{"metrics": domain.Object{
					"ssh": true, "docker": true, "jenkins": true, "swarm": false, "swarmStatus": "inactive", "swarmVerificationStatus": "not_joined",
				}},
			}
			task, err := runtime.CreateTask(ctx, "uninstall_docker_swarm", "server", "srv_gateway", "示例服务器", nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.RunWorkerOnce(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := store.LoadState(ctx)
			if err != nil || calls != 1 || findTask(state.Tasks, task.ID).Status != "succeeded" {
				t.Fatalf("确认后的 Swarm 退出未正常执行：calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestSwarmImpactReportsOnlyRelevantNamedResources(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "application_one", "name": "示例应用"}}
		state.Servers = []domain.Object{
			readinessServer("target", domain.Object{"swarmRole": "manager", "swarmClusterId": "cluster_one", "traefik": true, "jenkins": true}),
			readinessServer("peer", domain.Object{"swarmRole": "worker", "swarmClusterId": "cluster_one"}),
		}
		state.Servers[1]["name"] = "同群节点"
		state.ApplicationDeploymentConfigs = []domain.Object{{"id": "config", "buildServerId": "target"}}
		state.ApplicationInstances = []domain.Object{{"id": "instance", "serverId": "target", "applicationId": "application_one"}}
		state.FrpTunnels = []domain.FrpTunnel{{ID: "tunnel", Name: "示例穿透", ServerID: "target"}}
		state.FrpTunnelRuns = []domain.FrpTunnelRun{{ID: "tunnel_run", TunnelID: "tunnel", ServerID: "target", Status: domain.FrpTunnelStatusRunning}}
		state.ReleasePlans = []domain.Object{{"id": "release", "applicationId": "application_one", "deploymentManagerServerId": "target", "status": "building"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "target", "swarm")
	if err == nil || impact["blocked"] != true {
		t.Fatal("实际集群依赖未阻断")
	}
	reasons := objectSlice(impact["reasons"])
	if len(reasons) != 5 {
		t.Fatal("影响列表含无关引用或缺少依赖")
	}
	for _, reason := range reasons {
		if stringValue(reason["resourceName"]) == "" {
			t.Fatal("影响列表未提供可读资源名称")
		}
	}
}

func TestSwarmUnfinishedJoinImpactPreservesKnownIdentity(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{readinessServer("target", domain.Object{
			"swarmStatus": "error", "swarmNodeId": "known_node",
		})}
		state.Servers[0]["name"] = "示例服务器"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	impact, err := runtime.ServerRuntimeUninstallImpact(ctx, "target", "swarm")
	reasons := objectSlice(impact["reasons"])
	if err == nil || err.Error() != "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED" || impact["blocked"] != true || len(reasons) != 1 || reasons[0]["resourceName"] != "示例服务器" {
		t.Fatal("已知身份保护与影响预览不一致")
	}
}

func TestSwarmManagerLeaveStillChecksActualServicesAndPeers(t *testing.T) {
	for _, test := range []struct{ name, services, nodes, code string }{
		{"实际服务", "external_service", "manager_node", "SWARM_LEAVE_BLOCKED_SERVICES"},
		{"实际成员", "", "manager_node\nworker_node", "SWARM_LEAVE_BLOCKED_NODES"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch {
				case command == "docker info --format '{{json .Swarm}}'":
					return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_one"}}`, 0
				case strings.HasPrefix(command, "docker network inspect "):
					return "overlay|swarm", 0
				case command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
					return "192.0.2.10:2377", 0
				case command == "docker service ls -q":
					return test.services, 0
				case command == "docker node ls -q":
					return test.nodes, 0
				default:
					t.Error("存在实际依赖时执行了退出或无关命令")
					return "", 1
				}
			})
			_, err := NewSystemServerRuntimeExecutor().UninstallDockerSwarm(context.Background(), ServerRuntimeExecutionInput{Server: server, SSHKey: key})
			if err == nil || err.Error() != test.code {
				t.Fatalf("远端真实依赖未阻断：%v", err)
			}
		})
	}
}
