package application

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) markJenkinsReleaseTriggered(ctx context.Context, runID string, trigger domain.Object) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		for index := range state.JenkinsReleaseRuns {
			if stringValue(state.JenkinsReleaseRuns[index]["id"]) != runID {
				continue
			}
			if jenkinsRunTerminal(state.JenkinsReleaseRuns[index]) || state.JenkinsReleaseRuns[index]["status"] == "triggered" {
				return nil
			}
			if stringValue(state.JenkinsReleaseRuns[index]["status"]) != "pending" {
				return errors.New("JENKINS_RUN_STATE_INVALID")
			}
			state.JenkinsReleaseRuns[index]["status"] = "triggered"
			jenkinsRun := domain.Object{"runId": runID, "status": "queued"}
			if queueURL := stringValue(trigger["queueUrl"]); queueURL != "" {
				if strings.HasPrefix(queueURL, "/") {
					queueURL = "http://127.0.0.1:" + strconv.Itoa(r.Config.JenkinsPort) + queueURL
				}
				jenkinsRun["queueUrl"] = queueURL
			}
			state.JenkinsReleaseRuns[index]["jenkinsRun"] = jenkinsRun
			state.JenkinsReleaseRuns[index]["updatedAt"] = now
			for planIndex := range state.ReleasePlans {
				if stringValue(state.ReleasePlans[planIndex]["id"]) != stringValue(state.JenkinsReleaseRuns[index]["releasePlanId"]) {
					continue
				}
				if stringValue(state.ReleasePlans[planIndex]["status"]) != "executing" {
					return errors.New("RELEASE_PLAN_STATE_INVALID")
				}
				state.ReleasePlans[planIndex]["status"] = "jenkins_triggered"
				state.ReleasePlans[planIndex]["updatedAt"] = now
			}
			return nil
		}
		return errors.New("JENKINS_RUN_NOT_FOUND")
	})
}

func (r *Runtime) jenkinsReleaseCancelRequested(ctx context.Context, operationRunID string) (bool, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return false, err
	}
	for _, task := range state.Tasks {
		if task.ID == operationRunID {
			return task.CancelRequested, nil
		}
	}
	return false, errors.New("TASK_NOT_FOUND")
}

func (r *Runtime) failJenkinsReleaseRun(ctx context.Context, runID string, code string) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		releasePlanID := ""
		for index := range state.JenkinsReleaseRuns {
			if stringValue(state.JenkinsReleaseRuns[index]["id"]) != runID {
				continue
			}
			if jenkinsRunTerminal(state.JenkinsReleaseRuns[index]) && code != "JENKINS_CLEANUP_REQUIRED" {
				return nil
			}
			releasePlanID = stringValue(state.JenkinsReleaseRuns[index]["releasePlanId"])
			state.JenkinsReleaseRuns[index]["status"] = "failed"
			if code == "JENKINS_CLEANUP_REQUIRED" {
				state.JenkinsReleaseRuns[index]["status"] = "cleanup_required"
			}
			state.JenkinsReleaseRuns[index]["tokenHash"] = ""
			state.JenkinsReleaseRuns[index]["callbackTokenHash"] = ""
			state.JenkinsReleaseRuns[index]["lastErrorSummary"] = stableJenkinsReleaseCode(code)
			state.JenkinsReleaseRuns[index]["updatedAt"] = now
			break
		}
		if releasePlanID == "" {
			return errors.New("JENKINS_RUN_NOT_FOUND")
		}
		for index := range state.ReleasePlans {
			if stringValue(state.ReleasePlans[index]["id"]) == releasePlanID {
				state.ReleasePlans[index]["status"] = "failed"
				state.ReleasePlans[index]["lastErrorCode"] = stableJenkinsReleaseCode(code)
				state.ReleasePlans[index]["updatedAt"] = now
				break
			}
		}
		return nil
	})
}

func (r *Runtime) cancelJenkinsReleaseRun(ctx context.Context, runID string, summary string) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		var operationRunID string
		var releasePlanID string
		for index := range state.JenkinsReleaseRuns {
			if stringValue(state.JenkinsReleaseRuns[index]["id"]) != runID {
				continue
			}
			if jenkinsRunTerminal(state.JenkinsReleaseRuns[index]) {
				return nil
			}
			releasePlanID = stringValue(state.JenkinsReleaseRuns[index]["releasePlanId"])
			operationRunID = firstNonEmptyString(stringValue(state.JenkinsReleaseRuns[index]["operationRunId"]), stringValue(state.JenkinsReleaseRuns[index]["taskId"]))
			state.JenkinsReleaseRuns[index]["status"] = "cancelled"
			state.JenkinsReleaseRuns[index]["tokenHash"] = ""
			state.JenkinsReleaseRuns[index]["callbackTokenHash"] = ""
			state.JenkinsReleaseRuns[index]["lastCallbackAt"] = now
			state.JenkinsReleaseRuns[index]["lastErrorSummary"] = domain.LogSummary(summary)
			state.JenkinsReleaseRuns[index]["updatedAt"] = now
			break
		}
		if releasePlanID == "" {
			return errors.New("JENKINS_RUN_NOT_FOUND")
		}
		for index := range state.ReleasePlans {
			if stringValue(state.ReleasePlans[index]["id"]) == releasePlanID && !releasePlanTerminal(state.ReleasePlans[index]) {
				state.ReleasePlans[index]["status"] = "cancelled"
				state.ReleasePlans[index]["summary"] = domain.LogSummary(summary)
				state.ReleasePlans[index]["completedAt"] = now
				state.ReleasePlans[index]["updatedAt"] = now
			}
		}
		for index := range state.OperationRuns {
			if state.OperationRuns[index].ID == operationRunID && !domain.IsOperationRunStatusTerminal(state.OperationRuns[index].Status) {
				state.OperationRuns[index].Status = "cancelled"
				state.OperationRuns[index].Summary = domain.LogSummary(summary)
				state.OperationRuns[index].UpdatedAt = now
			}
		}
		return nil
	})
}

func jenkinsRunTerminal(run domain.Object) bool {
	switch stringValue(run["status"]) {
	case "succeeded", "failed", "expired", "cancelled":
		return true
	default:
		return false
	}
}

func stableJenkinsReleaseError(err error) string {
	if err == nil {
		return "JENKINS_RELEASE_FAILED"
	}
	return stableJenkinsReleaseCode(err.Error())
}

func stableJenkinsReleaseCode(value string) string {
	for _, code := range []string{
		"JENKINS_CALLBACK_TUNNEL_UNAVAILABLE",
		"JENKINS_CALLBACK_TIMEOUT",
		"JENKINS_HANDOFF_FAILED",
		"JENKINS_RELEASE_CANCELLED",
		"JENKINS_RELEASE_EXPIRED",
		"JENKINS_CLEANUP_REQUIRED",
		"JENKINS_RELEASE_INTERRUPTED",
		"JENKINS_RELEASE_FAILED",
		"JENKINS_RUN_STATE_UPDATE_FAILED",
		"JENKINS_RECOVERY_RUN_NOT_FOUND",
		"JENKINS_COMMIT_MISMATCH",
	} {
		if strings.Contains(value, code) {
			return code
		}
	}
	return "JENKINS_RELEASE_FAILED"
}
