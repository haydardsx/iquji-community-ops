package httpapi

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationRunHistoryExcludesSynchronousRecordsAndKeepsTaskStages(t *testing.T) {
	api := newTestAPI(t)
	first, err := api.runtime.CreateTask(t.Context(), "rotate_secret_asset", "secret_asset", "secret_example", "示例凭据", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := api.runtime.CreateTask(t.Context(), "rotate_secret_asset", "secret_asset", "secret_other", "另一凭据", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.OperationLogs = append(state.OperationLogs,
			domain.OperationLog{ID: "sync_create", Action: "create_secret_assets", Status: "succeeded", TargetKind: "secret_assets", TargetID: "secret_example", Message: "配置已创建", Timestamp: "2026-01-01T00:00:00Z"},
			domain.OperationLog{ID: "sync_update", Action: "update_secret_assets", Status: "succeeded", TargetKind: "secret_assets", TargetID: "secret_example", Message: "配置已保存", Timestamp: "2026-01-01T00:01:00Z"},
			domain.OperationLog{ID: "restored_record", OperationRunID: "missing_run", Action: "rotate_secret_asset", Status: "failed", TargetKind: "secret_asset", TargetID: "secret_example", Message: "保留的历史记录", Timestamp: "2026-01-01T00:02:00Z"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	remaining := map[string]bool{first.ID: true, second.ID: true}
	for page := 1; page <= 3; page++ {
		path := fmt.Sprintf("/operations/runs?category=secrets&targetKind=secret_asset&status=queued&page=%d&pageSize=1", page)
		result := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, path, nil))
		items := result["items"].([]any)
		if result["total"] != float64(2) || result["page"] != float64(page) || page <= 2 && len(items) != 1 || page == 3 && len(items) != 0 {
			t.Fatalf("第 %d 页任务总数或分页大小不正确", page)
		}
		if page <= 2 {
			id := items[0].(map[string]any)["id"].(string)
			if !remaining[id] {
				t.Fatalf("第 %d 页包含非任务记录或重复任务", page)
			}
			delete(remaining, id)
		}
	}
	stages := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/logs/runtime?operationRunId="+first.ID, nil))
	stageItems := stages["items"].([]any)
	if stages["total"] != float64(1) || len(stageItems) != 1 || stageItems[0].(map[string]any)["operationRunId"] != first.ID || stageItems[0].(map[string]any)["phase"] != "created" {
		t.Fatal("按运行 ID 查询未返回预期的创建阶段")
	}
	records := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/logs/operations?category=secrets", nil))
	if records["total"] != float64(5) {
		t.Fatal("后台操作记录数量不正确")
	}
	preserved := map[string]bool{}
	for _, item := range records["items"].([]any) {
		preserved[item.(map[string]any)["id"].(string)] = true
	}
	if !preserved["sync_create"] || !preserved["sync_update"] || !preserved["restored_record"] {
		t.Fatal("同步操作与孤立历史记录必须继续保留在原始记录接口")
	}
}
