package application

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestSwarmManagerEndpointKeepsTheActualPortAndRejectsInvalidOutput(t *testing.T) {
	for _, test := range []struct{ value, expected string }{
		{"192.0.2.10:42377", "192.0.2.10:42377"},
		{"[2001:db8::10]:42377", "[2001:db8::10]:42377"},
		{"192.0.2.10", ""}, {"192.0.2.10:0", ""}, {"192.0.2.10:65536", ""},
		{"0.0.0.0:2377", ""}, {"invalid\noutput:2377", ""},
	} {
		endpoint, err := normalizeSwarmManagerEndpoint(test.value)
		if endpoint != test.expected || (err != nil) != (test.expected == "") {
			t.Fatalf("控制端点解析错误：%q -> %q %v", test.value, endpoint, err)
		}
	}
}

func TestSwarmWorkerJoinReconcilesActualMembershipAndDoesNotRepeatPendingJoin(t *testing.T) {
	const token = "demo-join"
	for _, test := range []struct {
		name, initialState, finalState, commandError, expectedCode, mode string
		exitStatus                                                       uint32
		joinCount                                                        int32
	}{
		{"显式公网加入", "inactive", "active", "", "", "public", 0, 1},
		{"默认模式加入", "inactive", "active", "", "", "", 0, 1},
		{"命令错误但身份已完成", "inactive", "active", "temporary info read failed " + token, "", "", 1, 1},
		{"已有后台加入", "pending", "active", "", "", "", 0, 0},
		{"无效令牌", "inactive", "inactive", "invalid join token " + token, "SWARM_JOIN_TOKEN_REJECTED", "", 1, 1},
		{"网络失败保留错误状态", "inactive", "error", "Timeout was reached before node joined " + token, "SWARM_MANAGER_ENDPOINT_UNAVAILABLE", "", 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var joins, workerReads atomic.Int32
			var joined atomic.Bool
			logs := []string{}
			_, manager, managerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
				switch {
				case command == "docker info --format '{{json .Swarm}}'":
					return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_one"}}`, 0
				case strings.HasPrefix(command, "docker network inspect "):
					return "overlay|swarm", 0
				case command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
					return "192.0.2.10:42377", 0
				case command == "docker node inspect 'worker_node' --format '{{.Status.Addr}}'":
					return "192.0.2.24", 0
				case command == "docker swarm join-token -q worker":
					return token, 0
				case command == "docker node ls -q":
					return "manager_node\nworker_node", 0
				case strings.HasPrefix(command, "docker node update --label-add "):
					return "worker_node", 0
				default:
					t.Error("未预期的 Manager 命令")
					return "", 1
				}
			})
			_, worker, workerKey := runtimeProbeSSHWithInput(t, func(command string, input io.Reader) (string, uint32) {
				switch {
				case command == "docker version --format '{{.Server.Version}}'":
					return "29.0.0", 0
				case command == "docker info --format '{{json .Swarm}}'":
					state := test.initialState
					if joined.Load() || test.initialState == "pending" && workerReads.Load() > 0 {
						state = test.finalState
					}
					workerReads.Add(1)
					if state == "active" {
						return `{"LocalNodeState":"active","ControlAvailable":false,"NodeID":"worker_node","NodeAddr":"192.0.2.24"}`, 0
					}
					if state == "error" {
						return `{"LocalNodeState":"error","Error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded"}`, 0
					}
					return `{"LocalNodeState":"` + state + `"}`, 0
				case command == "docker system dial-stdio":
					joins.Add(1)
					joined.Store(true)
					request, err := http.ReadRequest(bufio.NewReader(input))
					if err != nil || request.Method != http.MethodPost || request.URL.Path != "/swarm/join" {
						t.Error("加入请求的方法或路径不符")
						return "", 1
					}
					defer request.Body.Close()
					var body struct {
						AdvertiseAddr, ListenAddr, JoinToken, DataPathAddr string
						RemoteAddrs                                        []string
					}
					if json.NewDecoder(request.Body).Decode(&body) != nil || body.AdvertiseAddr != "192.0.2.24" || body.ListenAddr != "0.0.0.0:2377" || body.JoinToken != token || len(body.RemoteAddrs) != 1 || body.RemoteAddrs[0] != "192.0.2.10:42377" {
						t.Error("加入请求未通过标准输入交付预期端点和凭据")
					}
					if test.mode == "" && body.DataPathAddr != "" || test.mode != "" && body.DataPathAddr != body.AdvertiseAddr {
						t.Error("数据通道地址与连接模式不符")
					}
					if test.exitStatus == 0 {
						return "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n", 0
					}
					reply, _ := json.Marshal(map[string]string{"message": test.commandError})
					return fmt.Sprintf("HTTP/1.1 503 Service Unavailable\r\nContent-Length: %d\r\n\r\n%s", len(reply), reply), 0
				default:
					t.Error("未预期的 Worker 命令")
					return "", 1
				}
			})
			input := ServerRuntimeExecutionInput{Server: worker, SSHKey: workerKey, SwarmMode: "worker", SwarmConnectionMode: test.mode, SwarmAdvertiseAddr: "192.0.2.24", SwarmManagerServer: manager, SwarmManagerSSHKey: managerKey, SwarmManagerServerID: "manager", LogStep: func(_, _, _, message string) { logs = append(logs, message) }}
			_, err := NewSystemServerRuntimeExecutor().InstallDockerSwarm(context.Background(), input)
			code := ""
			if err != nil {
				code = err.Error()
			}
			if code != test.expectedCode || joins.Load() != test.joinCount {
				t.Fatalf("加入状态未收敛：code=%s joins=%d", code, joins.Load())
			}
			if strings.Contains(strings.Join(logs, "\n"), token) || strings.Contains(code, token) {
				t.Fatal("诊断日志或公开错误包含加入令牌")
			}
		})
	}
}

func TestSwarmWorkerJoinRejectsFailedAndLockedStatesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		state remoteSwarmStatus
		code  string
	}{
		{remoteSwarmStatus{Status: "error", FailureCode: "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"}, "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"},
		{remoteSwarmStatus{Status: "error"}, "SWARM_NODE_ERROR"},
		{remoteSwarmStatus{Status: "locked"}, "SWARM_NODE_LOCKED"},
		{remoteSwarmStatus{Status: "unknown"}, "SWARM_JOIN_STATE_INVALID"},
		{remoteSwarmStatus{Status: "active", Active: true, Role: "manager"}, "SWARM_ROLE_CONFLICT"},
	} {
		err := installSwarmWorker(context.Background(), nil, ServerRuntimeExecutionInput{}, test.state)
		if err == nil || err.Error() != test.code {
			t.Fatal("失败或未知状态未阻断")
		}
	}
}

func TestPendingSwarmWaitIsBoundedAndPreservesCallerCancellation(t *testing.T) {
	client, _, _ := runtimeProbeSSH(t, func(string) (string, uint32) { return `{"LocalNodeState":"pending"}`, 0 })
	_, err := waitForSwarmWorker(context.Background(), client, ServerRuntimeExecutionInput{}, 10*time.Millisecond)
	if err == nil || err.Error() != "SWARM_JOIN_PENDING" {
		t.Fatalf("后台加入超时未给出待完成状态：%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = waitForSwarmWorker(ctx, client, ServerRuntimeExecutionInput{}, time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("调用方时限被覆盖：%v", err)
	}
}

func TestSwarmNodeLabelCancellationPreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if command == "docker info --format '{{json .Swarm}}'" {
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster"}}`, 0
		}
		if command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'" {
			return "192.0.2.10:2377", 0
		}
		if command == "docker node ls -q" {
			return "worker_node", 0
		}
		if strings.HasPrefix(command, "docker node update ") {
			cancel()
			return "", 1
		}
		t.Error("未预期命令")
		return "", 1
	})
	err := verifyAndLabelSwarmWorker(ctx, manager, ServerRuntimeExecutionInput{Server: map[string]any{"id": "worker"}}, remoteSwarmStatus{Active: true, Role: "worker", NodeID: "worker_node"}, remoteSwarmStatus{Active: true, Role: "manager", ClusterID: "cluster"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("标签阶段取消被覆盖：%v", err)
	}
}

func TestSwarmJoinRejectsManagerClusterReplacementBeforeLabeling(t *testing.T) {
	manager, _, _ := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if command == "docker info --format '{{json .Swarm}}'" {
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"new_cluster"}}`, 0
		}
		if command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'" {
			return "192.0.2.10:2377", 0
		}
		t.Error("目标集群被替换后仍执行了成员变更")
		return "", 1
	})
	err := verifyAndLabelSwarmWorker(context.Background(), manager, ServerRuntimeExecutionInput{}, remoteSwarmStatus{Active: true, Role: "worker", NodeID: "worker_node"}, remoteSwarmStatus{Active: true, Role: "manager", ClusterID: "original_cluster"})
	if err == nil || err.Error() != "SWARM_CLUSTER_MISMATCH" {
		t.Fatalf("加入期间的集群替换未阻断：%v", err)
	}
}

type swarmTestDialer struct{ dialError error }

func (d swarmTestDialer) NewSession() (*ssh.Session, error) { return nil, errors.New("unused") }
func (d swarmTestDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, d.dialError
}

func TestSwarmNetworkPreflightDistinguishesUnsupportedForwardingAndNetworkFailure(t *testing.T) {
	input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{})
	for _, test := range []struct {
		err      error
		expected string
	}{
		{&ssh.OpenChannelError{Reason: ssh.Prohibited}, ""},
		{&ssh.OpenChannelError{Reason: ssh.UnknownChannelType}, ""},
		{&ssh.OpenChannelError{Reason: ssh.ConnectionFailed}, "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"},
		{context.DeadlineExceeded, "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"},
	} {
		err := probeSwarmManagerEndpoint(context.Background(), swarmTestDialer{dialError: test.err}, "192.0.2.10:42377", input)
		code := ""
		if err != nil {
			code = err.Error()
		}
		if code != test.expected {
			t.Fatalf("转发能力与连通性混淆：%v -> %v", test.err, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := probeSwarmManagerEndpoint(ctx, swarmTestDialer{dialError: context.Canceled}, "192.0.2.10:42377", input); !errors.Is(err, context.Canceled) {
		t.Fatalf("预检取消被覆盖：%v", err)
	}
}

func TestSwarmJoinDiagnosticsExposeOnlyAllowlistedCodes(t *testing.T) {
	for _, test := range []struct{ output, code string }{
		{"rpc error: code = DeadlineExceeded desc = context deadline exceeded", "SWARM_MANAGER_ENDPOINT_UNAVAILABLE"},
		{"invalid advertise address", "SWARM_ADVERTISE_ADDRESS_INVALID"},
		{"could not choose an IP address to advertise", "SWARM_ADVERTISE_ADDRESS_INVALID"},
		{"invalid join token SWMTKN-secret", "SWARM_JOIN_TOKEN_REJECTED"},
		{"x509: certificate has expired", "SWARM_JOIN_CERTIFICATE_FAILED"},
		{"This node is already part of a swarm", "SWARM_JOIN_STATE_INVALID"},
		{"unknown private command SWMTKN-secret", "SWARM_WORKER_JOIN_FAILED"},
	} {
		if actual := swarmJoinFailureCode(test.output); actual != test.code {
			t.Fatalf("诊断类别错误：%s", actual)
		}
	}
}
