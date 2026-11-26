package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackupExcludesPendingCredentialsAndExecutionMarkers(t *testing.T) {
	state := EmptyState()
	state.SecretAssets = []Object{
		{"id": "asset_pending", "secretRef": "secret_not_applied", "pendingCreationOperationId": "operation_pending"},
		{"id": "asset_ready", "secretRef": "secret_applied", "appliedOperationRunId": "operation_applied"},
	}
	state.Tasks = []Task{{ID: "operation_pending", Payload: map[string]any{"stagedSecretRef": "pending_secret_material"}}}
	payload := BuildBackupPayload(state, BackupSchemaVersion, []BackupScope{BackupScopeSecretAssets}, true)
	if len(payload.SecretAssets.Assets) != 1 || StringValue(payload.SecretAssets.Assets[0]["id"]) != "asset_ready" {
		t.Fatal("未生效凭据出现在备份中")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"pendingCreationOperationId", "appliedOperationRunId", "stagedSecretRef", "secret_not_applied", "pending_secret_material"} {
		if strings.Contains(string(encoded), marker) {
			t.Fatalf("备份包含临时凭据状态：%s", marker)
		}
	}
	if StringValue(state.SecretAssets[0]["pendingCreationOperationId"]) != "operation_pending" || StringValue(state.SecretAssets[1]["appliedOperationRunId"]) != "operation_applied" {
		t.Fatal("生成备份不得修改运行状态")
	}
}
