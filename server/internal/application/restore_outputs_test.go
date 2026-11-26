package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestRestoreCrossProjectOutputReferences(t *testing.T) {
	output := func(id, key string) domain.Object {
		return domain.Object{"id": id, "key": key, "visibility": "cross_project", "valueSource": "plain_text", "plainValue": key}
	}
	consumer := func(app, producer, outputID string) domain.Object {
		return domain.Object{"id": "config_" + app, "applicationId": app, "environmentVariables": []domain.Object{
			{"key": "ADDRESS", "stage": "build", "valueSource": "output_ref", "outputId": outputID, "outputApplicationId": producer},
		}}
	}
	source := domain.EmptyState()
	source.Projects = []domain.Object{{"id": "project_from", "slug": "backend"}, {"id": "project_web", "slug": "web"}}
	source.Applications = []domain.Object{{"id": "app_from", "name": "Database", "projectId": "project_from"}, {"id": "app_web", "name": "Web", "projectId": "project_web"}}
	source.ApplicationDeploymentConfigs = []domain.Object{
		{"id": "cfg_from", "applicationId": "app_from", "outputVariables": []domain.Object{output("out_from", "ADDRESS"), output("out_other", "REGION")}},
		consumer("app_web", "app_from", "out_from"),
	}
	target := domain.EmptyState()
	target.Projects = []domain.Object{{"id": "project_target", "slug": "backend"}}
	target.Applications = []domain.Object{{"id": "app_target", "name": "Database", "projectId": "project_target"}, {"id": "external", "name": "External", "projectId": "external_project"}}
	target.ApplicationDeploymentConfigs = []domain.Object{
		{"id": "cfg_target", "applicationId": "app_target", "outputVariables": []domain.Object{output("out_target", "ADDRESS")}},
		consumer("external", "app_target", "out_target"),
	}
	target.ApplicationDeploymentConfigs[1]["outputVariables"] = []domain.Object{output("out_other", "OTHER")}
	scopes := []domain.BackupScope{domain.BackupScopeProjects}
	payload := domain.BuildBackupPayload(source, domain.BackupSchemaVersion, scopes, false)
	plan := buildRestoreObjectPlan(target, payload, scopes, "upload", "")
	if err := applyRestoreObjectPlan(&target, plan, domain.RestoreConflictOverwriteSelected); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"app_web", "external"} {
		cfg := findObjectBy(target.ApplicationDeploymentConfigs, "applicationId", app)
		ref := objectSlice(cfg["environmentVariables"])[0]
		values, err := applicationInputSnapshot(target, app, cfg)
		if ref["outputId"] != "out_target" || ref["outputApplicationId"] != "app_target" || err != nil || values[0]["plainValue"] != "ADDRESS" {
			t.Fatal("跨项目引用恢复到了错误的生产者", err)
		}
	}
	region := findObjectBy(objectSlice(target.ApplicationDeploymentConfigs[0]["outputVariables"]), "key", "REGION")
	if region == nil || region["id"] == "out_other" {
		t.Fatal("新输出与无关应用的 ID 冲突")
	}
	objectSlice(source.ApplicationDeploymentConfigs[0]["outputVariables"])[0]["visibility"] = "project"
	payload = domain.BuildBackupPayload(source, domain.BackupSchemaVersion, scopes, false)
	plan = buildRestoreObjectPlan(target, payload, scopes, "upload", "")
	if err := applyRestoreObjectPlan(&target, plan, domain.RestoreConflictOverwriteSelected); err == nil || err.Error() != "APPLICATION_OUTPUT_REFERENCE_INVALID" {
		t.Fatal("恢复覆盖不应破坏现有的跨项目引用", err)
	}
}

func TestBackupDropsObservedApplicationValues(t *testing.T) {
	state := domain.EmptyState()
	app := domain.Object{"id": "app_demo", "projectId": "project_demo", "publishedBuildOutputs": domain.Object{"out": "old"}, "appliedInputSignatures": domain.Object{"runtime": "old"}, "appliedRuntimeKeys": []string{"OLD"}, "runtimeSnapshot": domain.Object{"availability": "available"}}
	route := domain.Object{"id": "route_demo", "applicationId": "app_demo", "accessibleUrls": []string{"https://example.test/"}, "lastApplied": domain.Object{"verified": true}}
	state.Applications, state.Routes = []domain.Object{app}, []domain.Object{route}
	scopes := []domain.BackupScope{domain.BackupScopeProjects}
	payload := domain.BuildBackupPayload(state, domain.BackupSchemaVersion, scopes, false)
	assertEmpty := func(a, r domain.Object) {
		t.Helper()
		if a["publishedBuildOutputs"] != nil || a["appliedInputSignatures"] != nil || a["appliedRuntimeKeys"] != nil || a["runtimeSnapshot"] != nil || r["lastApplied"] != nil || r["accessibleUrls"] != nil {
			t.Fatal("备份或恢复保留了未经验证的运行状态")
		}
	}
	assertEmpty(payload.Projects.Applications[0], payload.Projects.Routes[0])
	payload.Projects.Applications, payload.Projects.Routes = state.Applications, state.Routes
	restored := backupPayloadState(payload, scopes, false)
	assertEmpty(restored.Applications[0], restored.Routes[0])
}
