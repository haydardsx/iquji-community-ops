package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

type recordingJenkinsExecutor struct{ tunnel *recordingJenkinsTunnel }

func (e *recordingJenkinsExecutor) OpenCallbackTunnel(context.Context, JenkinsCallbackTunnelInput) (JenkinsCallbackTunnel, error) {
	return e.tunnel, nil
}

type recordingJenkinsTunnel struct {
	runtime   *Runtime
	input     JenkinsReleaseTriggerInput
	cancelled int
	startErr  error
	cancelErr error
	started   bool
	events    []string
}

func (t *recordingJenkinsTunnel) ControlPlaneBaseURL() string                    { return "http://127.0.0.1:49152" }
func (t *recordingJenkinsTunnel) Validate(context.Context, string, string) error { return nil }
func (t *recordingJenkinsTunnel) Errors() <-chan error                           { return make(chan error) }
func (t *recordingJenkinsTunnel) Close() error                                   { return nil }
func (t *recordingJenkinsTunnel) Trigger(_ context.Context, input JenkinsReleaseTriggerInput) (domain.Object, error) {
	t.events = append(t.events, "trigger")
	t.input = input
	return domain.Object{"runId": input.RunID, "status": "prepared", "commitSha": strings.Repeat("a", 40)}, nil
}
func (t *recordingJenkinsTunnel) Start(ctx context.Context, id string) (domain.Object, error) {
	state, _ := t.runtime.Store.LoadState(ctx)
	plan := findObject(state.ReleasePlans, t.input.ReleasePlanID)
	if stringValue(plan["commitSha"]) != strings.Repeat("a", 40) || stringValue(objectMapFromAny(objectMapFromAny(plan["buildSnapshot"])["sourceSnapshot"])["commitSha"]) != strings.Repeat("a", 40) {
		return nil, errors.New("COMMIT_NOT_FROZEN_BEFORE_EXECUTION")
	}
	t.started = true
	return domain.Object{"runId": id, "jobPath": "/job/iquops/job/" + id, "queueUrl": "http://127.0.0.1:58080/queue/item/1/"}, t.startErr
}
func (t *recordingJenkinsTunnel) Cancel(context.Context, domain.Object) error {
	t.events = append(t.events, "cancel")
	t.cancelled++
	return t.cancelErr
}
func (t *recordingJenkinsTunnel) Status(_ context.Context, id string) (domain.Object, error) {
	return domain.Object{"runId": id, "status": "succeeded", "commitSha": strings.Repeat("a", 40), "imageDigest": "sha256:" + strings.Repeat("b", 64), "summary": "发布完成", "buildNumber": 1}, nil
}

func seededJenkinsExecution(t *testing.T) (*Runtime, domain.Task, *recordingJenkinsTunnel) {
	t.Helper()
	ctx := context.Background()
	runtime, store := newJenkinsReleaseTestRuntime(t, ctx)
	metrics := domain.Object{"ssh": true, "docker": true, "jenkins": true, "jenkinsBundleVersion": "v12-jenkins-2.568.1", "swarm": true, "swarmRole": "manager", "swarmVerificationStatus": "verified", "swarmClusterId": "cluster_demo", "swarmNodeId": "node_demo", "traefik": true}
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{probeResult: domain.Object{"metrics": metrics, "checks": []domain.Object{}}}
	if err := store.UpsertSecretMaterial(ctx, "ssh_material", validTestSSHPrivateKey); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSecretMaterial(ctx, "db_material", "private-database-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "app_demo", "name": "DEMO", "projectId": "project_demo"}}
		state.Servers = []domain.Object{{"id": "server_demo", "publicHost": "192.0.2.10", "secretAssetIds": []string{"ssh_asset"}, "health": domain.Object{"metrics": metrics}}}
		state.SecretAssets = []domain.Object{{"id": "ssh_asset", "assetType": "ssh_key", "purpose": "deployment_server", "secretRef": "ssh_material"}, {"id": "asset_db", "assetType": "application_secret", "purpose": "application_variables", "secretRef": "db_material"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_demo", "buildServerId": "server_demo", "deploymentManagerServerId": "server_demo", "sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.com/demo.git", "ref": "main", "refType": "branch", "jenkinsfilePath": "Jenkinsfile", "sourcePath": "."}, "swarmRuntime": domain.Object{"containerPort": 3000, "services": []domain.Object{{"slotKey": "one", "serviceName": "demo-one", "serverId": "server_demo", "replicas": 1}}}, "environmentVariables": []domain.Object{{"key": "VALUE", "scope": "service", "serviceSlotKey": "one", "plainValue": "service-value"}, {"key": "VALUE", "scope": "global", "plainValue": "global-value"}, {"key": "PASSWORD", "scope": "global", "valueSource": "secret_ref", "secretRef": "db_material"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.CreateApplicationReleaseOperation(ctx, "app_demo", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ConfirmTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	tunnel := &recordingJenkinsTunnel{runtime: runtime}
	runtime.JenkinsReleaseExecutor = &recordingJenkinsExecutor{tunnel: tunnel}
	return runtime, task, tunnel
}
func TestJenkinsWorkerFreezesCommitAndKeepsSecretsOutOfPublicBuilds(t *testing.T) {
	runtime, task, tunnel := seededJenkinsExecution(t)
	if _, err := runtime.executeRequestJenkinsRelease(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if !tunnel.started || tunnel.cancelled != 0 {
		t.Fatal("执行器生命周期错误")
	}
	services := objectSlice(tunnel.input.Materials["services"])
	if services[0]["accessWeight"] != nil || services[0]["weight"] != nil {
		t.Fatal("部署材料混入了入口流量权重")
	}
	if tunnel.input.Materials["buildServerId"] != "server_demo" || tunnel.input.Materials["deploymentManagerServerId"] != "server_demo" || tunnel.input.Materials["clusterId"] != "cluster_demo" || tunnel.input.Materials["gatewayServerId"] != nil {
		t.Fatal("发布材料未固定构建与部署节点身份")
	}
	environment := objectMapFromAny(services[0]["environment"])
	if environment["VALUE"] != "service-value" || environment["PASSWORD"] != "private-database-value" {
		t.Fatal("变量作用域或秘密解析错误")
	}
	builds, err := runtime.ApplicationBuilds(context.Background(), "app_demo")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(builds)
	for _, secret := range []string{"private-database-value", validTestSSHPrivateKey, tunnel.input.RunToken, "callbackTokenHash", "materialsSnapshot"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("构建摘要泄露材料")
		}
	}
	if !strings.Contains(string(encoded), "succeeded") || !strings.Contains(string(encoded), strings.Repeat("a", 40)) {
		t.Fatal("构建摘要没有真实结果")
	}
}

func TestJenkinsWorkerRejectsFailedLiveProbeDespiteVerifiedSnapshot(t *testing.T) {
	runtime, task, tunnel := seededJenkinsExecution(t)
	runtime.ServerRuntimeExecutor = &fakeServerRuntimeExecutor{probeErr: errors.New("remote unavailable")}
	_, err := runtime.executeRequestJenkinsRelease(context.Background(), task)
	if err == nil || err.Error() != "JENKINS_RUNTIME_NOT_READY" || tunnel.started {
		t.Fatalf("实时探测失败后仍使用旧快照启动发布：%v started=%v", err, tunnel.started)
	}
}
func TestJenkinsStartFailureCancelsPreparedRun(t *testing.T) {
	runtime, task, tunnel := seededJenkinsExecution(t)
	tunnel.startErr = errors.New("JENKINS_HANDOFF_FAILED")
	if _, err := runtime.executeRequestJenkinsRelease(context.Background(), task); err == nil {
		t.Fatal("启动失败不应成功")
	}
	if tunnel.cancelled != 1 {
		t.Fatalf("准备材料必须回收: %d", tunnel.cancelled)
	}
}

func TestJenkinsCleanupFailureBlocksAnotherRelease(t *testing.T) {
	runtime, task, tunnel := seededJenkinsExecution(t)
	tunnel.startErr = errors.New("JENKINS_HANDOFF_FAILED")
	tunnel.cancelErr = errors.New("JENKINS_CLEANUP_REQUIRED")
	if _, err := runtime.executeRequestJenkinsRelease(context.Background(), task); err == nil || err.Error() != "JENKINS_CLEANUP_REQUIRED" {
		t.Fatalf("清理失败必须覆盖一般启动失败: %v", err)
	}
	state, err := runtime.Store.LoadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	run := state.JenkinsReleaseRuns[0]
	if run["status"] != "cleanup_required" || run["tokenHash"] != "" || run["callbackTokenHash"] != "" {
		t.Fatal("清理未完成必须保留阻塞状态并撤销令牌")
	}
	next, err := runtime.CreateApplicationReleaseOperation(context.Background(), "app_demo", "")
	if err != nil {
		t.Fatalf("清理未完成时仍应接受后续发布排队: %v", err)
	}
	if _, err := runtime.ConfirmTask(context.Background(), next.ID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := runtime.Store.ClaimNextTask(context.Background(), -1); err != nil || claimed != nil {
		t.Fatal("远端清理前不得领取发布")
	}
}

func TestJenkinsCancellationRequiresMatchingTerminalResponse(t *testing.T) {
	for _, output := range []string{`{}`, `{"runId":"other","status":"cancelled"}`, `{"runId":"jrun_one","status":"running"}`, `{"runId":"jrun_one","status":"cleanup_required"}`} {
		if err := validateJenkinsCancellation(output, "jrun_one"); err == nil {
			t.Fatal("取消响应不能确认远端清理")
		}
	}
	if err := validateJenkinsCancellation(`{"runId":"jrun_one","status":"cancelled"}`, "jrun_one"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReconcilesJenkinsCleanupBeforeAllowingNextRelease(t *testing.T) {
	runtime, task, tunnel := seededJenkinsExecution(t)
	ctx := context.Background()
	if _, err := runtime.Store.ClaimNextTask(ctx, -1); err != nil {
		t.Fatal(err)
	}
	tunnel.startErr = errors.New("JENKINS_HANDOFF_FAILED")
	tunnel.cancelErr = errors.New("JENKINS_CLEANUP_REQUIRED")
	_, _ = runtime.executeRequestJenkinsRelease(ctx, task)
	if err := runtime.Store.CompleteTask(ctx, task.ID, "failed", "JENKINS_CLEANUP_REQUIRED"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RunWorkerOnce(ctx); err == nil || err.Error() != "JENKINS_CLEANUP_REQUIRED" {
		t.Fatalf("远端仍未清理不得解除阻塞: %v", err)
	}
	tunnel.cancelErr = nil
	result, err := runtime.RunWorkerOnce(ctx)
	if err != nil || result.Processed != 1 {
		t.Fatal("Worker 应确认清理终态")
	}
	if _, err := runtime.CreateApplicationReleaseOperation(ctx, "app_demo", ""); err != nil {
		t.Fatalf("清理完成应允许新的发布: %v", err)
	}
}
func TestJenkinsPurposeTokensCannotCrossEndpointsOrOutliveRun(t *testing.T) {
	runtime, _, _ := seededJenkinsExecution(t)
	ctx := context.Background()
	token := "test-token-materials"
	if err := runtime.Store.MutateState(ctx, func(state *domain.State) error {
		state.JenkinsReleaseRuns = []domain.Object{{"id": "jrun_scope", "status": "triggered", "tokenHash": domain.HashSecret(token), "callbackTokenHash": domain.HashSecret(jenkinsPurposeToken(token, "callback")), "expiresAt": time.Now().Add(time.Hour).Format(time.RFC3339Nano)}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	callbackToken := jenkinsPurposeToken(token, "callback")
	if _, ok, _ := runtime.AuthorizeJenkinsRun(ctx, "jrun_scope", callbackToken); ok {
		t.Fatal("回调token不能读取材料")
	}
	if _, ok, _ := runtime.AuthorizeJenkinsCallback(ctx, "jrun_scope", token); ok {
		t.Fatal("材料token不能提交终态")
	}
	if _, ok, _ := runtime.AuthorizeJenkinsCallback(ctx, "jrun_scope", callbackToken); !ok {
		t.Fatal("正确用途token被拒绝")
	}
	_ = runtime.Store.MutateState(ctx, func(state *domain.State) error { state.JenkinsReleaseRuns[0]["status"] = "expired"; return nil })
	if _, ok, _ := runtime.AuthorizeJenkinsCallback(ctx, "jrun_scope", callbackToken); ok {
		t.Fatal("恢复失效token不能继续使用")
	}
}
