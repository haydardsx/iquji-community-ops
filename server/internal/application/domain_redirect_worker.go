package application

import (
	"context"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) redirectRuntimeInput(ctx context.Context, state domain.State, source, draft, snapshot domain.Object, task domain.Task) (RouteRuntimeInput, error) {
	gatewayID := stringValue(draft["gatewayServerId"])
	if len(snapshot) > 0 {
		gatewayID = stringValue(snapshot["gatewayServerId"])
	}
	gateway := findObject(state.Servers, gatewayID)
	if gateway == nil {
		return RouteRuntimeInput{}, errors.New("DOMAIN_REDIRECT_GATEWAY_REQUIRED")
	}
	key, ok := r.serverSSHSecretMaterial(ctx, state, gateway)
	if !ok {
		return RouteRuntimeInput{}, errors.New("SERVER_SSH_SECRET_MATERIAL_REQUIRED")
	}
	metrics := serverRuntimeMetrics(gateway)
	input := RouteRuntimeInput{
		CertResolver: r.Config.TraefikCertResolver,

		Route: domain.Object{"id": "redirect-" + stringValue(source["id"])}, Gateway: gateway, SSHKey: key,
		ClusterID: stringValue(metrics["swarmClusterId"]), TraefikName: r.Config.TraefikService,
		HTTPPort:  numberOrDefault(metrics["traefikHttpPort"], r.Config.TraefikHTTPPort),
		HTTPSPort: numberOrDefault(metrics["traefikHttpsPort"], r.Config.TraefikHTTPSPort),
		Network:   r.Config.SwarmNetwork, Rules: []domain.Object{{"path": "/"}},
		Log: func(source, phase, level, message string) {
			r.appendRuntimeMessage(task, source, phase, level, message)
		},
	}
	if len(snapshot) > 0 {
		input.Domains, input.Redirect = objectSlice(snapshot["domains"]), objectMapFromAny(snapshot["redirect"])
		input.ClusterID, input.Network = stringValue(snapshot["clusterId"]), stringValue(snapshot["network"])
		input.HTTPPort, input.HTTPSPort = numberValue(snapshot["httpPort"]), numberValue(snapshot["httpsPort"])
		input.Revision = stringValue(snapshot["revision"])
		return input, nil
	}
	asset, err := routeDomainSnapshot(state, source, gatewayID)
	if err != nil {
		return RouteRuntimeInput{}, err
	}
	input.Domains = []domain.Object{asset}
	input.Redirect = cloneObject(draft)
	input.Redirect["targetHost"] = findObject(state.Domains, stringValue(draft["targetDomainId"]))["name"]
	if draft["targetDomainId"] == source["id"] {
		// 同域升级由 HTTP 入口处理，HTTPS 保留给业务路由。
		asset["tls"] = false
		if input.HTTPSPort > 0 && input.HTTPSPort != 443 {
			input.Redirect["targetPort"] = input.HTTPSPort
		}
	}
	input.Revision = routeRevision(domain.Object{"source": asset["name"], "certificateId": asset["certificateId"], "redirect": input.Redirect, "cluster": input.ClusterID, "httpPort": input.HTTPPort, "httpsPort": input.HTTPSPort})
	return input, nil
}

func (r *Runtime) executeDomainRedirect(ctx context.Context, task domain.Task) (string, error) {
	err := r.publishDomainRedirect(ctx, task)
	if err != nil {
		finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = r.Store.MutateState(finish, func(state *domain.State) error {
			if source := findObject(state.Domains, task.TargetID); source != nil {
				runtime := objectMapFromAny(source["redirectRuntime"])
				runtime["status"], runtime["lastErrorCode"] = "failed", stableErrorCode(err, "DOMAIN_REDIRECT_FAILED")
				source["redirectRuntime"] = runtime
			}
			return nil
		})
		return "域名跳转操作失败", err
	}
	return "域名跳转操作已完成", nil
}

func (r *Runtime) publishDomainRedirect(ctx context.Context, task domain.Task) error {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return err
	}
	source := findObject(state.Domains, task.TargetID)
	if source == nil || task.TargetKind != "domain" {
		return errors.New("DOMAIN_NOT_FOUND")
	}
	draft := objectMapFromAny(source["redirect"])
	if task.Payload["redirectRevision"] != routeRevision(draft) {
		return errors.New("DOMAIN_REDIRECT_DRAFT_CHANGED")
	}
	applying := task.Action == applyDomainRedirect
	runtime := objectMapFromAny(source["redirectRuntime"])
	var next RouteRuntimeInput
	if applying {
		if err := validateRedirect(state, task.TargetID, draft); err != nil {
			return err
		}
		next, err = r.redirectRuntimeInput(ctx, state, source, draft, nil, task)
		if err != nil {
			return err
		}
	}
	if r.RouteExecutor == nil {
		return errors.New("ROUTE_EXECUTOR_NOT_CONFIGURED")
	}
	if !applying {
		if err := r.retireLegacyRedirects(ctx, state, source, task); err != nil {
			return err
		}
	}
	// 同一网关只撤销一次，探测覆盖此前已生效和中断时可能写入的两个版本。
	gateways := map[string][]domain.Object{}
	for _, snapshot := range possibleRouteSnapshots(runtime) {
		id := stringValue(snapshot["gatewayServerId"])
		gateways[id] = append(gateways[id], snapshot)
	}
	for id, snapshots := range gateways {
		pending := objectMapFromAny(runtime["pendingRemote"])
		if applying && id == stringValue(next.Gateway["id"]) && (len(pending) == 0 || stringValue(pending["revision"]) == next.Revision) {
			continue
		}
		previous, err := r.redirectRuntimeInput(ctx, state, source, draft, snapshots[len(snapshots)-1], task)
		if err != nil {
			return err
		}
		previous.PossibleSnapshots = snapshots
		if _, err := r.RouteExecutor.Remove(ctx, previous); err != nil {
			return err
		}
		if err := r.Store.MutateState(ctx, func(current *domain.State) error {
			runtime := objectMapFromAny(findObject(current.Domains, task.TargetID)["redirectRuntime"])
			for _, field := range []string{"lastApplied", "pendingRemote"} {
				if stringValue(objectMapFromAny(runtime[field])["gatewayServerId"]) == id {
					delete(runtime, field)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	var result domain.Object
	if applying {
		err := r.Store.MutateState(ctx, func(current *domain.State) error {
			source := findObject(current.Domains, task.TargetID)
			if source == nil || task.Payload["redirectRevision"] != routeRevision(source["redirect"]) {
				return errors.New("DOMAIN_REDIRECT_DRAFT_CHANGED")
			}
			target := findObject(current.Domains, stringValue(draft["targetDomainId"]))
			if source["name"] != next.Domains[0]["name"] || source["certificateId"] != next.Domains[0]["certificateId"] || target["name"] != next.Redirect["targetHost"] {
				return errors.New("DOMAIN_REDIRECT_DRAFT_CHANGED")
			}
			if err := validateRedirect(*current, task.TargetID, draft); err != nil {
				return err
			}
			currentRuntime := objectMapFromAny(source["redirectRuntime"])
			currentRuntime["pendingRemote"] = routeRuntimeSnapshot(next)
			source["redirectRuntime"] = currentRuntime
			return nil
		})
		if err != nil {
			return err
		}
		result, err = r.RouteExecutor.Apply(ctx, next)
		if err != nil {
			return err
		}
		if err := r.retireLegacyRedirects(ctx, state, source, task); err != nil {
			return err
		}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return r.Store.MutateState(finish, func(current *domain.State) error {
		item := findObject(current.Domains, task.TargetID)
		if item == nil {
			return errors.New("DOMAIN_NOT_FOUND")
		}
		if applying {
			item["redirectRuntime"] = domain.Object{"status": "active", "lastApplied": result}
		} else {
			delete(item, "redirect")
			delete(item, "redirectRuntime")
		}
		item["updatedAt"] = domain.Now()
		return nil
	})
}
