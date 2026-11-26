package application

import (
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

var applicationVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func normalizeApplicationVariables(state domain.State, variables, services []domain.Object, applicationID string, allowUnboundCredentials bool) ([]domain.Object, error) {
	serviceIDs := map[string]bool{}
	for _, service := range services {
		serviceIDs[stringValue(service["slotKey"])] = true
	}
	seen := map[string]bool{}
	normalized := make([]domain.Object, 0, len(variables))
	for _, variable := range variables {
		if !objectOnlyContainsKeys(variable, "id", "key", "scope", "serviceSlotKey", "valueSource", "valueType", "isRequired", "plainValue", "secretRef", "maskedSummary", "description", "stage", "outputId", "outputApplicationId") {
			return nil, errors.New("APPLICATION_VARIABLE_INVALID")
		}
		next := cloneObject(variable)
		key := strings.TrimSpace(stringValue(next["key"]))
		if len(key) > 256 || !applicationVariableName.MatchString(key) {
			return nil, errors.New("APPLICATION_VARIABLE_KEY_INVALID")
		}
		next["key"] = key
		scope := firstNonEmptyString(stringValue(next["scope"]), "global")
		if scope != "global" && scope != "service" {
			return nil, errors.New("APPLICATION_VARIABLE_SCOPE_INVALID")
		}
		next["scope"] = scope
		if scope == "service" {
			if !serviceIDs[stringValue(next["serviceSlotKey"])] {
				return nil, errors.New("APPLICATION_VARIABLE_SERVICE_REQUIRED")
			}
		} else {
			delete(next, "serviceSlotKey")
		}
		stage := stringValue(next["stage"])
		if stage != "runtime" && stage != "build" {
			return nil, errors.New("APPLICATION_VARIABLE_STAGE_INVALID")
		}
		if stage == "build" && (key == "CI" || key == "HOME" || key == "PATH" || key == "BUILD_REVISION" || key == "NODE_OPTIONS" || key == "LD_PRELOAD") {
			return nil, errors.New("APPLICATION_VARIABLE_KEY_RESERVED")
		}
		next["stage"] = stage
		identity := stage + ":" + scope + ":" + stringValue(next["serviceSlotKey"]) + ":" + key
		if seen[identity] {
			return nil, errors.New("APPLICATION_VARIABLE_KEY_DUPLICATE")
		}
		seen[identity] = true
		source := stringValue(next["valueSource"])
		if source == "" {
			source = "plain_text"
			if stringValue(next["secretRef"]) != "" {
				source = "secret_ref"
			}
		}
		next["valueSource"] = source
		valueType := firstNonEmptyString(stringValue(next["valueType"]), "string")
		switch valueType {
		case "string", "number", "boolean", "json", "certificate_ref":
		default:
			return nil, errors.New("APPLICATION_VARIABLE_TYPE_INVALID")
		}
		next["valueType"] = valueType
		delete(next, "maskedSummary")
		switch source {
		case "plain_text":
			if _, exists := next["plainValue"]; !exists {
				next["plainValue"] = ""
			}
			value, ok := next["plainValue"].(string)
			if !ok || next["isRequired"] == true && value == "" || !validApplicationVariableValue(value, valueType) {
				return nil, errors.New("APPLICATION_VARIABLE_VALUE_INVALID")
			}
			delete(next, "secretRef")
			delete(next, "outputId")
			delete(next, "outputApplicationId")
		case "secret_ref":
			ref := stringValue(next["secretRef"])
			// 模板草稿允许预留必填凭据，用户保存配置和发布时仍须绑定有效材料。
			if !allowUnboundCredentials || ref != "" || next["isRequired"] != true {
				if err := checkApplicationCredential(state, ref); err != nil {
					return nil, err
				}
			}
			delete(next, "plainValue")
			delete(next, "outputId")
			delete(next, "outputApplicationId")
		case "output_ref":
			if allowUnboundCredentials && next["isRequired"] == true && stringValue(next["outputId"]) == "" && stringValue(next["outputApplicationId"]) == "" {
				delete(next, "plainValue")
				delete(next, "secretRef")
				break
			}
			owner, _, ok := visibleApplicationOutput(state, applicationID, stringValue(next["outputId"]))
			if !ok || next["outputApplicationId"] != owner["applicationId"] {
				return nil, errors.New("APPLICATION_OUTPUT_REFERENCE_INVALID")
			}
			next["outputApplicationId"] = owner["applicationId"]
			delete(next, "plainValue")
			delete(next, "secretRef")
		default:
			return nil, errors.New("APPLICATION_VARIABLE_SOURCE_INVALID")
		}
		normalized = append(normalized, next)
	}
	return normalized, nil
}

func validApplicationVariableValue(value, valueType string) bool {
	if value == "" {
		return true
	}
	switch valueType {
	case "number":
		number, err := strconv.ParseFloat(value, 64)
		return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
	case "boolean":
		return value == "true" || value == "false"
	case "json":
		return json.Valid([]byte(value))
	default:
		return true
	}
}
