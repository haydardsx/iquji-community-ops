package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestManagedFrpsReadyRequiresExactNodeOwnership(t *testing.T) {
	const ownedSpec = `{"iquops.owner":"iquops","iquops.component":"frps","iquops.lifecycle":"persistent","iquops.nodeId":"node_example","iquops.tunnelRunId":"run_example"}` + "\n" +
		`["node.id == node_example"]` + "\n" + `{"Replicas":0}` + "\n" + "registry.example/frps:v1"
	for _, tt := range []struct {
		name    string
		listed  string
		spec    string
		wantErr string
	}{
		{name: "已确认安装", listed: "service_example|iquops-frps-node-node_example", spec: ownedSpec},
		{name: "名称前缀相同", listed: "service_other|iquops-frps-node-node_example2"},
		{name: "其他节点", listed: "service_example|iquops-frps-node-node_example", spec: strings.Replace(ownedSpec, "node.id == node_example", "node.id == node_other", 1), wantErr: "FRPS_SERVICE_OWNERSHIP_MISMATCH"},
		{name: "不同归属", listed: "service_example|iquops-frps-node-node_example", spec: strings.Replace(ownedSpec, `"iquops.owner":"iquops"`, `"iquops.owner":"other"`, 1), wantErr: "FRPS_SERVICE_OWNERSHIP_MISMATCH"},
		{name: "非副本模式", listed: "service_example|iquops-frps-node-node_example", spec: strings.Replace(ownedSpec, `{"Replicas":0}`, "null", 1), wantErr: "FRPS_SERVICE_PROBE_FAILED"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			result, err := probeManagedFrps(func(command string) (string, error) {
				if strings.HasPrefix(command, "docker service ls") {
					return tt.listed, nil
				}
				if strings.HasSuffix(command, " 'service_example'") {
					return tt.spec, nil
				}
				t.Fatal("意外远端命令")
				return "", nil
			}, "node_example")
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr || result.Installed {
					t.Fatal("非受管服务核验结果错误")
				}
				return
			}
			if err != nil || result.Installed != (tt.spec != "") {
				t.Fatal("安装状态错误")
			}
			if result.Installed && (result.Replicas != 0 || result.RunID != "run_example" || result.ID != "service_example") {
				t.Fatal("服务回读错误")
			}
		})
	}
}

func TestManagedFrpsInstallDoesNotExposePorts(t *testing.T) {
	command := managedFrpsCreateCommand("node_example", "registry.example/frps:v1")
	for _, expected := range []string{"--replicas 0", "node.id==node_example", "iquops.nodeId=node_example", "iquops.lifecycle=persistent", "traefik.enable=false"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("安装命令缺少 %q", expected)
		}
	}
	for _, forbidden := range []string{"--publish", "--env", "--mount"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("待机服务提前配置了 %q", forbidden)
		}
	}
}

type testFrpsRuntimeExecutor struct{ installs, uninstalls int }

func (f *testFrpsRuntimeExecutor) InstallFrps(context.Context, ServerRuntimeExecutionInput, string, string) error {
	f.installs++
	return nil
}

func (f *testFrpsRuntimeExecutor) UninstallFrps(context.Context, ServerRuntimeExecutionInput) error {
	f.uninstalls++
	return nil
}

func TestFrpsRuntimeActionsVerifyProbeAndBlockActiveRun(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	executor := &testFrpsRuntimeExecutor{}
	runtime.FrpsRuntimeExecutor = executor
	probe := runtime.ServerRuntimeExecutor.(*fakeServerRuntimeExecutor)
	task := domain.Task{TargetID: "srv_gateway"}
	probe.probeResult = domain.Object{"metrics": domain.Object{"ssh": true, "frps": false, "frpsStatus": "missing", "frpsManagedPresent": false}}
	if _, err := runtime.executeFrpsRuntimeAction(ctx, task, true); err == nil || err.Error() != "FRPS_POST_INSTALL_PROBE_FAILED" {
		t.Fatalf("实际安装未核验时应失败：%v", err)
	}
	probe.probeResult = domain.Object{"metrics": domain.Object{"ssh": true, "frps": true, "frpsStatus": "installed", "frpsPresent": true, "frpsManagedPresent": true}}
	if _, err := runtime.executeFrpsRuntimeAction(ctx, task, true); err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.FrpTunnelRuns = append(state.FrpTunnelRuns, domain.FrpTunnelRun{ID: "run_example", ServerID: "srv_gateway", Status: domain.FrpTunnelStatusRunning})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.executeFrpsRuntimeAction(ctx, task, false); err == nil || err.Error() != "FRPS_UNINSTALL_BLOCKED_ACTIVE_TUNNEL" {
		t.Fatalf("活动隧道必须阻断卸载：%v", err)
	}
	if executor.installs != 2 || executor.uninstalls != 0 {
		t.Fatal("远端调用次数错误")
	}
}

func TestFrpsIdleNoticeReflectsCurrentTunnelState(t *testing.T) {
	server := domain.Object{"id": "server_example", "health": domain.Object{"metrics": domain.Object{"frpsPresent": true}}}
	state := domain.State{Servers: []domain.Object{server}}
	if metrics := serverRuntimeMetrics(enrichServer(server, state)); metrics["frpsIdle"] != true {
		t.Fatal("无隧道但存在 FRPS 应提示卸载")
	}
	state.FrpTunnelRuns = []domain.FrpTunnelRun{{ID: "run_example", ServerID: "server_example", Status: domain.FrpTunnelStatusRunning}}
	if metrics := serverRuntimeMetrics(enrichServer(server, state)); metrics["frpsIdle"] != false || metrics["frpsInUse"] != true {
		t.Fatal("活动隧道应隐藏卸载提醒")
	}
	state.FrpTunnelRuns[0].Status = domain.FrpTunnelStatusCleanupRequired
	if metrics := serverRuntimeMetrics(enrichServer(server, state)); metrics["frpsIdle"] != true || metrics["frpsInUse"] != true {
		t.Fatal("已断开的隧道应提醒卸载，停用入口前仍阻止卸载")
	}
	if _, err := probeManagedFrps(func(string) (string, error) { return "", errors.New("unavailable") }, "node_example"); err == nil {
		t.Fatal("远端探测错误不能被当作未安装")
	}
}
