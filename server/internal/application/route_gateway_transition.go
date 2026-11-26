package application

import (
	"context"
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 同一网关上的不同修订共用一个路由文件。只在该网关验证其域名、路径和端口。
func (r *Runtime) revokePreviousGateways(ctx context.Context, state domain.State, route domain.Object, task domain.Task, target RouteRuntimeInput, removing bool) error {
	cleaned := map[string]bool{}
	for _, snapshot := range possibleRouteSnapshots(route) {
		gateway := stringValue(snapshot["gatewayServerId"])
		if cleaned[gateway] {
			continue
		}
		if gateway == stringValue(target.Gateway["id"]) && (removing || stringValue(snapshot["network"]) == "" || stringValue(snapshot["network"]) == target.Network) {
			continue
		}
		atGateway := cloneObject(route)
		for _, field := range []string{"lastApplied", "pendingRemote"} {
			if stringValue(objectMapFromAny(atGateway[field])["gatewayServerId"]) != gateway {
				delete(atGateway, field)
			}
		}
		previous, err := r.routeRuntimeInput(ctx, state, atGateway, true, task)
		if err != nil {
			return err
		}
		if _, err := r.RouteExecutor.Remove(ctx, previous); err != nil {
			return err
		}
		// 撤销成功后才释放旧位置；进程在落库前中断时可以再次撤销同一个文件。
		clearSnapshots := func(item domain.Object) {
			for _, field := range []string{"lastApplied", "pendingRemote"} {
				if len(objectMapFromAny(item[field])) > 0 && stringValue(objectMapFromAny(item[field])["gatewayServerId"]) == gateway {
					delete(item, field)
					if field == "lastApplied" {
						delete(item, "accessibleUrls")
					}
				}
			}
		}
		if err := r.Store.MutateState(ctx, func(current *domain.State) error {
			item := findObject(current.Routes, task.TargetID)
			if item == nil {
				return errors.New("ROUTE_NOT_FOUND")
			}
			clearSnapshots(item)
			item["status"] = "pending"
			return nil
		}); err != nil {
			return err
		}
		clearSnapshots(route)
		cleaned[gateway] = true
	}
	return nil
}
