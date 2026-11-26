package application

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func physicalVolumeName(appID, name string) string {
	return "iquops-" + appID + "-" + name
}

func validateVolumeDeclarations(swarm domain.Object) error {
	if raw, exists := swarm["volumes"]; exists && !applicationObjectArray(raw) {
		// 早期的空卷配置也可能是空字符串数组。
		if old, ok := raw.([]string); !ok || len(old) != 0 {
			return errors.New("APPLICATION_VOLUME_INVALID")
		}
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for _, volume := range objectSlice(swarm["volumes"]) {
		name, target := stringValue(volume["name"]), stringValue(volume["target"])
		if !objectOnlyContainsKeys(volume, "name", "target") || !portName.MatchString(name) || !path.IsAbs(target) || target == "/" || strings.ContainsAny(target, "\x00\r\n,") {
			return errors.New("APPLICATION_VOLUME_INVALID")
		}
		clean := path.Clean(target)
		if clean != target {
			return errors.New("APPLICATION_VOLUME_INVALID")
		}
		if names[name] {
			return errors.New("APPLICATION_VOLUME_NAME_DUPLICATE")
		}
		if targets[clean] {
			return errors.New("APPLICATION_VOLUME_TARGET_DUPLICATE")
		}
		names[name], targets[clean] = true, true
	}
	services := objectSlice(swarm["services"])
	if len(names) > 0 && (len(services) > 1 || (len(services) == 1 && !integerInRange(numberOrDefault(services[0]["replicas"], 1), 1, 1))) {
		return errors.New("APPLICATION_PERSISTENT_SERVICE_REQUIRES_SINGLE_NODE")
	}
	return nil
}

func validateVolumeClaims(state domain.State, appID string, swarm domain.Object) error {
	sources := map[string]bool{}
	for _, volume := range objectSlice(swarm["volumes"]) {
		sources[physicalVolumeName(appID, stringValue(volume["name"]))] = true
	}
	if len(sources) == 0 {
		return nil
	}
	if !applicationResourceName.MatchString(appID) {
		return errors.New("APPLICATION_VOLUME_INVALID")
	}
	check := func(other string, config domain.Object) error {
		if other == appID {
			return nil
		}
		for _, volume := range objectSlice(config["volumes"]) {
			if sources[physicalVolumeName(other, stringValue(volume["name"]))] {
				return errors.New("APPLICATION_VOLUME_SOURCE_CONFLICT")
			}
		}
		return nil
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		if err := check(stringValue(config["applicationId"]), objectMapFromAny(config["swarmRuntime"])); err != nil {
			return err
		}
	}
	// 卸载和编辑草稿不会删除持久数据。旧发布中的归属也不能被另一应用认领。
	for _, plan := range state.ReleasePlans {
		snapshot := objectMapFromAny(plan["buildSnapshot"])
		if err := check(stringValue(plan["applicationId"]), objectMapFromAny(snapshot["swarmRuntime"])); err != nil {
			return err
		}
	}
	return nil
}

func serviceVolumeTargets(existing domain.Object, appID string) (map[string]string, error) {
	template := objectMapFromAny(objectMapFromAny(existing["Spec"])["TaskTemplate"])
	result := map[string]string{}
	for _, mount := range objectSlice(objectMapFromAny(template["ContainerSpec"])["Mounts"]) {
		source := stringValue(mount["Source"])
		name, owned := strings.CutPrefix(source, physicalVolumeName(appID, ""))
		if !owned || !portName.MatchString(name) || mount["Type"] != "volume" || result[source] != "" {
			return nil, errors.New("APPLICATION_VOLUME_OWNERSHIP_MISMATCH")
		}
		result[source] = stringValue(mount["Target"])
	}
	return result, nil
}

// 仅在任务将运行的节点查询和创建本地卷。旧 Jenkins 无标签卷依靠已核验服务的原挂载证明归属。
func ensureNodeVolumes(ctx context.Context, call func(context.Context, string, []byte, string) (string, error), appID string, swarm, existing domain.Object) error {
	mounted, err := serviceVolumeTargets(existing, appID)
	if err != nil {
		return err
	}
	volumes := objectSlice(swarm["volumes"])
	if len(volumes) == 0 {
		return nil
	}
	list, err := call(ctx, "docker volume ls --format '{{.Name}}'", nil, "APPLICATION_VOLUME_QUERY_FAILED")
	if err != nil {
		return err
	}
	present := strings.Fields(list)
	for _, v := range volumes {
		source := physicalVolumeName(appID, stringValue(v["name"]))
		previous, inService := mounted[source]
		if !containsString(present, source) {
			if inService {
				return errors.New("APPLICATION_VOLUME_MISSING")
			}
			if _, err := call(ctx, "docker volume create --driver local --label iquops.owner=iquops --label "+shellQuote("iquops.application-id="+appID)+" "+shellQuote(source), nil, "APPLICATION_VOLUME_CREATE_FAILED"); err != nil {
				return err
			}
		}
		raw, err := call(ctx, "docker volume inspect "+shellQuote(source)+" --format '{{json .}}'", nil, "APPLICATION_VOLUME_QUERY_FAILED")
		if err != nil {
			return err
		}
		var actual struct {
			Name, Driver, Scope string
			Labels, Options     map[string]string
		}
		if json.Unmarshal([]byte(raw), &actual) != nil || actual.Name != source || actual.Driver != "local" || actual.Scope != "local" || len(actual.Options) != 0 {
			return errors.New("APPLICATION_VOLUME_OWNERSHIP_MISMATCH")
		}
		owner, id := actual.Labels["iquops.owner"], actual.Labels["iquops.application-id"]
		if owner == "iquops" && id == appID {
			continue
		}
		if owner != "" || id != "" || !inService || previous != v["target"] {
			return errors.New("APPLICATION_VOLUME_OWNERSHIP_MISMATCH")
		}
	}
	return nil
}
