package application

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const (
	applyDomainRedirect  = "apply_domain_redirect"
	removeDomainRedirect = "remove_domain_redirect"
)

func redirectOccupiesHost(asset domain.Object) bool {
	return len(possibleRouteSnapshots(objectMapFromAny(asset["redirectRuntime"]))) > 0
}

// 同域升级只接管 HTTP，应用的 HTTPS 路由仍应正常提供服务。
func redirectBlocksBackend(asset domain.Object, scheme string) bool {
	for _, snapshot := range possibleRouteSnapshots(objectMapFromAny(asset["redirectRuntime"])) {
		binding := objectMapFromAny(snapshot["redirect"])
		if binding["targetDomainId"] != asset["id"] || scheme == "http" {
			return true
		}
	}
	return false
}

func redirectBindings(asset domain.Object) []domain.Object {
	bindings := []domain.Object{objectMapFromAny(asset["redirect"])}
	for _, snapshot := range possibleRouteSnapshots(objectMapFromAny(asset["redirectRuntime"])) {
		bindings = append(bindings, objectMapFromAny(snapshot["redirect"]))
	}
	return bindings
}

func validateRedirect(state domain.State, id string, draft domain.Object) error {
	if err := validateRedirectDefinition(state, id, draft); err != nil {
		return err
	}
	gatewayID := stringValue(draft["gatewayServerId"])
	server := findObject(state.Servers, gatewayID)
	if server == nil || serverRuntimeMetrics(server)["traefik"] != true {
		return errors.New("DOMAIN_REDIRECT_GATEWAY_REQUIRED")
	}
	source := findObject(state.Domains, id)
	if _, err := routeDomainSnapshot(state, source, gatewayID); err != nil {
		return err
	}
	if jenkinsDomainInUse(state, id, "") && draft["targetDomainId"] != id {
		return errors.New("DOMAIN_REDIRECT_HOST_IN_USE")
	}
	return validateRedirectHostAvailable(state, source, draft)
}

// 已发布版本和未核验的版本也可能接收流量，检查环路时必须包含这些边。
func validateRedirectDefinition(state domain.State, id string, draft domain.Object) error {
	targetID := stringValue(draft["targetDomainId"])
	source, target := findObject(state.Domains, id), findObject(state.Domains, targetID)
	if source == nil || target == nil || !validDomainName(stringValue(target["name"])) {
		return errors.New("DOMAIN_REDIRECT_TARGET_INVALID")
	}
	if !objectOnlyContainsKeys(draft, "targetDomainId", "gatewayServerId", "scheme", "mode") ||
		(draft["scheme"] != "https" && draft["scheme"] != "http") ||
		(draft["mode"] != "permanent" && draft["mode"] != "temporary") {
		return errors.New("DOMAIN_REDIRECT_CONFIG_INVALID")
	}
	if gateway, ok := draft["gatewayServerId"].(string); !ok || gateway != "" && findObject(state.Servers, gateway) == nil {
		return errors.New("DOMAIN_REDIRECT_GATEWAY_REQUIRED")
	}
	if id == targetID {
		if draft["scheme"] != "https" || stringValue(source["certificateId"]) == "" {
			return errors.New("DOMAIN_REDIRECT_UPGRADE_REQUIRES_HTTPS")
		}
		return nil
	}
	seen := map[string]bool{}
	queue := []string{targetID}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == id {
			return errors.New("DOMAIN_REDIRECT_CYCLE")
		}
		if next != "" && !seen[next] {
			seen[next] = true
			for _, edge := range redirectBindings(findObject(state.Domains, next)) {
				if target := stringValue(edge["targetDomainId"]); target != next {
					queue = append(queue, target)
				}
			}
		}
	}
	return nil
}

func validateRedirectHostAvailable(state domain.State, source, draft domain.Object) error {
	id, host := stringValue(source["id"]), stringValue(source["name"])
	upgrade := draft["targetDomainId"] == id
	for _, route := range state.Routes {
		for _, snapshot := range possibleRouteSnapshots(route) {
			for _, bound := range objectSlice(snapshot["domains"]) {
				if (bound["id"] == id || bound["name"] == host) && (!upgrade || bound["tls"] != true) {
					return errors.New("DOMAIN_REDIRECT_HOST_IN_USE")
				}
			}
		}
	}
	for _, run := range state.FrpTunnelRuns {
		if !run.Status.IsActive() {
			continue
		}
		for _, mapping := range run.Mappings {
			if mapping.PublicDomain == host && (!upgrade || mapping.Entrypoint == "web") {
				return errors.New("DOMAIN_REDIRECT_HOST_IN_USE")
			}
		}
	}
	return nil
}

func redirectOperationActive(state domain.State, id string) error {
	for _, action := range []string{applyDomainRedirect, removeDomainRedirect} {
		if active := findActiveTask(state.Tasks, action, "domain", id); active != nil {
			return &TaskAlreadyActiveError{ActiveTaskID: active.ID}
		}
	}
	return nil
}

func (r *Runtime) SaveDomainRedirect(ctx context.Context, id string, draft domain.Object) (domain.Object, error) {
	var result domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		source := findObject(state.Domains, id)
		if source == nil {
			return errors.New("DOMAIN_NOT_FOUND")
		}
		if err := redirectOperationActive(*state, id); err != nil {
			return err
		}
		if err := validateRedirect(*state, id, draft); err != nil {
			return err
		}
		source["redirect"], source["updatedAt"] = cloneObject(draft), domain.Now()
		runtime := objectMapFromAny(source["redirectRuntime"])
		runtime["status"] = "pending"
		source["redirectRuntime"] = runtime
		result = cloneObject(source)
		return nil
	})
	return result, err
}

func (r *Runtime) CreateDomainRedirectOperation(ctx context.Context, id, action string) (domain.Task, error) {
	if action != applyDomainRedirect && action != removeDomainRedirect {
		return domain.Task{}, errors.New("DOMAIN_REDIRECT_ACTION_INVALID")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	source := findObject(state.Domains, id)
	draft := objectMapFromAny(source["redirect"])
	if source == nil || len(draft) == 0 && !redirectOccupiesHost(source) {
		return domain.Task{}, errors.New("DOMAIN_REDIRECT_NOT_FOUND")
	}
	if action == applyDomainRedirect {
		if err := validateRedirect(state, id, draft); err != nil {
			return domain.Task{}, err
		}
	}
	return r.CreateTask(ctx, action, "domain", id, stringValue(source["name"]), domain.Object{"redirectRevision": routeRevision(draft)})
}

// Traefik 直接返回固定目标；匹配原始 URI，以保留转义路径、尾斜杠和查询串。
func buildDomainRedirectRoute(input RouteRuntimeInput) ([]byte, error) {
	if len(input.Domains) != 1 {
		return nil, errors.New("DOMAIN_REDIRECT_SOURCE_INVALID")
	}
	source := stringValue(input.Domains[0]["name"])
	target := stringValue(input.Redirect["targetHost"])
	if !validDomainName(source) || !validDomainName(target) {
		return nil, errors.New("DOMAIN_REDIRECT_TARGET_INVALID")
	}
	prefix := "iquops-" + routeRevision(input.Route["id"])
	marker, redirect := prefix+"-revision", prefix+"-redirect"
	middlewares := domain.Object{
		marker:   domain.Object{"headers": domain.Object{"customResponseHeaders": domain.Object{"X-IQuOps-Route": routeRevision(input.Route["id"]) + ":" + input.Revision}}},
		redirect: redirectMiddleware(source, input.Redirect),
	}
	router := domain.Object{"rule": "Host(" + strconv.Quote(source) + ")", "entryPoints": []string{"web"}, "priority": 10000, "middlewares": []string{marker, redirect}, "service": "noop@internal"}
	routers := domain.Object{prefix + "-http": router}
	if input.Domains[0]["tls"] == true && source != target {
		secure := cloneObject(router)
		secure["entryPoints"], secure["tls"] = []string{"websecure"}, domain.Object{"certResolver": managedTraefikResolver(input.CertResolver)}
		routers[prefix+"-https"] = secure
	}
	return json.Marshal(domain.Object{"http": domain.Object{"routers": routers, "middlewares": middlewares}})
}

func redirectMiddleware(source string, rule domain.Object) domain.Object {
	return domain.Object{"redirectRegex": domain.Object{"regex": "(?i)^https?://" + regexp.QuoteMeta(source) + "(?::[0-9]+)?(/.*)$", "replacement": redirectOrigin(rule) + "${1}", "permanent": rule["mode"] == "permanent"}}
}

func verifiedDomainRedirect(redirect domain.Object, uri string, response *http.Response) bool {
	status := http.StatusFound
	if redirect["mode"] == "permanent" {
		status = http.StatusMovedPermanently
	}
	expected := redirectOrigin(redirect) + uri
	return response.StatusCode == status && response.Header.Get("Location") == expected
}

func redirectOrigin(rule domain.Object) string {
	host := stringValue(rule["targetHost"])
	if port := numberValue(rule["targetPort"]); port > 0 {
		host += ":" + strconv.Itoa(port)
	}
	return stringValue(rule["scheme"]) + "://" + host
}
