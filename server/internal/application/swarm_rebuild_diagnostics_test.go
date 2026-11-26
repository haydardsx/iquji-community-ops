package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildScopeReasonsStaySpecificAndPublicCodeStable(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		change       func(*swarmRebuildFixture)
		path         string
		response     any
		failure      bool
	}{
		{name: "自动锁定", reason: "AUTOLOCK_ENABLED", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(f.cluster["Spec"])["EncryptionConfig"])["AutoLockManagers"] = true
		}},
		{name: "外部CA", reason: "EXTERNAL_CA_CONFIG", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(f.cluster["Spec"])["CAConfig"])["ExternalCAs"] = []domain.Object{{"Protocol": "cfssl"}}
		}},
		{name: "CA轮换", reason: "CA_ROTATION_ACTIVE", change: func(f *swarmRebuildFixture) { f.cluster["RootRotationInProgress"] = true }},
		{name: "成员数量", reason: "MANAGER_COUNT", path: "/nodes", response: []domain.Object{}},
		{name: "Manager身份", reason: "MANAGER_IDENTITY", change: func(f *swarmRebuildFixture) { objectMapFromAny(f.node["Spec"])["Role"] = "worker" }},
		{name: "Secret资源", reason: "SECRETS_PRESENT", path: "/secrets", response: []domain.Object{{"ID": "secret_example"}}},
		{name: "Config资源", reason: "CONFIGS_PRESENT", path: "/configs", response: []domain.Object{{"ID": "config_example"}}},
		{name: "业务服务", reason: "SERVICE_COUNT", change: func(f *swarmRebuildFixture) { f.services = append(f.services, domain.Object{"ID": "service_other"}) }},
		{name: "节点不可调度", reason: "NODE_AVAILABILITY", change: func(f *swarmRebuildFixture) { objectMapFromAny(f.node["Spec"])["Availability"] = "drain" }},
		{name: "网关归属", reason: "GATEWAY_IDENTITY", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["Labels"])["iquops.lifecycle"] = "external"
		}},
		{name: "网关副本", reason: "GATEWAY_MODE", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["Mode"])["Replicated"])["Replicas"] = 0
		}},
		{name: "网关Secret引用", reason: "GATEWAY_SECRET_REFERENCE", change: func(f *swarmRebuildFixture) {
			rebuildFixtureContainer(f)["Secrets"] = []domain.Object{{"SecretID": "secret_example"}}
		}},
		{name: "网关Config引用", reason: "GATEWAY_CONFIG_REFERENCE", change: func(f *swarmRebuildFixture) {
			rebuildFixtureContainer(f)["Configs"] = []domain.Object{{"ConfigID": "config_example"}}
		}},
		{name: "数据挂载", reason: "GATEWAY_MOUNTS", change: func(f *swarmRebuildFixture) {
			rebuildFixtureContainer(f)["Mounts"] = []domain.Object{{"Type": "volume", "Source": "volume_example", "Target": "/data"}}
		}},
		{name: "节点约束", reason: "GATEWAY_CONSTRAINTS", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["Placement"])["Constraints"] = []string{"node.id!=node_old"}
		}},
		{name: "镜像未固定", reason: "IMAGE_NOT_PINNED", change: func(f *swarmRebuildFixture) { rebuildFixtureContainer(f)["Image"] = "traefik:example" }},
		{name: "镜像缓存不可用", reason: "IMAGE_CACHE_UNAVAILABLE", path: "/images/", failure: true},
		{name: "网络配置", reason: "NETWORK_CONFIG", change: func(f *swarmRebuildFixture) { f.networks[1]["ConfigOnly"] = true }},
		{name: "网络驱动", reason: "NETWORK_DRIVER", change: func(f *swarmRebuildFixture) { f.networks[1]["Driver"] = "bridge" }},
		{name: "网络IPAM", reason: "NETWORK_IPAM", change: func(f *swarmRebuildFixture) { objectMapFromAny(f.networks[1]["IPAM"])["Driver"] = "external" }},
		{name: "自定义Ingress", reason: "NETWORK_INGRESS_UNSUPPORTED", change: func(f *swarmRebuildFixture) { f.networks[0]["Labels"] = domain.Object{"purpose": "custom"} }},
		{name: "网关网络引用", reason: "GATEWAY_NETWORKS", change: func(f *swarmRebuildFixture) {
			objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["Networks"] = []domain.Object{{"Target": "network_unknown"}}
		}},
		{name: "共享数据目录", reason: "SHARED_CONTAINER_DATA", path: "/containers/json?all=1", response: []domain.Object{{"ID": "container_other", "Mounts": []domain.Object{{"Source": traefikRemoteRoot + "/acme"}}}}},
		{name: "共享网络容器", reason: "SHARED_CONTAINER_NETWORK", path: "/containers/json?all=1", response: []domain.Object{{"ID": "container_other", "NetworkSettings": domain.Object{"Networks": domain.Object{"example": domain.Object{"NetworkID": "network_public"}}}}}},
		{name: "未知网络端点", reason: "NETWORK_ENDPOINT", change: func(f *swarmRebuildFixture) {
			f.networks[1]["Containers"] = domain.Object{"container_other": domain.Object{"Name": "example"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSwarmRebuildFixture()
			if test.change != nil {
				test.change(f)
			}
			call := func(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
				if test.path != "" && strings.HasPrefix(path, test.path) {
					if test.failure {
						return nil, errors.New("synthetic upstream error")
					}
					return json.Marshal(test.response)
				}
				return f.call(ctx, method, path, body)
			}
			_, err := readSwarmRebuildManifest(context.Background(), call, f.input(), f.local)
			if err == nil || err.Error() != "SWARM_REBUILD_SCOPE_UNSUPPORTED" || swarmRebuildReason(fmt.Errorf("检查失败: %w", err)) != test.reason {
				t.Fatalf("未返回固定公开码及预期类别 %s", test.reason)
			}
			if len(f.phases) != 0 || f.backupCreated || f.leaveCount != 0 {
				t.Fatal("范围检查失败后仍推进了备份或重建")
			}
		})
	}
}

func rebuildFixtureContainer(f *swarmRebuildFixture) domain.Object {
	return objectMapFromAny(objectMapFromAny(objectMapFromAny(f.services[0]["Spec"])["TaskTemplate"])["ContainerSpec"])
}

func TestSwarmRebuildReasonNeverEchoesUnknownErrors(t *testing.T) {
	for _, test := range []struct {
		err    error
		reason string
	}{
		{errors.New("synthetic upstream error"), "ENGINE_REQUEST_FAILED"},
		{errors.New("SWARM_REBUILD_BACKUP_FAILED: synthetic upstream error"), "ENGINE_REQUEST_FAILED"},
		{errors.New("SWARM_REBUILD_BACKUP_FAILED"), "BACKUP_FAILED"},
		{errors.New("SWARM_REBUILD_CHECKPOINT_INVALID"), "CHECKPOINT_INVALID"},
		{context.Canceled, "OPERATION_CANCELED"},
		{context.DeadlineExceeded, "OPERATION_TIMED_OUT"},
		{nil, "ENGINE_REQUEST_FAILED"},
	} {
		if got := swarmRebuildReason(test.err); got != test.reason {
			t.Fatalf("诊断未得到预期类别 %s", test.reason)
		}
	}
	if code := safeSwarmRebuildError(swarmRebuildScopeError("GATEWAY_MOUNTS"), "SWARM_REBUILD_STATE_UNAVAILABLE").Error(); code != "SWARM_REBUILD_SCOPE_UNSUPPORTED" {
		t.Fatalf("内部诊断改变了公开错误码：%s", code)
	}
}
