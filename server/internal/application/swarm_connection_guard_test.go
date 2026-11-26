package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmConnectionSystemRejectsMigrationBeforeLocalStateEngineAndCheckpoint(t *testing.T) {
	var unexpected atomic.Int32
	_, server, key := runtimeProbeSSH(t, func(command string) (string, uint32) {
		switch {
		case command == "docker info --format '{{json .Swarm}}'":
			// 旧缓存和本机地址都指向公网，只有真实 ManagerStatus 仍为私网。
			return `{"LocalNodeState":"active","ControlAvailable":true,"NodeID":"node_manager","NodeAddr":"192.0.2.10","Cluster":{"ID":"cluster_example"},"RemoteManagers":[{"NodeID":"node_manager","Addr":"192.0.2.10:3377"}]}`, 0
		case command == "docker node inspect 'node_manager' --format '{{.ManagerStatus.Addr}}'":
			return netip.AddrPortFrom(fixtureManagerIP, 3377).String(), 0
		case strings.HasPrefix(command, "docker network inspect "):
			return "overlay|swarm", 0
		default:
			unexpected.Add(1)
			return "", 1
		}
	})
	saves := 0
	input := ServerRuntimeExecutionInput{
		Server: server, SSHKey: key, SwarmConnectionMode: "public", SwarmAdvertiseAddr: "192.0.2.10",
		SaveSwarmConnectionCheckpoint: func(context.Context, domain.Object) error { saves++; return nil },
	}
	_, err := NewSystemServerRuntimeExecutor().ReconfigureDockerSwarm(context.Background(), input)
	if err == nil || err.Error() != "SWARM_CONNECTION_MIGRATION_REQUIRED" || unexpected.Load() != 0 || saves != 0 {
		t.Fatalf("实际执行入口未在读取本机状态、Engine 请求或检查点前阻止迁移：%v commands=%d saves=%d", err, unexpected.Load(), saves)
	}
}

func TestSwarmConnectionExistingEndpointOnlyVerifiesWithoutPostsOrNewCheckpoint(t *testing.T) {
	for _, test := range []struct {
		name, requested, actual, localEndpoint, dataPath, code string
	}{
		{"隐式数据地址", "192.0.2.10", "192.0.2.10:3377", "192.0.2.10:3377", "", ""},
		{"显式数据地址", "192.0.2.10", "192.0.2.10:3377", "192.0.2.10:3377", "192.0.2.10", ""},
		{"等价IPv6", "2001:0db8:0:0:0:0:0:1", "[2001:db8::1]:3377", "[2001:db8::1]:3377", "2001:db8::1", ""},
		{"数据地址不同", "192.0.2.10", "192.0.2.10:3377", "192.0.2.10:3377", "192.0.2.11", "SWARM_CONNECTION_MIGRATION_REQUIRED"},
		{"本机配置与实际端点不同", "192.0.2.10", "192.0.2.10:3377", netip.AddrPortFrom(fixtureManagerIP, 3377).String(), "", "SWARM_CONNECTION_MIGRATION_REQUIRED"},
		{"实际端点未知", "192.0.2.10", "", "192.0.2.10:3377", "", "SWARM_CONNECTION_STATE_UNAVAILABLE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSwarmConnectionFixture()
			f.local.AdvertiseAddr, f.local.DataPathAddr = test.localEndpoint, test.dataPath
			reads, writes, saves := 0, 0, 0
			call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
				if method != http.MethodGet {
					writes++
					return nil, errors.New("unexpected mutation")
				}
				reads++
				return f.call(ctx, method, path, body)
			}
			input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{
				SwarmConnectionMode: "public", SwarmAdvertiseAddr: test.requested,
				SaveSwarmConnectionCheckpoint: func(context.Context, domain.Object) error { saves++; return nil },
			})
			current := remoteSwarmStatus{Active: true, ControlAvailable: true, Role: "manager", NodeID: "node_manager", ClusterID: f.clusterID, ManagerAddress: test.actual}
			err := verifyOrRestoreSwarmConnection(context.Background(), input, current, call, func(context.Context) (swarmLocalConnection, error) { return f.local, nil })
			code := ""
			if err != nil {
				code = err.Error()
			}
			if code != test.code || writes != 0 || saves != 0 || len(f.posts) != 0 {
				t.Fatalf("现有端点执行了初始化或新建检查点：code=%s writes=%d saves=%d", code, writes, saves)
			}
			if test.code == "" && reads == 0 || test.code != "" && reads != 0 {
				t.Fatalf("未按前置判断进入完整只读核验：reads=%d", reads)
			}
		})
	}
}

func TestSwarmConnectionLegacyRestoreBypassesMigrationGuard(t *testing.T) {
	f := newSwarmConnectionFixture()
	original := f.baseline(t)
	var checkpoint domain.Object
	input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{
		SwarmConnectionMode: "private", SwarmAdvertiseAddr: fixtureManagerIP.String(),
		SaveSwarmConnectionCheckpoint: func(_ context.Context, value domain.Object) error { checkpoint = value; return nil },
	})
	if _, err := prepareSwarmConnectionCheckpoint(context.Background(), input, original); err != nil {
		t.Fatal(err)
	}
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	input.SwarmConnectionRestoreOnly, input.SwarmConnectionCheckpoint = true, checkpoint
	current := remoteSwarmStatus{Active: true, ControlAvailable: true, Role: "manager", NodeID: "node_manager", ClusterID: f.clusterID, ManagerAddress: f.local.AdvertiseAddr}
	err := verifyOrRestoreSwarmConnection(context.Background(), input, current, f.call, func(context.Context) (swarmLocalConnection, error) { return f.local, nil })
	if !errors.Is(err, context.Canceled) || f.local != original.Local || len(f.posts) != 1 {
		t.Fatalf("旧任务恢复被迁移保护阻止：%v posts=%d", err, len(f.posts))
	}
}

func TestSwarmConnectionLegacyRestoreFailureRemainsFailure(t *testing.T) {
	f := newSwarmConnectionFixture()
	original := f.baseline(t)
	var checkpoint domain.Object
	input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{
		SwarmConnectionMode: "private", SwarmAdvertiseAddr: fixtureManagerIP.String(),
		SaveSwarmConnectionCheckpoint: func(_ context.Context, value domain.Object) error { checkpoint = value; return nil },
	})
	if _, err := prepareSwarmConnectionCheckpoint(context.Background(), input, original); err != nil {
		t.Fatal(err)
	}
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	input.SwarmConnectionRestoreOnly, input.SwarmConnectionCheckpoint = true, checkpoint
	logs := []string{}
	input.LogStep = func(_, _, _, message string) { logs = append(logs, message) }
	call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		if method == http.MethodPost {
			return nil, errors.New("synthetic upstream error")
		}
		return f.call(ctx, method, path, body)
	}
	current := remoteSwarmStatus{Active: true, ControlAvailable: true, Role: "manager", NodeID: "node_manager", ClusterID: f.clusterID, ManagerAddress: f.local.AdvertiseAddr}
	err := verifyOrRestoreSwarmConnection(context.Background(), input, current, call, func(context.Context) (swarmLocalConnection, error) { return f.local, nil })
	if err == nil || err.Error() != "SWARM_CONNECTION_ROLLBACK_FAILED" || strings.Contains(strings.Join(logs, "\n"), "synthetic upstream error") || !strings.Contains(strings.Join(logs, "\n"), "ENGINE_REQUEST_FAILED") {
		t.Fatal("恢复失败未保留稳定错误码或安全诊断")
	}
}

func TestSwarmConnectionReadOnlyVerificationKeepsCancellation(t *testing.T) {
	f := newSwarmConnectionFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads := 0
	call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		reads++
		if reads == 6 {
			cancel()
		}
		return f.call(ctx, method, path, body)
	}
	input := withServerRuntimeLogStep(ServerRuntimeExecutionInput{SwarmConnectionMode: "private", SwarmAdvertiseAddr: fixtureManagerIP.String()})
	current := remoteSwarmStatus{Active: true, ControlAvailable: true, Role: "manager", NodeID: "node_manager", ClusterID: f.clusterID, ManagerAddress: f.local.AdvertiseAddr}
	err := verifyOrRestoreSwarmConnection(ctx, input, current, call, func(context.Context) (swarmLocalConnection, error) { return f.local, nil })
	if !errors.Is(err, context.Canceled) || len(f.posts) != 0 {
		t.Fatalf("只读核验吞掉取消状态或触发初始化：%v", err)
	}
}
