package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) AuthorizeJenkinsRun(ctx context.Context, runID string, token string) (domain.Object, bool, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(token) == "" {
		return nil, false, nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, run := range state.JenkinsReleaseRuns {
		if stringValue(run["id"]) != runID {
			continue
		}
		hash := stringValue(run["tokenHash"])
		if hash == "" || hash != domain.HashSecret(token) || jenkinsRunTerminal(run) {
			return nil, false, nil
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, stringValue(run["expiresAt"]))
		if parseErr != nil || !expiresAt.After(time.Now().UTC()) {
			return nil, false, nil
		}
		_ = r.Store.MutateState(ctx, func(state *domain.State) error {
			for index := range state.JenkinsReleaseRuns {
				if stringValue(state.JenkinsReleaseRuns[index]["id"]) == runID {
					state.JenkinsReleaseRuns[index]["lastMaterialsAccessedAt"] = domain.Now()
					state.JenkinsReleaseRuns[index]["updatedAt"] = domain.Now()
				}
			}
			return nil
		})
		return run, true, nil
	}
	return nil, false, nil
}

func (r *Runtime) JenkinsGitCredentialMaterial(ctx context.Context, run domain.Object) (domain.Object, error) {
	source := objectMapFromAny(objectMapFromAny(run["materialsSnapshot"])["sourceSnapshot"])
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	token, err := r.readGitSourceToken(ctx, state, source)
	if err != nil {
		return nil, err
	}
	result := domain.Object{"repositoryUrl": source["repositoryUrl"], "authMode": "none"}
	if token != "" {
		result["authMode"], result["username"], result["token"] = "git_token", firstNonEmptyString(stringValue(source["authUsername"]), "git"), token
	}
	return result, nil
}

func (r *Runtime) RecordJenkinsReleaseCallback(ctx context.Context, input domain.Object) (domain.Object, error) {
	runID := stringValue(input["runId"])
	releasePlanID := stringValue(input["releasePlanId"])
	applicationID := stringValue(input["applicationId"])
	status := stringValue(input["status"])
	jenkinsRun := objectMapFromAny(input["jenkinsRun"])
	if runID == "" || releasePlanID == "" || applicationID == "" || !validJenkinsCallbackStatus(status) || stringValue(jenkinsRun["runId"]) == "" || stringValue(jenkinsRun["status"]) != status {
		return nil, errors.New("JENKINS_CALLBACK_INVALID")
	}
	callbackLogs := objectSlice(input["runtimeLogs"])
	if len(callbackLogs) > 200 {
		return nil, errors.New("JENKINS_CALLBACK_INVALID")
	}
	// 外来摘要或阶段消息只在可核验遮蔽材料时留存。缺少历史材料时保留发布结果。
	secrets, redactionErr := r.applicationLogSecrets(ctx, applicationID, "")
	safeText := func(text string) string {
		if text == "" {
			return ""
		}
		if redactionErr != nil {
			return "诊断正文因脱敏材料不可用已隐藏"
		}
		return domain.RedactLogText(text, secrets)
	}
	summary := domain.LogSummary(safeText(stringValue(input["summary"])))
	var releasePlan domain.Object
	resample := false
	err := r.Store.MutateState(ctx, func(state *domain.State) error {
		now := domain.Now()
		var operationRunID string
		runFound := false
		for index := range state.JenkinsReleaseRuns {
			run := state.JenkinsReleaseRuns[index]
			if stringValue(run["id"]) != runID {
				continue
			}
			if stringValue(run["releasePlanId"]) != releasePlanID || stringValue(run["applicationId"]) != applicationID {
				return errors.New("JENKINS_CALLBACK_RUN_MISMATCH")
			}
			if stringValue(run["status"]) != "triggered" {
				return errors.New("JENKINS_CALLBACK_RUN_STATE_INVALID")
			}
			operationRunID = firstNonEmptyString(stringValue(run["operationRunId"]), stringValue(run["taskId"]))
			state.JenkinsReleaseRuns[index]["status"] = status
			state.JenkinsReleaseRuns[index]["tokenHash"] = ""
			state.JenkinsReleaseRuns[index]["callbackTokenHash"] = ""
			for _, key := range []string{"commitSha", "imageDigest"} {
				if value := stringValue(input[key]); value != "" {
					state.JenkinsReleaseRuns[index][key] = domain.LogSummary(value)
				}
			}
			state.JenkinsReleaseRuns[index]["summary"] = summary
			state.JenkinsReleaseRuns[index]["lastCallbackAt"] = now
			state.JenkinsReleaseRuns[index]["updatedAt"] = now
			state.JenkinsReleaseRuns[index]["jenkinsRun"] = cloneObject(jenkinsRun)
			if status == "failed" {
				state.JenkinsReleaseRuns[index]["lastErrorSummary"] = summary
			}
			runFound = true
			break
		}
		if !runFound {
			return errors.New("JENKINS_RUN_NOT_FOUND")
		}
		for index := range state.ReleasePlans {
			if stringValue(state.ReleasePlans[index]["id"]) != releasePlanID {
				continue
			}
			next := cloneObject(state.ReleasePlans[index])
			if sha := stringValue(input["commitSha"]); sha != "" {
				if err := checkJenkinsCommit(next, sha); err != nil {
					return err
				}
			}
			if stringValue(next["jenkinsRunId"]) != runID || stringValue(next["status"]) != "jenkins_triggered" {
				return errors.New("JENKINS_CALLBACK_RUN_STATE_INVALID")
			}
			if stringValue(next["applicationId"]) != applicationID {
				return errors.New("JENKINS_CALLBACK_RUN_MISMATCH")
			}
			if status == "succeeded" {
				if raw := input["artifacts"]; raw != nil || registrySyncConfig(objectMapFromAny(next["buildSnapshot"]))["enabled"] == true {
					facts, err := verifiedGitArtifactFacts(raw, next, runID)
					if err != nil {
						return err
					}
					next["artifacts"] = facts
				}
				outputs := objectMapFromAny(input["outputValues"])
				keys := objectMapFromAny(next["buildOutputKeys"])
				if len(outputs) != len(keys) {
					return errors.New("JENKINS_CALLBACK_OUTPUT_INVALID")
				}
				for key, value := range outputs {
					s, ok := value.(string)
					if !ok || redactionErr != nil || domain.RedactLogText(s, secrets) != s || len(s) > 4096 || !applicationVariableName.MatchString(key) || stringValue(keys[key]) == "" {
						return errors.New("JENKINS_CALLBACK_OUTPUT_INVALID")
					}
				}
				app := findObject(state.Applications, applicationID)
				if app != nil {
					published := domain.Object{}
					for key, value := range outputs {
						published[stringValue(keys[key])] = value
					}
					cfg := findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", applicationID)
					for _, out := range objectSlice(cfg["outputVariables"]) {
						if out["valueSource"] == "service_host" || out["valueSource"] == "service_port" {
							resample = true
							break
						}
					}
					app["publishedBuildOutputs"] = published
					recordAppliedApplicationInputs(state, applicationID, objectSlice(next["configSnapshot"]), "build", "runtime")
				}
			}
			next["status"] = status
			for _, key := range []string{"commitSha", "imageDigest"} {
				if value := stringValue(input[key]); value != "" {
					next[key] = value
				}
			}
			next["summary"] = summary
			next["jenkinsRun"] = cloneObject(jenkinsRun)
			next["completedAt"] = now
			if status == "failed" {
				next["lastErrorCode"] = "JENKINS_RELEASE_FAILED"
			}
			next["updatedAt"] = now
			state.ReleasePlans[index] = next
			releasePlan = next

			for _, callbackLog := range callbackLogs {
				entry, err := jenkinsCallbackRuntimeLog(callbackLog, operationRunID, applicationID, safeText)
				if err != nil {
					return err
				}
				state.RuntimeLogs = append(state.RuntimeLogs, entry)
			}
			for runIndex := range state.OperationRuns {
				operationRun := &state.OperationRuns[runIndex]
				if operationRun.ID != operationRunID || operationRun.Status == "succeeded" || operationRun.Status == "failed" || operationRun.Status == "cancelled" {
					continue
				}
				operationRun.Status = status
				operationRun.Summary = summary
				operationRun.UpdatedAt = now
				if status == "failed" {
					operationRun.ErrorCode = "JENKINS_RELEASE_FAILED"
				}
			}
			return nil
		}
		return errors.New("RELEASE_PLAN_NOT_FOUND")
	})
	if err == nil && resample {
		// 回调已处理，端点验证单独记录实际运行态，不能使已消费的回调再执行。
		_, _ = r.ApplicationRuntime(ctx, applicationID)
	}
	return releasePlan, err
}

func validJenkinsCallbackStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func jenkinsCallbackRuntimeLog(input domain.Object, operationRunID string, applicationID string, safeText func(string) string) (domain.RuntimeLog, error) {
	level := stringValue(input["level"])
	if !domain.IsLogLevel(level) {
		return domain.RuntimeLog{}, errors.New("JENKINS_CALLBACK_LOG_INVALID")
	}
	source := strings.TrimSpace(stringValue(input["source"]))
	phase := strings.TrimSpace(stringValue(input["phase"]))
	message := strings.TrimSpace(stringValue(input["message"]))
	timestamp := strings.TrimSpace(stringValue(input["timestamp"]))
	if !domain.IsLogIdentifier(source) || len(source) > 80 || !domain.IsLogIdentifier(phase) || message == "" {
		return domain.RuntimeLog{}, errors.New("JENKINS_CALLBACK_LOG_INVALID")
	}
	if _, err := time.Parse(time.RFC3339Nano, timestamp); err != nil {
		return domain.RuntimeLog{}, errors.New("JENKINS_CALLBACK_LOG_INVALID")
	}
	if operationRunID == "" {
		return domain.RuntimeLog{}, errors.New("JENKINS_CALLBACK_RUN_STATE_INVALID")
	}
	task := domain.Task{ID: operationRunID, TargetKind: "application", TargetID: applicationID}
	if safeText(source) != source || safeText(phase) != phase {
		source, phase = "jenkins", "stage"
	}
	return domain.NewRuntimeLog(task, source, phase, level, safeText(message), timestamp), nil
}

func (r *Runtime) ProjectApplications(ctx context.Context, projectID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	items := filterObjectsBy(state.Applications, "projectId", projectID)
	for index, app := range items {
		items[index] = applicationWithInputStatus(state, app)
	}
	return map[string]any{"items": items}, nil
}

func (r *Runtime) ApplicationRoutes(ctx context.Context, applicationID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"items": filterObjectsBy(state.Routes, "applicationId", applicationID)}, nil
}

func (r *Runtime) OperationLog(ctx context.Context, id string) (domain.OperationLog, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return domain.OperationLog{}, err
	}
	for _, item := range state.OperationLogs {
		if item.ID == id {
			return item, nil
		}
	}
	return domain.OperationLog{}, errors.New("AUDIT_LOG_NOT_FOUND")
}
