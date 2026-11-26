package application

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmWorkerCanLeaveWithoutManagedManagerAndPreserveDeparture(t *testing.T) {
	for _, test := range []struct {
		name, tasks, postState string
		saveFail               bool
		code                   string
	}{
		{"外部manager不阻断", "", "inactive", false, ""},
		{"本机实际Swarm任务阻断", "task_running", "inactive", false, "SWARM_LEAVE_BLOCKED_SERVICES"},
		{"离群记录保存失败先阻断", "", "inactive", true, "SWARM_DEPARTURE_RECORD_SAVE_FAILED"},
		{"必须证明inactive", "", "active", false, "SWARM_POST_UNINSTALL_PROBE_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var leaves atomic.Int32
			_, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch command {
				case "docker info --format '{{json .Swarm}}'":
					if leaves.Load() > 0 && test.postState == "inactive" {
						return `{"LocalNodeState":"inactive","ControlAvailable":false}`, 0
					}
					return `{"LocalNodeState":"active","ControlAvailable":false,"NodeID":"worker_example","RemoteManagers":[{"NodeID":"manager_example","Addr":"192.0.2.10:2377"}]}`, 0
				case swarmLocalTaskContainersCommand:
					return test.tasks, 0
				case "docker swarm leave":
					leaves.Add(1)
					return "", 0
				default:
					t.Error("普通worker脱离调用了无关/强制操作")
					return "", 1
				}
			})
			server["health"] = domain.Object{"metrics": domain.Object{"swarmRole": "worker", "swarmClusterId": "cluster_external", "jenkins": true, "docker": true}}
			records := []domain.Object{}
			input := ServerRuntimeExecutionInput{Server: server, SSHKey: key, SaveSwarmDeparture: func(_ context.Context, record domain.Object) error {
				if test.saveFail {
					return fmt.Errorf("private storage output")
				}
				records = append(records, cloneObject(record))
				return nil
			}}
			_, err := NewSystemServerRuntimeExecutor().UninstallDockerSwarm(context.Background(), input)
			code := ""
			if err != nil {
				code = err.Error()
			}
			if code != test.code {
				t.Fatalf("本机脱离结果=%v，希望%s", err, test.code)
			}
			if test.tasks != "" || test.saveFail {
				if leaves.Load() != 0 {
					t.Fatal("前置失败后仍执行leave")
				}
				return
			}
			if leaves.Load() != 1 || len(records) != 2 {
				t.Fatalf("普通leave/持久身份次数错误：leaves=%d records=%d", leaves.Load(), len(records))
			}
			if test.postState == "inactive" {
				last := records[1]
				if last["nodeId"] != "worker_example" || last["clusterId"] != "cluster_external" || last["cleanupStatus"] != "pending" || last["reasonCode"] != "SWARM_WORKER_MANAGER_NOT_ASSOCIATED" || stringValue(last["departedAt"]) == "" {
					t.Fatal("外部manager节点记录被伪称清理或原身份丢失")
				}
			} else if stringValue(records[1]["departedAt"]) != "" {
				t.Fatal("未证明inactive即记录为已脱离")
			}
		})
	}
}

func TestSwarmWorkerLeaveCleansOnlyItsOwnedDownManagerRecord(t *testing.T) {
	for _, failure := range []string{"", "ownership", "unreachable", "remove"} {
		t.Run(failure, func(t *testing.T) {
			var left, removed atomic.Bool
			var removals atomic.Int32
			_, manager, managerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch {
				case command == "docker info --format '{{json .Swarm}}'":
					return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_example","Cluster":{"ID":"cluster_one"}}`, 0
				case command == "docker node inspect 'manager_example' --format '{{.ManagerStatus.Addr}}'":
					return "192.0.2.10:2377", 0
				case command == "docker node ls -q":
					if failure == "unreachable" && left.Load() {
						return "", 1
					}
					if removed.Load() {
						return "manager_example\nother_worker", 0
					}
					return "manager_example\nworker_example\nother_worker", 0
				case strings.HasPrefix(command, "docker node inspect --format "):
					owner := "server"
					if failure == "ownership" {
						owner = "another_server"
					}
					state := "ready"
					if left.Load() {
						state = "down"
					}
					return "worker_example|" + owner + "|worker|" + state, 0
				case strings.HasPrefix(command, "docker node ps "):
					if !strings.HasSuffix(command, "'worker_example'") {
						t.Error("查询了其它节点工作负载")
					}
					return "old_task|Running|Failed 1 hour ago", 0
				case command == "docker node rm 'worker_example'":
					removals.Add(1)
					if failure == "remove" {
						return "", 1
					}
					removed.Store(true)
					return "", 0
				default:
					t.Error("manager清理调用了无关/全局操作")
					return "", 1
				}
			})
			worker, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch command {
				case swarmLocalTaskContainersCommand:
					return "", 0
				case "docker swarm leave":
					left.Store(true)
					return "", 0
				case "docker info --format '{{json .Swarm}}'":
					return `{"LocalNodeState":"inactive","ControlAvailable":false}`, 0
				default:
					t.Error("worker脱离调用了无关命令")
					return "", 1
				}
			})
			server["health"] = domain.Object{"metrics": domain.Object{"swarmClusterId": "cluster_one", "swarmNodeId": "worker_example"}}
			var record domain.Object
			input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{Server: server, SSHKey: key, SwarmManagerServer: manager, SwarmManagerSSHKey: managerKey, SwarmManagerServerID: "manager", SaveSwarmDeparture: func(_ context.Context, value domain.Object) error { record = cloneObject(value); return nil }})
			err := uninstallSwarmWorker(context.Background(), worker, input, remoteSwarmStatus{Active: true, Role: "worker", NodeID: "worker_example", ClusterID: "cluster_one"})
			if err != nil || !left.Load() || stringValue(record["departedAt"]) == "" {
				t.Fatalf("manager清理不可用错误阻止了真实本机脱离：%v", err)
			}
			if failure == "" {
				if !removed.Load() || record["cleanupStatus"] != "removed" {
					t.Fatal("已管理旧节点未清理")
				}
			} else {
				if record["cleanupStatus"] != "pending" || stringValue(record["reasonCode"]) == "" {
					t.Fatal("未完成远端清理被伪称成功")
				}
				if failure != "remove" && removals.Load() != 0 {
					t.Fatal("删除了不应清理的节点")
				}
			}
		})
	}
}

func TestSwarmDepartureJoinCleanupSkipsDifferentClusterAndCurrentNode(t *testing.T) {
	for _, record := range []domain.Object{
		{"nodeId": "old_node", "clusterId": "cluster_old", "cleanupStatus": "pending"},
		{"nodeId": "current_node", "clusterId": "cluster_new", "cleanupStatus": "pending"},
		{"nodeId": "unknown_cluster_node", "clusterId": "", "cleanupStatus": "pending"},
	} {
		input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{Server: domain.Object{"id": "server", "swarmDepartures": []domain.Object{record}}})
		// 无可调用manager，任何误清理或错误CID校验都会暴露为失败。
		if err := cleanupSwarmDeparturesBeforeJoin(context.Background(), nil, input, remoteSwarmStatus{NodeID: "current_node"}, remoteSwarmStatus{ClusterID: "cluster_new"}); err != nil {
			t.Fatalf("旧记录误挡新集群或新成员：%v", err)
		}
		if record["cleanupStatus"] != "pending" {
			t.Fatal("无关离群事实被覆盖")
		}
	}
}

func TestSwarmDepartureJoinCleanupRemovesOnlySameClusterOldOwnedNode(t *testing.T) {
	var removed atomic.Bool
	manager, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker node ls -q":
			if removed.Load() {
				return "current_node", 0
			}
			return "old_node\ncurrent_node", 0
		case "docker node rm 'old_node'":
			removed.Store(true)
			return "", 0
		default:
			if strings.HasPrefix(command, "docker node inspect --format ") && strings.HasSuffix(command, "'old_node'") {
				return "old_node|server|worker|down", 0
			}
			t.Error("旧记录清理触碰了当前/无关节点")
			return "", 1
		}
	})
	old := domain.Object{"nodeId": "old_node", "clusterId": "cluster_new", "cleanupStatus": "pending", "departedAt": "earlier", "operationRunId": "leave_original"}
	other := domain.Object{"nodeId": "other_old", "clusterId": "cluster_other", "cleanupStatus": "pending", "operationRunId": "leave_other"}
	var saved domain.Object
	input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{Server: domain.Object{"id": "server", "swarmDepartures": []domain.Object{other, old}}, SaveSwarmDeparture: func(_ context.Context, value domain.Object) error { saved = cloneObject(value); return nil }})
	if err := cleanupSwarmDeparturesBeforeJoin(context.Background(), manager, input, remoteSwarmStatus{NodeID: "current_node"}, remoteSwarmStatus{ClusterID: "cluster_new"}); err != nil {
		t.Fatal(err)
	}
	if !removed.Load() || saved["nodeId"] != "old_node" || saved["cleanupStatus"] != "removed" || saved["operationRunId"] != "leave_original" || other["cleanupStatus"] != "pending" {
		t.Fatal("清理丢失原离群操作/跨群身份")
	}
}
