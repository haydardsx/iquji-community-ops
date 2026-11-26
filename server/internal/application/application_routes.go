package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 路由草稿与最后生效快照分开保存，编辑不会提前改变远端事实。
func (r *Runtime) UpsertApplicationRoute(ctx context.Context, applicationID, routeID string, input domain.Object) (domain.Object, error) {
	var result domain.Object
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		application := findObject(state.Applications, applicationID)
		if application == nil {
			return errors.New("APPLICATION_NOT_FOUND")
		}
		if active := applicationOperationActive(*state, applicationID); active != nil {
			return &TaskAlreadyActiveError{ActiveTaskID: active.ID}
		}
		if !objectOnlyContainsKeys(input, "domainIds", "services", "rules", "disabled") {
			return errors.New("ROUTE_CONFIG_INVALID")
		}
		next, err := validatedRouteConfig(*state, applicationID, input)
		if err != nil {
			return err
		}
		now := domain.Now()
		if routeID == "" {
			routeID = domain.NewID("route")
			next["createdAt"] = now
		} else {
			existing := findObject(state.Routes, routeID)
			if existing == nil || stringValue(existing["applicationId"]) != applicationID {
				return errors.New("ROUTE_NOT_FOUND")
			}
			for _, key := range []string{"createdAt", "lastApplied", "pendingRemote", "lastPublishedAt", "lastPublishSummary", "lastErrorCode"} {
				if value, ok := existing[key]; ok {
					next[key] = value
				}
			}
		}
		next["id"], next["applicationId"], next["projectId"] = routeID, applicationID, application["projectId"]
		next["status"], next["updatedAt"] = "pending", now
		next["accessibleUrls"] = routeAccessibleURLs(objectSlice(next["domains"]), objectSlice(next["rules"]))
		result = next
		replaced := false
		for index, item := range state.Routes {
			if stringValue(item["id"]) == routeID {
				state.Routes[index], replaced = next, true
				break
			}
		}
		if !replaced {
			state.Routes = append(state.Routes, next)
		}
		state.OperationLogs = append(state.OperationLogs, newOperationLog("save_route", "route", routeID, "succeeded", "路由草稿已保存", ""))
		return nil
	})
	return result, err
}

func validatedRouteConfig(state domain.State, applicationID string, input domain.Object) (domain.Object, error) {
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
	configured := objectSlice(objectMapFromAny(config["swarmRuntime"])["services"])
	domainIDs := objectStringSlice(input["domainIds"])
	if len(domainIDs) == 0 {
		return nil, errors.New("ROUTE_DOMAIN_REQUIRED")
	}
	domains := []domain.Object{}
	seen := map[string]bool{}
	for _, id := range domainIDs {
		asset := findObject(state.Domains, id)
		if asset == nil || !validDomainName(stringValue(asset["name"])) {
			return nil, errors.New("ROUTE_DOMAIN_INVALID")
		}
		if !seen[id] {
			domains = append(domains, boundRouteDomain(asset))
			seen[id] = true
		}
	}
	services := []domain.Object{}
	positiveWeight := false
	seen = map[string]bool{}
	bindings := objectSlice(input["services"])
	for _, item := range bindings {
		if !objectOnlyContainsKeys(item, "slotKey", "serviceName", "weight", "portName") {
			return nil, errors.New("ROUTE_SERVICE_INVALID")
		}
		slot := stringValue(item["slotKey"])
		service := findObjectBy(configured, "slotKey", slot)
		if service == nil || slot == "" || stringValue(service["serviceName"]) != stringValue(item["serviceName"]) || seen[slot] {
			return nil, errors.New("ROUTE_SERVICE_INVALID")
		}
		weight, err := validatedRouteServiceWeight(item)
		if err != nil {
			return nil, err
		}
		if len(bindings) == 1 {
			weight = 1
		}
		positiveWeight = positiveWeight || weight > 0
		seen[slot] = true
		binding := domain.Object{"slotKey": slot, "serviceName": service["serviceName"], "weight": weight}
		port, err := routeHTTPPort(objectMapFromAny(config["swarmRuntime"]), stringValue(item["portName"]))
		if err != nil {
			return nil, err
		}
		if port != "" {
			binding["portName"] = port
		}
		services = append(services, binding)
	}
	if len(services) == 0 {
		return nil, errors.New("ROUTE_SERVICE_REQUIRED")
	}
	if !positiveWeight {
		return nil, errors.New("ROUTE_SERVICE_WEIGHT_REQUIRED")
	}
	rules := []domain.Object{}
	seen = map[string]bool{}
	for _, rule := range objectSlice(input["rules"]) {
		path, handling := stringValue(rule["path"]), stringValue(rule["pathHandling"])
		if !objectOnlyContainsKeys(rule, "path", "pathHandling", "rewriteTargetPath") || !validRoutePath(path) || seen[path] {
			return nil, errors.New("ROUTE_PATH_INVALID")
		}
		if handling != "preserve" && handling != "strip_prefix" && handling != "rewrite" {
			return nil, errors.New("ROUTE_PATH_HANDLING_INVALID")
		}
		next := domain.Object{"path": path, "pathHandling": handling}
		if handling == "rewrite" {
			if !validRoutePath(stringValue(rule["rewriteTargetPath"])) {
				return nil, errors.New("ROUTE_REWRITE_PATH_INVALID")
			}
			next["rewriteTargetPath"] = rule["rewriteTargetPath"]
		}
		seen[path] = true
		rules = append(rules, next)
	}
	if len(rules) == 0 {
		return nil, errors.New("ROUTE_RULE_REQUIRED")
	}
	disabled, ok := input["disabled"].(bool)
	if !ok {
		return nil, errors.New("ROUTE_DISABLED_INVALID")
	}
	return domain.Object{"domains": domains, "services": services, "rules": rules, "disabled": disabled}, nil
}

func validatedRouteServiceWeight(service domain.Object) (int, error) {
	value, exists := service["weight"]
	if !exists {
		return 1, nil
	}
	if !integerInRange(value, 0, 2147483647) {
		return 0, errors.New("ROUTE_SERVICE_WEIGHT_INVALID")
	}
	return numberValue(value), nil
}

func validRoutePath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\x00\r\n?#") && len(path) <= 1024
}

func (r *Runtime) CreateRouteOperation(ctx context.Context, applicationID, routeID, action string) (domain.Task, error) {
	if action != "apply_route" && action != "disable_route" && action != "delete_route" {
		return domain.Task{}, errors.New("ROUTE_ACTION_INVALID")
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	route := findObject(state.Routes, routeID)
	if route == nil || stringValue(route["applicationId"]) != applicationID {
		return domain.Task{}, errors.New("ROUTE_NOT_FOUND")
	}
	if action == "delete_route" {
		if err := validateRouteOutputDeletion(state, applicationID, routeID); err != nil {
			return domain.Task{}, err
		}
	}
	return r.CreateTask(ctx, action, "route", routeID, "应用路由", map[string]any{
		"applicationId": applicationID, "routeSnapshot": cloneObject(route),
		"runtimeConfigSnapshot": cloneObject(findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)),
	})
}

func (r *Runtime) executeRouteOperation(ctx context.Context, task domain.Task) (string, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "路由读取失败", err
	}
	route := findObject(state.Routes, task.TargetID)
	if route == nil || stringValue(route["applicationId"]) != stringValue(task.Payload["applicationId"]) {
		return "路由不存在", errors.New("ROUTE_NOT_FOUND")
	}
	if task.Action == "delete_route" {
		if err := validateRouteOutputDeletion(state, stringValue(route["applicationId"]), task.TargetID); err != nil {
			return "应用输出正在引用此入口", err
		}
	}
	if snapshot := objectMapFromAny(task.Payload["routeSnapshot"]); len(snapshot) > 0 {
		current := route
		route = cloneObject(snapshot)
		for _, key := range []string{"lastApplied", "pendingRemote"} {
			route[key] = current[key]
		}
	}
	removing := task.Action != "apply_route"
	snapshot := objectMapFromAny(route["lastApplied"])
	if pending := objectMapFromAny(route["pendingRemote"]); len(pending) > 0 {
		snapshot = pending
	}
	var result domain.Object
	if !removing || len(snapshot) > 0 {
		if r.RouteExecutor == nil {
			return "路由执行器不可用", errors.New("ROUTE_EXECUTOR_NOT_CONFIGURED")
		}
		input, inputErr := r.routeRuntimeInput(ctx, state, route, removing, task)
		if inputErr != nil {
			err = inputErr
		} else if err = r.revokePreviousGateways(ctx, state, route, task, input, removing); err == nil {
			pending := objectMapFromAny(route["pendingRemote"])
			if removing {
				input.PossibleSnapshots = possibleRouteSnapshots(route)
			}
			if !removing && len(pending) > 0 && stringValue(pending["revision"]) != input.Revision {
				err = errors.New("ROUTE_PENDING_REVOKE_REQUIRED")
			} else {
				// 先持久化远端意图，进程退出或 SSH 响应丢失也不能把它当作纯草稿删除。
				err = r.Store.MutateState(ctx, func(current *domain.State) error {
					if findObject(current.Routes, task.TargetID) == nil {
						return errors.New("ROUTE_NOT_FOUND")
					}
					if !removing {
						if conflict := rejectConflictingRoute(*current, route, input.Domains, input.Rules); conflict != nil {
							return conflict
						}
					}
					if !removing || len(pending) == 0 {
						updateStateObject(&current.Routes, task.TargetID, domain.Object{"pendingRemote": routeRuntimeSnapshot(input)})
					}
					return nil
				})
				if err == nil {
					if removing {
						result, err = r.RouteExecutor.Remove(ctx, input)
					} else {
						result, err = r.RouteExecutor.Apply(ctx, input)
					}
				}
			}
		}
	}
	completionCtx, cancelCompletion := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelCompletion()
	if err != nil {
		_ = r.Store.MutateState(completionCtx, func(current *domain.State) error {
			updateStateObject(&current.Routes, task.TargetID, domain.Object{"status": "failed", "lastErrorCode": stableErrorCode(err, "ROUTE_EXECUTION_FAILED"), "lastPublishSummary": "远端操作未完成，请检查运行日志后重试", "updatedAt": domain.Now()})
			if len(result) > 0 {
				updateStateObject(&current.Routes, task.TargetID, domain.Object{"pendingRemote": result})
			}
			return nil
		})
		return "路由远端操作失败", err
	}
	err = r.Store.MutateState(completionCtx, func(current *domain.State) error {
		item := findObject(current.Routes, task.TargetID)
		if item == nil {
			return errors.New("ROUTE_NOT_FOUND")
		}
		if task.Action == "delete_route" {
			current.Routes = filterObjectsWithoutID(current.Routes, task.TargetID)
			return nil
		}
		item["updatedAt"], item["lastErrorCode"] = domain.Now(), ""
		if removing {
			delete(item, "lastApplied")
			delete(item, "pendingRemote")
			delete(item, "accessibleUrls")
			item["disabled"], item["status"], item["lastPublishSummary"] = true, "disabled", "路由已从网关撤销"
			return nil
		}
		item["disabled"], item["status"], item["lastPublishedAt"] = false, "active", domain.Now()
		delete(item, "pendingRemote")
		item["lastApplied"], item["lastPublishSummary"] = result, "网关路由与访问校验已通过"
		item["accessibleUrls"] = result["accessibleUrls"]
		for _, verification := range objectSlice(result["verifications"]) {
			certificateID := stringValue(verification["certificateId"])
			if certificateID != "" {
				updateStateObject(&current.Certificates, certificateID, domain.Object{"status": "valid", "renewalStatus": "managed", "lastVerifiedAt": domain.Now(), "expiresAt": verification["expiresAt"]})
			}
		}
		return nil
	})
	return "路由操作已完成并验证", err
}

func filterObjectsWithoutID(items []domain.Object, id string) []domain.Object {
	next := make([]domain.Object, 0, len(items))
	for _, item := range items {
		if stringValue(item["id"]) != id {
			next = append(next, item)
		}
	}
	return next
}

func routeRevision(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])[:24]
}
