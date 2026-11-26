package application

import (
	"errors"
	"slices"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

// 恢复相同应用的同名输出时保留目标 ID，使未参与本次恢复的消费者仍能引用它。
func reconcileRestoredOutputs(current, incoming domain.State, uploadID string) error {
	type identity struct{ app, output string }
	occupied := map[string]bool{}
	for _, cfg := range current.ApplicationDeploymentConfigs {
		for _, output := range objectSlice(cfg["outputVariables"]) {
			occupied[stringValue(output["id"])] = true
		}
	}
	mapped := map[identity]string{}
	for _, cfg := range incoming.ApplicationDeploymentConfigs {
		appID := stringValue(cfg["applicationId"])
		prior := findObjectBy(current.ApplicationDeploymentConfigs, "applicationId", appID)
		outputs := objectSlice(prior["outputVariables"])
		keys := map[string]bool{}
		reserved := map[string]bool{}
		for _, o := range objectSlice(cfg["outputVariables"]) {
			if prior := findObjectBy(outputs, "key", stringValue(o["key"])); prior != nil {
				reserved[stringValue(prior["id"])] = true
			}
		}
		for _, o := range objectSlice(cfg["outputVariables"]) {
			src, key := stringValue(o["id"]), stringValue(o["key"])
			original := identity{appID, src}
			if !applicationResourceName.MatchString(src) || !applicationVariableName.MatchString(key) || mapped[original] != "" || keys[key] {
				return errors.New("APPLICATION_OUTPUT_INVALID")
			}
			keys[key] = true
			id := src
			if previous := findObjectBy(outputs, "key", key); previous != nil {
				id = stringValue(previous["id"])
			} else if occupied[src] && (reserved[src] || findObject(outputs, src) == nil) {
				id = uniqueRestoredObjectID("output", src, uploadID+appID, occupied)
			}
			mapped[original], occupied[id], o["id"] = id, true, id
		}
	}
	for _, cfg := range incoming.ApplicationDeploymentConfigs {
		for _, v := range objectSlice(cfg["environmentVariables"]) {
			if v["valueSource"] != "output_ref" {
				continue
			}
			producer, id := stringValue(v["outputApplicationId"]), stringValue(v["outputId"])
			if next := mapped[identity{producer, id}]; next != "" {
				v["outputId"] = next
			} else {
				return errors.New("APPLICATION_OUTPUT_REFERENCE_INVALID")
			}
		}
	}
	state := current
	state.Applications = slices.Clone(current.Applications)
	state.ApplicationDeploymentConfigs = slices.Clone(current.ApplicationDeploymentConfigs)
	for _, a := range incoming.Applications {
		state.Applications = append(domain.DeleteObjectByID(state.Applications, stringValue(a["id"])), a)
	}
	for _, cfg := range incoming.ApplicationDeploymentConfigs {
		state = withApplicationConfig(state, stringValue(cfg["applicationId"]), cfg)
	}
	for _, cfg := range state.ApplicationDeploymentConfigs {
		for _, v := range objectSlice(cfg["environmentVariables"]) {
			if v["valueSource"] != "output_ref" {
				continue
			}
			owner, output, ok := visibleApplicationOutput(state, stringValue(cfg["applicationId"]), stringValue(v["outputId"]))
			if !ok || owner["applicationId"] != v["outputApplicationId"] {
				return errors.New("APPLICATION_OUTPUT_REFERENCE_INVALID")
			}
			if err := checkOutputCycle(state, owner, output, map[string]bool{}); err != nil {
				return err
			}
		}
	}
	return nil
}
