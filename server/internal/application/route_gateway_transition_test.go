package application

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type gatewayTransitionRecorder struct {
	events      []string
	unavailable string
}

func (e *gatewayTransitionRecorder) Apply(_ context.Context, input RouteRuntimeInput) (domain.Object, error) {
	e.events = append(e.events, "apply:"+stringValue(input.Gateway["id"])+":"+input.Network)
	return routeRuntimeSnapshot(input), nil
}

func (e *gatewayTransitionRecorder) Remove(_ context.Context, input RouteRuntimeInput) (domain.Object, error) {
	gateway := stringValue(input.Gateway["id"])
	e.events = append(e.events, "remove:"+gateway+":"+input.Network)
	if gateway == e.unavailable {
		return nil, errors.New("ROUTE_SSH_UNAVAILABLE")
	}
	for _, snapshot := range input.PossibleSnapshots {
		if snapshot["gatewayServerId"] != gateway {
			return nil, errors.New("混入其他网关的撤销探测")
		}
	}
	return routeRuntimeSnapshot(input), nil
}

func TestWorkerMovesRoutesBetweenGatewaysAndNetworks(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	seedFrpServer(t, ctx, store, true)
	recorder := &gatewayTransitionRecorder{}
	r.RouteExecutor = recorder
	if err := store.MutateState(ctx, func(s *domain.State) error {
		a := s.Servers[0]
		a["health"] = domain.Object{"metrics": domain.Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmClusterId": "cluster"}}
		b := cloneObject(a)
		b["id"] = "gateway-b"
		s.Servers = append(s.Servers, b)
		s.Applications = []domain.Object{{"id": "app"}}
		s.ApplicationDeploymentConfigs = []domain.Object{{"id": "cfg", "applicationId": "app", "deploymentManagerServerId": "srv_gateway", "gatewayServerId": "srv_gateway",
			"swarmRuntime": domain.Object{"overlayNetwork": "old-net", "services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}}}}}
		s.Domains = []domain.Object{{"id": "asset", "name": "site.example.test"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	save := func(changes domain.Object) {
		t.Helper()
		if _, err := r.UpsertApplicationConfig(ctx, "app", changes); err != nil {
			t.Fatal(err)
		}
	}
	route := func(path string) string {
		t.Helper()
		item, err := r.UpsertApplicationRoute(ctx, "app", "", domain.Object{"domainIds": []string{"asset"}, "services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}},
			"rules": []domain.Object{{"path": path, "pathHandling": "preserve"}}, "disabled": false})
		if err != nil {
			t.Fatal(err)
		}
		return stringValue(item["id"])
	}
	operate := func(id, action string, events ...string) domain.Object {
		t.Helper()
		recorder.events = nil
		task, err := r.CreateRouteOperation(ctx, "app", id, action)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = r.ConfirmTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = r.RunWorkerOnce(ctx); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(recorder.events, events) {
			t.Fatalf("%s：执行 %v，期望 %v", action, recorder.events, events)
		}
		s, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return findObject(s.Routes, id)
	}
	first, second := route("/first"), route("/second")
	for _, id := range []string{first, second} {
		operate(id, "apply_route", "apply:srv_gateway:old-net")
	}
	save(domain.Object{"gatewayServerId": "gateway-b"})
	recorder.unavailable = "srv_gateway"
	failed := operate(first, "apply_route", "remove:srv_gateway:old-net")
	if failed["lastErrorCode"] != "ROUTE_SSH_UNAVAILABLE" || objectMapFromAny(failed["lastApplied"])["gatewayServerId"] != "srv_gateway" {
		t.Fatal("旧网关故障后丢失快照")
	}
	recorder.unavailable = ""
	recovered := operate(first, "apply_route", "remove:srv_gateway:old-net", "apply:gateway-b:old-net")
	if recovered["status"] != "active" || recovered["lastErrorCode"] != "" {
		t.Fatal("路由恢复后仍显示发布故障")
	}
	s, _ := store.LoadState(ctx)
	other := findObject(s.Routes, second)
	if other["status"] != "pending" || objectMapFromAny(other["lastApplied"])["gatewayServerId"] != "srv_gateway" {
		t.Fatal("切换时更改了其他入口的生效快照")
	}
	operate(second, "delete_route", "remove:srv_gateway:old-net")
	save(domain.Object{"swarmRuntime": domain.Object{"overlayNetwork": "next-net", "services": []domain.Object{{"slotKey": "web", "serviceName": "app-web"}}}})
	operate(first, "apply_route", "remove:gateway-b:old-net", "apply:gateway-b:next-net")
	disabled := operate(first, "disable_route", "remove:gateway-b:next-net")
	if disabled["status"] != "disabled" || disabled["lastApplied"] != nil || disabled["pendingRemote"] != nil || disabled["accessibleUrls"] != nil {
		t.Fatal("停用后仍保留生效信息")
	}
}
