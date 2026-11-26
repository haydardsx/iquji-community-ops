package application

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var applicationResourceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func normalizeApplicationConfiguration(state domain.State, application, config domain.Object, allowUnboundCredentials bool) error {
	if raw, exists := config["sourceConfig"]; exists {
		source, err := validateApplicationSourceConfig(objectMapFromAny(raw))
		if err != nil {
			return err
		}
		if ref := stringValue(source["authSecretRef"]); ref != "" {
			if credentialForPurpose(state, ref, "git_token") == nil {
				return errors.New("APPLICATION_GIT_SECRET_INVALID")
			}
		}
		config["sourceConfig"] = source
	}
	projectID := stringValue(application["projectId"])
	for _, field := range []string{"buildServerId", "deploymentManagerServerId", "gatewayServerId"} {
		id := stringValue(config[field])
		if id == "" {
			if field == "deploymentManagerServerId" {
				config[field] = ""
			} else {
				delete(config, field)
			}
			continue
		}
		server := findObject(state.Servers, id)
		if server == nil {
			if field == "buildServerId" {
				return errors.New("BUILD_SERVER_NOT_FOUND")
			}
			if field == "deploymentManagerServerId" {
				return errors.New("DEPLOYMENT_MANAGER_NOT_FOUND")
			}
			return errors.New("GATEWAY_SERVER_NOT_FOUND")
		}
		if !serverAvailableToProject(server, projectID) {
			if field == "buildServerId" {
				return errors.New("BUILD_SERVER_NOT_AVAILABLE_TO_PROJECT")
			}
			if field == "deploymentManagerServerId" {
				return errors.New("DEPLOYMENT_MANAGER_NOT_AVAILABLE_TO_PROJECT")
			}
			return errors.New("APPLICATION_SERVER_NOT_AVAILABLE_TO_PROJECT")
		}
	}
	swarm := objectMapFromAny(config["swarmRuntime"])
	if len(swarm) > 0 {
		if err := normalizeApplicationSwarm(state, projectID, swarm); err != nil {
			return err
		}
		if err := validateVolumeClaims(state, stringValue(application["id"]), swarm); err != nil {
			return err
		}
		config["swarmRuntime"] = swarm
	}
	placement := objectMapFromAny(config["placement"])
	if !objectOnlyContainsKeys(placement, "eligibleServerIds") {
		return errors.New("APPLICATION_PLACEMENT_INVALID")
	}
	eligible := objectStringSlice(placement["eligibleServerIds"])
	for _, id := range eligible {
		server := findObject(state.Servers, id)
		if server == nil || !serverAvailableToProject(server, projectID) {
			return errors.New("APPLICATION_SERVER_NOT_AVAILABLE_TO_PROJECT")
		}
	}
	if variables, exists := config["environmentVariables"]; exists {
		if !applicationObjectArray(variables) {
			return errors.New("APPLICATION_VARIABLE_INVALID")
		}
		normalized, err := normalizeApplicationVariables(state, objectSlice(variables), objectSlice(swarm["services"]), stringValue(application["id"]), allowUnboundCredentials)
		if err != nil {
			return err
		}
		config["environmentVariables"] = normalized
	}
	if err := validateApplicationOutputs(state, application, config); err != nil {
		return err
	}
	if err := validateRegistryCredential(state, registrySyncConfig(config)); err != nil {
		return err
	}
	normalizeManagedDeliveryConfig(config)
	return nil
}

func normalizeApplicationSwarm(state domain.State, projectID string, swarm domain.Object) error {
	if !objectOnlyContainsKeys(swarm, "containerPort", "publishedPort", "overlayNetwork", "services", "ports", "volumes", "command", "shellEntrypoint", "initializeCommand", "healthCommand", "logCommand", "updateConfig", "rollbackConfig") {
		return errors.New("APPLICATION_SWARM_CONFIG_INVALID")
	}
	if err := validateContainerRuntime(swarm); err != nil {
		return err
	}
	if value := stringValue(swarm["overlayNetwork"]); value != "" && !applicationResourceName.MatchString(value) {
		return errors.New("APPLICATION_SWARM_NAME_INVALID")
	}
	if value, exists := swarm["containerPort"]; exists && !integerInRange(value, 1, 65535) {
		return errors.New("APPLICATION_CONTAINER_PORT_INVALID")
	}
	if value, exists := swarm["publishedPort"]; exists && !integerInRange(value, 0, 65535) {
		return errors.New("APPLICATION_PUBLISHED_PORT_INVALID")
	}
	slots, names := map[string]bool{}, map[string]bool{}
	if value, exists := swarm["services"]; exists && !applicationObjectArray(value) {
		return errors.New("APPLICATION_SERVICE_CONFIG_INVALID")
	}
	for _, service := range objectSlice(swarm["services"]) {
		if !objectOnlyContainsKeys(service, "slotKey", "serviceName", "serverId", "replicas", "publishedPort") {
			return errors.New("APPLICATION_SERVICE_CONFIG_INVALID")
		}
		slot, name := stringValue(service["slotKey"]), stringValue(service["serviceName"])
		if !applicationResourceName.MatchString(slot) || !applicationResourceName.MatchString(name) || slots[slot] || names[name] {
			return errors.New("APPLICATION_SERVICE_NAME_INVALID")
		}
		slots[slot], names[name] = true, true
		if id := stringValue(service["serverId"]); id != "" {
			server := findObject(state.Servers, id)
			if server == nil || !serverAvailableToProject(server, projectID) {
				return errors.New("APPLICATION_SERVER_NOT_AVAILABLE_TO_PROJECT")
			}
		}
		if value, exists := service["replicas"]; exists && !integerInRange(value, 1, 2147483647) {
			return errors.New("APPLICATION_REPLICAS_INVALID")
		}
		if value, exists := service["publishedPort"]; exists && !integerInRange(value, 0, 65535) {
			return errors.New("APPLICATION_PUBLISHED_PORT_INVALID")
		}
	}
	if update := objectMapFromAny(swarm["updateConfig"]); len(update) > 0 {
		if !objectOnlyContainsKeys(update, "parallelism", "order", "failureAction") || stringValue(update["order"]) != "start-first" {
			return errors.New("APPLICATION_UPDATE_STRATEGY_INVALID")
		}
	}
	return nil
}

func applicationObjectArray(value any) bool {
	switch items := value.(type) {
	case []domain.Object:
		return true
	case []any:
		for _, item := range items {
			switch item.(type) {
			case domain.Object, map[string]any:
			default:
				return false
			}
		}
		return true
	default:
		return false
	}
}

func integerInRange(value any, minimum, maximum float64) bool {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return false
		}
		number = parsed
	default:
		return false
	}
	return number >= minimum && number <= maximum && number == math.Trunc(number)
}
