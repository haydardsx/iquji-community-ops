package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskResourceLocksShareDependenciesAndExcludeMaintenance(t *testing.T) {
	state := EmptyState()
	state.Servers = []Object{{"id": "server_one", "secretAssetIds": []string{"asset_ssh"}, "health": Object{"metrics": Object{"swarmClusterId": "cluster_one"}}}}
	state.SecretAssets = []Object{{"id": "asset_git", "secretRef": "ref_git"}, {"id": "asset_ssh", "secretRef": "ref_ssh"}, {"id": "asset_env", "secretRef": "ref_env"}}
	state.ApplicationDeploymentConfigs = []Object{{"applicationId": "app_one", "buildServerId": "server_one", "sourceConfig": Object{"authSecretRef": "ref_git"}, "environmentVariables": []Object{{"secretRef": "ref_env"}}}, {"applicationId": "app_two", "gatewayServerId": "server_one"}}
	app := TaskResources(state, Task{TargetKind: "application", TargetID: "app_one"})
	other := TaskResources(state, Task{TargetKind: "application", TargetID: "app_two"})
	if TaskLocksConflict(app, other) {
		t.Fatal("不同应用共享基础运行时不应互斥")
	}
	for _, task := range []Task{{TargetKind: "server", TargetID: "server_one"}, {TargetKind: "secret_asset", TargetID: "asset_git"}, {TargetKind: "secret_asset", TargetID: "asset_ssh"}, {TargetKind: "secret_asset", TargetID: "asset_env"}} {
		if !TaskLocksConflict(app, TaskResources(state, task)) {
			t.Fatalf("维护或轮换必须等待使用者: %s", task.TargetID)
		}
	}
}

func TestBackupPayloadExcludesInternalTaskQueueMetadata(t *testing.T) {
	state := EmptyState()
	state.Projects = []Object{{"id": "project_example"}}
	state.Tasks = []Task{{ID: "task_example", Status: "executing", QueueOrder: 1, QueueResources: []TaskResourceLock{{Key: "control-plane"}}, Attempt: 2, ClaimToken: "claim_example", ClaimOwner: "worker_example", LeaseExpiresAt: "2026-01-01T00:00:00Z", RequestKey: "request_example", RequestDigest: "digest_example"}}
	payload := BuildBackupPayload(state, "schema-v1", []BackupScope{BackupScopeProjects}, true)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"queueOrder", "queueResources", "claimToken", "claimOwner", "leaseExpiresAt", "requestKey", "requestDigest", "claim_example"} {
		if strings.Contains(string(encoded), key) {
			t.Fatalf("普通备份包含内部队列字段: %s", key)
		}
	}
}

func TestJenkinsAccessResourcesIncludeInterruptedRemoteChanges(t *testing.T) {
	state := EmptyState()
	access := Object{}
	for _, phase := range []string{"applied", "pending", "retiring"} {
		access[phase] = Object{"gatewayServerId": "gateway_" + phase, "domainId": "domain_" + phase}
	}
	state.Servers = []Object{{"id": "builder", "jenkinsAccess": access}}
	for _, action := range []TaskAction{TaskActionBindJenkinsAccess, TaskActionUnbindJenkinsAccess, TaskActionUninstallJenkins} {
		task := Task{Action: string(action), TargetKind: "server", TargetID: "builder", Payload: Object{"gatewayServerId": "gateway_request", "domainId": "domain_request"}}
		resources := CurrentTaskResources(state, task)
		for _, phase := range []string{"applied", "pending", "retiring", "request"} {
			for _, key := range []string{"server:gateway_" + phase, "domain:domain_" + phase} {
				if !TaskLocksConflict(resources, []TaskResourceLock{{Key: key}}) {
					t.Fatalf("%s 未独占 %s", action, key)
				}
			}
		}
	}
}
