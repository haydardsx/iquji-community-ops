package domain

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestFreshTemplateCatalog(t *testing.T) {
	ids := make([]string, 0)
	for _, template := range EmptyState().ApplicationTemplates {
		ids = append(ids, StringValue(template["id"]))
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"demo-backend", "demo-frontend", "gitea", "pmail"}) {
		t.Fatalf("初始模板目录不符：%v", ids)
	}
}

func TestTemplateUpgradeKeepsCatalogEdits(t *testing.T) {
	state, err := NormalizeState([]byte(`{"applicationTemplates":[{"id":"custom"}]}`))
	if err != nil || len(state.ApplicationTemplates) != 4 {
		t.Fatal("旧目录缺少新增模板", err)
	}
	state.ApplicationTemplates = slices.Delete(state.ApplicationTemplates, 1, len(state.ApplicationTemplates))
	saved, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NormalizeState(saved)
	if err != nil || len(reopened.ApplicationTemplates) != 1 || reopened.ApplicationTemplates[0]["id"] != "custom" {
		t.Fatal("重启覆盖了用户对模板库的修改", err)
	}
	existing, err := NormalizeState([]byte(`{"applicationTemplates":[{"id":"pmail","name":"自定义邮件"}]}`))
	if err != nil || len(existing.ApplicationTemplates) != 3 || existing.ApplicationTemplates[0]["name"] != "自定义邮件" {
		t.Fatal("升级替换了已有的同 ID 模板", err)
	}
}

func TestDemoTemplatesUpgradeAddsOnlyMissingSeeds(t *testing.T) {
	state, err := NormalizeState([]byte(`{"applicationTemplateSeedVersion":1,"applicationTemplates":[{"id":"demo-backend","name":"自定义后端"}]}`))
	if err != nil || len(state.ApplicationTemplates) != 2 || state.ApplicationTemplates[0]["name"] != "自定义后端" || state.ApplicationTemplates[1]["id"] != "demo-frontend" {
		t.Fatal("升级应保留本地后端并补入前端演示模板", err)
	}
}
