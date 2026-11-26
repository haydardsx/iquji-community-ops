package domain

import "strings"

var activeTaskStatuses = map[string]struct{}{
	"pending_confirmation": {},
	"queued":               {},
	"preflight_running":    {},
	"executing":            {},
	"verifying":            {},
}

func IsTaskActive(status string) bool {
	_, ok := activeTaskStatuses[status]
	return ok
}

func IsTaskStatus(status string) bool {
	return IsTaskActive(status) || IsTaskTerminal(status)
}

func IsTaskTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "rolled_back", "cancelled":
		return true
	default:
		return false
	}
}

func NormalizedTaskTarget(task Task) ResourceRef {
	kind := strings.TrimSpace(task.TargetKind)
	if kind == "" {
		kind = "task"
	}
	id := strings.TrimSpace(task.TargetID)
	if id == "" {
		id = strings.TrimSpace(task.ID)
	}
	if id == "" {
		id = "legacy_task"
	}
	name := strings.TrimSpace(task.TargetName)
	if name == "" {
		name = strings.TrimSpace(task.Action)
	}
	if name == "" {
		name = id
	}
	return Resource(kind, id, name)
}
