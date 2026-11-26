package domain

// 旧配置的网关兼任集群管理节点。仅迁移尚未拆分的记录，用户清空管理节点后不再回填。
func MigrateApplicationDeploymentLocations(state *State) {
	migrate := func(config Object) bool {
		if _, current := config["deploymentManagerServerId"]; current {
			return false
		}
		manager := StringValue(config["gatewayServerId"])
		if manager == "server_unassigned" {
			manager = ""
			delete(config, "gatewayServerId")
		}
		config["deploymentManagerServerId"] = manager
		return true
	}
	verifiedCluster := func(id string) string {
		server := FindObject(state.Servers, id)
		metrics := ObjectFromAny(ObjectFromAny(server["health"])["metrics"])
		if metrics["swarm"] == true && metrics["swarmVerificationStatus"] == "verified" && metrics["swarmRole"] == "manager" && StringValue(metrics["swarmVerificationError"]) == "" {
			return StringValue(metrics["swarmClusterId"])
		}
		return ""
	}
	originalManagers := map[string]string{}
	for _, config := range state.ApplicationDeploymentConfigs {
		delete(ObjectFromAny(config["swarmRuntime"]), "stackName")
		legacy := migrate(config)
		manager := StringValue(config["deploymentManagerServerId"])
		originalManagers[StringValue(config["applicationId"])] = manager
		if legacy && verifiedCluster(manager) == "" {
			config["deploymentManagerServerId"] = ""
		}
	}
	history := map[string]Object{}
	for _, plan := range state.ReleasePlans {
		if !migrate(plan) {
			continue
		}
		delete(plan, "gatewayServerId")
		appID := StringValue(plan["applicationId"])
		if plan["status"] == "succeeded" || plan["status"] == "jenkins_triggered" || len(ObjectSliceFromAny(plan["artifacts"])) > 0 {
			if prior := history[appID]; prior == nil || StringValue(prior["createdAt"]) <= StringValue(plan["createdAt"]) {
				history[appID] = plan
			}
		}
		// 尚未执行的旧计划在升级时按已核验的管理节点固定集群，执行前仍须实地核对。
		if plan["status"] == "queued" && StringValue(plan["deploymentClusterId"]) == "" {
			plan["deploymentClusterId"] = verifiedCluster(StringValue(plan["deploymentManagerServerId"]))
		}
	}
	for _, task := range state.Tasks {
		if snapshot := ObjectFromAny(task.Payload["runtimeConfigSnapshot"]); len(snapshot) > 0 {
			migrate(snapshot)
		}
	}
	for _, app := range state.Applications {
		id := StringValue(app["id"])
		runtime := ObjectFromAny(app["runtimeSnapshot"])
		plan := history[id]
		if _, exists := app["deploymentLocation"]; exists || runtime["availability"] == "not_deployed" || plan == nil && len(ObjectSliceFromAny(runtime["services"])) == 0 {
			continue
		}
		manager := StringValue(runtime["deploymentManagerServerId"])
		if manager == "" {
			manager = StringValue(plan["deploymentManagerServerId"])
		}
		if manager == "" {
			manager = originalManagers[id]
		}
		cluster := StringValue(runtime["deploymentClusterId"])
		if cluster == "" {
			cluster = StringValue(plan["deploymentClusterId"])
		}
		app["deploymentLocation"] = Object{"deploymentManagerServerId": manager, "deploymentClusterId": cluster}
	}
}
