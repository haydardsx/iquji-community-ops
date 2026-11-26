package application

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func checkApplicationCredential(state domain.State, ref string) error {
	if ref == "" {
		return errors.New("APPLICATION_VARIABLE_SECRET_REQUIRED")
	}
	if credentialForPurpose(state, ref, "application_secret") == nil {
		return errors.New("APPLICATION_CREDENTIAL_INVALID")
	}
	return nil
}

func visibleApplicationOutput(state domain.State, consumerID, id string) (domain.Object, domain.Object, bool) {
	consumer := findObject(state.Applications, consumerID)
	if id == "" || consumer == nil {
		return nil, nil, false
	}
	for _, config := range state.ApplicationDeploymentConfigs {
		output := findObject(objectSlice(config["outputVariables"]), id)
		if output == nil {
			continue
		}
		owner := findObject(state.Applications, stringValue(config["applicationId"]))
		if owner == nil || owner["id"] == consumerID || owner["projectId"] != consumer["projectId"] && output["visibility"] != "cross_project" {
			return nil, nil, false
		}
		return config, output, true
	}
	return nil, nil, false
}

func availableApplicationOutputs(state domain.State, projectID string) []domain.Object {
	result := []domain.Object{}
	for _, config := range state.ApplicationDeploymentConfigs {
		app := findObject(state.Applications, stringValue(config["applicationId"]))
		if app == nil {
			continue
		}
		for _, o := range objectSlice(config["outputVariables"]) {
			if app["projectId"] != projectID && o["visibility"] != "cross_project" {
				continue
			}
			result = append(result, domain.Object{"id": o["id"], "key": o["key"], "applicationId": app["id"], "applicationName": app["name"], "projectId": app["projectId"], "sensitive": sensitiveOutput(state, config, o, map[string]bool{})})
		}
	}
	return result
}

func sensitiveOutput(state domain.State, config, o domain.Object, seen map[string]bool) bool {
	id := stringValue(o["id"])
	if seen[id] {
		return true
	}
	seen[id] = true
	if o["valueSource"] == "secret_ref" {
		return true
	}
	if o["valueSource"] != "variable" {
		return false
	}
	v := applicationOutputInput(config, o)
	if v["valueSource"] != "output_ref" {
		return v["valueSource"] == "secret_ref"
	}
	owner, next, ok := visibleApplicationOutput(state, stringValue(config["applicationId"]), stringValue(v["outputId"]))
	return !ok || sensitiveOutput(state, owner, next, seen)
}

func withApplicationConfig(state domain.State, id string, config domain.Object) domain.State {
	items := slices.Clone(state.ApplicationDeploymentConfigs)
	config = cloneObject(config)
	config["applicationId"] = id
	for i, item := range items {
		if item["applicationId"] == id {
			items[i] = config
			state.ApplicationDeploymentConfigs = items
			return state
		}
	}
	state.ApplicationDeploymentConfigs = append(items, config)
	return state
}

func validateApplicationOutputs(state domain.State, app, config domain.Object) error {
	id := stringValue(app["id"])
	if raw, ok := config["outputVariables"]; ok && !applicationObjectArray(raw) {
		return errors.New("APPLICATION_OUTPUT_INVALID")
	}
	previous := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", id)
	state = withApplicationConfig(state, id, config)
	keys, ids := map[string]bool{}, map[string]bool{}
	for _, o := range objectSlice(config["outputVariables"]) {
		if !objectOnlyContainsKeys(o, "id", "key", "visibility", "valueSource", "plainValue", "variableKey", "variableStage", "serviceSlotKey", "portName", "endpointMode", "secretRef", "routeId") {
			return errors.New("APPLICATION_OUTPUT_INVALID")
		}
		key, oid := stringValue(o["key"]), stringValue(o["id"])
		if !applicationResourceName.MatchString(oid) || len(oid) > 128 || ids[oid] || !applicationVariableName.MatchString(key) || keys[key] || len(key) > 256 || o["visibility"] != "project" && o["visibility"] != "cross_project" {
			return errors.New("APPLICATION_OUTPUT_INVALID")
		}
		keys[key], ids[oid] = true, true
		for _, other := range state.ApplicationDeploymentConfigs {
			if other["applicationId"] != id && findObject(objectSlice(other["outputVariables"]), oid) != nil {
				return errors.New("APPLICATION_OUTPUT_ID_CONFLICT")
			}
		}
		switch o["valueSource"] {
		case "plain_text":
			if _, ok := o["plainValue"].(string); !ok {
				return errors.New("APPLICATION_OUTPUT_VALUE_REQUIRED")
			}
		case "secret_ref":
			if err := checkApplicationCredential(state, stringValue(o["secretRef"])); err != nil {
				return err
			}
		case "variable":
			if applicationOutputInput(config, o) == nil {
				return outputSourceError(state, "APPLICATION_OUTPUT_INPUT_REQUIRED", id, oid)
			}
		case "service_host", "service_port":
			if mode := stringValue(o["endpointMode"]); mode != "" && mode != "internal" && mode != "published" {
				return errors.New("APPLICATION_OUTPUT_ENDPOINT_INVALID")
			}
			swarm := objectMapFromAny(config["swarmRuntime"])
			service := findObjectBy(objectSlice(swarm["services"]), "slotKey", stringValue(o["serviceSlotKey"]))
			if service == nil {
				return outputSourceError(state, "APPLICATION_OUTPUT_SERVICE_REQUIRED", id, oid)
			}
			name := stringValue(o["portName"])
			if o["valueSource"] == "service_port" || o["endpointMode"] == "published" || name != "" {
				ports := configuredServicePorts(swarm, service)
				port := findObjectBy(ports, "name", name)
				if name == "" && len(ports) == 1 {
					port = ports[0]
				}
				if port == nil || o["endpointMode"] == "published" && numberValue(port["publishedPort"]) <= 0 {
					return outputSourceError(state, "APPLICATION_OUTPUT_PORT_REQUIRED", id, oid)
				}
			}
		case "route_url", "static_url":
			if route := findObject(state.Routes, stringValue(o["routeId"])); route == nil || route["applicationId"] != id {
				return outputSourceError(state, "APPLICATION_OUTPUT_ROUTE_REQUIRED", id, oid)
			}
		case "build_output":
			if objectMapFromAny(config["sourceConfig"])["provider"] != "git" {
				return errors.New("APPLICATION_OUTPUT_SOURCE_INVALID")
			}
		default:
			return errors.New("APPLICATION_OUTPUT_SOURCE_INVALID")
		}
		if o["valueSource"] != "plain_text" {
			delete(o, "plainValue")
		}
		if o["valueSource"] != "secret_ref" {
			delete(o, "secretRef")
		}
	}
	for _, v := range objectSlice(config["environmentVariables"]) {
		provider := objectMapFromAny(config["sourceConfig"])["provider"]
		if v["stage"] == "build" && provider == "image" {
			return errors.New("APPLICATION_VARIABLE_STAGE_INVALID")
		}
	}
	changes := []string{}
	for _, old := range objectSlice(previous["outputVariables"]) {
		oid := stringValue(old["id"])
		next := findObject(objectSlice(config["outputVariables"]), oid)
		if next == nil || old["visibility"] == "cross_project" && next["visibility"] != "cross_project" {
			changes = append(changes, oid)
		}
	}
	if len(changes) > 0 {
		if err := outputUsageError(state, "APPLICATION_OUTPUT_IN_USE", id, changes, false, true); err != nil {
			return err
		}
	}
	for _, consumer := range state.ApplicationDeploymentConfigs {
		for _, v := range objectSlice(consumer["environmentVariables"]) {
			if v["valueSource"] != "output_ref" {
				continue
			}
			oid := stringValue(v["outputId"])
			// 模板草稿的未绑定输入尚不依赖任何生产方，正式保存时由输入校验补齐。
			if oid == "" {
				continue
			}
			if consumer["applicationId"] != id && findObject(objectSlice(previous["outputVariables"]), oid) == nil && !ids[oid] {
				continue
			}
			owner, output, ok := visibleApplicationOutput(state, stringValue(consumer["applicationId"]), oid)
			if !ok {
				return outputSourceError(state, "APPLICATION_OUTPUT_IN_USE", id, oid)
			}
			if err := checkOutputCycle(state, owner, output, map[string]bool{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func applicationOutputInput(config, output domain.Object) domain.Object {
	stage := firstNonEmptyString(stringValue(output["variableStage"]), "runtime")
	for _, v := range objectSlice(config["environmentVariables"]) {
		if v["key"] == output["variableKey"] && firstNonEmptyString(stringValue(v["stage"]), "runtime") == stage && stringValue(v["serviceSlotKey"]) == stringValue(output["serviceSlotKey"]) {
			return v
		}
	}
	return nil
}

func checkOutputCycle(state domain.State, config, o domain.Object, path map[string]bool) error {
	id := stringValue(o["id"])
	if path[id] {
		return errors.New("APPLICATION_OUTPUT_CYCLE")
	}
	path[id] = true
	if v := applicationOutputInput(config, o); o["valueSource"] == "variable" && v["valueSource"] == "output_ref" {
		next, out, ok := visibleApplicationOutput(state, stringValue(config["applicationId"]), stringValue(v["outputId"]))
		if !ok {
			return errors.New("APPLICATION_OUTPUT_REFERENCE_INVALID")
		}
		return checkOutputCycle(state, next, out, path)
	}
	return nil
}

func resolveApplicationValue(state domain.State, appID, consumerID string, variable domain.Object, seen map[string]bool) (domain.Object, error) {
	switch variable["valueSource"] {
	case "secret_ref":
		ref := stringValue(variable["secretRef"])
		if err := checkApplicationCredential(state, ref); err != nil {
			return nil, err
		}
		asset := findObjectBy(state.SecretAssets, "secretRef", ref)
		version := firstNonEmptyString(stringValue(asset["materialRef"]), ref)
		return domain.Object{"valueSource": "secret_ref", "secretRef": ref, "materialRevisionRef": version}, nil
	case "output_ref":
		id := stringValue(variable["outputId"])
		if seen[id] {
			return nil, errors.New("APPLICATION_OUTPUT_CYCLE")
		}
		seen[id] = true
		config, o, ok := visibleApplicationOutput(state, appID, id)
		if !ok || stringValue(variable["outputApplicationId"]) != "" && variable["outputApplicationId"] != config["applicationId"] {
			return nil, errors.New("APPLICATION_OUTPUT_REFERENCE_INVALID")
		}
		ownerID := stringValue(config["applicationId"])
		app := findObject(state.Applications, ownerID)
		switch o["valueSource"] {
		case "variable":
			v := applicationOutputInput(config, o)
			if v == nil {
				break
			}
			return resolveApplicationValue(state, ownerID, consumerID, v, seen)
		case "secret_ref", "plain_text":
			return resolveApplicationValue(state, ownerID, consumerID, o, seen)
		case "service_host", "service_port":
			runtime := objectMapFromAny(app["runtimeSnapshot"])
			service := findObjectBy(objectSlice(runtime["services"]), "serviceSlotKey", stringValue(o["serviceSlotKey"]))
			if runtime["availability"] != "available" || numberValue(service["runningReplicas"]) < 1 {
				break
			}
			published := o["endpointMode"] == "published"
			if !published {
				consumer := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", consumerID)
				consumerManager := findObject(state.Servers, stringValue(consumer["deploymentManagerServerId"]))
				producerManager := findObject(state.Servers, stringValue(config["deploymentManagerServerId"]))
				cluster := stringValue(serverRuntimeMetrics(producerManager)["swarmClusterId"])
				consumerNetwork := firstNonEmptyString(stringValue(objectMapFromAny(consumer["swarmRuntime"])["overlayNetwork"]), "iquops-public")
				producerNetwork := firstNonEmptyString(stringValue(objectMapFromAny(config["swarmRuntime"])["overlayNetwork"]), "iquops-public")
				if cluster == "" || cluster != stringValue(serverRuntimeMetrics(consumerManager)["swarmClusterId"]) || consumerNetwork != producerNetwork {
					return nil, errors.New("APPLICATION_OUTPUT_NETWORK_MISMATCH")
				}
				if actual := stringValue(runtime["deploymentClusterId"]); actual != "" && actual != cluster {
					return nil, errors.New("APPLICATION_OUTPUT_NETWORK_MISMATCH")
				}
			}
			ports := objectSlice(service["ports"])
			for _, port := range ports {
				unnamedHost := o["valueSource"] == "service_host" && !published && stringValue(o["portName"]) == ""
				if !unnamedHost && port["name"] != o["portName"] && !(len(ports) == 1 && stringValue(o["portName"]) == "") {
					continue
				}
				if !published && port["internalVerified"] != true {
					continue
				}
				if published && (port["publishedVerified"] != true || stringValue(port["publishedHost"]) != serverPublicHost(findObject(state.Servers, stringValue(service["serverId"])))) {
					continue
				}
				if !published && o["valueSource"] == "service_host" && stringValue(service["serviceName"]) != "" {
					return plainApplicationValue(stringValue(service["serviceName"])), nil
				}
				n := numberValue(port["containerPort"])
				if published {
					n = numberValue(port["publishedPort"])
				}
				if n <= 0 {
					continue
				}
				if o["valueSource"] == "service_port" {
					return plainApplicationValue(strconv.Itoa(n)), nil
				}
				if host := stringValue(port["publishedHost"]); host != "" {
					return plainApplicationValue(host), nil
				}
			}
		case "static_url", "route_url":
			route := findObject(state.Routes, stringValue(o["routeId"]))
			if route["applicationId"] != ownerID || len(objectMapFromAny(route["pendingRemote"])) > 0 {
				break
			}
			urls := objectStringSlice(objectMapFromAny(route["lastApplied"])["accessibleUrls"])
			if len(urls) > 0 && strings.TrimSpace(urls[0]) != "" {
				return plainApplicationValue(urls[0]), nil
			}
		case "build_output":
			published := objectMapFromAny(app["publishedBuildOutputs"])
			if val, ok := published[id].(string); ok {
				return plainApplicationValue(val), nil
			}
		}
		return nil, errors.New("APPLICATION_OUTPUT_UNAVAILABLE")
	default:
		return plainApplicationValue(stringValue(variable["plainValue"])), nil
	}
}

func plainApplicationValue(value string) domain.Object {
	return domain.Object{"valueSource": "plain_text", "plainValue": value}
}
