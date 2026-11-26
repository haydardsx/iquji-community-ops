package application

import (
	"context"
	"errors"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func (r *Runtime) executeManagedJenkinsRelease(ctx context.Context, task domain.Task) (message string, executionErr error) {
	ctx, stop := context.WithTimeout(ctx, jenkinsReleaseExecutionTimeout)
	defer stop()
	planID, err := r.resolveJenkinsReleasePlanForTask(ctx, task)
	if err != nil {
		return "无法创建发布快照", err
	}
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return "", err
	}
	if plan := findObject(state.ReleasePlans, planID); plan != nil {
		if id := stringValue(plan["jenkinsRunId"]); id != "" {
			run := findObject(state.JenkinsReleaseRuns, id)
			if run == nil || run["releasePlanId"] != planID || firstNonEmptyString(stringValue(run["operationRunId"]), stringValue(run["taskId"])) != task.ID {
				return "", errors.New("JENKINS_RUN_STATE_INVALID")
			}
			if jenkinsRunTerminal(run) {
				return jenkinsResult(run)
			}
			tunnel, err := r.openReleaseTunnel(ctx, state, plan)
			if err != nil {
				_ = r.failJenkinsReleaseRun(ctx, id, "JENKINS_CLEANUP_REQUIRED")
				return "", err
			}
			defer func() { _ = tunnel.Close() }()
			return r.monitorJenkinsRun(ctx, task, id, tunnel, true)
		}
	}
	report, err := r.RefreshReleasePlanRuntimeReadiness(ctx, planID)
	if err != nil {
		return "发布节点预检失败", err
	}
	if !report.Decision.Allowed {
		return "发布节点未通过实时核验", errors.New("JENKINS_RUNTIME_NOT_READY")
	}
	state, err = r.Store.LoadState(ctx)
	if err != nil {
		return "无法读取发布快照", err
	}
	if len(task.QueueResources) > 0 && !domain.TaskResourceScopeMatches(state, task) {
		return "发布资源范围已变化，请重新提交确认", errors.New("TASK_RESOURCE_SCOPE_CHANGED")
	}
	plan := findObject(state.ReleasePlans, planID)
	if plan == nil {
		return "发布快照不存在", errors.New("RELEASE_PLAN_NOT_FOUND")
	}
	if !runtimeReadinessAllPassed(deploymentRuntimeReadiness(state, releasePlanRuntimeConfig(plan))) {
		return "发布节点未就绪", errors.New("JENKINS_RUNTIME_NOT_READY")
	}
	materials, err := r.trustedJenkinsMaterials(ctx, state, plan)
	if err != nil {
		return "发布材料不可用", err
	}
	tunnel, err := r.openReleaseTunnel(ctx, state, plan)
	if err != nil {
		return "构建回调通道不可用", err
	}
	defer func() { _ = tunnel.Close() }()
	runID, token := domain.NewID("jrun"), jenkinsRandomSecret()
	if err := tunnel.Validate(ctx, runID, token); err != nil {
		return "受管 Jenkins 安全预检失败", err
	}
	run, err := r.createJenkinsReleaseRun(ctx, task, plan, state, runID, token, tunnel.ControlPlaneBaseURL())
	if err != nil {
		return "无法登记发布运行", err
	}
	finished := false
	defer func() {
		if !finished {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			if cleanupErr := tunnel.Cancel(cleanupCtx, domain.Object{"runId": runID}); cleanupErr != nil {
				_ = r.failJenkinsReleaseRun(context.WithoutCancel(ctx), runID, "JENKINS_CLEANUP_REQUIRED")
				message, executionErr = "发布尚未完成远端清理", errors.New("JENKINS_CLEANUP_REQUIRED")
			}
		}
	}()
	trigger, err := tunnel.Trigger(ctx, JenkinsReleaseTriggerInput{RunID: runID, ReleasePlanID: planID, ApplicationID: stringValue(plan["applicationId"]), RunToken: token, ControlPlaneBaseURL: tunnel.ControlPlaneBaseURL(), Materials: materials, ExpiresAt: stringValue(run["expiresAt"])})
	if err != nil {
		_ = r.failJenkinsReleaseRun(context.WithoutCancel(ctx), runID, stableJenkinsReleaseError(err))
		return "Jenkins 启动发布失败", err
	}
	if err := r.markJenkinsReleaseTriggered(ctx, runID, trigger); err != nil {
		return "无法记录 Jenkins 运行", err
	}
	if err := r.recordJenkinsRunFacts(ctx, runID, trigger); err != nil {
		return "无法记录构建源码版本", err
	}
	started, startErr := tunnel.Start(ctx, runID)
	if startErr != nil {
		_ = r.failJenkinsReleaseRun(context.WithoutCancel(ctx), runID, "JENKINS_HANDOFF_FAILED")
		return "Jenkins 启动发布失败", startErr
	}
	if err := r.recordJenkinsRunFacts(ctx, runID, started); err != nil {
		return "无法记录 Jenkins 队列", err
	}
	finished = true
	return r.monitorJenkinsRun(ctx, task, runID, tunnel, false)
}

func (r *Runtime) openReleaseTunnel(ctx context.Context, state domain.State, plan domain.Object) (JenkinsCallbackTunnel, error) {
	server := findObject(state.Servers, releasePlanBuildServerID(plan))
	key, ok := r.serverSSHSecretMaterial(ctx, state, server)
	if !ok {
		return nil, errors.New("JENKINS_BUILD_SSH_REQUIRED")
	}
	return r.JenkinsReleaseExecutor.OpenCallbackTunnel(ctx, JenkinsCallbackTunnelInput{Server: server, SSHKey: key, JenkinsContainer: r.Config.JenkinsContainer, JenkinsPort: r.Config.JenkinsPort, LocalAPIAddress: localControlPlaneAddress(r.Config.Host, r.Config.Port)})
}

func jenkinsResult(run domain.Object) (string, error) {
	switch stringValue(run["status"]) {
	case "succeeded":
		return "源码测试、镜像构建和指定节点部署已完成", nil
	case "cancelled":
		return "", errors.New("JENKINS_RELEASE_CANCELLED")
	default:
		summary := firstNonEmptyString(stringValue(run["lastErrorSummary"]), stringValue(run["summary"]))
		return "", errors.New(stableErrorCode(errors.New(summary), "JENKINS_RELEASE_FAILED"))
	}
}

// 接管只查询已经登记的运行。如果中断发生在入队前，只对已核验的 prepared 运行提交一次原作业。
func (r *Runtime) monitorJenkinsRun(ctx context.Context, task domain.Task, runID string, tunnel JenkinsCallbackTunnel, recovering bool) (string, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		remote, err := tunnel.Status(ctx, runID)
		if err == nil {
			if remote["runId"] != runID {
				err = errors.New("JENKINS_RUN_STATE_INVALID")
			} else if recovering && (remote["status"] == "prepared" || remote["status"] == "succeeded") && stringValue(remote["commitSha"]) == "" {
				err = errors.New("JENKINS_COMMIT_MISMATCH")
			} else {
				err = r.recordJenkinsRunFacts(ctx, runID, remote)
			}
			if err != nil {
				_ = r.failJenkinsReleaseRun(ctx, runID, "JENKINS_CLEANUP_REQUIRED")
				return "", err
			}
			if recovering && remote["status"] == "prepared" {
				if err := r.markJenkinsReleaseTriggered(ctx, runID, remote); err != nil {
					return "", err
				}
				// 服务端仅在 prepared 状态接受交接。返回丢失时继续读取同一运行。
				_, _ = tunnel.Start(ctx, runID)
			} else if jenkinsRunTerminal(remote) {
				if recovering {
					if err := r.markJenkinsReleaseTriggered(ctx, runID, remote); err != nil {
						return "", err
					}
				}
				if _, err := r.finalizeJenkinsFromBroker(ctx, runID, remote); err != nil {
					_ = r.failJenkinsReleaseRun(ctx, runID, "JENKINS_CLEANUP_REQUIRED")
					return "", err
				}
				return jenkinsResult(remote)
			}
		} else if recovering && err.Error() == "JENKINS_RUN_NOT_FOUND" {
			_ = r.failJenkinsReleaseRun(ctx, runID, "JENKINS_RECOVERY_RUN_NOT_FOUND")
			return "", errors.New("JENKINS_RECOVERY_RUN_NOT_FOUND")
		}
		cancelled, cancelErr := r.jenkinsReleaseCancelRequested(ctx, task.ID)
		if cancelled || cancelErr != nil || ctx.Err() != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			defer cancel()
			if err := tunnel.Cancel(cleanupCtx, domain.Object{"runId": runID}); err != nil {
				_ = r.failJenkinsReleaseRun(cleanupCtx, runID, "JENKINS_CLEANUP_REQUIRED")
				return "", err
			}
			if cancelled {
				_ = r.cancelJenkinsReleaseRun(cleanupCtx, runID, "JENKINS_RELEASE_CANCELLED")
				return "", errors.New("JENKINS_RELEASE_CANCELLED")
			}
			code := "JENKINS_RELEASE_INTERRUPTED"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code = "JENKINS_RELEASE_EXPIRED"
			}
			_ = r.failJenkinsReleaseRun(cleanupCtx, runID, code)
			return "", errors.New(code)
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		case err := <-tunnel.Errors():
			if err != nil {
				_ = r.failJenkinsReleaseRun(ctx, runID, "JENKINS_CLEANUP_REQUIRED")
				return "", err
			}
		}
	}
}
func (r *Runtime) recordJenkinsRunFacts(ctx context.Context, runID string, facts domain.Object) error {
	return r.Store.MutateState(ctx, func(state *domain.State) error {
		run := findObject(state.JenkinsReleaseRuns, runID)
		if run == nil {
			return errors.New("JENKINS_RUN_NOT_FOUND")
		}
		if sha := stringValue(facts["commitSha"]); sha != "" {
			plan := findObject(state.ReleasePlans, stringValue(run["releasePlanId"]))
			if err := checkJenkinsCommit(plan, sha); err != nil {
				return err
			}
		}
		for _, key := range []string{"commitSha", "imageDigest", "jobPath", "buildNumber", "stage", "queueUrl"} {
			if value, exists := facts[key]; exists && (key != "commitSha" || stringValue(value) != "") {
				run[key] = value
			}
		}
		run["updatedAt"] = domain.Now()
		if plan := findObject(state.ReleasePlans, stringValue(run["releasePlanId"])); plan != nil {
			if value := stringValue(facts["commitSha"]); value != "" {
				plan["commitSha"] = value
				plan["resolvedRef"] = value
				source := objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])
				source["resolvedRef"] = value
				source["commitSha"] = value
			}
		}
		return nil
	})
}
func (r *Runtime) finalizeJenkinsFromBroker(ctx context.Context, runID string, facts domain.Object) (domain.Object, error) {
	state, err := r.Store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	run := findObject(state.JenkinsReleaseRuns, runID)
	if run == nil {
		return nil, errors.New("JENKINS_RUN_NOT_FOUND")
	}
	if jenkinsRunTerminal(run) {
		return findObject(state.ReleasePlans, stringValue(run["releasePlanId"])), nil
	}
	status := stringValue(facts["status"])
	return r.RecordJenkinsReleaseCallback(ctx, domain.Object{"runId": runID, "releasePlanId": run["releasePlanId"], "applicationId": run["applicationId"], "status": status, "outputValues": facts["outputValues"], "artifacts": facts["artifacts"], "summary": facts["summary"], "commitSha": facts["commitSha"], "imageDigest": facts["imageDigest"], "jenkinsRun": domain.Object{"runId": runID, "status": status, "runNumber": facts["buildNumber"]}})
}
