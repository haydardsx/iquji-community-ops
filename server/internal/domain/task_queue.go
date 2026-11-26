package domain

import (
	"context"
	"sort"
	"strings"
)

// TaskResourceLock 仅供 Worker 调度，不作为业务 API 返回。
type TaskResourceLock struct {
	Key       string `json:"key"`
	Exclusive bool   `json:"exclusive,omitempty"`
}

type taskClaimKey struct{}

// WithTaskClaim 将执行身份传给持久化边界；脱离取消时仍须保留此身份。
func WithTaskClaim(ctx context.Context, task Task) context.Context {
	return context.WithValue(ctx, taskClaimKey{}, task)
}

func TaskClaimFromContext(ctx context.Context) (Task, bool) {
	task, ok := ctx.Value(taskClaimKey{}).(Task)
	return task, ok && task.ClaimToken != ""
}

func TaskLocksConflict(left, right []TaskResourceLock) bool {
	for _, a := range left {
		for _, b := range right {
			if a.Key == b.Key && (a.Exclusive || b.Exclusive) {
				return true
			}
		}
	}
	return false
}

// CurrentTaskResources 从提交快照及当前资源关系推导锁，供领取前核验作用域。
func CurrentTaskResources(state State, task Task) []TaskResourceLock {
	locks := map[string]bool{"control-plane": false}
	add := func(key string, exclusive bool) {
		if key != "" {
			locks[key] = locks[key] || exclusive
		}
	}
	secret := func(id string, exclusive bool) {
		if id != "" {
			add("secret_asset:"+id, exclusive)
		}
	}
	secretReference := func(ref string) {
		for _, asset := range state.SecretAssets {
			if StringValue(asset["secretRef"]) == ref && ref != "" {
				secret(StringValue(asset["id"]), false)
			}
		}
	}
	server := func(id string, exclusive bool) {
		if id == "" {
			return
		}
		add("server:"+id, exclusive)
		item := FindObject(state.Servers, id)
		metrics := ObjectFromAny(ObjectFromAny(item["health"])["metrics"])
		if cluster := StringValue(metrics["swarmClusterId"]); cluster != "" {
			add("swarm:"+cluster, exclusive)
		}
		for _, id := range StringSliceFromAny(item["secretAssetIds"]) {
			secret(id, false)
		}
	}
	domainAsset := func(id string) {
		if id == "" {
			return
		}
		add("domain:"+id, true)
		asset := FindObject(state.Domains, id)
		if certificateID := StringValue(asset["certificateId"]); certificateID != "" {
			add("certificate:"+certificateID, false)
		}
	}
	config := ObjectFromAny(task.Payload["runtimeConfigSnapshot"])
	appID := StringValue(task.Payload["applicationId"])
	switch task.TargetKind {
	case "domain":
		domainAsset(task.TargetID)
		asset := FindObject(state.Domains, task.TargetID)
		runtime := ObjectFromAny(asset["redirectRuntime"])
		for _, scope := range []Object{ObjectFromAny(asset["redirect"]), ObjectFromAny(runtime["lastApplied"]), ObjectFromAny(runtime["pendingRemote"])} {
			server(StringValue(scope["gatewayServerId"]), false)
			domainAsset(StringValue(scope["targetDomainId"]))
			domainAsset(StringValue(ObjectFromAny(scope["redirect"])["targetDomainId"]))
		}
		for _, route := range state.Routes {
			for _, field := range []string{"lastApplied", "pendingRemote"} {
				snapshot := ObjectFromAny(route[field])
				bound := FindObject(ObjectSliceFromAny(snapshot["domains"]), task.TargetID)
				if bound["httpsRedirectEnabled"] == true || bound["inheritedUpgrade"] == true {
					server(StringValue(snapshot["gatewayServerId"]), false)
					add("application:"+StringValue(route["applicationId"]), true)
				}
			}
		}
	case "application":
		appID = task.TargetID
	case "route":
		if appID == "" {
			appID = StringValue(FindObject(state.Routes, task.TargetID)["applicationId"])
		}
		route := FindObject(state.Routes, task.TargetID)
		configured := ObjectFromAny(task.Payload["routeSnapshot"])
		if len(configured) == 0 {
			configured = route
		}
		for _, snapshot := range []Object{configured, ObjectFromAny(route["lastApplied"]), ObjectFromAny(route["pendingRemote"])} {
			for _, asset := range ObjectSliceFromAny(snapshot["domains"]) {
				domainAsset(StringValue(asset["id"]))
			}
		}
	case "release_plan":
		appID = StringValue(FindObject(state.ReleasePlans, task.TargetID)["applicationId"])
	case "server":
		server(task.TargetID, true)
		server(StringValue(task.Payload["managerServerId"]), true)
		switch TaskAction(task.Action) {
		case TaskActionBindJenkinsAccess, TaskActionUnbindJenkinsAccess, TaskActionUninstallJenkins:
			bindings := JenkinsAccessSnapshots(FindObject(state.Servers, task.TargetID))
			bindings = append(bindings, task.Payload, ObjectFromAny(task.Payload["previousBinding"]))
			for _, binding := range bindings {
				server(StringValue(binding["gatewayServerId"]), true)
				if id := StringValue(binding["domainId"]); id != "" {
					domainAsset(id)
				}
			}
		}
	case "frp_tunnel", "frp_tunnel_run":
		id := StringValue(task.Payload["serverId"])
		if id != "" {
			add("frp_slot:"+id, true)
		}
		server(id, false)
	case "certificate":
		add("certificate:"+task.TargetID, true)
		cert := FindObject(state.Certificates, task.TargetID)
		resolver := FindObject(state.Resolvers, StringValue(cert["resolverId"]))
		ingress := FindObject(state.Ingresses, StringValue(resolver["ingressId"]))
		for _, id := range StringSliceFromAny(ingress["serverIds"]) {
			server(id, false)
		}
	case "secret_asset":
		secret(task.TargetID, true)
		server(StringValue(task.Payload["bindServerId"]), false)
	case "backup_restore":
		add("control-plane", true)
	default:
		add(task.TargetKind+":"+task.TargetID, true)
	}
	if appID != "" {
		add("application:"+appID, true)
		if len(config) == 0 {
			config = FindObjectBy(state.ApplicationDeploymentConfigs, "applicationId", appID)
		}
		if plan := FindObject(state.ReleasePlans, StringValue(task.Payload["releasePlanId"])); plan != nil {
			config = ObjectFromAny(plan["buildSnapshot"])
			server(StringValue(plan["deploymentManagerServerId"]), false)
		}
		server(StringValue(config["buildServerId"]), false)
		server(StringValue(config["deploymentManagerServerId"]), false)
		if task.TargetKind == "route" {
			server(StringValue(config["gatewayServerId"]), false)
			route := FindObject(state.Routes, task.TargetID)
			for _, field := range []string{"lastApplied", "pendingRemote"} {
				server(StringValue(ObjectFromAny(route[field])["gatewayServerId"]), false)
			}
		}
		for _, item := range ObjectSliceFromAny(ObjectFromAny(config["swarmRuntime"])["services"]) {
			server(StringValue(item["serverId"]), false)
		}
		secretReference(StringValue(ObjectFromAny(config["sourceConfig"])["authSecretRef"]))
		secretReference(StringValue(ObjectFromAny(config["sourceSnapshot"])["authSecretRef"]))
	}
	// 环境配置只保存秘密引用；引用资产参与共享锁，轮换期间等待。
	var visit func(any)
	visit = func(value any) {
		if object := ObjectFromAny(value); len(object) > 0 {
			for key, child := range object {
				if key == "secretRef" || key == "authSecretRef" {
					for _, asset := range state.SecretAssets {
						if StringValue(asset["secretRef"]) == StringValue(child) {
							secret(StringValue(asset["id"]), false)
						}
					}
				} else {
					visit(child)
				}
			}
		} else {
			for _, object := range ObjectSliceFromAny(value) {
				visit(object)
			}
		}
	}
	visit(config)
	if plan := FindObject(state.ReleasePlans, StringValue(task.Payload["releasePlanId"])); plan != nil {
		visit(plan["configSnapshot"])
	}
	result := make([]TaskResourceLock, 0, len(locks))
	for key, exclusive := range locks {
		if strings.TrimSpace(key) != "" {
			result = append(result, TaskResourceLock{Key: key, Exclusive: exclusive})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

// TaskResources 同时保留原锁，作用域漂移的排队项终结前也不能短暂释放旧资源。
func TaskResources(state State, task Task) []TaskResourceLock {
	locks := CurrentTaskResources(state, task)
	byKey := map[string]bool{}
	for _, lock := range append(locks, task.QueueResources...) {
		byKey[lock.Key] = byKey[lock.Key] || lock.Exclusive
	}
	result := make([]TaskResourceLock, 0, len(byKey))
	for key, exclusive := range byKey {
		result = append(result, TaskResourceLock{Key: key, Exclusive: exclusive})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func TaskResourceScopeMatches(state State, task Task) bool {
	current := CurrentTaskResources(state, task)
	if len(current) != len(task.QueueResources) {
		return false
	}
	for i, lock := range current {
		if lock != task.QueueResources[i] {
			return false
		}
	}
	return true
}

// TaskExternalBlocker 保留尚未清理的远端执行与待恢复备份对资源的占用。
func TaskExternalBlocker(state State, candidate Task) (string, string) {
	resources := TaskResources(state, candidate)
	for _, other := range state.Tasks {
		if other.ID == candidate.ID || IsTaskActive(other.Status) || other.Action != string(TaskActionRebuildDockerSwarm) {
			continue
		}
		checkpoint := ObjectFromAny(other.Payload["swarmRebuildCheckpoint"])
		phase := StringValue(checkpoint["phase"])
		if len(checkpoint) == 0 || phase == "verified" || phase == "recovered" {
			continue
		}
		if candidate.Action == string(TaskActionRecoverDockerSwarmRebuild) && StringValue(candidate.Payload["sourceOperationRunId"]) == other.ID {
			continue
		}
		if TaskLocksConflict(resources, TaskResources(state, other)) {
			return other.ID, "等待 Swarm 原始备份恢复"
		}
	}
	for _, run := range state.JenkinsReleaseRuns {
		if StringValue(run["status"]) != "cleanup_required" {
			continue
		}
		owner := StringValue(run["operationRunId"])
		if owner == "" {
			owner = StringValue(run["taskId"])
		}
		for _, other := range state.Tasks {
			if other.ID == owner && TaskLocksConflict(resources, TaskResources(state, other)) {
				return owner, "等待远端执行资源清理"
			}
		}
	}
	return "", ""
}

func TaskPrecedes(left, right Task) bool {
	if left.QueueOrder != right.QueueOrder {
		return left.QueueOrder < right.QueueOrder
	}
	if left.CreatedAt != right.CreatedAt {
		return left.CreatedAt < right.CreatedAt
	}
	return left.ID < right.ID
}

func NextTaskQueueOrder(tasks []Task) uint64 {
	var value uint64
	for _, task := range tasks {
		if task.QueueOrder > value {
			value = task.QueueOrder
		}
	}
	return value + 1
}
