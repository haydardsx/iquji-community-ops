package application

import (
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationActivityRestoresActiveAndUnreadWithResourceAncestry(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Projects = []domain.Object{{"id": "project_example"}}
		state.Applications = []domain.Object{{"id": "application_example", "projectId": "project_example"}}
		state.Routes = []domain.Object{{"id": "route_example", "applicationId": "application_example"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "application_example", "buildServerId": "server_build", "gatewayServerId": "server_gateway"}}
		state.Tasks = []domain.Task{{ID: "pending", Status: "pending_confirmation"}}
		state.OperationRuns = []domain.OperationRun{
			{ID: "route", TargetKind: "route", TargetID: "route_example", Status: "running", UpdatedAt: "2026-01-01T00:00:00Z"},
			{ID: "secret", TargetKind: "secret_asset", TargetID: "secret_example", Status: "succeeded", UpdatedAt: "2026-01-01T00:01:00Z"},
			{ID: "pending", TargetKind: "server", TargetID: "server_gateway", Status: "queued", UpdatedAt: "2026-01-01T00:01:00Z"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	activity, err := runtime.OperationActivity(ctx, TaskQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if activity["activeCount"] != 1 || activity["unreadCount"] != 1 {
		t.Fatal("运行中或未读的任务数不正确")
	}
	page, err := runtime.ListOperationRuns(ctx, TaskQuery{ProjectID: "project_example", ServerID: "server_build"})
	if err != nil || page["total"] != 1 {
		t.Fatal("按项目和服务器筛选后的任务数不正确")
	}
	row := page["items"].([]map[string]any)[0]
	scope := row["scope"].(OperationScope)
	if scope.ApplicationID != "application_example" || scope.Navigation != "projects" || len(scope.ServerIDs) != 2 {
		t.Fatal("任务关联的应用、导航或服务器数量不正确")
	}
	if err := runtime.MarkOperationRead(ctx, "secret", "succeeded", "2026-01-01T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	activity, err = runtime.OperationActivity(ctx, TaskQuery{})
	if err != nil || activity["unreadCount"] != 0 || activity["activeCount"] != 1 {
		t.Fatal("标记已读后未读数或运行中任务数不正确")
	}
}

func TestOperationLogsPaginateInStableChronologicalOrder(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.RuntimeLogs = []domain.RuntimeLog{
			{ID: "log_b", OperationRunID: "operation_example", Timestamp: "2026-01-01T00:01:00Z"},
			{ID: "log_a", OperationRunID: "operation_example", Timestamp: "2026-01-01T00:00:00Z"},
			{ID: "log_other", OperationRunID: "operation_other", Timestamp: "2026-01-01T00:00:00Z"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	page, err := runtime.ListRuntimeLogsPage(ctx, "", "", "operation_example", 2, 1, "asc")
	if err != nil || page["total"] != 2 || len(page["items"].([]domain.RuntimeLog)) != 1 || page["items"].([]domain.RuntimeLog)[0].ID != "log_b" {
		t.Fatal("升序日志的第二页缺少预期记录")
	}
	latest, err := runtime.ListRuntimeLogsPage(ctx, "", "", "operation_example", 1, 1, "desc")
	if err != nil || latest["items"].([]domain.RuntimeLog)[0].ID != "log_b" {
		t.Fatal("倒序日志的第一页缺少最新记录")
	}
}
