package httpapi

import (
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationActivityReadVersionAndSynchronousHistory(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.OperationRuns = []domain.OperationRun{{ID: "operation_example", TargetKind: "secret_asset", TargetID: "secret_example", Status: "succeeded", UpdatedAt: "2026-01-01T00:01:00Z"}}
		state.OperationLogs = []domain.OperationLog{
			{ID: "log_old", TargetKind: "secret_assets", TargetID: "secret_example", Status: "succeeded", Message: "旧配置已保存", Timestamp: "2026-01-01T00:00:00Z"},
			{ID: "log_new", TargetKind: "secret_asset", TargetID: "secret_example", Status: "succeeded", Message: "配置已保存", Timestamp: "2026-01-01T00:01:00Z"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	activity := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/operations/activity", nil))
	if activity["unreadCount"] != float64(1) {
		t.Fatal("新终态应未读")
	}
	stale := api.request(t, http.MethodPost, "/operations/runs/operation_example/read", map[string]any{"status": "succeeded", "updatedAt": "2026-01-01T00:00:00Z"})
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("旧日志版本应拒绝标记已读：%d", stale.StatusCode)
	}
	_ = stale.Body.Close()
	current := api.request(t, http.MethodPost, "/operations/runs/operation_example/read", map[string]any{"status": "succeeded", "updatedAt": "2026-01-01T00:01:00Z"})
	if current.StatusCode != http.StatusOK {
		t.Fatalf("当前日志应可标记已读：%d", current.StatusCode)
	}
	_ = current.Body.Close()
	activity = decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/operations/activity", nil))
	if activity["unreadCount"] != float64(0) {
		t.Fatal("成功展示后应消除未读")
	}
	history := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/logs/operations?targetKind=secret_asset&category=secrets&page=2&pageSize=1", nil))
	rows := history["items"].([]any)
	if history["total"] != float64(2) || len(rows) != 1 || rows[0].(map[string]any)["id"] != "log_old" {
		t.Fatal("同步记录应统一归属并真实分页")
	}
}
