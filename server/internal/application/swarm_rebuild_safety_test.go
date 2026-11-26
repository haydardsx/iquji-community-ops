package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildAllowsIndependentJenkinsAndComposeContainers(t *testing.T) {
	f := newSwarmRebuildFixture()
	remote := f.remote()
	remote.Call = func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		if method == http.MethodGet && path == "/containers/json?all=1" {
			return json.Marshal([]domain.Object{{"Id": "standalone_example", "State": "running", "Mounts": []domain.Object{{"Source": "/srv/example"}}, "NetworkSettings": domain.Object{"Networks": domain.Object{"bridge": domain.Object{"NetworkID": "local_network"}}}}})
		}
		return f.call(ctx, method, path, body)
	}
	if _, err := executeSwarmRebuild(context.Background(), f.input(), remote); err != nil {
		t.Fatalf("独立容器误挡受限重建：%v", err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "standalone_example") {
			t.Fatal("重建触碰了独立容器")
		}
	}
}

func TestSwarmRebuildCheckpointFailureNeverStopsGateway(t *testing.T) {
	f := newSwarmRebuildFixture()
	input := f.input()
	input.SaveSwarmRebuildCheckpoint = func(context.Context, domain.Object) error { return errors.New("private store diagnostic") }
	_, err := executeSwarmRebuild(context.Background(), input, f.remote())
	if err == nil || err.Error() != "SWARM_REBUILD_CHECKPOINT_FAILED" || len(f.services) != 1 || f.leaveCount != 0 {
		t.Fatalf("检查点未持久化即破坏了运行状态：%v", err)
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, http.MethodPost) || strings.HasPrefix(call, http.MethodDelete) {
			t.Fatal("检查点失败后发生Engine写入")
		}
	}
}

func TestSwarmRebuildGatewayReplacementDuringBackupIsNeverDeleted(t *testing.T) {
	f := newSwarmRebuildFixture()
	remote := f.remote()
	remote.Output = func(ctx context.Context, command string) (string, error) {
		output, err := f.output(ctx, command)
		if strings.Contains(command, "cat --") && len(f.services) > 0 {
			f.services[0]["ID"] = "replacement_service"
			objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["Labels"])["manual"] = "changed"
		}
		return output, err
	}
	_, err := executeSwarmRebuild(context.Background(), f.input(), remote)
	if err == nil || f.leaveCount != 0 || len(f.services) != 1 || f.services[0]["ID"] != "replacement_service" {
		t.Fatalf("备份后替换的同名网关被删除：%v", err)
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, http.MethodDelete) {
			t.Fatal("替换检测后仍执行删除")
		}
	}
}

func TestSwarmRebuildRecoveryRefusesUnknownOrChangedNetwork(t *testing.T) {
	for _, extra := range []bool{false, true} {
		f := newSwarmRebuildFixture()
		if _, err := executeSwarmRebuild(context.Background(), f.input(), f.remote()); err != nil {
			t.Fatal(err)
		}
		if extra {
			network := cloneObject(f.networks[1])
			network["Id"], network["Name"] = "unknown_network", "manual-network"
			f.networks = append(f.networks, network)
		} else {
			objectMapFromAny(f.networks[1]["IPAM"])["Config"] = []domain.Object{{"Subnet": "203.0.113.0/24", "Gateway": "203.0.113.1"}}
		}
		input := f.input()
		input.SwarmRebuildCheckpoint = cloneObject(f.checkpoint)
		input.SwarmRebuildCheckpoint["phase"] = "restored"
		input.SwarmRebuildRestoreOnly = true
		before := len(f.calls)
		_, err := executeSwarmRebuild(context.Background(), input, f.remote())
		if err == nil || err.Error() != "SWARM_REBUILD_RECOVERY_REQUIRED" {
			t.Fatalf("恢复接受了未备份的网络变更：%v", err)
		}
		for _, call := range f.calls[before:] {
			if strings.HasPrefix(call, http.MethodPost) || strings.HasPrefix(call, http.MethodDelete) {
				t.Fatal("恢复删除未备份网络")
			}
		}
	}
}

func TestSwarmRebuildIgnoresStaleInternalOwnerLabelInUserBaseline(t *testing.T) {
	f := newSwarmRebuildFixture()
	objectMapFromAny(f.cluster["Spec"])["Labels"] = domain.Object{swarmRebuildOwnerLabel: "task_previous", "example": "preserved"}
	if _, err := executeSwarmRebuild(context.Background(), f.input(), f.remote()); err != nil {
		t.Fatalf("历史内部标记导致新重建失败：%v", err)
	}
	labels := objectMapFromAny(objectMapFromAny(f.cluster["Spec"])["Labels"])
	if labels["example"] != "preserved" || labels[swarmRebuildOwnerLabel] != nil {
		t.Fatal("用户标签未保留或内部标记未清理")
	}
}

func TestSwarmRebuildVerifiesTopLevelClusterNetworkSettings(t *testing.T) {
	for _, key := range []string{"DataPathPort", "SubnetSize", "DefaultAddrPool"} {
		f := newSwarmRebuildFixture()
		if _, err := executeSwarmRebuild(context.Background(), f.input(), f.remote()); err != nil {
			t.Fatal(err)
		}
		var m swarmRebuildManifest
		_ = json.Unmarshal(f.manifest, &m)
		var cp swarmRebuildCheckpoint
		_ = json.Unmarshal(mustRebuildJSON(f.checkpoint), &cp)
		if key == "DefaultAddrPool" {
			f.cluster[key] = []string{"203.0.113.0/24"}
		} else {
			f.cluster[key] = 12345
		}
		err := verifySwarmRebuild(context.Background(), f.remote(), m, cp, f.local)
		if err == nil || err.Error() != "SWARM_REBUILD_VERIFY_FAILED" {
			t.Fatalf("集群网络参数变化未被核验：%s %v", key, err)
		}
	}
}

func TestSwarmRebuildBackupTamperingStopsCommittedReplay(t *testing.T) {
	f := newSwarmRebuildFixture()
	if _, err := executeSwarmRebuild(context.Background(), f.input(), f.remote()); err != nil {
		t.Fatal(err)
	}
	f.manifest = append(f.manifest, ' ')
	input := f.input()
	input.SwarmRebuildCheckpoint = f.checkpoint
	before := len(f.calls)
	if _, err := executeSwarmRebuild(context.Background(), input, f.remote()); err == nil || err.Error() != "SWARM_REBUILD_RECOVERY_REQUIRED" {
		t.Fatalf("篡改备份被信任：%v", err)
	}
	if len(f.calls) != before {
		t.Fatal("备份核验失败后仍访问或修改Engine")
	}
}

func TestSwarmRebuildCancellationWaitsForDeletedGatewayContainersBeforeRecovery(t *testing.T) {
	f := newSwarmRebuildFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remote := f.remote()
	pending, deleted := 0, false
	remote.Call = func(callCtx context.Context, method, path string, body any) (json.RawMessage, error) {
		if err := callCtx.Err(); err != nil {
			return nil, err
		}
		if method == http.MethodDelete && strings.HasPrefix(path, "/services/") && !deleted {
			result, err := f.call(callCtx, method, path, body)
			pending, deleted = 2, true
			cancel()
			return result, err
		}
		if method == http.MethodGet && strings.HasPrefix(path, "/containers/json") && pending > 0 {
			if strings.Contains(path, "filters=") {
				pending--
			}
			return json.Marshal([]domain.Object{{"ID": "old_gateway_container", "State": "running", "Labels": domain.Object{"com.docker.swarm.service.id": "service_old", "com.docker.swarm.service.name": "iquops-traefik"}, "Mounts": []domain.Object{{"Source": traefikRemoteRoot + "/acme"}}}})
		}
		return f.call(callCtx, method, path, body)
	}
	_, err := executeSwarmRebuild(ctx, f.input(), remote)
	if !errors.Is(err, errSwarmRebuildRecovered) || pending != 0 || f.leaveCount != 0 || len(f.services) != 1 || f.checkpoint["phase"] != "recovered" {
		t.Fatalf("异步停止窗口内的取消未先等待再恢复：%v pending=%d phase=%v", err, pending, f.checkpoint["phase"])
	}
}

func TestSwarmRebuildCheckpointRecoveryUsesIndependentBoundedContext(t *testing.T) {
	f := newSwarmRebuildFixture()
	f.failAllInit = true
	_, _ = executeSwarmRebuild(context.Background(), f.input(), f.remote())
	f.failAllInit = false
	input := f.input()
	input.SwarmRebuildRestoreOnly = true
	input.SwarmRebuildCheckpoint = f.checkpoint
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executeSwarmRebuild(ctx, input, f.remote()); !errors.Is(err, errSwarmRebuildRecovered) || f.checkpoint["phase"] != "recovered" || len(f.services) != 1 {
		t.Fatalf("父上下文取消阻断了必要恢复或误报已恢复：%v", err)
	}
}
