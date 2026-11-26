package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestSwarmRebuildRecoveryEndpointUsesOriginalBackupAndRequiresConfirmation(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(context.Background(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_example", "name": "示例网关"}}
		state.Tasks = []domain.Task{{ID: "task_original", Action: "rebuild_docker_swarm", Status: "failed", TargetKind: "server", TargetID: "server_example", Payload: domain.Object{
			"connectionMode": "public", "advertiseAddr": "192.0.2.10",
			"swarmRebuildCheckpoint": domain.Object{"version": 1, "backupId": "task_original", "manifestHash": strings.Repeat("a", 64), "dataHash": "none", "phase": "recovering", "newClusterId": "", "newNodeId": ""},
		}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	response := api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild/recover", map[string]any{"operationRunId": "task_original", "force": true})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("恢复入口接受了未知字段")
	}
	response = api.request(t, http.MethodPost, "/servers/server_other/swarm/rebuild/recover", map[string]any{"operationRunId": "task_original"})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("恢复入口接受了其他服务器的备份")
	}
	response = api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild/recover", map[string]any{"operationRunId": "task_original"})
	var run map[string]any
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || run["action"] != "recover_docker_swarm_rebuild" || run["id"] == "task_original" {
		t.Fatalf("恢复入口未返回独立正式操作：%d", response.StatusCode)
	}
	state, err := api.store.LoadState(context.Background())
	if err != nil || len(state.Tasks) != 2 || state.Tasks[1].Status != "pending_confirmation" || len(state.ConfirmationTokens) != 1 {
		t.Fatal("恢复入口绕过了危险操作确认")
	}
	checkpoint := state.Tasks[1].Payload["swarmRebuildCheckpoint"].(map[string]any)
	if checkpoint["backupId"] != "task_original" {
		t.Fatal("恢复任务未引用原始备份身份")
	}
	response = api.request(t, http.MethodPost, "/servers/server_example/swarm/rebuild/recover", map[string]any{"operationRunId": "task_original"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("重复恢复未接受排队：%d", response.StatusCode)
	}
}
