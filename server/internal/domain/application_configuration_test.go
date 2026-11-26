package domain

import "testing"

func TestApplicationSourceMigrationPreservesExecutorChoice(t *testing.T) {
	state := EmptyState()
	state.Applications = []Object{
		{"id": "app_one", "sourceConfig": Object{"provider": "git", "repositoryUrl": "https://example.test/old.git", "defaultRef": "stable"}},
		{"id": "app_two", "sourceConfig": Object{"provider": "git", "repositoryUrl": "https://example.test/two.git", "branch": "develop"}},
	}
	state.ApplicationDeploymentConfigs = []Object{{"id": "config_one", "applicationId": "app_one", "environmentVariables": []Object{{"key": "OLD"}, {"key": "BUILD", "stage": "build"}}, "sourceConfig": Object{"provider": "git", "repositoryUrl": "https://example.test/current.git", "ref": "main"}}}
	MigrateApplicationSources(&state)
	MigrateApplicationSources(&state)
	if len(state.ApplicationDeploymentConfigs) != 2 || state.Applications[0]["sourceConfig"] != nil || state.Applications[1]["sourceConfig"] != nil || state.Applications[0]["runtimeMode"] != "swarm" {
		t.Fatal("来源迁移保留重复事实或不幂等")
	}
	inputs := ObjectSliceFromAny(state.ApplicationDeploymentConfigs[0]["environmentVariables"])
	if inputs[0]["stage"] != "runtime" || inputs[1]["stage"] != "build" {
		t.Fatal("已有变量应迁移到运行期并保留显式构建阶段")
	}
	first := ObjectFromAny(state.ApplicationDeploymentConfigs[0]["sourceConfig"])
	second := ObjectFromAny(state.ApplicationDeploymentConfigs[1]["sourceConfig"])
	if first["repositoryUrl"] != "https://example.test/current.git" || second["ref"] != "develop" || second["branch"] != nil {
		t.Fatal("迁移改变已有来源选择或保留旧字段")
	}
}
