package httpapi

import (
	"net/http"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationSectionQuerySeparatesSameServerAndApplication(t *testing.T) {
	api := newTestAPI(t)
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_example"}}
		state.Applications = []domain.Object{{"id": "application_example"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "application_example", "buildServerId": "server_example"}}
		state.Routes = []domain.Object{{"id": "route_example", "applicationId": "application_example"}}
		for _, run := range []domain.OperationRun{
			{ID: "runtime", Action: "install_docker_engine", TargetKind: "server", TargetID: "server_example"},
			{ID: "build", Action: "request_jenkins_release", TargetKind: "application", TargetID: "application_example"},
			{ID: "route", Action: "apply_route", TargetKind: "route", TargetID: "route_example"},
			{ID: "app_runtime", Action: "stop_application", TargetKind: "application", TargetID: "application_example"},
		} {
			run.Status, run.CreatedAt, run.UpdatedAt = "succeeded", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
			state.OperationRuns = append(state.OperationRuns, run)
			state.OperationLogs = append(state.OperationLogs, domain.OperationLog{ID: "log_" + run.ID, OperationRunID: run.ID, Action: run.Action, TargetKind: run.TargetKind, TargetID: run.TargetID, Status: run.Status})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ query, id string }{
		{"section=server-runtime&serverId=server_example", "runtime"},
		{"section=application-routes&applicationId=application_example", "route"},
	} {
		page := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/operations/runs?"+test.query, nil))
		items := page["items"].([]any)
		if page["total"] != float64(1) || len(items) != 1 || items[0].(map[string]any)["id"] != test.id {
			t.Fatalf("区域筛选缺少预期的 %s 操作", test.id)
		}
		scope := items[0].(map[string]any)["scope"].(map[string]any)
		if scope["section"] == "" || scope["reachable"] != true {
			t.Fatal("筛选结果缺少可达区域")
		}
		activity := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/operations/activity?"+test.query, nil))
		if activity["unreadCount"] != float64(1) {
			t.Fatal("筛选后的未读活动数量不正确")
		}
		audit := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/logs/operations?"+test.query, nil))
		if audit["total"] != float64(1) {
			t.Fatal("筛选后的操作记录数量不正确")
		}
	}
	for _, path := range []string{"/operations/runs", "/operations/activity", "/logs/operations"} {
		for _, section := range []string{"unknown-section", "server-environments", "application-runtime"} {
			response := api.request(t, http.MethodGet, path+"?section="+section, nil)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("未知或已移除区域必须拒绝：%s %s %d", path, section, response.StatusCode)
			}
			_ = response.Body.Close()
		}
	}
	if err := api.store.MutateState(t.Context(), func(state *domain.State) error {
		state.Servers, state.Applications = nil, nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	activity := decodeTestJSON[map[string]any](t, api.request(t, http.MethodGet, "/operations/activity", nil))
	items := activity["items"].([]any)
	if len(items) != 4 {
		t.Fatal("删除资源后活动记录数量不正确")
	}
	for _, item := range items {
		scope := item.(map[string]any)["scope"].(map[string]any)
		reachable, exists := scope["reachable"]
		if !exists || reachable != false {
			t.Fatal("响应缺少明确的不可达状态")
		}
	}
}
