package application

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildBacksUpBeforeLeaveAndRestoresGateway(t *testing.T) {
	f := newSwarmRebuildFixture()
	status, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
	if err != nil {
		t.Fatal(err)
	}
	if f.leaveCount != 1 || f.initCount != 1 || status.ClusterID == "cluster_old" || status.NodeID == "node_old" || status.ManagerAddress != "192.0.2.10:3377" || f.checkpoint["phase"] != "verified" {
		t.Fatal("重建没有真实切换集群/端点并提交")
	}
	if !f.dataCopied || len(f.services) != 1 {
		t.Fatal("网关数据没有备份或网关未恢复")
	}
	placement := objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["Placement"])
	if !strings.Contains(strings.Join(objectStringSlice(placement["Constraints"]), ","), "node.id==node_new_1") {
		t.Fatal("网关仍绑定旧节点身份")
	}
	for _, command := range f.commands {
		if strings.Contains(command, "systemctl") || strings.Contains(command, "docker rm") || strings.Contains(command, "rm -rf") {
			t.Fatal("重建影响了独立Docker容器或删除受管数据根")
		}
	}
	rebuildAssertNoSecrets(t, f)
	before := len(f.calls)
	input := f.input()
	input.SwarmRebuildCheckpoint = f.checkpoint
	input.SwarmRebuildRestoreOnly = true
	if _, err := executeSwarmRebuild(context.Background(), input, f.remote()); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.calls[before:] {
		if strings.HasPrefix(call, http.MethodPost) || strings.HasPrefix(call, http.MethodDelete) {
			t.Fatal("已提交成功任务重放产生写入")
		}
	}
}

func TestSwarmRebuildBackupFailureRestoresOriginalGatewayWithoutLeaving(t *testing.T) {
	f := newSwarmRebuildFixture()
	f.failBackup = true
	_, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
	if err == nil || err.Error() != "SWARM_REBUILD_INTERRUPTED_RECOVERED" || f.leaveCount != 0 || f.initCount != 0 || len(f.services) != 1 || f.checkpoint["phase"] != "recovered" {
		t.Fatalf("备份失败越过了leave边界或未恢复原网关：%v phases=%v", err, f.phases)
	}
	rebuildAssertNoSecrets(t, f)
}

func TestSwarmRebuildRestoresAfterAmbiguousInitOrServiceFailure(t *testing.T) {
	for _, failure := range []string{"init", "service", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			f := newSwarmRebuildFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "init":
				f.failInitOnce = true
			case "service":
				f.failServiceOnce = true
			case "cancel":
				f.cancelOnLeave = cancel
			}
			_, err := executeSwarmRebuild(ctx, f.input(), f.remote())
			if err == nil || f.local.AdvertiseAddr != netip.AddrPortFrom(fixtureManagerIP, 3377).String() || len(f.services) != 1 || f.checkpoint["phase"] != "recovered" {
				t.Fatalf("部分失败未完成旧连接/网关逻辑恢复：%v phase=%v", err, f.checkpoint["phase"])
			}
			if failure == "cancel" && !errors.Is(err, errSwarmRebuildRecovered) {
				t.Fatalf("取消语义丢失：%v", err)
			}
			if f.cluster["ID"] == "cluster_old" {
				t.Fatal("leave后的逻辑恢复错误宣称原集群身份")
			}
			rebuildAssertNoSecrets(t, f)
			before := len(f.calls)
			input := f.input()
			input.SwarmRebuildCheckpoint = f.checkpoint
			input.SwarmRebuildRestoreOnly = true
			if _, err := executeSwarmRebuild(context.Background(), input, f.remote()); !errors.Is(err, errSwarmRebuildRecovered) {
				t.Fatalf("已恢复提交点无法只读重放：%v", err)
			}
			for _, call := range f.calls[before:] {
				if strings.HasPrefix(call, http.MethodPost) || strings.HasPrefix(call, http.MethodDelete) {
					t.Fatal("已恢复任务重放拆改了健康集群")
				}
			}
		})
	}
}

func TestSwarmRebuildFailedRecoveryCanReuseOriginalBackup(t *testing.T) {
	f := newSwarmRebuildFixture()
	f.failAllInit = true
	_, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
	if err == nil || err.Error() != "SWARM_REBUILD_ROLLBACK_FAILED" || f.active {
		t.Fatalf("自动恢复失败没有保留可恢复检查点：%v", err)
	}
	manifestHash := f.checkpoint["manifestHash"]
	f.failAllInit = false
	input := f.input()
	input.SwarmRebuildRestoreOnly = true
	input.SwarmRebuildCheckpoint = f.checkpoint
	if _, err := executeSwarmRebuild(context.Background(), input, f.remote()); !errors.Is(err, errSwarmRebuildRecovered) {
		t.Fatalf("修复外部条件后无法复用原始备份恢复：%v", err)
	}
	if f.local.AdvertiseAddr != netip.AddrPortFrom(fixtureManagerIP, 3377).String() || f.checkpoint["phase"] != "recovered" || f.checkpoint["manifestHash"] != manifestHash || len(f.services) != 1 {
		t.Fatal("继续恢复覆盖了原备份或未恢复原网关")
	}
}

func TestSwarmRebuildCommittedCleanupFailureNeverRollsBackHealthyTarget(t *testing.T) {
	f := newSwarmRebuildFixture()
	f.failCleanup = true
	_, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
	if err != nil || f.checkpoint["phase"] != "verified" || f.leaveCount != 1 || f.initCount != 1 || f.local.AdvertiseAddr != "192.0.2.10:3377" {
		t.Fatalf("已提交的健康新集群因标记清理失败被回退：%v", err)
	}
	input := f.input()
	input.SwarmRebuildCheckpoint = f.checkpoint
	if _, err := executeSwarmRebuild(context.Background(), input, f.remote()); err != nil || f.leaveCount != 1 || f.initCount != 1 {
		t.Fatalf("标记清理重试重建了集群：%v", err)
	}
}

func TestSwarmRebuildPreflightRejectsUnsupportedResourcesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*swarmRebuildFixture)
	}{
		{"业务服务", func(f *swarmRebuildFixture) { f.services = append(f.services, domain.Object{"ID": "business_service"}) }},
		{"自动锁定", func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(f.cluster["Spec"])["EncryptionConfig"])["AutoLockManagers"] = true
		}},
		{"节点不可调度", func(f *swarmRebuildFixture) { objectMapFromAny(f.node["Spec"])["Availability"] = "drain" }},
		{"不支持的节点约束", func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["Placement"])["Constraints"] = []string{"node.id!=node_old"}
		}},
		{"外部数据挂载", func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["ContainerSpec"])["Mounts"] = []domain.Object{{"Type": "bind", "Source": "/srv/example", "Target": "/data"}}
		}},
		{"未固定镜像", func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["ContainerSpec"])["Image"] = "traefik:example"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSwarmRebuildFixture()
			test.change(f)
			_, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
			if err == nil || err.Error() != "SWARM_REBUILD_SCOPE_UNSUPPORTED" || f.backupCreated || len(f.phases) != 0 {
				t.Fatalf("不支持的集群越过前置保护：%v", err)
			}
			for _, call := range f.calls {
				if strings.HasPrefix(call, http.MethodPost) || strings.HasPrefix(call, http.MethodDelete) {
					t.Fatal("前置保护前发生写入")
				}
			}
		})
	}
}

func TestSwarmRebuildDetectsBaselineChangeBeforeLeave(t *testing.T) {
	f := newSwarmRebuildFixture()
	f.beforeBackup = func() { objectMapFromAny(f.node["Spec"])["Labels"] = domain.Object{"manual": "changed"} }
	_, err := executeSwarmRebuild(context.Background(), f.input(), f.remote())
	if err == nil || f.leaveCount != 0 || objectMapFromAny(objectMapFromAny(f.node["Spec"])["Labels"])["manual"] != "changed" {
		t.Fatalf("备份期间变更被旧备份覆盖：%v", err)
	}
}
