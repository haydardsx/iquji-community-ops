package application

import (
	"errors"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func deployedLocation(app domain.Object) domain.Object {
	if location := objectMapFromAny(app["deploymentLocation"]); len(location) > 0 {
		return location
	}
	if runtime := objectMapFromAny(app["runtimeSnapshot"]); len(objectSlice(runtime["services"])) > 0 {
		return runtime
	}
	return nil
}

// 旧应用的部署配置可能等待重新选择节点。运行读回和卸载仍定位原现场。
func runtimeLocationConfig(app, config domain.Object) domain.Object {
	if stringValue(config["deploymentManagerServerId"]) == "" {
		if original := stringValue(deployedLocation(app)["deploymentManagerServerId"]); original != "" {
			config = cloneObject(config)
			if config == nil {
				config = domain.Object{}
			}
			config["deploymentManagerServerId"] = original
		}
	}
	return config
}

// 发布和远端操作共用同一份现场身份。历史应用没有集群证据时，先读回原管理节点的受管服务。
func validateDeploymentLocation(app, config domain.Object, cluster string, observing bool) error {
	manager := stringValue(config["deploymentManagerServerId"])
	if expected := stringValue(config["deploymentClusterId"]); expected != "" && expected != cluster {
		return errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	location := deployedLocation(app)
	if location == nil {
		return nil
	}
	if original := stringValue(location["deploymentManagerServerId"]); original != "" && manager != original {
		return errors.New("APPLICATION_DEPLOYMENT_LOCATION_CHANGED")
	}
	fixed := stringValue(location["deploymentClusterId"])
	if fixed != "" && fixed != cluster {
		return errors.New("APPLICATION_SWARM_CLUSTER_MISMATCH")
	}
	if manager == "" || fixed == "" && !observing {
		return errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
	}
	return nil
}

func pinReleaseLocation(state *domain.State, plan domain.Object) error {
	id := stringValue(plan["applicationId"])
	app := findObject(state.Applications, id)
	if app == nil {
		return errors.New("APPLICATION_NOT_FOUND")
	}
	cluster := stringValue(plan["deploymentClusterId"])
	cfg := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", id)
	if cfg == nil || cfg["deploymentManagerServerId"] != plan["deploymentManagerServerId"] {
		return errors.New("APPLICATION_DEPLOYMENT_LOCATION_CHANGED")
	}
	if cluster == "" {
		return errors.New("APPLICATION_RUNTIME_CHECK_REQUIRED")
	}
	if err := validateDeploymentLocation(app, cfg, cluster, false); err != nil {
		return err
	}
	app["deploymentLocation"] = domain.Object{"deploymentManagerServerId": plan["deploymentManagerServerId"], "deploymentClusterId": cluster}
	return nil
}
