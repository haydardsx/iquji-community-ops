package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ListObjects(ctx context.Context, name string) (map[string]any, error) {
	if name == "tasks" {
		return r.ListTasks(ctx, TaskQuery{Page: 1, PageSize: 100})
	}
	if name == "operationRuns" {
		return r.ListOperationRuns(ctx, TaskQuery{Page: 1, PageSize: 100})
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	switch name {
	case "projects":
		items := make([]domain.Object, 0, len(state.Projects))
		for _, project := range state.Projects {
			items = append(items, projectWithApplications(project, filterObjectsBy(state.Applications, "projectId", stringValue(project["id"]))))
		}
		return page(items), nil
	case "servers":
		latest := latestSwarmSnapshots(state)
		items := make([]domain.Object, 0, len(state.Servers))
		for _, server := range state.Servers {
			items = append(items, enrichServerWithSnapshot(server, state, latest[stringValue(server["id"])]))
		}
		return page(items), nil
	case "sourceCredentials":
		items := []domain.Object{}
		for _, asset := range state.SecretAssets {
			if stringValue(asset["assetType"]) == "git_token" || stringValue(asset["purpose"]) == "git_auth" {
				items = append(items, r.publicSecretAsset(ctx, asset))
			}
		}
		return page(items), nil
	case "secretAssets":
		items := make([]domain.Object, 0, len(state.SecretAssets))
		for _, asset := range state.SecretAssets {
			items = append(items, r.publicSecretAsset(ctx, asset))
		}
		return map[string]any{"items": items}, nil
	case "domains":
		return map[string]any{"items": state.Domains}, nil
	case "certificates":
		return map[string]any{"items": state.Certificates}, nil
	case "resolvers":
		return map[string]any{"items": state.Resolvers}, nil
	case "ingresses":
		return map[string]any{"items": state.Ingresses}, nil
	case "monitoringAgents":
		return page(monitoringAgentViews(state, time.Now())), nil
	}
	return nil, errors.New("UNKNOWN_RESOURCE")
}

func (r *Runtime) GetOperationRun(ctx context.Context, id string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range state.OperationRuns {
		if item.ID == id {
			return operationRunResponseWithRecovery(state, item), nil
		}
	}
	return nil, errors.New("OPERATION_RUN_NOT_FOUND")
}

type TaskQuery struct {
	ProjectID     string
	ApplicationID string
	ServerID      string
	TunnelID      string
	Category      string
	Section       string
	Navigation    string
	Action        string
	Status        string
	Target        string
	TargetKind    string
	TargetID      string
	Page          int
	PageSize      int
}

func (r *Runtime) ListTasks(ctx context.Context, query TaskQuery) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return listTasks(state.Tasks, query), nil
}

func listTasks(tasks []domain.Task, query TaskQuery) map[string]any {
	if query.Page < 1 {
		query.Page = 1
	}
	if query.PageSize < 1 {
		query.PageSize = 20
	}
	if query.PageSize > 100 {
		query.PageSize = 100
	}
	action := strings.ToLower(strings.TrimSpace(query.Action))
	target := strings.ToLower(strings.TrimSpace(query.Target))
	filtered := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		if action != "" && !strings.Contains(strings.ToLower(task.Action), action) {
			continue
		}
		if query.Status != "" && task.Status != query.Status {
			continue
		}
		if query.TargetKind != "" && task.TargetKind != query.TargetKind {
			continue
		}
		if query.TargetID != "" && task.TargetID != query.TargetID {
			continue
		}
		if target != "" {
			targetText := strings.ToLower(strings.Join([]string{task.TargetKind, task.TargetID, task.TargetName}, " "))
			if !strings.Contains(targetText, target) {
				continue
			}
		}
		filtered = append(filtered, task)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].UpdatedAt == filtered[j].UpdatedAt {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].UpdatedAt > filtered[j].UpdatedAt
	})
	total := len(filtered)
	start := (query.Page - 1) * query.PageSize
	if start > total {
		start = total
	}
	end := start + query.PageSize
	if end > total {
		end = total
	}
	items := make([]map[string]any, 0, end-start)
	for _, task := range filtered[start:end] {
		items = append(items, taskListItem(task))
	}
	return map[string]any{"items": items, "page": query.Page, "pageSize": query.PageSize, "total": total}
}

func page(items []domain.Object) map[string]any {
	return domain.PageObjects(items)
}

func taskListItem(task domain.Task) map[string]any {
	payload := task.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	target := domain.NormalizedTaskTarget(task)
	result := map[string]any{
		"id":             task.ID,
		"action":         task.Action,
		"status":         task.Status,
		"targetKind":     target.Kind,
		"targetId":       target.ID,
		"targetName":     target.Name,
		"payload":        payload,
		"payloadSummary": task.Action,
		"createdAt":      task.CreatedAt,
		"updatedAt":      task.UpdatedAt,
	}
	if task.LogRef != "" {
		result["logRef"] = task.LogRef
	}
	return result
}

func (r *Runtime) GetTask(ctx context.Context, id string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range state.Tasks {
		if task.ID == id {
			return domain.TaskResponse(task), nil
		}
	}
	return nil, errors.New("TASK_NOT_FOUND")
}

func (r *Runtime) CreateTask(ctx context.Context, action string, targetKind string, targetID string, targetName string, payload map[string]any) (domain.Task, error) {
	task := newTask(action, targetKind, targetID, targetName, payload)
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		if reused, err := reuseTaskRequest(ctx, *state, &task); reused || err != nil {
			return err
		}
		if appID := taskApplicationID(*state, task); appID != "" {
			if app := findObject(state.Applications, appID); app != nil {
				task.Payload["applicationId"], task.Payload["projectId"] = appID, app["projectId"]
			}
		}
		return enqueueTaskRequest(ctx, state, &task)
	})
	return task, err
}

func newTask(action string, targetKind string, targetID string, targetName string, payload map[string]any) domain.Task {
	if payload == nil {
		payload = map[string]any{}
	}
	now := domain.Now()
	task := domain.Task{
		ID:         domain.NewID("task"),
		Action:     action,
		Status:     "queued",
		TargetKind: targetKind,
		TargetID:   targetID,
		TargetName: targetName,
		Payload:    payload,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if domain.IsDangerousAction(action) {
		task.Status = "pending_confirmation"
		task.ConfirmationTokenID = domain.NewID("confirm")
	}
	return task
}

func appendTaskState(state *domain.State, task domain.Task) {
	state.Tasks = append(state.Tasks, task)
	state.OperationRuns = append(state.OperationRuns, domain.OperationRun{
		ID: task.ID, Action: task.Action, Status: "queued",
		TargetKind: task.TargetKind, TargetID: task.TargetID, TargetName: task.TargetName,
		Summary: "操作已创建", CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	})
	if task.Status == "pending_confirmation" {
		state.ConfirmationTokens = append(state.ConfirmationTokens, domain.Confirmation{
			ID:        task.ConfirmationTokenID,
			TaskID:    task.ID,
			Action:    task.Action,
			Status:    "pending",
			Token:     domain.NewID("token"),
			CreatedAt: domain.Now(),
			ExpiresAt: domain.PlusHours(1),
		})
	}
	appendOperationLog(state, task, "info", "操作已创建")
	message := "操作已创建并入队"
	if task.Status == "pending_confirmation" {
		message = "操作已创建，等待确认"
	}
	state.RuntimeLogs = append(state.RuntimeLogs, domain.NewRuntimeLog(task, "api", "created", "info", message, task.CreatedAt))
}

func findActiveTask(tasks []domain.Task, action string, targetKind string, targetID string) *domain.Task {
	for index := range tasks {
		task := &tasks[index]
		if task.Action == action && task.TargetKind == targetKind && task.TargetID == targetID && domain.IsTaskActive(task.Status) {
			return task
		}
	}
	return nil
}

func (r *Runtime) ConfirmTask(ctx context.Context, taskID string) (domain.Task, error) {
	var result domain.Task
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		for index := range state.Tasks {
			if state.Tasks[index].ID != taskID {
				continue
			}
			if state.Tasks[index].Status != "pending_confirmation" {
				return errors.New("TASK_NOT_CONFIRMABLE")
			}
			state.Tasks[index].Status = "queued"
			state.Tasks[index].QueueOrder = domain.NextTaskQueueOrder(state.Tasks)
			state.Tasks[index].UpdatedAt = domain.Now()
			result = state.Tasks[index]
			for tokenIndex := range state.ConfirmationTokens {
				if state.ConfirmationTokens[tokenIndex].TaskID == taskID {
					state.ConfirmationTokens[tokenIndex].Status = "confirmed"
				}
			}
			appendOperationLog(state, result, "info", "操作已确认")
			state.RuntimeLogs = append(state.RuntimeLogs, domain.NewRuntimeLog(result, "api", "queue", "info", "操作已确认并入队，等待关联资源与执行槽位", result.UpdatedAt))
			return nil
		}
		return errors.New("TASK_NOT_FOUND")
	})
	return result, err
}

func (r *Runtime) CancelTask(ctx context.Context, taskID string) (domain.Task, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.Task{}, err
	}
	candidate := domain.Task{}
	for _, task := range state.Tasks {
		if task.ID == taskID {
			candidate = task
			break
		}
	}
	if candidate.ID == "" {
		return domain.Task{}, errors.New("TASK_NOT_FOUND")
	}
	deleteSecretRefs := []string{}
	restoreUploadID := ""
	if isFrpTunnelStartTask(candidate) {
		if run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, candidate.TargetID); run != nil && run.TokenSecretRef != "" {
			deleteSecretRefs = append(deleteSecretRefs, run.TokenSecretRef)
		}
	}
	if isRestoreBackupTask(candidate) {
		restoreUploadID = strings.TrimSpace(domain.StringValue(candidate.Payload["uploadId"]))
		for _, item := range state.BackupRestoreJobs {
			if domain.StringValue(item["kind"]) != "restore_upload" || domain.StringValue(item["id"]) != restoreUploadID {
				continue
			}
			if ref := strings.TrimSpace(domain.StringValue(item["sessionSecretRef"])); ref != "" {
				deleteSecretRefs = append(deleteSecretRefs, ref)
			}
		}
	}
	var result domain.Task
	err = r.Store.MutateStateWithSecretChanges(ctx, nil, deleteSecretRefs, func(state *domain.State) error {
		for index := range state.Tasks {
			if state.Tasks[index].ID != taskID {
				continue
			}
			if swarmMaintenanceNeedsCleanup(state.Tasks[index]) {
				state.Tasks[index].CancelRequested = true
				state.Tasks[index].UpdatedAt = domain.Now()
				result = state.Tasks[index]
				for i := range state.OperationRuns {
					if state.OperationRuns[i].ID == taskID {
						state.OperationRuns[i].Summary = "正在取消 Swarm 维护并恢复已备份状态"
						state.OperationRuns[i].UpdatedAt = result.UpdatedAt
					}
				}
				appendOperationLog(state, result, "warn", "已请求取消 Swarm 维护")
				return nil
			}
			if state.Tasks[index].Status == "executing" && state.Tasks[index].Action == string(domain.TaskActionRequestJenkinsRelease) {
				state.Tasks[index].CancelRequested = true
				state.Tasks[index].UpdatedAt = domain.Now()
				result = state.Tasks[index]
				for runIndex := range state.JenkinsReleaseRuns {
					if stringValue(state.JenkinsReleaseRuns[runIndex]["operationRunId"]) == taskID {
						state.JenkinsReleaseRuns[runIndex]["cancelRequested"] = true
						state.JenkinsReleaseRuns[runIndex]["updatedAt"] = result.UpdatedAt
					}
				}
				for runIndex := range state.OperationRuns {
					if state.OperationRuns[runIndex].ID == taskID && state.OperationRuns[runIndex].Status == "running" {
						state.OperationRuns[runIndex].Summary = "正在取消 Jenkinsfile 发布"
						state.OperationRuns[runIndex].UpdatedAt = result.UpdatedAt
					}
				}
				appendOperationLog(state, result, "warn", "已请求取消 Jenkinsfile 发布")
				return nil
			}
			if state.Tasks[index].Status != "pending_confirmation" && state.Tasks[index].Status != "queued" {
				return errors.New("TASK_NOT_CANCELLABLE")
			}
			state.Tasks[index].Status = "cancelled"
			state.Tasks[index].UpdatedAt = domain.Now()
			result = state.Tasks[index]
			for runIndex := range state.OperationRuns {
				run := &state.OperationRuns[runIndex]
				if run.ID == result.ID && (run.Status == "queued" || run.Status == "running") {
					run.Status = "cancelled"
					run.Summary = "操作已取消"
					run.UpdatedAt = result.UpdatedAt
				}
			}
			for tokenIndex := range state.ConfirmationTokens {
				if state.ConfirmationTokens[tokenIndex].TaskID == taskID && state.ConfirmationTokens[tokenIndex].Status == "pending" {
					state.ConfirmationTokens[tokenIndex].Status = "rejected"
				}
			}
			appendOperationLog(state, result, "warn", "操作已取消")
			if isFrpTunnelStartTask(result) {
				run := domain.FindFrpTunnelRun(state.FrpTunnelRuns, result.TargetID)
				if run == nil {
					return errors.New("FRP_TUNNEL_RUN_NOT_FOUND")
				}
				run.Status = frpTunnelStatusCancelled
				run.StoppedAt = domain.Now()
				run.UpdatedAt = run.StoppedAt
				run.ErrorSummary = "start cancelled"
			}
			if restoreUploadID != "" {
				state.BackupRestoreJobs = removeRestoreUploadSession(state.BackupRestoreJobs, restoreUploadID)
			}
			return nil
		}
		return errors.New("TASK_NOT_FOUND")
	})
	if err == nil {
		if finalizeErr := r.finalizeSecretAssetTask(ctx, result, "cancelled"); finalizeErr != nil {
			return result, finalizeErr
		}
		if restoreUploadID != "" {
			if removeErr := os.Remove(r.restoreUploadArchivePath(restoreUploadID)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = fmt.Errorf("RESTORE_STAGING_CLEANUP_FAILED: %w", removeErr)
			}
		}
	}
	return result, err
}

func appendOperationLog(state *domain.State, task domain.Task, level string, message string) {
	state.OperationLogs = append(state.OperationLogs, domain.NewOperationLog(task, "api", level, message, domain.Now()))
}

func newOperationLog(action string, targetKind string, targetID string, status string, message string, operationRunID string) domain.OperationLog {
	task := domain.Task{ID: operationRunID, Action: action, Status: status, TargetKind: targetKind, TargetID: targetID}
	return domain.NewOperationLog(task, "api", domain.LogStatusLevel(status), message, domain.Now())
}

func isRestoreBackupTask(task domain.Task) bool {
	return task.Action == string(domain.TaskActionRestoreBackup) || task.Action == string(domain.TaskActionRestoreBackupOverwrite)
}

func isFrpTunnelStartTask(task domain.Task) bool {
	if task.Action != string(domain.TaskActionStartFrpTunnel) {
		return false
	}
	return task.TargetKind == "frp_tunnel_run" || task.TargetKind == "frp_tunnel_session"
}

func findTask(tasks []domain.Task, taskID string) *domain.Task {
	for index := range tasks {
		if tasks[index].ID == taskID {
			return &tasks[index]
		}
	}
	return nil
}
