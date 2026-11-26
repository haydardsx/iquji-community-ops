package application

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSwarmWorkerRemovalRequiresExactOwnershipAndOfflineState(t *testing.T) {
	for _, tc := range []struct {
		name, output      string
		exists, removable bool
		code              string
	}{
		{"离线归属节点", "node_one|srv_one|worker|down", true, true, ""},
		{"在线归属节点", "node_one|srv_one|worker|ready", true, false, ""},
		{"未知状态", "node_one|srv_one|worker|unknown", true, false, ""},
		{"其他服务器", "node_one|srv_other|worker|down", false, false, "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH"},
		{"管理节点", "node_one|srv_one|manager|down", false, false, "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH"},
		{"节点不匹配", "node_other|srv_one|worker|down", false, false, "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(command string) (string, error) {
				if command == "docker node ls -q" {
					return "node_one\nnode_two", nil
				}
				if !strings.HasSuffix(command, "'node_one'") {
					t.Fatal("未限定节点标识")
				}
				return tc.output, nil
			}
			exists, removable, err := inspectOwnedSwarmWorker(run, "node_one", "srv_one")
			code := ""
			if err != nil {
				code = err.Error()
			}
			if exists != tc.exists || removable != tc.removable || code != tc.code {
				t.Fatalf("归属检查结果不正确：%v %v %v", exists, removable, err)
			}
		})
	}
}

func TestSwarmWorkerRemovalRetryDistinguishesMissingNodeFromUnavailableManager(t *testing.T) {
	exists, removable, err := inspectOwnedSwarmWorker(func(string) (string, error) { return "another_node", nil }, "node_one", "srv_one")
	if err != nil || exists || removable {
		t.Fatalf("已移除节点重试应完成：%v %v %v", exists, removable, err)
	}
	_, _, err = inspectOwnedSwarmWorker(func(string) (string, error) { return "", errors.New("connection lost") }, "node_one", "srv_one")
	if err == nil || err.Error() != "SWARM_MANAGER_NOT_READY" {
		t.Fatal("连接失败不能视作节点已删除")
	}
}

func TestManagerDissolutionRequiresOnlyLocalNodeAndNoServices(t *testing.T) {
	for _, test := range []struct{ name, services, nodes, code string }{
		{"唯一管理节点", "", "manager_one", ""},
		{"多个管理节点", "", "manager_one\nmanager_two", "SWARM_LEAVE_BLOCKED_NODES"},
		{"另有工作节点", "", "manager_one\nworker_one", "SWARM_LEAVE_BLOCKED_NODES"},
		{"节点查询为空", "", "", "SWARM_LEAVE_BLOCKED_NODES"},
		{"唯一节点身份不符", "", "other_node", "SWARM_LEAVE_BLOCKED_NODES"},
		{"集群仍有服务", "service_one", "manager_one", "SWARM_LEAVE_BLOCKED_SERVICES"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var left atomic.Bool
			_, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch {
				case command == "docker info --format '{{json .Swarm}}'":
					if left.Load() {
						return `{"LocalNodeState":"inactive"}`, 0
					}
					return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_one","Cluster":{"ID":"example_cluster"}}`, 0
				case command == "docker node inspect 'manager_one' --format '{{.ManagerStatus.Addr}}'":
					return "192.0.2.10:2377", 0
				case strings.HasPrefix(command, "docker network inspect "):
					return "overlay|swarm", 0
				case command == "docker service ls -q":
					return test.services, 0
				case command == "docker node ls -q":
					return test.nodes, 0
				case command == "docker swarm leave --force":
					left.Store(true)
					return "", 0
				}
				t.Error("接收到未预期的模拟命令")
				return "", 1
			})
			_, err := NewSystemServerRuntimeExecutor().UninstallDockerSwarm(context.Background(), ServerRuntimeExecutionInput{Server: server, SSHKey: key})
			if test.code != "" && (err == nil || err.Error() != test.code) || test.code == "" && err != nil || left.Load() != (test.code == "") {
				t.Fatalf("模拟管理节点的解散保护不匹配：left=%t err=%v", left.Load(), err)
			}
		})
	}
}
