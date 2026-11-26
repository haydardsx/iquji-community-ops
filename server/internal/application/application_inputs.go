package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 快照只固定公开值和不可变的材料修订引用，供 Worker 在执行边界读取。
func applicationInputSnapshot(state domain.State, appID string, config domain.Object) ([]domain.Object, error) {
	return applicationStageInputSnapshot(state, appID, config, "")
}
func applicationStageInputSnapshot(state domain.State, appID string, config domain.Object, stage string) ([]domain.Object, error) {
	result := []domain.Object{}
	for _, v := range objectSlice(config["environmentVariables"]) {
		if stage != "" && firstNonEmptyString(stringValue(v["stage"]), "runtime") != stage {
			continue
		}
		frozen, err := resolveApplicationValue(state, appID, appID, v, map[string]bool{})
		if err != nil {
			return nil, err
		}
		if frozen["valueSource"] == "plain_text" && (v["isRequired"] == true && stringValue(frozen["plainValue"]) == "" || !validApplicationVariableValue(stringValue(frozen["plainValue"]), stringValue(v["valueType"]))) {
			return nil, errors.New("APPLICATION_VARIABLE_VALUE_INVALID")
		}
		frozen["stage"] = firstNonEmptyString(stringValue(v["stage"]), "runtime")
		frozen["scope"] = firstNonEmptyString(stringValue(v["scope"]), "global")
		frozen["key"] = v["key"]
		if v["valueSource"] == "output_ref" {
			frozen["outputId"], frozen["outputApplicationId"] = v["outputId"], v["outputApplicationId"]
		}
		if v["serviceSlotKey"] != nil {
			frozen["serviceSlotKey"] = v["serviceSlotKey"]
		}
		result = append(result, frozen)
	}
	return result, nil
}

func (r *Runtime) resolvedStageValues(ctx context.Context, snapshot []domain.Object, stage, slot string) (map[string]string, error) {
	result := map[string]string{}
	var state *domain.State
	for _, scope := range []string{"global", "service"} {
		for _, v := range snapshot {
			if firstNonEmptyString(stringValue(v["stage"]), "runtime") != stage || firstNonEmptyString(stringValue(v["scope"]), "global") != scope || scope == "service" && stringValue(v["serviceSlotKey"]) != slot {
				continue
			}
			value := stringValue(v["plainValue"])
			if v["valueSource"] == "secret_ref" {
				var err error
				if state == nil {
					loaded, e := r.Store.LoadState(ctx)
					if e != nil {
						return nil, e
					}
					state = &loaded
				}
				value, err = r.readApplicationMaterial(ctx, *state, v)
				if err != nil {
					return nil, err
				}
			}
			result[stringValue(v["key"])] = value
		}
	}
	return result, nil
}

func inputSignature(snapshot []domain.Object, stage string) string {
	items := []string{}
	for _, v := range snapshot {
		if firstNonEmptyString(stringValue(v["stage"]), "runtime") != stage {
			continue
		}
		fields := []string{stringValue(v["key"]), stringValue(v["serviceSlotKey"]), stringValue(v["valueSource"]), stringValue(v["plainValue"]), stringValue(v["secretRef"]), stringValue(v["materialRevisionRef"])}
		if v["outputId"] != nil {
			fields = append(fields, stringValue(v["outputApplicationId"]), stringValue(v["outputId"]))
		}
		raw, _ := json.Marshal(fields)
		items = append(items, string(raw))
	}
	sort.Strings(items)
	raw, _ := json.Marshal(items)
	return domain.HashSecret(string(raw))
}

func recordAppliedApplicationInputs(state *domain.State, appID string, snapshot []domain.Object, stages ...string) {
	app := findObject(state.Applications, appID)
	if app == nil {
		return
	}
	signatures := objectMapFromAny(app["appliedInputSignatures"])
	if signatures == nil {
		signatures = domain.Object{}
	}
	rows := objectMapFromAny(app["appliedInputRevisions"])
	if rows == nil {
		rows = domain.Object{}
	}
	for _, stage := range stages {
		rows[stage] = stageInputRevisions(snapshot, stage)
		signatures[stage] = inputSignature(snapshot, stage)
	}
	app["appliedInputSignatures"] = signatures
	app["appliedInputRevisions"] = rows
	for _, stage := range stages {
		if stage == "runtime" {
			keys := []string{}
			for _, v := range snapshot {
				if firstNonEmptyString(stringValue(v["stage"]), "runtime") == "runtime" {
					keys = append(keys, stringValue(v["key"]))
				}
			}
			app["appliedRuntimeKeys"] = uniqueStrings(keys)
		}
	}
}

func applicationInputStatus(state domain.State, app domain.Object) string {
	config := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", stringValue(app["id"]))
	previous := objectMapFromAny(app["appliedInputSignatures"])
	hasDeployment := len(previous) > 0 || len(objectSlice(objectMapFromAny(app["runtimeSnapshot"])["services"])) > 0
	if !hasDeployment {
		return ""
	}
	values, err := applicationInputSnapshot(state, stringValue(app["id"]), config)
	if err != nil {
		return "unavailable"
	}
	for _, stage := range []string{"build", "runtime"} {
		current := inputSignature(values, stage)
		if stringValue(previous[stage]) == current || previous[stage] == nil && current == inputSignature(nil, stage) {
			continue
		}
		if stage == "build" {
			return "rebuild"
		}
		return "restart"
	}
	return ""
}

func applicationBuildOutputKeys(config domain.Object) domain.Object {
	keys := domain.Object{}
	for _, o := range objectSlice(config["outputVariables"]) {
		if o["valueSource"] == "build_output" {
			keys[stringValue(o["key"])] = o["id"]
		}
	}
	return keys
}

func sortedKeysOfApplicationValues(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
