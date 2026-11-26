package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"golang.org/x/crypto/ssh"
)

func TestOptionalRuntimeProbeSkipsMissingComponents(t *testing.T) {
	client, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"inactive","ControlAvailable":false}`, 0
		case "docker container ls -a --format '{{.Names}}'":
			return "", 0
		default:
			t.Error("未预期的探测命令")
			return "", 1
		}
	})
	input := ServerRuntimeExecutionInput{Server: server, SSHKey: key}
	result := domain.Object{}
	applyRemoteSwarmProbe(context.Background(), client, input, result)
	applyRemoteTraefikProbe(context.Background(), client, input, result)
	applyRemoteJenkinsProbe(context.Background(), client, input, result)
	report := legacyRuntimeProcess(domain.CheckKindRuntimeProbe, domain.Resource("server", "server", "server"), "probe", result)
	if !report.Decision.Allowed || len(report.Checks) != 3 || len(report.Decision.Warnings) != 0 {
		t.Fatal("可选组件未安装导致全局告警")
	}
	for _, check := range report.Checks {
		if check.Outcome != domain.CheckOutcomeSkipped || check.Severity != domain.CheckSeverityInfo {
			t.Fatal("未安装探测未标为中性跳过")
		}
	}
}

func TestInstalledJenkinsFaultIsProbedWithoutApplicationReferences(t *testing.T) {
	client, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if command == "docker container ls -a --format '{{.Names}}'" {
			return "iquops-jenkins", 0
		}
		if strings.HasPrefix(command, "docker inspect -f ") {
			return "false|managed-image|iquops|jenkins|1", 0
		}
		t.Error("未预期的探测命令")
		return "", 1
	})
	result := domain.Object{}
	applyRemoteJenkinsProbe(context.Background(), client, ServerRuntimeExecutionInput{Server: server, SSHKey: key}, result)
	checks := objectSlice(result["checks"])
	if len(checks) != 1 || checks[0]["status"] != serverRuntimeCheckDanger || checks[0]["summary"] != "JENKINS_HEALTHCHECK_FAILED" {
		t.Fatal("未被应用引用的已安装 Jenkins 故障被忽略")
	}
}

func TestInstalledTraefikFaultIsProbedWithoutApplicationReferences(t *testing.T) {
	client, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case command == "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster"}}`, 0
		case command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
			return "192.0.2.10:2377", 0
		case command == "docker service ls --format '{{.Name}}'":
			return "iquops-traefik", 0
		case strings.HasPrefix(command, "docker service ls --filter "):
			return "iquops-traefik|0/1", 0
		case strings.Contains(command, "{{.Id}}|{{.Driver}}|{{.Scope}}"):
			return "network|overlay|swarm", 0
		case strings.HasPrefix(command, "docker network inspect "):
			return "overlay|swarm", 0
		case strings.Contains(command, "{{range .Spec.TaskTemplate.Networks}}"):
			return "network", 0
		case strings.HasPrefix(command, "docker service inspect -f "):
			return "iquops|traefik_gateway|traefik:v3.7", 0
		default:
			t.Error("未预期的探测命令")
			return "", 1
		}
	})
	result := domain.Object{}
	applyRemoteTraefikProbe(context.Background(), client, ServerRuntimeExecutionInput{Server: server, SSHKey: key}, result)
	checks := objectSlice(result["checks"])
	if len(checks) != 1 || checks[0]["status"] != serverRuntimeCheckDanger || !strings.HasPrefix(stringValue(checks[0]["summary"]), "TRAEFIK_GATEWAY_UNAVAILABLE") {
		t.Fatal("未被应用引用的已安装 Traefik 故障被忽略")
	}
}

func TestSwarmWorkerUninstallStillRejectsRemoteRunningTasks(t *testing.T) {
	_, manager, managerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case command == "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_one"}}`, 0
		case command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
			return "192.0.2.10:2377", 0
		case command == "docker node ls -q":
			return "manager_node\nworker_node", 0
		case strings.HasPrefix(command, "docker node inspect --format "):
			return "worker_node|server|worker|ready", 0
		case strings.HasPrefix(command, "docker node ps "):
			return "running_task", 0
		default:
			t.Error("运行中的 Worker 卸载执行了额外命令")
			return "", 1
		}
	})
	workerClient, worker, workerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
		if command == swarmLocalTaskContainersCommand {
			return "", 0
		}
		t.Error("运行中的 Worker 被执行了退出命令")
		return "", 1
	})
	input := ServerRuntimeExecutionInput{Server: worker, SSHKey: workerKey, SwarmManagerServer: manager, SwarmManagerSSHKey: managerKey, SwarmManagerServerID: "manager"}
	err := uninstallSwarmWorker(context.Background(), workerClient, input, remoteSwarmStatus{Active: true, Role: "worker", NodeID: "worker_node"})
	if err == nil || err.Error() != "SWARM_LEAVE_BLOCKED_SERVICES" {
		t.Fatalf("运行中的 Worker 未被远端任务检查阻断：%v", err)
	}
}

func TestExistingWorkerCanBeAssociatedAndVerifiedWithoutJoiningAgain(t *testing.T) {
	_, manager, managerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case command == "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"manager_node","Cluster":{"ID":"cluster_one"}}`, 0
		case command == "docker node inspect 'manager_node' --format '{{.ManagerStatus.Addr}}'":
			return "192.0.2.10:2377", 0
		case strings.HasPrefix(command, "docker network inspect "):
			return "overlay|swarm", 0
		case command == "docker node inspect 'worker_node' --format '{{.Status.Addr}}'":
			return "192.0.2.24", 0
		case command == "docker node ls -q":
			return "manager_node\nworker_node", 0
		case strings.HasPrefix(command, "docker node update --label-add "):
			return "worker_node", 0
		default:
			t.Error("既有 Worker 关联执行了额外命令")
			return "", 1
		}
	})
	workerClient, worker, workerKey := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch command {
		case "docker version --format '{{.Server.Version}}'":
			return "29.0.0", 0
		case "docker info --format '{{json .Swarm}}'":
			return `{"LocalNodeState":"active","ControlAvailable":false,"NodeID":"worker_node"}`, 0
		default:
			t.Error("既有 Worker 被重复加入或执行其他变更")
			return "", 1
		}
	})
	input := ServerRuntimeExecutionInput{Server: worker, SSHKey: workerKey, SwarmMode: "worker", SwarmManagerServer: manager, SwarmManagerSSHKey: managerKey, SwarmManagerServerID: "manager"}
	if _, err := NewSystemServerRuntimeExecutor().InstallDockerSwarm(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	result := domain.Object{}
	applyRemoteSwarmProbe(context.Background(), workerClient, input, result)
	metrics := objectMapFromAny(result["metrics"])
	if metrics["swarmClusterId"] != "cluster_one" || metrics["swarmVerificationStatus"] != "verified" || metrics["swarmManagerServerId"] != "manager" {
		t.Fatal("手工加入的 Worker 未被正确收编")
	}
}

// 使用本机临时 SSH 服务覆盖实际命令入口，不访问外部服务器。
func runtimeProbeSSH(t *testing.T, run func(string) (string, uint32)) (*ssh.Client, domain.Object, string) {
	t.Helper()
	return runtimeProbeSSHWithInput(t, func(command string, _ io.Reader) (string, uint32) { return run(command) })
}

func runtimeProbeSSHWithInput(t *testing.T, run func(string, io.Reader) (string, uint32)) (*ssh.Client, domain.Object, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "runtime-probe-test")
	if err != nil {
		t.Fatal(err)
	}
	key := string(pem.EncodeToMemory(block))
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go serveRuntimeProbeSSH(connection, config, run)
		}
	}()
	server := domain.Object{"id": "server", "publicHost": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port}
	domain.NormalizeServerConnections([]domain.Object{server})
	client, err := sshClient(context.Background(), server, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, server, key
}

func serveRuntimeProbeSSH(connection net.Conn, config *ssh.ServerConfig, run func(string, io.Reader) (string, uint32)) {
	defer connection.Close()
	server, channels, requests, err := ssh.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(ssh.Prohibited, "转发探测未启用")
			continue
		}
		stream, requests, err := channel.Accept()
		if err != nil {
			return
		}
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			var payload struct{ Command string }
			if ssh.Unmarshal(request.Payload, &payload) != nil {
				_ = request.Reply(false, nil)
				break
			}
			_ = request.Reply(true, nil)
			output, exitStatus := run(payload.Command, stream)
			_, _ = io.WriteString(stream, output)
			_, _ = stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exitStatus}))
			_ = stream.Close()
			break
		}
	}
}
