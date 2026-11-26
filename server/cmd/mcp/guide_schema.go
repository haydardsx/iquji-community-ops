package main

import (
	"slices"

	"github.com/haydardsx/iquji-community-ops/server/internal/automation"
)

// 每个引导动作使用自己的参数和目标约束，避免一个动作的授权被套用到另一个动作。
func addGuideScopes(schema, props map[string]any, grants []automation.Permission) {
	var actions []string
	var branches []any
	for _, cmd := range automation.Catalog {
		if !cmd.Guide {
			continue
		}
		var targets []string
		for _, p := range grants {
			if p.Command != cmd.Name {
				continue
			}
			if len(p.Targets) == 0 {
				targets = []string{}
				break
			}
			targets = append(targets, p.Targets...)
		}
		if targets == nil {
			continue
		}
		actions = append(actions, cmd.Name)
		fields := map[string]any{"action": map[string]any{"const": cmd.Name}}
		for _, field := range cmd.Fields {
			if cmd.GuideField(field) {
				fields[field] = props[field]
			}
		}
		required := cmd.GuideRequired()
		if len(targets) > 0 {
			slices.Sort(targets)
			fields["target"] = map[string]any{"type": "string", "enum": slices.Compact(targets)}
			if !slices.Contains(required, "target") {
				required = append(required, "target")
			}
		}
		branch := map[string]any{"properties": fields, "required": required, "additionalProperties": false}
		if dependencies := cmd.GuideDependencies(); len(dependencies) > 0 {
			branch["dependentRequired"] = dependencies
		}
		branches = append(branches, branch)
	}
	props["action"] = map[string]any{"type": "string", "enum": actions, "description": "选择获准的原动作；目标和上下文须符合该动作对应的权限范围。"}
	schema["oneOf"] = branches
}
