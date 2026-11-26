package application

import (
	"encoding/json"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestTemplateCatalogCRUDAndSnapshots(t *testing.T) {
	r, store, ctx := newFrpRuntime(t)
	catalog, err := r.ApplicationTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	template := findObject(objectSlice(catalog["items"]), "gitea")
	template["id"] = "custom-git"
	if err := r.SaveApplicationTemplate(ctx, "custom-git", template, true); err != nil {
		t.Fatal(err)
	}
	create := func() domain.Object {
		app, err := r.CreateObject(ctx, "applications", "app", domain.Object{"projectId": domain.BuiltinProjectID, "name": "Git", "templateId": "custom-git"})
		if err != nil {
			t.Fatal(err)
		}
		state, err := store.LoadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return findObjectBy(state.ApplicationDeploymentConfigs, "applicationId", stringValue(app["id"]))
	}
	first := create()
	cfg := objectMapFromAny(template["config"])
	objectMapFromAny(cfg["sourceConfig"])["image"] = "example/git:2"
	if err := r.SaveApplicationTemplate(ctx, "custom-git", template, false); err != nil {
		t.Fatal(err)
	}
	second := create()
	if objectMapFromAny(first["sourceConfig"])["image"] == objectMapFromAny(second["sourceConfig"])["image"] || objectSlice(first["outputVariables"])[0]["id"] == objectSlice(second["outputVariables"])[0]["id"] {
		t.Fatal("两次实例化没有产生独立快照")
	}
	catalog, _ = r.ApplicationTemplates(ctx)
	for _, item := range objectSlice(catalog["items"]) {
		if err := r.DeleteApplicationTemplate(ctx, stringValue(item["id"])); err != nil {
			t.Fatal(err)
		}
	}
	state, err := store.LoadState(ctx)
	if err != nil || len(state.ApplicationTemplates) != 0 || len(state.ApplicationDeploymentConfigs) != 2 {
		t.Fatal("删除模板影响实例或重载后被重新播种", err)
	}
	old, err := domain.NormalizeState([]byte(`{"projects":[]}`))
	if err != nil || len(old.ApplicationTemplates) != len(domain.DefaultApplicationTemplates()) {
		t.Fatal("旧状态未播种", err)
	}
	empty, err := domain.NormalizeState([]byte(`{"applicationTemplates":[]}`))
	if err != nil || len(empty.ApplicationTemplates) != 0 {
		t.Fatal("空模板库应当持久保留", err)
	}
}

func TestTemplateImportValidatesEntireBatchAndConflicts(t *testing.T) {
	r, _, ctx := newFrpRuntime(t)
	catalog, _ := r.ApplicationTemplates(ctx)
	template := findObject(objectSlice(catalog["items"]), "gitea")
	template["name"] = "Changed"
	newItem := domain.CloneObjectWithoutUserReferences(template)
	newItem["id"] = "queue"
	input := TemplateImport{Document: domain.Object{"version": 1, "items": []domain.Object{newItem, template}}, Policy: "skip"}
	preview, err := r.ImportApplicationTemplates(ctx, input, true)
	if err != nil || len(objectStringSlice(preview["conflicts"])) != 1 {
		t.Fatal("模板预览结果不符")
	}
	input.Revision = stringValue(preview["revision"])
	if _, err := r.ImportApplicationTemplates(ctx, input, false); err != nil {
		t.Fatal(err)
	}
	catalog, _ = r.ApplicationTemplates(ctx)
	if findObject(objectSlice(catalog["items"]), "gitea")["name"] == "Changed" || findObject(objectSlice(catalog["items"]), "queue") == nil {
		t.Fatal("跳过冲突时应创建新模板并保留已有模板")
	}
	if _, err := r.ImportApplicationTemplates(ctx, input, false); err == nil {
		t.Fatal("预览后发生的冲突没有被检测")
	}
	input.Policy = "overwrite"
	preview, _ = r.ImportApplicationTemplates(ctx, input, true)
	input.Revision = stringValue(preview["revision"])
	if _, err := r.ImportApplicationTemplates(ctx, input, false); err != nil {
		t.Fatal(err)
	}
	catalog, _ = r.ApplicationTemplates(ctx)
	if findObject(objectSlice(catalog["items"]), "gitea")["name"] != "Changed" {
		t.Fatal("覆盖策略未生效")
	}
	newItem["id"] = "new-queue"
	objectMapFromAny(template["config"])["deploymentManagerServerId"] = "server-example"
	if _, err := r.ImportApplicationTemplates(ctx, input, false); err == nil {
		t.Fatal("接受了含环境绑定的模板")
	}
	catalog, _ = r.ApplicationTemplates(ctx)
	if findObject(objectSlice(catalog["items"]), "new-queue") != nil {
		t.Fatal("无效批次留下部分导入")
	}
}

func TestTemplateCatalogProjectBackup(t *testing.T) {
	original := domain.EmptyState()
	obj := domain.CloneObjectWithoutUserReferences(original.ApplicationTemplates[0])
	obj["id"] = "custom"
	original.ApplicationTemplates = append(original.ApplicationTemplates, obj)
	scopes := []domain.BackupScope{domain.BackupScopeProjects}
	payload := domain.BuildBackupPayload(original, domain.BackupSchemaVersion, scopes, false)
	current := domain.EmptyState()
	plan := buildRestoreObjectPlan(current, payload, scopes, "upload", t.TempDir())
	if len(plan.conflicts) != 0 {
		t.Fatal("相同的初始模板被计入冲突", plan.conflicts)
	}
	if err := applyRestoreObjectPlan(&current, plan, domain.RestoreConflictAbortOnConflict); err != nil {
		t.Fatal(err)
	}
	restored := findObject(current.ApplicationTemplates, "custom")
	if !templateContentsEqual(obj, restored) {
		t.Fatal("模板在恢复中被修改")
	}
	for _, templates := range [][]domain.Object{nil, {}} {
		payload.Projects.ApplicationTemplates = templates
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var loaded domain.BackupPayload
		if err := json.Unmarshal(raw, &loaded); err != nil {
			t.Fatal(err)
		}
		if (loaded.Projects.ApplicationTemplates == nil) != (templates == nil) {
			t.Fatal("旧归档与空模板库失去区分")
		}
	}
}

func TestPortlessTemplateRetainsEndpointSemantics(t *testing.T) {
	swarm := domain.Object{"ports": []any{}}
	labels := domain.Object{}
	if len(buildServiceEndpoints(swarm, domain.Object{}, labels)) != 0 || labels["traefik.enable"] != "false" || labels["iquops.port.web"] != nil {
		t.Fatal(labels)
	}
	if _, err := routeHTTPPort(swarm, ""); err == nil {
		t.Fatal("无端口服务被接受为路由目标")
	}
	swarm["containerPort"] = 8080
	ports := configuredServicePorts(swarm, domain.Object{})
	if len(ports) != 1 || ports[0]["targetPort"] != 8080 {
		t.Fatal("旧端口映射未保留")
	}
}
