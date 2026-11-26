package application

import (
	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
	"testing"
)

func TestApplicationConfigurationAtomicSourceAndVariables(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "prj_one"}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "prj_one"}}
		state.Servers = []domain.Object{{"id": "srv_one", "privateHost": fixtureWorkerIP.String(), "publicHost": "192.0.2.1"}}
		state.SecretAssets = []domain.Object{{"id": "asset_demo", "assetType": "application_secret", "purpose": "application_variables", "secretRef": "secret_saved"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := domain.Object{
		"sourceConfig": domain.Object{"provider": "git", "repositoryUrl": "https://example.test/app.git", "ref": "release", "refType": "tag"},
		"swarmRuntime": domain.Object{"services": []domain.Object{{"slotKey": "web", "serviceName": "app-web", "serverId": "srv_one", "replicas": 1}}},
		"environmentVariables": []domain.Object{
			{"key": "TOKEN", "stage": "runtime", "scope": "service", "serviceSlotKey": "web", "valueSource": "secret_ref", "secretRef": "secret_saved"},
			{"key": "PUBLIC_NAME", "stage": "build", "scope": "global", "valueSource": "plain_text", "plainValue": "demo"},
		},
	}
	config, err := runtime.UpsertApplicationConfig(ctx, "app_one", input)
	if err != nil {
		t.Fatal(err)
	}
	variables := objectSlice(config["environmentVariables"])
	if objectMapFromAny(config["sourceConfig"])["jenkinsfilePath"] != "Jenkinsfile" || variables[0]["secretRef"] != "secret_saved" || variables[0]["serviceSlotKey"] != "web" || variables[0]["plainValue"] != nil || variables[1]["plainValue"] != "demo" || variables[1]["stage"] != "build" {
		t.Fatal("配置丢失正式来源或变量")
	}
	input["sourceConfig"] = domain.Object{"provider": "git", "repositoryUrl": "https://example.test/changed.git"}
	input["environmentVariables"] = []domain.Object{{"key": "TOKEN", "stage": "runtime", "scope": "service", "serviceSlotKey": "missing", "valueSource": "secret_ref", "secretRef": "secret_saved"}}
	if _, err := runtime.UpsertApplicationConfig(ctx, "app_one", input); err == nil {
		t.Fatal("非法服务变量被保存")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if objectMapFromAny(state.ApplicationDeploymentConfigs[0]["sourceConfig"])["repositoryUrl"] != "https://example.test/app.git" {
		t.Fatal("失败保存部分覆盖了来源")
	}
	preview, err := runtime.ReleasePlanPreview(ctx, "app_one", domain.Object{})
	if err != nil || objectMapFromAny(preview["releasePlan"])["ref"] != "release" {
		t.Fatal("预检未采用已保存版本")
	}
	if _, err := runtime.UpdateObject(ctx, "applications", "app_one", domain.Object{"sourceConfig": domain.Object{}}); err == nil {
		t.Fatal("应用旧来源入口未关闭")
	}
}

func TestProjectWorkspaceSeparatesOwnedResourcesAndCandidates(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "prj_one"}, {"id": "prj_two"}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "prj_one"}, {"id": "app_two", "projectId": "prj_two"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one", "buildServerId": "srv_used"}, {"applicationId": "app_two"}}
		state.Servers = []domain.Object{{"id": "srv_free"}, {"id": "srv_used"}, {"id": "srv_other", "projectIds": []string{"prj_two"}}}
		state.Routes = []domain.Object{{"id": "route_one", "applicationId": "app_one"}, {"id": "route_two", "applicationId": "app_two"}}
		state.ApplicationInstances = []domain.Object{{"applicationId": "app_one"}, {"applicationId": "app_two"}}
		state.OperationRuns = []domain.OperationRun{{ID: "op_one", TargetKind: "route", TargetID: "route_one"}, {ID: "op_two", TargetKind: "application", TargetID: "app_two"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.Workspace(ctx, "prj_one", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objectSlice(value["routes"])) != 1 || len(objectSlice(value["applicationDeploymentConfigs"])) != 1 || len(objectSlice(value["applicationInstances"])) != 1 || len(objectSlice(value["servers"])) != 2 || len(objectSlice(value["relatedServers"])) != 1 || len(value["relatedOperations"].([]domain.OperationRun)) != 1 || value["relatedTasks"] != nil {
		t.Fatal("工作区串入其他项目或把候选计作关联")
	}
}

func TestConfigurationDeletionRequiresRemoteCleanupAndPreservesHistory(t *testing.T) {
	runtime, store, ctx := newFrpRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "prj_one"}}
		state.Servers = []domain.Object{{"id": "srv_one", "projectIds": []string{"prj_one"}}}
		state.Applications = []domain.Object{{"id": "app_one", "projectId": "prj_one"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "app_one"}}
		state.Routes = []domain.Object{{"id": "route_one", "applicationId": "app_one", "lastApplied": domain.Object{"revision": "old"}}}
		state.RuntimeLogs = []domain.RuntimeLog{{ID: "log_one", TargetKind: "application", TargetID: "app_one"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteObject(ctx, "projects", "prj_one"); err == nil || err.Error() != "PROJECT_HAS_APPLICATIONS" {
		t.Fatalf("非空项目未阻断：%v", err)
	}
	if err := runtime.DeleteObject(ctx, "applications", "app_one"); err == nil || err.Error() != "APPLICATION_ROUTE_REVOKE_REQUIRED" {
		t.Fatalf("生效路由未阻断：%v", err)
	}
	if err := store.MutateState(ctx, func(state *domain.State) error { delete(state.Routes[0], "lastApplied"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DeleteObject(ctx, "applications", "app_one"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Applications) != 0 || len(state.ApplicationDeploymentConfigs) != 0 || len(state.Routes) != 0 || len(state.RuntimeLogs) != 1 {
		t.Fatal("空应用删除未原子清理草稿或删除了历史")
	}
	if err := runtime.DeleteObject(ctx, "projects", "prj_one"); err != nil {
		t.Fatal(err)
	}
	state, _ = store.LoadState(ctx)
	if len(objectStringSlice(state.Servers[0]["projectIds"])) != 0 {
		t.Fatal("删除项目留下服务器关联")
	}
}
