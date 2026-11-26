package domain

// MigrateApplicationSources 在状态载入和归档导入时把来源归并到应用配置。
// 归并后不再保留 Application 上的旧字段，运行时只读取正式位置。
func MigrateApplicationSources(state *State) {
	if state == nil {
		return
	}
	for _, application := range state.Applications {
		application["runtimeMode"] = "swarm"
		source := ObjectFromAny(application["sourceConfig"])
		if len(source) > 0 {
			id := StringValue(application["id"])
			config := FindObjectBy(state.ApplicationDeploymentConfigs, "applicationId", id)
			if config == nil {
				config = Object{"id": "depconf_" + id, "applicationId": id,
					"createdAt": application["createdAt"], "updatedAt": application["updatedAt"]}
				state.ApplicationDeploymentConfigs = append(state.ApplicationDeploymentConfigs, config)
			}
			if len(ObjectFromAny(config["sourceConfig"])) == 0 {
				config["sourceConfig"] = CloneObject(source)
			}
		}
		delete(application, "sourceConfig")
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		for _, v := range ObjectSliceFromAny(config["environmentVariables"]) {
			if v["valueSource"] == "environment_value" {
				v["valueSource"] = "plain_text"
			}
			if _, existed := v["stage"]; !existed {
				v["stage"] = "runtime"
			}
		}
		source := CloneObject(ObjectFromAny(config["sourceConfig"]))
		if len(source) == 0 {
			continue
		}
		if StringValue(source["ref"]) == "" {
			for _, key := range []string{"branch", "defaultRef"} {
				if value := StringValue(source[key]); value != "" {
					source["ref"] = value
					break
				}
			}
		}
		if StringValue(source["refType"]) == "" && StringValue(source["defaultRefType"]) != "" {
			source["refType"] = source["defaultRefType"]
		}
		delete(source, "branch")
		delete(source, "defaultRef")
		delete(source, "defaultRefType")
		config["sourceConfig"] = source
	}
}
