package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestCreateCertificateDerivesSingleManagedVerificationEntry(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_gateway"}, {"id": "srv_backup"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "主站证书", "domains": []string{"app.example.test"}, "serverIds": []string{"srv_gateway", "srv_backup"},
	})
	if err != nil {
		t.Fatalf("创建证书资产失败：%v", err)
	}
	if first["status"] != "pending" || first["renewalStatus"] != "managed" {
		t.Fatal("保存资产不应冒充已签发")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Ingresses) != 1 || len(state.Resolvers) != 1 {
		t.Fatalf("网关分组与签发入口应各派生一份：ingresses=%d resolvers=%d", len(state.Ingresses), len(state.Resolvers))
	}
	resolverID := stringValue(first["resolverId"])
	if resolverID != stringValue(state.Resolvers[0]["id"]) {
		t.Fatal("证书未引用派生签发入口")
	}

	// 同一组网关必须复用入口，不能因新增证书产生重复资产。
	if _, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "第二证书", "domains": []string{"api.example.test"}, "serverIds": []string{"srv_backup", "srv_gateway"},
	}); err != nil {
		t.Fatalf("创建第二证书失败：%v", err)
	}
	state, _ = store.LoadState(ctx)
	if len(state.Ingresses) != 1 || len(state.Resolvers) != 1 {
		t.Fatalf("相同网关集合被重复派生：ingresses=%d resolvers=%d", len(state.Ingresses), len(state.Resolvers))
	}

	// 不同网关集合必须形成独立入口，避免验证通道错配。
	if _, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "第三证书", "domains": []string{"edge.example.test"}, "serverIds": []string{"srv_gateway"},
	}); err != nil {
		t.Fatalf("创建第三证书失败：%v", err)
	}
	state, _ = store.LoadState(ctx)
	if len(state.Ingresses) != 2 || len(state.Resolvers) != 2 {
		t.Fatalf("不同网关集合未形成独立入口：ingresses=%d resolvers=%d", len(state.Ingresses), len(state.Resolvers))
	}
}

func TestCertificateAssetRequiresGatewayServer(t *testing.T) {
	runtime, _, ctx := newFrpRuntime(t)
	if _, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "无网关证书", "domains": []string{"app.example.test"},
	}); err == nil || err.Error() != "CERTIFICATE_GATEWAY_SERVER_REQUIRED" {
		t.Fatalf("缺少网关服务器时不应创建证书：%v", err)
	}
}

func TestDeletingCertificateKeepsSharedVerificationEntry(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_gateway"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	first, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "证书一", "domains": []string{"a.example.test"}, "serverIds": []string{"srv_gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "证书二", "domains": []string{"b.example.test"}, "serverIds": []string{"srv_gateway"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteObject(ctx, "certificates", stringValue(first["id"])); err != nil {
		t.Fatalf("删除证书失败：%v", err)
	}
	state, _ := store.LoadState(ctx)
	if findObject(state.Certificates, stringValue(second["id"])) == nil {
		t.Fatal("删除证书误删了其他资产")
	}
	if len(state.Resolvers) != 1 || len(state.Ingresses) != 1 {
		t.Fatalf("仍有证书引用时不应清理入口：resolvers=%d ingresses=%d", len(state.Resolvers), len(state.Ingresses))
	}

	// 最后一个证书被删除后，派生入口与网关分组都应清理干净。
	if err := runtime.DeleteObject(ctx, "certificates", stringValue(second["id"])); err != nil {
		t.Fatalf("删除最后一个证书失败：%v", err)
	}
	state, _ = store.LoadState(ctx)
	if len(state.Resolvers) != 0 || len(state.Ingresses) != 0 {
		t.Fatalf("空引用入口未被清理：resolvers=%d ingresses=%d", len(state.Resolvers), len(state.Ingresses))
	}
}

func TestIngressDeletionCascadesResolvers(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Ingresses = []domain.Object{{"id": "ingress_one", "name": "手工分组", "serverIds": []string{"srv_one"}}}
		state.Resolvers = []domain.Object{{"id": "resolver_one", "name": "手工入口", "ingressId": "ingress_one"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteObject(ctx, "ingresses", "ingress_one"); err != nil {
		t.Fatalf("删除网关分组失败：%v", err)
	}
	state, _ := store.LoadState(ctx)
	if len(state.Ingresses) != 0 || len(state.Resolvers) != 0 {
		t.Fatalf("网关分组删除未级联清理签发入口：ingresses=%d resolvers=%d", len(state.Ingresses), len(state.Resolvers))
	}
}

func TestIngressDeletionBlockedByCertificateReference(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "srv_gateway"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.CreateCertificateAsset(ctx, domain.Object{
		"name": "证书", "domains": []string{"a.example.test"}, "serverIds": []string{"srv_gateway"},
	}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.LoadState(ctx)
	ingressID := stringValue(state.Ingresses[0]["id"])
	err := runtime.DeleteObject(ctx, "ingresses", ingressID)
	if err == nil || err.Error() != "INGRESS_HAS_CERTIFICATE" {
		t.Fatalf("被证书引用的网关分组不应删除：%v", err)
	}
	state, _ = store.LoadState(ctx)
	if len(state.Ingresses) != 1 || len(state.Resolvers) != 1 {
		t.Fatal("被阻断的删除改动了状态")
	}
}
