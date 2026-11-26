package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 从标准私网前缀分配两个本地测试节点。
var (
	fixtureManagerIP = netip.MustParsePrefix("10.0.0.0/8").Addr().Next()
	fixtureWorkerIP  = fixtureManagerIP.Next()
)

type swarmConnectionFixture struct {
	local        swarmLocalConnection
	availability string
	clusterID    string
	spec         domain.Object
	dataPort     uint32
	subnet       uint32
	addrPool     []string
	managers     int
	posts        []map[string]any
	failFirst    bool
	running      int
	failNextRead bool
}

func newSwarmConnectionFixture() *swarmConnectionFixture {
	return &swarmConnectionFixture{
		local: swarmLocalConnection{ListenAddr: "0.0.0.0:3377", AdvertiseAddr: netip.AddrPortFrom(fixtureManagerIP, 3377).String()}, availability: "drain", clusterID: "cluster_example", managers: 1, running: 2,
		spec: domain.Object{"Annotations": domain.Object{"Name": "default"}, "Dispatcher": domain.Object{"HeartbeatPeriod": float64(7000000000)}}, dataPort: 7777, subnet: 24, addrPool: []string{"192.0.2.0/24"},
	}
}

func (f *swarmConnectionFixture) call(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var value any
	if method == http.MethodPost {
		input := body.(map[string]any)
		f.posts = append(f.posts, input)
		f.local = swarmLocalConnection{ListenAddr: input["ListenAddr"].(string), AdvertiseAddr: input["AdvertiseAddr"].(string), DataPathAddr: input["DataPathAddr"].(string)}
		f.addrPool, _ = input["DefaultAddrPool"].([]string)
		// 模拟 Engine 初始化时为未显式设置的池补写官方默认值。
		if len(f.addrPool) == 0 {
			f.addrPool = []string{"10.0.0.0/8"}
		}
		if len(f.posts) == 1 && f.failFirst {
			f.failNextRead = true
			return nil, errors.New("remote output must stay private")
		}
		value = "node_manager"
	} else {
		if f.failNextRead {
			f.failNextRead = false
			return nil, errors.New("SWARM_ENGINE_API_UNAVAILABLE")
		}
		switch path {
		case "/swarm":
			value = domain.Object{"ID": f.clusterID, "Spec": f.spec, "DataPathPort": f.dataPort, "DefaultAddrPool": f.addrPool, "SubnetSize": f.subnet}
		case "/nodes":
			nodes := []domain.Object{{"ID": "node_manager", "Spec": domain.Object{"Role": "manager", "Availability": f.availability}, "ManagerStatus": domain.Object{"Addr": f.local.AdvertiseAddr, "Reachability": "reachable", "Leader": true}}}
			if f.managers > 1 {
				nodes = append(nodes, domain.Object{"ID": "node_other", "Spec": domain.Object{"Role": "manager"}})
			}
			value = nodes
		case "/services":
			value = []domain.Object{{"ID": "service_example", "Spec": domain.Object{"Name": "example", "Mode": domain.Object{"Replicated": domain.Object{"Replicas": 3}}}}}
		case "/tasks":
			tasks := []domain.Object{{"ID": "old_task", "ServiceID": "service_example", "DesiredState": "shutdown", "Status": domain.Object{"State": "running"}}}
			for i := 0; i < f.running; i++ {
				tasks = append(tasks, domain.Object{"ServiceID": "service_example", "DesiredState": "running", "Status": domain.Object{"State": "running"}})
			}
			value = tasks
		default:
			if !strings.HasPrefix(path, "/networks?") {
				return nil, errors.New("unexpected path")
			}
			value = []domain.Object{{"ID": "network_example", "Scope": "swarm"}}
		}
	}
	raw, err := json.Marshal(value)
	return raw, err
}

func (f *swarmConnectionFixture) baseline(t *testing.T) swarmEngineBaseline {
	t.Helper()
	baseline, err := readSwarmEngineBaseline(context.Background(), f.call, "node_manager", f.local)
	if err != nil {
		t.Fatal(err)
	}
	baseline.ReadLocal = func(context.Context) (swarmLocalConnection, error) { return f.local, nil }
	return baseline
}

func TestSwarmConnectionInputMatchesActualAddressType(t *testing.T) {
	shared := netip.MustParsePrefix("100.64.0.0/10").Addr().Next()
	uniqueLocal := netip.MustParsePrefix("fc00::/7").Addr().Next()
	thisNetwork := netip.MustParsePrefix("0.0.0.0/8").Addr().Next()
	reserved := netip.MustParsePrefix("240.0.0.0/4").Addr().Next()
	for _, test := range []struct{ mode, address, code string }{
		{"public", "192.0.2.10", ""}, {"public", "2001:db8::1", ""}, {"private", fixtureManagerIP.String(), ""}, {"private", uniqueLocal.String(), ""}, {"private", shared.String(), ""},
		{"public", fixtureManagerIP.String(), "SWARM_CONNECTION_ADDRESS_MODE_MISMATCH"}, {"private", "192.0.2.10", "SWARM_CONNECTION_ADDRESS_MODE_MISMATCH"},
		{"other", "192.0.2.10", "SWARM_CONNECTION_MODE_INVALID"}, {"public", "", "SWARM_ADVERTISE_ADDRESS_REQUIRED"}, {"public", "0.0.0.0", "SWARM_ADVERTISE_ADDRESS_INVALID"}, {"public", "127.0.0.1", "SWARM_ADVERTISE_ADDRESS_INVALID"},
		{"public", thisNetwork.String(), "SWARM_ADVERTISE_ADDRESS_INVALID"}, {"public", reserved.String(), "SWARM_ADVERTISE_ADDRESS_INVALID"},
		{"public", netip.AddrFrom16(thisNetwork.As16()).String(), "SWARM_ADVERTISE_ADDRESS_INVALID"}, {"public", netip.AddrFrom16(reserved.As16()).String(), "SWARM_ADVERTISE_ADDRESS_INVALID"},
	} {
		err := ValidateSwarmConnectionInput(test.mode, test.address, true)
		if test.code == "" && err != nil || test.code != "" && (err == nil || err.Error() != test.code) {
			t.Fatalf("连接方式校验 %s/%s = %v", test.mode, test.address, err)
		}
	}
}

func TestSwarmConnectionRestorePreservesConfigurationAndPartialServiceBaseline(t *testing.T) {
	f := newSwarmConnectionFixture()
	baseline := f.baseline(t)
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	if err := restoreSwarmConnection(context.Background(), f.call, baseline); err != nil {
		t.Fatal(err)
	}
	if len(f.posts) != 1 || f.local != baseline.Local {
		t.Fatal("恢复未使用原控制端口和数据地址")
	}
	if !swarmJSONEqual(f.posts[0]["Spec"].(json.RawMessage), baseline.Init["Spec"]) || f.posts[0]["DataPathPort"] != uint32(7777) || f.posts[0]["Availability"] != "drain" {
		t.Fatal("恢复覆盖了原集群配置")
	}
	// 原服务期望三副本、实际两副本，恢复保持原运行基线即可通过。
	if err := restoreSwarmConnection(context.Background(), f.call, baseline); err != nil || len(f.posts) != 1 {
		t.Fatal("相同设置重试没有收敛")
	}
}

func TestSwarmConnectionRestoreDoesNotAppendExistingExternalCAs(t *testing.T) {
	f := newSwarmConnectionFixture()
	f.spec["CAConfig"] = domain.Object{"ExternalCAs": []domain.Object{{"Protocol": "cfssl", "URL": "https://ca.example.test"}}}
	baseline := f.baseline(t)
	originalHash := swarmValueHash(baseline.Init["Spec"])
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		if method == http.MethodPost {
			var spec domain.Object
			raw, err := json.Marshal(body.(map[string]any)["Spec"])
			if err != nil || json.Unmarshal(raw, &spec) != nil {
				t.Fatal("无法读取恢复请求")
			}
			// 模拟 Moby 的追加语义，重复提交旧列表会改变实际集群配置。
			ca := objectMapFromAny(f.spec["CAConfig"])
			ca["ExternalCAs"] = append(objectSlice(ca["ExternalCAs"]), objectSlice(objectMapFromAny(spec["CAConfig"])["ExternalCAs"])...)
		}
		return f.call(ctx, method, path, body)
	}
	if err := restoreSwarmConnection(context.Background(), call, baseline); err != nil {
		t.Fatal(err)
	}
	if swarmValueHash(f.spec) != originalHash || swarmValueHash(baseline.Init["Spec"]) != originalHash {
		t.Fatal("原始外部 CA 列表被追加或修改")
	}
}

func TestSwarmConnectionRestoreCanRetryAfterAmbiguousResponse(t *testing.T) {
	f := newSwarmConnectionFixture()
	baseline := f.baseline(t)
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	f.failFirst = true
	if err := restoreSwarmConnection(context.Background(), f.call, baseline); err == nil {
		t.Fatal("恢复请求响应丢失被误报为成功")
	}
	if err := restoreSwarmConnection(context.Background(), f.call, baseline); err != nil || f.local != baseline.Local {
		t.Fatalf("恢复请求重试没有收敛：%v", err)
	}
}

func TestSwarmConnectionVerificationChecksAllConfiguration(t *testing.T) {
	changes := []struct {
		name   string
		reason string
		change func(*swarmConnectionFixture)
	}{
		{"集群身份", "CLUSTER_ID_CHANGED", func(f *swarmConnectionFixture) { f.clusterID = "changed" }},
		{"集群配置", "CLUSTER_SPEC_CHANGED", func(f *swarmConnectionFixture) { f.spec = domain.Object{"changed": true} }},
		{"数据端口", "DATA_PATH_PORT_CHANGED", func(f *swarmConnectionFixture) { f.dataPort = 4789 }},
		{"自定义地址池", "DEFAULT_ADDR_POOL_CHANGED", func(f *swarmConnectionFixture) { f.addrPool = []string{"198.51.100.0/24"} }},
		{"默认子网", "SUBNET_SIZE_CHANGED", func(f *swarmConnectionFixture) { f.subnet = 25 }},
		{"节点可调度状态", "MANAGER_AVAILABILITY_CHANGED", func(f *swarmConnectionFixture) { f.availability = "active" }},
		{"监听地址", "LOCAL_LISTEN_CHANGED", func(f *swarmConnectionFixture) { f.local.ListenAddr = "0.0.0.0:2377" }},
		{"数据通告", "LOCAL_DATA_PATH_CHANGED", func(f *swarmConnectionFixture) { f.local.DataPathAddr = "192.0.2.99" }},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			f := newSwarmConnectionFixture()
			baseline := f.baseline(t)
			test.change(f)
			err := verifySwarmConnection(context.Background(), f.call, baseline, baseline.Local)
			if err == nil || err.Error() != "SWARM_CONNECTION_VERIFY_FAILED" || swarmConnectionFailureReason(err) != test.reason {
				t.Fatalf("核验未保留稳定错误码和具体诊断类别：%v", err)
			}
		})
	}
	f := newSwarmConnectionFixture()
	f.managers = 2
	if _, err := readSwarmEngineBaseline(context.Background(), f.call, "node_manager", f.local); err == nil || err.Error() != "SWARM_CONNECTION_MULTIPLE_MANAGERS" {
		t.Fatal("多管理节点未受保护")
	}
}

func TestSwarmConnectionCheckpointRetainsOriginalBaselineAcrossRetry(t *testing.T) {
	f := newSwarmConnectionFixture()
	original := f.baseline(t)
	var checkpoint domain.Object
	input := ServerRuntimeExecutionInput{SaveSwarmConnectionCheckpoint: func(_ context.Context, value domain.Object) error { checkpoint = value; return nil }}
	if _, err := prepareSwarmConnectionCheckpoint(context.Background(), input, original); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(checkpoint)
	if strings.Contains(string(raw), "HeartbeatPeriod") || strings.Contains(string(raw), "Replicas") {
		t.Fatal("完整配置被持久化到任务")
	}
	f.local = swarmLocalConnection{ListenAddr: original.Local.ListenAddr, AdvertiseAddr: "192.0.2.10:3377", DataPathAddr: "192.0.2.10"}
	f.running = 0
	current := f.baseline(t)
	restored, err := prepareSwarmConnectionCheckpoint(context.Background(), ServerRuntimeExecutionInput{SwarmConnectionCheckpoint: checkpoint}, current)
	if err != nil || restored.Running["service_example"] != 2 || restored.Local != original.Local {
		t.Fatalf("重试降低了原始基线：%v", err)
	}
	if verifySwarmConnection(context.Background(), f.call, restored, f.local) == nil {
		t.Fatal("迁移中断后的零副本被误报为成功")
	}
	f.running = 2
	if verifySwarmConnection(context.Background(), f.call, restored, f.local) != nil {
		t.Fatal("原始基线恢复后仍无法核验目标地址")
	}
	delete(checkpoint, "running")
	if _, err := prepareSwarmConnectionCheckpoint(context.Background(), ServerRuntimeExecutionInput{SwarmConnectionCheckpoint: checkpoint}, current); err == nil || err.Error() != "SWARM_CONNECTION_RECOVERY_REQUIRED" {
		t.Fatal("损坏的检查点继承了新基线")
	}
}

func TestSwarmConnectionProtectsAutolock(t *testing.T) {
	f := newSwarmConnectionFixture()
	f.spec["EncryptionConfig"] = domain.Object{"AutoLockManagers": true}
	if _, err := readSwarmEngineBaseline(context.Background(), f.call, "node_manager", f.local); err == nil || err.Error() != "SWARM_CONNECTION_AUTOLOCK_UNSUPPORTED" || len(f.posts) != 0 {
		t.Fatal("开启自动锁定的节点在保护前被重启")
	}
}

func TestSwarmConnectionRestoreAcceptsEngineMaterializedDefaultAddrPool(t *testing.T) {
	f := newSwarmConnectionFixture()
	f.addrPool = nil
	baseline := f.baseline(t)
	f.local.AdvertiseAddr, f.local.DataPathAddr = "192.0.2.10:3377", "192.0.2.10"
	if err := restoreSwarmConnection(context.Background(), f.call, baseline); err != nil {
		t.Fatalf("Engine 补写默认池导致恢复被误判：%v", err)
	}
	if len(f.addrPool) != 1 || f.addrPool[0] != "10.0.0.0/8" || f.local != baseline.Local {
		t.Fatal("默认池或原连接恢复状态不正确")
	}
}

func TestSwarmConnectionLegacyCheckpointAcceptsOnlyEffectiveDefaultAddrPool(t *testing.T) {
	for _, legacyPool := range []any{nil, []string{}} {
		f := newSwarmConnectionFixture()
		f.addrPool = nil
		original := f.baseline(t)
		var checkpoint domain.Object
		input := ServerRuntimeExecutionInput{SaveSwarmConnectionCheckpoint: func(_ context.Context, value domain.Object) error { checkpoint = value; return nil }}
		if _, err := prepareSwarmConnectionCheckpoint(context.Background(), input, original); err != nil {
			t.Fatal(err)
		}
		checkpoint["defaultAddrPool"] = legacyPool
		f.addrPool = []string{"10.0.0.0/8"}
		current := f.baseline(t)
		if _, err := prepareSwarmConnectionCheckpoint(context.Background(), ServerRuntimeExecutionInput{SwarmConnectionCheckpoint: checkpoint}, current); err != nil {
			t.Fatalf("旧检查点未识别 Engine 补写的有效默认池：%v", err)
		}
		f.addrPool = []string{"198.51.100.0/24"}
		if _, err := prepareSwarmConnectionCheckpoint(context.Background(), ServerRuntimeExecutionInput{SwarmConnectionCheckpoint: checkpoint}, f.baseline(t)); err == nil || err.Error() != "SWARM_CONNECTION_RECOVERY_REQUIRED" {
			t.Fatal("旧默认池检查点接受了真实自定义池变化")
		}
	}
	f := newSwarmConnectionFixture()
	f.addrPool = []string{"192.0.2.0/24", "198.51.100.0/24"}
	baseline := f.baseline(t)
	f.addrPool = []string{"198.51.100.0/24", "192.0.2.0/24"}
	if err := verifySwarmConnection(context.Background(), f.call, baseline, baseline.Local); err == nil || swarmConnectionFailureReason(err) != "DEFAULT_ADDR_POOL_CHANGED" {
		t.Fatal("自定义多池的分配顺序变化未被阻止")
	}
}

func TestSwarmConnectionVerificationRetainsSafeFailureReason(t *testing.T) {
	f := newSwarmConnectionFixture()
	baseline := f.baseline(t)
	f.spec = domain.Object{"Name": "different"}
	err := waitSwarmConnection(context.Background(), f.call, baseline, baseline.Local, time.Millisecond)
	if err == nil || err.Error() != "SWARM_CONNECTION_VERIFY_FAILED" || swarmConnectionFailureReason(err) != "CLUSTER_SPEC_CHANGED" {
		t.Fatalf("等待核验吞掉了具体诊断类别：%v", err)
	}
	f = newSwarmConnectionFixture()
	reads := 0
	call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
		reads++
		if reads == 6 {
			f.spec = domain.Object{"Name": "different"}
		}
		return f.call(ctx, method, path, body)
	}
	logs := []string{}
	input := ServerRuntimeExecutionInput{SwarmConnectionMode: "private", SwarmAdvertiseAddr: fixtureManagerIP.String(), LogStep: func(_, _, _, message string) { logs = append(logs, message) }}
	current := remoteSwarmStatus{Active: true, ControlAvailable: true, Role: "manager", NodeID: "node_manager", ClusterID: f.clusterID, ManagerAddress: f.local.AdvertiseAddr}
	if err := verifyOrRestoreSwarmConnection(context.Background(), input, current, call, func(context.Context) (swarmLocalConnection, error) { return f.local, nil }); err == nil || !strings.Contains(strings.Join(logs, "\n"), "CLUSTER_SPEC_CHANGED") {
		t.Fatal("连接核验失败日志没有保留固定诊断类别")
	}
	if reason := swarmConnectionFailureReason(errors.New("remote output must stay private")); reason != "ENGINE_REQUEST_FAILED" {
		t.Fatalf("未知远端错误未归类为固定安全文本：%s", reason)
	}
}
