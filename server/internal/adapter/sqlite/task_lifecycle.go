package sqlite

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (s *Store) ClaimNextTask(ctx context.Context, staleAfter time.Duration, options ...TaskClaimOptions) (*domain.Task, error) {
	option := TaskClaimOptions{Concurrency: 4}
	if len(options) > 0 {
		option = options[0]
	}
	if option.Concurrency < 1 {
		option.Concurrency = 4
	}
	var claimed *domain.Task
	err := s.MutateState(ctx, func(state *domain.State) error {
		initializeTaskQueue(state)
		now := domain.Now()
		nowTime := time.Now().UTC()
		for index := range state.Tasks {
			task := &state.Tasks[index]
			if (task.Status == "executing" || task.Status == "preflight_running" || task.Status == "verifying") && staleAfter >= 0 {
				if taskLeaseExpired(*task, nowTime, staleAfter) {
					task.Status = "queued"
					task.ClaimToken = ""
					task.ClaimOwner = ""
					task.LeaseExpiresAt = ""
					task.UpdatedAt = now
					if task.Action == string(domain.TaskActionRequestJenkinsRelease) {
						for runIndex := range state.JenkinsReleaseRuns {
							run := state.JenkinsReleaseRuns[runIndex]
							runOperationID := domain.StringValue(run["operationRunId"])
							if runOperationID == "" {
								runOperationID = domain.StringValue(run["taskId"])
							}
							if runOperationID != task.ID {
								continue
							}
							runStatus, _ := run["status"].(string)
							switch runStatus {
							case "pending", "triggered":
								state.JenkinsReleaseRuns[runIndex]["tokenHash"] = ""
								state.JenkinsReleaseRuns[runIndex]["callbackTokenHash"] = ""
								// 原回调通道已失效。保留运行状态，由新 Worker 通过受认证的 broker 查询接管。
								state.JenkinsReleaseRuns[runIndex]["updatedAt"] = now
							}
						}
					}
					state.RuntimeLogs = append(state.RuntimeLogs, domain.NewRuntimeLog(*task, "worker", "worker-recovery", "warn", "执行租约已过期，保留检查点重新排队恢复", now))
				}
			}
		}
		active := 0
		for _, task := range state.Tasks {
			if task.Status == "executing" || task.Status == "verifying" || task.Status == "preflight_running" {
				active++
			}
		}
		if active >= option.Concurrency {
			return nil
		}
		sort.SliceStable(state.Tasks, func(i, j int) bool { return domain.TaskPrecedes(state.Tasks[i], state.Tasks[j]) })
		for index := range state.Tasks {
			if state.Tasks[index].Status != "queued" {
				continue
			}
			if !domain.TaskResourceScopeMatches(*state, state.Tasks[index]) {
				if state.Tasks[index].Payload == nil {
					state.Tasks[index].Payload = map[string]any{}
				}
				state.Tasks[index].Payload["queueScopeChanged"] = true
			}
			if blocker := taskClaimBlocker(*state, state.Tasks[index]); blocker != "" {
				continue
			}
			state.Tasks[index].Status = "executing"
			state.Tasks[index].Attempt++
			state.Tasks[index].ClaimToken = domain.NewID("claim")
			state.Tasks[index].ClaimOwner = option.Owner
			state.Tasks[index].LeaseExpiresAt = nowTime.Add(15 * time.Minute).Format(time.RFC3339Nano)
			state.Tasks[index].UpdatedAt = now
			state.Tasks[index].LogRef = state.Tasks[index].ID
			for runIndex := range state.OperationRuns {
				run := &state.OperationRuns[runIndex]
				if run.ID == state.Tasks[index].ID && run.Status == "queued" {
					run.Status = "running"
					run.UpdatedAt = now
				}
			}
			taskCopy := state.Tasks[index]
			claimed = &taskCopy
			state.RuntimeLogs = append(state.RuntimeLogs, domain.NewRuntimeLog(taskCopy, "worker", "claim", "info", "Worker 已领取任务并开始执行", now))
			return nil
		}
		return nil
	})
	return claimed, err
}

func (s *Store) HeartbeatTask(ctx context.Context, taskID string) error {
	return s.MutateState(ctx, func(state *domain.State) error {
		for index := range state.Tasks {
			if state.Tasks[index].ID != taskID {
				continue
			}
			if state.Tasks[index].Status != "executing" && state.Tasks[index].Status != "verifying" {
				return errors.New("TASK_LEASE_NOT_ACTIVE")
			}
			state.Tasks[index].UpdatedAt = domain.Now()
			state.Tasks[index].LeaseExpiresAt = time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339Nano)
			return nil
		}
		return errors.New("TASK_NOT_FOUND")
	})
}

func (s *Store) CompleteTask(ctx context.Context, taskID string, status string, message string) error {
	if status != "succeeded" && status != "failed" && status != "rolled_back" && status != "cancelled" {
		return errors.New("TASK_TERMINAL_STATUS_INVALID")
	}
	message = domain.LogSummary(message)
	err := s.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		for index := range state.Tasks {
			if state.Tasks[index].ID != taskID {
				continue
			}
			if state.Tasks[index].Status != "preflight_running" && state.Tasks[index].Status != "executing" && state.Tasks[index].Status != "verifying" {
				return errors.New("TASK_NOT_COMPLETABLE")
			}
			if task := state.Tasks[index]; task.Action == string(domain.TaskActionDeployApplication) {
				for _, plan := range state.ReleasePlans {
					if plan["id"] == task.Payload["releasePlanId"] && plan["operationRunId"] == task.ID {
						plan["status"], plan["summary"], plan["updatedAt"] = status, message, now
						if status == "failed" {
							plan["errorCode"] = message
						}
						break
					}
				}
			}
			state.Tasks[index].Status = status
			state.Tasks[index].UpdatedAt = now
			state.Tasks[index].LogRef = state.Tasks[index].ID
			entry := domain.NewRuntimeLog(state.Tasks[index], "worker", "complete", domain.LogStatusLevel(status), message, now)
			state.RuntimeLogs = append(state.RuntimeLogs, entry)
			state.OperationLogs = append(state.OperationLogs, domain.NewOperationLog(state.Tasks[index], "worker", domain.LogStatusLevel(status), message, now))
			for runIndex := range state.OperationRuns {
				run := &state.OperationRuns[runIndex]
				if run.ID == taskID && (run.Status == "queued" || run.Status == "running") {
					runStatus := status
					if status == "rolled_back" {
						runStatus = "failed"
					}
					run.Status = runStatus
					run.Summary = message
					run.UpdatedAt = now
					if runStatus == "failed" {
						run.ErrorCode = entry.ErrorCode
					}
				}
			}
			return nil
		}
		return errors.New("TASK_NOT_FOUND")
	})
	return err
}
