package domain

// MigrateRouteServiceWeights 把旧运行槽位的流量比例迁入各入口草稿。
// 已发布和待核对的远端快照保留原样，不能借迁移伪造远端生效事实。
func MigrateRouteServiceWeights(state *State) {
	if state == nil {
		return
	}
	for _, route := range state.Routes {
		config := FindObjectBy(state.ApplicationDeploymentConfigs, "applicationId", StringValue(route["applicationId"]))
		configured := ObjectSliceFromAny(ObjectFromAny(config["swarmRuntime"])["services"])
		bindings := CloneObjectSlice(ObjectSliceFromAny(route["services"]))
		for _, bound := range bindings {
			if _, exists := bound["weight"]; !exists {
				bound["weight"] = 1
				service := FindObjectBy(configured, "slotKey", StringValue(bound["slotKey"]))
				if weight, exists := service["accessWeight"]; exists {
					bound["weight"] = weight
				}
			}
			if len(bindings) == 1 {
				if NumberValue(bound["weight"]) == 0 {
					route["status"] = "pending"
				}
				bound["weight"] = 1
			}
		}
		if len(bindings) > 0 {
			route["services"] = bindings
		}
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		swarm := CloneObject(ObjectFromAny(config["swarmRuntime"]))
		services := CloneObjectSlice(ObjectSliceFromAny(swarm["services"]))
		for _, service := range services {
			delete(service, "accessWeight")
		}
		if len(services) > 0 {
			swarm["services"] = services
			config["swarmRuntime"] = swarm
		}
	}
}
