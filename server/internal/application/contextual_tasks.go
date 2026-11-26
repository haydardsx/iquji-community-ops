package application

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

const MaxContextualTaskTargetRefs = 100

type TaskTargetRef struct {
	Kind string
	ID   string
}

type contextualTaskKey struct {
	target TaskTargetRef
	action string
}

func (r *Runtime) ListContextualTasks(ctx context.Context, targetRefs []TaskTargetRef) (map[string]any, error) {
	normalized, err := normalizeTaskTargetRefs(targetRefs)
	if err != nil {
		return nil, err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return contextualTasks(state.Tasks, normalized), nil
}

func normalizeTaskTargetRefs(targetRefs []TaskTargetRef) ([]TaskTargetRef, error) {
	if len(targetRefs) == 0 {
		return nil, errors.New("TASK_TARGET_REF_REQUIRED")
	}
	if len(targetRefs) > MaxContextualTaskTargetRefs {
		return nil, errors.New("TOO_MANY_TASK_TARGET_REFS")
	}
	seen := make(map[TaskTargetRef]struct{}, len(targetRefs))
	normalized := make([]TaskTargetRef, 0, len(targetRefs))
	for _, targetRef := range targetRefs {
		targetRef.Kind = strings.TrimSpace(targetRef.Kind)
		targetRef.ID = strings.TrimSpace(targetRef.ID)
		if targetRef.Kind == "" || targetRef.ID == "" {
			return nil, errors.New("INVALID_TASK_TARGET_REF")
		}
		if _, exists := seen[targetRef]; exists {
			continue
		}
		seen[targetRef] = struct{}{}
		normalized = append(normalized, targetRef)
	}
	return normalized, nil
}

func contextualTasks(tasks []domain.Task, targetRefs []TaskTargetRef) map[string]any {
	targetSet := make(map[TaskTargetRef]struct{}, len(targetRefs))
	for _, targetRef := range targetRefs {
		targetSet[targetRef] = struct{}{}
	}

	selected := make([]domain.Task, 0)
	latestTerminal := make(map[contextualTaskKey]domain.Task)
	for _, task := range tasks {
		targetRef := TaskTargetRef{Kind: task.TargetKind, ID: task.TargetID}
		if _, matches := targetSet[targetRef]; !matches {
			continue
		}
		if domain.IsTaskActive(task.Status) {
			selected = append(selected, task)
			continue
		}
		if !domain.IsTaskTerminal(task.Status) {
			continue
		}
		key := contextualTaskKey{target: targetRef, action: task.Action}
		current, exists := latestTerminal[key]
		if !exists || taskUpdatedAfter(task, current) {
			latestTerminal[key] = task
		}
	}
	for _, task := range latestTerminal {
		selected = append(selected, task)
	}

	sort.Slice(selected, func(i, j int) bool {
		leftPriority := contextualTaskPriority(selected[i].Status)
		rightPriority := contextualTaskPriority(selected[j].Status)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return taskUpdatedAfter(selected[i], selected[j])
	})
	items := make([]map[string]any, 0, len(selected))
	for _, task := range selected {
		items = append(items, taskListItem(task))
	}
	return map[string]any{"items": items, "total": len(items)}
}

func contextualTaskPriority(status string) int {
	switch status {
	case "pending_confirmation":
		return 0
	case "preflight_running", "executing", "verifying":
		return 1
	case "queued":
		return 2
	case "failed", "rolled_back":
		return 3
	default:
		return 4
	}
}

func taskUpdatedAfter(left domain.Task, right domain.Task) bool {
	if left.UpdatedAt != right.UpdatedAt {
		return left.UpdatedAt > right.UpdatedAt
	}
	return left.ID > right.ID
}
