package application

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmUnfinishedJoinRequiresExplicitSafeLeaveAndInactiveProof(t *testing.T) {
	for _, test := range []struct {
		name, state, nodeID, previousCluster, postState, expectedCode string
		leaveExit                                                     uint32
		leaveCount                                                    int32
	}{
		{"退出失败加入", "error", "", "", "inactive", "", 0, 1},
		{"退出后台加入", "pending", "", "", "inactive", "", 0, 1},
		{"保留已有节点身份", "error", "known_node", "", "inactive", "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED", 0, 0},
		{"保留可信集群身份", "error", "", "known_cluster", "inactive", "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED", 0, 0},
		{"锁定状态", "locked", "", "", "inactive", "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED", 0, 0},
		{"退出命令失败", "error", "", "", "inactive", "SWARM_WORKER_LEAVE_FAILED", 1, 1},
		{"退出后仍未完成", "error", "", "", "pending", "SWARM_POST_UNINSTALL_PROBE_FAILED", 0, 1},
		{"退出后状态缺失", "error", "", "", "", "SWARM_POST_UNINSTALL_PROBE_FAILED", 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var leaves atomic.Int32
			_, worker, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch command {
				case "docker info --format '{{json .Swarm}}'":
					state, nodeID := test.state, test.nodeID
					if leaves.Load() > 0 {
						state, nodeID = test.postState, ""
					}
					return fmt.Sprintf(`{"LocalNodeState":%q,"NodeID":%q,"ControlAvailable":false}`, state, nodeID), 0
				case "docker swarm leave":
					leaves.Add(1)
					return "", test.leaveExit
				default:
					t.Error("未预期或强制退出命令")
					return "", 1
				}
			})
			worker["health"] = domain.Object{"metrics": domain.Object{"swarmClusterId": test.previousCluster}}
			_, err := NewSystemServerRuntimeExecutor().UninstallDockerSwarm(context.Background(), ServerRuntimeExecutionInput{Server: worker, SSHKey: key})
			code := ""
			if err != nil {
				code = err.Error()
			}
			if code != test.expectedCode || leaves.Load() != test.leaveCount {
				t.Fatalf("未完成加入的退出保护错误：code=%s leaves=%d", code, leaves.Load())
			}
		})
	}
}

func TestSwarmInfoMissingStateCannotProveUninstall(t *testing.T) {
	client, _, _ := runtimeProbeSSH(t, func(string) (string, uint32) { return `{}`, 0 })
	status, err := readRemoteSwarmStatus(context.Background(), client, "")
	if err != nil || status.Status != "unknown" {
		t.Fatal("缺失状态被当作已退群")
	}
}

func TestSwarmProbeKeepsJoinFailureDiagnosisAndPendingState(t *testing.T) {
	for _, test := range []struct{ state, diagnostic, code string }{
		{"error", "rpc error: code = DeadlineExceeded desc = context deadline exceeded", "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"},
		{"pending", "", "SWARM_JOIN_PENDING"},
		{"locked", "", "SWARM_NODE_LOCKED"},
	} {
		client, _, _ := runtimeProbeSSH(t, func(string) (string, uint32) {
			return fmt.Sprintf(`{"LocalNodeState":%q,"Error":%q}`, test.state, test.diagnostic), 0
		})
		result := domain.Object{}
		applyRemoteSwarmProbe(context.Background(), client, ServerRuntimeExecutionInput{}, result)
		if code := swarmIdentityReadinessCode(objectMapFromAny(result["metrics"])); code != test.code {
			t.Fatalf("复探测覆盖了具体加入原因：%s", code)
		}
	}
}

func TestSwarmManagerAddressComesFromTheMatchingManagerIdentity(t *testing.T) {
	client, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker info --format '{{json .Swarm}}'":
			return fmt.Sprintf(`{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_self","NodeAddr":%q,"Cluster":{"ID":"cluster"},"RemoteManagers":[{"NodeID":"manager_other","Addr":"192.0.2.11:2377"},{"NodeID":"manager_self","Addr":%q}]}`, fixtureManagerIP.String(), netip.AddrPortFrom(fixtureManagerIP, 42377).String()), 0
		case "docker node inspect 'manager_self' --format '{{.ManagerStatus.Addr}}'":
			return "192.0.2.10:42377", 0
		default:
			t.Error("未预期的 manager 地址探测")
			return "", 1
		}
	})
	status, err := readRemoteSwarmStatus(context.Background(), client, "")
	if err != nil || status.ManagerAddress != "192.0.2.10:42377" {
		t.Fatal("Manager 地址未按身份匹配")
	}
	result := domain.Object{}
	applySwarmIdentityProbe(ServerRuntimeExecutionInput{}, result, status, "")
	if objectMapFromAny(result["metrics"])["swarmManagerAddress"] != status.ManagerAddress {
		t.Fatal("完整 Manager 控制端点未公开")
	}
	applySwarmIdentityProbe(ServerRuntimeExecutionInput{}, result, remoteSwarmStatus{Status: "error"}, "SWARM_CLUSTER_UNVERIFIED")
	if strings.TrimSpace(stringValue(objectMapFromAny(result["metrics"])["swarmManagerAddress"])) != "" {
		t.Fatal("错误身份仍展示旧端点为当前事实")
	}
}

func TestSwarmManagerAddressFailureDoesNotRestoreStaleOrLocalAddress(t *testing.T) {
	client, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if command == "docker info --format '{{json .Swarm}}'" {
			return fmt.Sprintf(`{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_self","NodeAddr":%q,"Cluster":{"ID":"cluster"},"RemoteManagers":[{"NodeID":"manager_self","Addr":"192.0.2.10:2377"}]}`, fixtureManagerIP.String()), 0
		}
		return "", 1
	})
	status, err := readRemoteSwarmStatus(context.Background(), client, "")
	if err != nil || status.ManagerAddress != "" || status.AdvertiseAddr != "" {
		t.Fatal("manager 地址探测失败后恢复了旧地址")
	}
	result := domain.Object{}
	applySwarmIdentityProbe(ServerRuntimeExecutionInput{}, result, status, "")
	if objectMapFromAny(result["metrics"])["swarmConnectionMode"] != "unknown" {
		t.Fatal("未知 manager 地址被标成已知网络类型")
	}
}
