package domain

import "testing"

func TestDeploymentMigrationDropsUnusedStackAndKeepsService(t *testing.T) {
	state := EmptyState()
	runtime := Object{"stackName": "old", "services": []Object{{"slotKey": "main", "serviceName": "existing"}}}
	state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app", "swarmRuntime": runtime}}
	MigrateApplicationDeploymentLocations(&state)
	if _, present := runtime["stackName"]; present {
		t.Fatal("仍在返回旧栈名称")
	}
	service := ObjectSliceFromAny(runtime["services"])[0]
	if service["slotKey"] != "main" || service["serviceName"] != "existing" {
		t.Fatal("迁移修改了现有服务")
	}
}

func TestDeploymentLocationMigrationPreservesExplicitChoicesAndOriginalRelease(t *testing.T) {
	state := EmptyState()
	state.Servers = []Object{{"id": "old", "health": Object{"metrics": Object{"swarm": true, "swarmVerificationStatus": "verified", "swarmRole": "manager", "swarmClusterId": "new-cluster"}}}}
	state.Applications = []Object{{"id": "app", "runtimeSnapshot": Object{"services": []Object{{"serviceId": "service-old"}}}}}
	state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app", "gatewayServerId": "new"}, {"applicationId": "blank", "gatewayServerId": "server_unassigned"}, {"applicationId": "known", "gatewayServerId": "old"}}
	state.ReleasePlans = []Object{{"applicationId": "app", "gatewayServerId": "old", "status": "succeeded"}, {"applicationId": "app", "gatewayServerId": "old", "status": "queued"}}
	state.Tasks = []Task{{Payload: map[string]any{"runtimeConfigSnapshot": Object{"gatewayServerId": "old"}}}}
	MigrateApplicationDeploymentLocations(&state)
	if cfg := state.ApplicationDeploymentConfigs[0]; cfg["deploymentManagerServerId"] != "" || cfg["gatewayServerId"] != "new" {
		t.Fatal("未经核验的旧 HTTP 网关被自动选作管理节点")
	}
	if state.ApplicationDeploymentConfigs[2]["deploymentManagerServerId"] != "old" {
		t.Fatal("已核验的旧管理节点未被复用")
	}
	if cfg := state.ApplicationDeploymentConfigs[1]; cfg["deploymentManagerServerId"] != "" || cfg["gatewayServerId"] != nil {
		t.Fatal("未配置应用导入了虚假管理节点")
	}
	if state.ReleasePlans[1]["deploymentClusterId"] != "new-cluster" || ObjectFromAny(state.Tasks[0].Payload["runtimeConfigSnapshot"])["deploymentManagerServerId"] != "old" {
		t.Fatal("未迁移排队计划与任务快照")
	}
	location := ObjectFromAny(state.Applications[0]["deploymentLocation"])
	if location["deploymentManagerServerId"] != "old" || location["deploymentClusterId"] != "" {
		t.Fatal("把当前服务器集群或草稿管理节点误作历史部署现场")
	}
	state.ApplicationDeploymentConfigs[0]["deploymentManagerServerId"] = ""
	MigrateApplicationDeploymentLocations(&state)
	if state.ApplicationDeploymentConfigs[0]["deploymentManagerServerId"] != "" {
		t.Fatal("重复迁移恢复了用户清空的管理节点")
	}
}
