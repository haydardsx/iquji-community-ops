package application

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) ApplicationBuilds(ctx context.Context, applicationID string) (map[string]any, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	if applicationID == "" || findObject(state.Applications, applicationID) == nil {
		return nil, errors.New("APPLICATION_NOT_FOUND")
	}
	items := []domain.Object{}
	for _, plan := range state.ReleasePlans {
		if stringValue(plan["applicationId"]) != applicationID {
			continue
		}
		run := findObject(state.JenkinsReleaseRuns, stringValue(plan["jenkinsRunId"]))
		item := publicApplicationBuild(plan, run)
		if run == nil {
			for _, operation := range state.OperationRuns {
				if operation.ID == stringValue(plan["operationRunId"]) {
					item["status"] = operation.Status
					item["summary"] = operation.Summary
					item["updatedAt"] = operation.UpdatedAt
					item["errorCode"] = operation.ErrorCode
				}
			}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return stringValue(items[i]["createdAt"]) > stringValue(items[j]["createdAt"]) })
	total := len(items)
	if total > 20 {
		items = items[:20]
	}
	return map[string]any{"items": items, "page": 1, "pageSize": 20, "total": total}, nil
}
func publicApplicationBuild(plan, run domain.Object) domain.Object {
	item := domain.Object{"id": plan["id"], "applicationId": plan["applicationId"], "operationRunId": firstNonEmptyString(stringValue(plan["operationRunId"]), ""), "status": plan["status"], "createdAt": plan["createdAt"], "updatedAt": plan["updatedAt"]}
	snapshot := objectMapFromAny(plan["buildSnapshot"])
	source := objectMapFromAny(snapshot["sourceSnapshot"])
	if facts := publicPullMirrorFacts(plan, snapshot); len(facts) > 0 {
		item["pullMirrors"] = facts
	}
	if provider := firstNonEmptyString(stringValue(source["provider"]), stringValue(source["sourceType"])); provider != "" {
		item["provider"] = provider
	}
	for _, source := range []domain.Object{plan, run} {
		for _, key := range []string{"summary", "errorCode", "commitSha", "imageDigest"} {
			if value := stringValue(source[key]); value != "" {
				item[key] = domain.LogSummary(value)
			}
		}
	}
	if facts := objectSlice(plan["artifacts"]); len(facts) > 0 {
		item["artifacts"] = facts
	}
	if run != nil {
		item["id"] = run["id"]
		item["status"] = run["status"]
		item["updatedAt"] = run["updatedAt"]
		base := "/api/logs/builds/" + stringValue(run["id"])
		item["runUrl"] = base + "/summary"
		item["consoleUrl"] = base + "/console"
		item["stageUrl"] = base + "/stage"
	}
	return item
}
func (r *Runtime) JenkinsBuildOutput(ctx context.Context, runID, kind string) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	run := findObject(state.JenkinsReleaseRuns, runID)
	if run == nil {
		return nil, errors.New("JENKINS_RUN_NOT_FOUND")
	}
	plan := findObject(state.ReleasePlans, stringValue(run["releasePlanId"]))
	if kind == "summary" {
		return domain.Object{"content": publicApplicationBuild(plan, run), "contentType": "application/json"}, nil
	}
	if kind != "console" && kind != "stage" {
		return nil, errors.New("JENKINS_BUILD_OUTPUT_INVALID")
	}
	server := findObject(state.Servers, releasePlanBuildServerID(plan))
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return nil, errors.New("JENKINS_BUILD_SSH_REQUIRED")
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client, err := sshClient(callCtx, server, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	action := kind
	if kind == "stage" {
		action = "stages"
	}
	output, err := remoteOutput(callCtx, client, jenkinsBrokerCommand(r.Config.JenkinsContainer, action, runID))
	if err != nil {
		return nil, errors.New("JENKINS_BUILD_OUTPUT_UNAVAILABLE")
	}
	result, err := decodeJenkinsBrokerObject(output)
	if err != nil {
		return nil, err
	}
	if text, ok := result["content"].(string); ok {
		secrets, err := r.applicationLogSecrets(callCtx, stringValue(run["applicationId"]), "")
		if err != nil {
			return nil, err
		}
		text = domain.RedactLogText(text, secrets)
		const maxBytes = 256 * 1024
		if len(text) > maxBytes {
			start := len(text) - maxBytes
			for !utf8.RuneStart(text[start]) {
				start++
			}
			text = text[start:]
		}
		result["content"] = text
	}
	return result, nil
}
func (r *Runtime) AuthorizeJenkinsCallback(ctx context.Context, runID, token string) (domain.Object, bool, error) {
	if strings.TrimSpace(token) == "" {
		return nil, false, nil
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, false, err
	}
	run := findObject(state.JenkinsReleaseRuns, runID)
	if run == nil || jenkinsRunTerminal(run) || stringValue(run["callbackTokenHash"]) != domain.HashSecret(token) {
		return nil, false, nil
	}
	expiry, err := time.Parse(time.RFC3339Nano, stringValue(run["expiresAt"]))
	if err != nil || !expiry.After(time.Now()) {
		return nil, false, nil
	}
	return run, true, nil
}

// 只有 Worker 回收待清理运行；成功确认远端终态后才解除下一次发布的阻塞。
func (r *Runtime) reconcileJenkinsCleanup(ctx context.Context) (WorkerResult, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return WorkerResult{}, err
	}
	for _, run := range state.JenkinsReleaseRuns {
		if stringValue(run["status"]) != "cleanup_required" {
			continue
		}
		plan := findObject(state.ReleasePlans, stringValue(run["releasePlanId"]))
		server := findObject(state.Servers, releasePlanBuildServerID(plan))
		key, ok := r.serverSSHSecretMaterial(ctx, state, server)
		if !ok {
			return WorkerResult{}, errors.New("JENKINS_BUILD_SSH_REQUIRED")
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, time.Minute)
		tunnel, err := r.JenkinsReleaseExecutor.OpenCallbackTunnel(cleanupCtx, JenkinsCallbackTunnelInput{Server: server, SSHKey: key, JenkinsContainer: r.Config.JenkinsContainer, JenkinsPort: r.Config.JenkinsPort, LocalAPIAddress: localControlPlaneAddress(r.Config.Host, r.Config.Port)})
		if err == nil {
			err = tunnel.Cancel(cleanupCtx, domain.Object{"runId": run["id"]})
			_ = tunnel.Close()
		}
		cancel()
		if err != nil {
			return WorkerResult{}, errors.New("JENKINS_CLEANUP_REQUIRED")
		}
		if err := r.cancelJenkinsReleaseRun(ctx, stringValue(run["id"]), "JENKINS_CLEANUP_COMPLETED"); err != nil {
			return WorkerResult{}, err
		}
		return WorkerResult{Processed: 1}, nil
	}
	return WorkerResult{}, nil
}

func decodeJenkinsBrokerObject(output string) (domain.Object, error) {
	var result domain.Object
	if json.Unmarshal([]byte(output), &result) != nil {
		return nil, errors.New("JENKINS_BROKER_RESPONSE_INVALID")
	}
	return result, nil
}
