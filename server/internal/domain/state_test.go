package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeStateStripsLegacyUserReferences(t *testing.T) {
	state, err := NormalizeState([]byte(`{
		"users": [{"id": "user_1", "email": "operator@example.invalid"}],
		"sessions": [{"id": "session_1", "userId": "user_1"}],
		"projects": [{
			"id": "project_1",
			"ownerUserIds": ["user_1"],
			"metadata": {"actor": {"email": "operator@example.invalid"}}
		}],
		"releasePlans": [{"id": "release_1", "requestedBy": "operator@example.invalid"}],
		"importExportJobs": [{"id": "export_1", "exportedBy": "operator@example.invalid"}],
		"tasks": [{
			"id": "task_1",
			"action": "deploy_application",
			"status": "queued",
			"payload": {
				"userId": "user_1",
				"nested": [{"actor": "operator@example.invalid"}]
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("normalize state: %v", err)
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var checkKeys func(any)
	checkKeys = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				switch key {
				case "actor", "exportedBy", "ownerUserIds", "requestedBy", "userId", "users", "sessions":
					t.Fatalf("规范化后仍有旧用户关联字段 %q", key)
				}
				checkKeys(child)
			}
		case []any:
			for _, child := range node {
				checkKeys(child)
			}
		}
	}
	checkKeys(document)
}

func TestNormalizeStateMigratesLegacyFrpSessionsOnce(t *testing.T) {
	state, err := NormalizeState([]byte(`{
		"servers": [{"id": "srv_1", "name": "Gateway"}],
		"frpTunnelSessions": [
			{
				"id": "tun_legacy_running",
				"serverId": "srv_1",
				"status": "running",
				"frpsBindPort": 7000,
				"frpsImage": "ghcr.io/fatedier/frps:v0.69.1",
				"tokenSecretRef": "secret_legacy",
				"containerName": "iquops-frps-tun_legacy_running",
				"remoteDir": "/opt/iquops/tunnels/tun_legacy_running",
				"taskId": "task_legacy",
				"mappings": [{
					"name": "probe",
					"localHost": "127.0.0.1",
					"localPort": 17990,
					"frpsProxyPort": 17990,
					"publicPathPrefix": "/"
				}],
				"createdAt": "2026-07-01T00:00:00Z",
				"updatedAt": "2026-07-01T00:01:00Z"
			},
			{
				"id": "tun_legacy_failed",
				"name": "Failed API",
				"serverId": "srv_1",
				"status": "failed",
				"frpsBindPort": 7001,
				"mappings": [{
					"name": "local-api",
					"localHost": "127.0.0.1",
					"localPort": 3001,
					"remotePort": 18081
				}],
				"createdAt": "2026-07-02T00:00:00Z",
				"updatedAt": "2026-07-02T00:01:00Z"
			}
		]
	}`))
	if err != nil {
		t.Fatalf("normalize legacy state: %v", err)
	}
	if len(state.FrpTunnels) != 2 || len(state.FrpTunnelRuns) != 2 || len(state.LegacyFrpTunnelSessions) != 0 {
		t.Fatal("unexpected migration result")
	}
	running := FindFrpTunnelRun(state.FrpTunnelRuns, "tun_legacy_running")
	if running == nil || running.TunnelID != "tun_legacy_running" || running.Status != FrpTunnelStatusRunning {
		t.Fatal("legacy runtime identity was not preserved")
	}
	if running.ContainerName != "iquops-frps-tun_legacy_running" || running.RemoteDir != "/opt/iquops/tunnels/tun_legacy_running" || running.StartOperationRunID != "task_legacy" {
		t.Fatal("legacy cleanup identity was not preserved")
	}
	if len(running.Mappings) != 1 || running.Mappings[0].TargetType != FrpTunnelMappingTargetHTTPProbe {
		t.Fatal("legacy probe mapping was not classified")
	}
	failed := FindFrpTunnelRun(state.FrpTunnelRuns, "tun_legacy_failed")
	if failed == nil || failed.Status != FrpTunnelStatusFailed || failed.Mappings[0].TargetType != FrpTunnelMappingTargetLocalService || failed.Mappings[0].FrpsProxyPort != 18081 {
		t.Fatal("legacy failed session was not migrated")
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal migrated state: %v", err)
	}
	if strings.Contains(string(data), "frpTunnelSessions") {
		t.Fatal("migrated state still contains the legacy collection")
	}
	reloaded, err := NormalizeState(data)
	if err != nil {
		t.Fatalf("normalize migrated state: %v", err)
	}
	if len(reloaded.FrpTunnels) != 2 || len(reloaded.FrpTunnelRuns) != 2 {
		t.Fatal("migration must be idempotent")
	}
}

func TestNormalizeStateMigratesImportExportJobsToBackupRestoreJobs(t *testing.T) {
	state, err := NormalizeState([]byte(`{
		"importExportJobs": [{"id": "legacy_import", "status": "succeeded"}]
	}`))
	if err != nil {
		t.Fatalf("normalize legacy jobs: %v", err)
	}
	if len(state.BackupRestoreJobs) != 1 || StringValue(state.BackupRestoreJobs[0]["id"]) != "legacy_import" {
		t.Fatal("legacy jobs were not migrated")
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal migrated jobs: %v", err)
	}
	if strings.Contains(string(data), "importExportJobs") || !strings.Contains(string(data), "backupRestoreJobs") {
		t.Fatal("unexpected migrated job shape")
	}
}

func TestNormalizeStateMigratesBuildServerAndManagedJenkinsIdentity(t *testing.T) {
	state, err := NormalizeState([]byte(`{
		"applicationDeploymentConfigs": [{
			"id": "config_one",
			"applicationId": "app_one",
			"deliveryConfig": {
				"buildServerId": "srv_build",
				"pipeline": {"folder": "legacy", "jobName": "app-one", "jobTemplate": "legacy-template", "triggerMode": "webhook"}
			}
		}],
		"releasePlans": [{
			"id": "release_one",
			"buildSnapshot": {
				"deliveryConfig": {
					"buildServerId": "srv_release",
					"pipeline": {"jobName": "another-app-job"}
				}
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("normalize legacy build server state: %v", err)
	}
	config := state.ApplicationDeploymentConfigs[0]
	if config["buildServerId"] != "srv_build" {
		t.Fatal("legacy build server was not moved to top level")
	}
	delivery := config["deliveryConfig"].(map[string]any)
	if _, exists := delivery["buildServerId"]; exists {
		t.Fatal("legacy nested build server was retained")
	}
	pipeline := delivery["pipeline"].(map[string]any)
	if pipeline["folder"] != ManagedJenkinsFolder || pipeline["jobName"] != ManagedJenkinsJob || pipeline["jobTemplate"] != ManagedJenkinsJobTemplate || pipeline["triggerMode"] != "ops_manual" {
		t.Fatal("managed Jenkins identity was not normalized")
	}
	plan := state.ReleasePlans[0]
	if plan["buildServerId"] != "srv_release" || plan["buildSnapshot"].(map[string]any)["buildServerId"] != "srv_release" {
		t.Fatal("release snapshot build server was not migrated")
	}
}
