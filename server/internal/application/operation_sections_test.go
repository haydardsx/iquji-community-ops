package application

import (
	"reflect"
	"sort"
	"testing"

	"github.com/haydardsx/iquji-community-ops/server/internal/domain"
)

func TestOperationSectionsKeepRuntimeSuitesAndRoutesSeparate(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	runs := []domain.OperationRun{
		{ID: "runtime_one", Action: "install_docker_swarm", TargetKind: "server", TargetID: "server_one"},
		{ID: "runtime_two", Action: "install_jenkins", TargetKind: "server", TargetID: "server_two"},
		{ID: "build_one", Action: "request_jenkins_release", TargetKind: "application", TargetID: "application_one"},
		{ID: "route_one", Action: "apply_route", TargetKind: "route", TargetID: "route_one"},
		{ID: "route_two", Action: "disable_route", TargetKind: "route", TargetID: "route_two"},
		{ID: "route_removed", Action: "delete_route", TargetKind: "route", TargetID: "route_removed"},
		{ID: "application_runtime", Action: "restart_application", TargetKind: "application", TargetID: "application_one"},
		{ID: "server_metadata", Action: "update_servers", TargetKind: "servers", TargetID: "server_one"},
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Applications = []domain.Object{{"id": "application_one", "projectId": "project_one"}, {"id": "application_two", "projectId": "project_two"}}
		state.ApplicationDeploymentConfigs = []domain.Object{{"applicationId": "application_one", "buildServerId": "server_one", "gatewayServerId": "server_one"}}
		state.Routes = []domain.Object{{"id": "route_one", "applicationId": "application_one"}, {"id": "route_two", "applicationId": "application_two"}}
		for _, run := range runs {
			run.Status, run.CreatedAt, run.UpdatedAt = "succeeded", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
			state.OperationRuns = append(state.OperationRuns, run)
			state.OperationLogs = append(state.OperationLogs, domain.OperationLog{ID: "log_" + run.ID, OperationRunID: run.ID, Action: run.Action, TargetKind: run.TargetKind, TargetID: run.TargetID, Status: "succeeded", Timestamp: run.UpdatedAt})
		}
		state.Tasks = append(state.Tasks,
			domain.Task{ID: "runtime_one", TargetKind: "server", TargetID: "server_one", QueueResources: []domain.TaskResourceLock{{Key: "server:server_two"}}},
			domain.Task{ID: "route_removed", TargetKind: "route", TargetID: "route_removed", Payload: map[string]any{"applicationId": "application_one"}},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query TaskQuery
		want  []string
	}{
		{TaskQuery{Section: "server-runtime", ServerID: "server_one"}, []string{"runtime_one"}},
		{TaskQuery{Section: "server-runtime", ServerID: "server_two"}, []string{"runtime_two"}},
		{TaskQuery{Section: "application-routes", ApplicationID: "application_one"}, []string{"route_one", "route_removed"}},
		{TaskQuery{Section: "application-routes", ApplicationID: "application_two"}, []string{"route_two"}},
	} {
		t.Run(test.query.Section+"/"+test.query.ServerID+test.query.ApplicationID, func(t *testing.T) {
			page, err := runtime.ListOperationRuns(ctx, test.query)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, item := range page["items"].([]map[string]any) {
				ids = append(ids, item["id"].(string))
			}
			sort.Strings(ids)
			if !reflect.DeepEqual(ids, test.want) {
				t.Fatalf("区域历史混入其他操作：got=%v want=%v", ids, test.want)
			}
			activity, err := runtime.OperationActivity(ctx, test.query)
			if err != nil || activity["unreadCount"] != len(test.want) {
				t.Fatal("活动区域归属不一致")
			}
			audit, err := runtime.ListOperationLogPage(ctx, test.query)
			if err != nil || audit["total"] != len(test.want) {
				t.Fatal("操作记录区域归属不一致")
			}
		})
	}
}

func TestDeletedTargetsKeepHistoryWithoutUnreachableNavigation(t *testing.T) {
	runtime, store, ctx := newTaskTestRuntime(t)
	want := map[string]bool{
		"application_removed": false, "application_lifecycle": false, "backup_restore": false,
		"server_removed": false, "tunnel_removed": false, "route_removed": true, "secret_removed": true, "certificate_removed": true,
	}
	if err := store.MutateState(ctx, func(state *domain.State) error {
		state.Servers = []domain.Object{{"id": "server_present"}}
		state.Applications = []domain.Object{{"id": "application_present"}}
		state.Tasks = []domain.Task{
			{ID: "route_removed", TargetKind: "route", TargetID: "route_removed", Payload: map[string]any{"applicationId": "application_present"}},
			{ID: "tunnel_removed", TargetKind: "frp_tunnel_run", TargetID: "tunnel_run_removed", Payload: map[string]any{"tunnelId": "tunnel_removed"}},
		}
		for _, run := range []domain.OperationRun{
			{ID: "application_removed", Action: "request_jenkins_release", TargetKind: "application", TargetID: "application_removed"},
			{ID: "application_lifecycle", Action: "restart_application", TargetKind: "application", TargetID: "application_present"},
			{ID: "backup_restore", Action: "restore_backup", TargetKind: "backup_restore", TargetID: "upload_example"},
			{ID: "server_removed", Action: "install_docker_engine", TargetKind: "server", TargetID: "server_removed"},
			{ID: "tunnel_removed", Action: "start_frp_tunnel", TargetKind: "frp_tunnel_run", TargetID: "tunnel_run_removed"},
			{ID: "route_removed", Action: "delete_route", TargetKind: "route", TargetID: "route_removed"},
			{ID: "secret_removed", Action: "rotate_secret_asset", TargetKind: "secret_asset", TargetID: "secret_removed"},
			{ID: "certificate_removed", Action: "verify_certificate", TargetKind: "certificate", TargetID: "certificate_removed"},
		} {
			run.Status, run.CreatedAt, run.UpdatedAt = "succeeded", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z"
			state.OperationRuns = append(state.OperationRuns, run)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	activity, err := runtime.OperationActivity(ctx, TaskQuery{})
	if err != nil {
		t.Fatal(err)
	}
	items := activity["items"].([]map[string]any)
	if len(items) != len(want) {
		t.Fatal("不可达记录仍需保留跟踪")
	}
	for _, item := range items {
		scope := item["scope"].(OperationScope)
		if scope.Reachable != want[item["id"].(string)] {
			t.Fatal("删除后可达性错误")
		}
	}
	history, err := runtime.ListOperationRuns(ctx, TaskQuery{})
	if err != nil || history["total"] != len(want) {
		t.Fatal("删除资源不能丢弃历史")
	}
}
