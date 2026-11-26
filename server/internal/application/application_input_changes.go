package application

import (
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func stageInputRevisions(snapshot []domain.Object, stage string) domain.Object {
	revisions := domain.Object{}
	for _, v := range snapshot {
		if firstNonEmptyString(stringValue(v["stage"]), "runtime") != stage {
			continue
		}
		revisions[inputSlotKey(v)] = inputSignature([]domain.Object{v}, stage)
	}
	return revisions
}

func inputSlotKey(v domain.Object) string {
	return stringValue(v["serviceSlotKey"]) + ":" + stringValue(v["key"])
}

// 投影只返回发生变化的变量及来源名称。逐变量的修订和冻结材料只供服务端比较。
func applicationWithInputStatus(state domain.State, app domain.Object) domain.Object {
	view := cloneObject(app)
	applied := objectMapFromAny(app["appliedInputRevisions"])
	status := applicationInputStatus(state, app)
	view["inputStatus"] = status
	delete(view, "appliedInputRevisions")
	delete(view, "appliedInputSignatures")
	delete(view, "publishedBuildOutputs")
	delete(view, "appliedRuntimeKeys")
	if status == "" {
		return view
	}
	id := stringValue(app["id"])
	cfg := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", id)
	changes := []domain.Object{}
	for _, stage := range []string{"build", "runtime"} {
		old := objectMapFromAny(applied[stage])
		// 旧版仅存阶段签名。该阶段一致时避免列出未变化的变量。
		if old == nil {
			all, evalErr := applicationStageInputSnapshot(state, id, cfg, stage)
			if evalErr == nil && objectMapFromAny(app["appliedInputSignatures"])[stage] == inputSignature(all, stage) {
				continue
			}
		}
		configured := map[string]bool{}
		for _, v := range objectSlice(cfg["environmentVariables"]) {
			if firstNonEmptyString(stringValue(v["stage"]), "runtime") != stage {
				continue
			}
			key := inputSlotKey(v)
			configured[key] = true
			snapshot, err := applicationStageInputSnapshot(state, id, domain.Object{"environmentVariables": []domain.Object{v}}, stage)
			if err == nil && old[key] == inputSignature(snapshot, stage) {
				continue
			}
			item := domain.Object{"key": v["key"], "stage": stage, "serviceSlotKey": stringValue(v["serviceSlotKey"]), "source": v["valueSource"]}
			if v["valueSource"] == "output_ref" {
				owner, out, ok := visibleApplicationOutput(state, id, stringValue(v["outputId"]))
				if ok {
					item["outputName"] = out["key"]
					item["applicationName"] = findObject(state.Applications, stringValue(owner["applicationId"]))["name"]
				}
			}
			ref := stringValue(v["secretRef"])
			if len(snapshot) > 0 {
				ref = stringValue(snapshot[0]["secretRef"])
			}
			if ref != "" {
				item["credentialName"] = findObjectBy(state.SecretAssets, "secretRef", ref)["name"]
			}
			changes = append(changes, item)
		}
		var removed []string
		for key := range old {
			if !configured[key] {
				removed = append(removed, key)
			}
		}
		sort.Strings(removed)
		for _, key := range removed {
			slot, name, _ := strings.Cut(key, ":")
			changes = append(changes, domain.Object{"key": name, "serviceSlotKey": slot, "stage": stage, "source": "removed"})
		}
	}
	view["inputChanges"] = changes
	return view
}
